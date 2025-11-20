package krs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/tinylib/msgp/msgp"
	"golang.org/x/sync/errgroup"
)

type STTConfig struct {
	URL    string
	APIKey string
}

func NewSTTClient(config *STTConfig) (client *STTClient, err error) {
	// Create the client
	client = &STTClient{
		apiKey: config.APIKey,
	}
	// Prepare the URL
	if client.url, err = url.Parse(config.URL); err != nil {
		err = fmt.Errorf("failed to parse the URL: %w", err)
		return
	}
	client.url.Path = path.Join(client.url.Path, "/api/asr-streaming")
	parameters := client.url.Query()
	parameters.Set("format", "PcmMessagePack")
	client.url.RawQuery = parameters.Encode()
	// Preparations done
	return
}

type STTClient struct {
	url    *url.URL
	apiKey string
}

func (client *STTClient) Connect(ctx context.Context) (sttc STTConnection, err error) {
	// Prepare the websocket client
	if sttc.conn, _, err = websocket.Dial(ctx, client.url.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"kyutai-api-key": []string{client.apiKey},
		},
		// TODO
	}); err != nil {
		err = fmt.Errorf("failed to dial websocket: %w", err)
		return
	}
	// Prepare the channels
	sttc.writerChan = make(chan []float32)
	sttc.readerChan = make(chan MessagePack)
	sttc.flushChan = make(chan any)
	// Start workers
	sttc.workers, sttc.workersCtx = errgroup.WithContext(ctx)
	sttc.workers.Go(sttc.writer)
	sttc.workers.Go(sttc.reader)
	return
}

type STTConnection struct {
	conn         *websocket.Conn
	workers      *errgroup.Group
	workersCtx   context.Context
	markerIDsGen atomic.Int64
	writerChan   chan []float32
	readerChan   chan MessagePack
	flushChan    chan any
}

func (sttc *STTConnection) GetContext() context.Context {
	return sttc.workersCtx
}

func (sttc *STTConnection) GetWriteChan() chan<- []float32 {
	return sttc.writerChan
}

func (sttc *STTConnection) SendMarker() (markerID int64, err error) {
	markerID = sttc.markerIDsGen.Add(1)
	if err = sttc.send(&MessagePackMarker{
		Type: MessagePackTypeMarker,
		ID:   markerID,
	}); err != nil {
		err = fmt.Errorf("failed to send marker ID %d: %w", markerID, err)
		return
	}
	return
}

func (sttc *STTConnection) GetReadChan() <-chan MessagePack {
	return sttc.readerChan
}

func (sttc *STTConnection) Done() (err error) {
	if err = sttc.workers.Wait(); err != nil {
		var code websocket.StatusCode
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = websocket.StatusGoingAway
		} else {
			code = websocket.StatusInternalError
		}
		_ = sttc.conn.Close(code, "") // discard any closing error as we want to keep the initial stop error
		return
	}
	if err = sttc.conn.Close(websocket.StatusNormalClosure, ""); errors.Is(err, io.EOF) {
		// dunno why we can receive EOF here
		err = nil
	}
	return
}

var (
	oneSecondOfSilence = make([]float32, SampleRate)
)

func (sttc *STTConnection) writer() (err error) {
	var (
		input  []float32
		buffer []float32
		open   bool
	)
	for {
		select {
		case input, open = <-sttc.writerChan:
			if open {
				// If this is the first data we send, start with 1 second if silence
				// https://github.com/kyutai-labs/delayed-streams-modeling/blob/433dca3751a2a21a95a6d7ca1fd2a44c516a729c/scripts/stt_from_file_rust_server.py#L67-L69
				if buffer == nil {
					if err = sttc.send(&MessagePackAudio{
						Type: MessagePackTypeAudio,
						PCM:  oneSecondOfSilence,
					}); err != nil {
						err = fmt.Errorf("failed to send message: %w", err)
						return
					}
				}
				// Add input data to the buffer
				buffer = append(buffer, input...)
				// Send our buffer by respecting the frame size (there will be leftovers)
				for len(buffer) >= FrameSize {
					// respect the frame size
					if err = sttc.send(&MessagePackAudio{
						Type: MessagePackTypeAudio,
						PCM:  buffer[:FrameSize],
					}); err != nil {
						err = fmt.Errorf("failed to send message: %w", err)
						return
					}
					buffer = buffer[FrameSize:]
				}
			} else {
				// Flush out our buffer
				for len(buffer) > 0 {
					// fill buffer with silence
					if len(buffer) < FrameSize {
						buffer = append(buffer, make([]float32, FrameSize-len(buffer))...)
					}
					// send it
					if err = sttc.send(&MessagePackAudio{
						Type: MessagePackTypeAudio,
						PCM:  buffer,
					}); err != nil {
						err = fmt.Errorf("failed to send message: %w", err)
						return
					}
					// nullify it
					buffer = buffer[:]
				}
				// Send the end marker
				if err = sttc.send(MessagePackMarker{
					Type: MessagePackTypeMarker,
					ID:   0, // special ID the SendMarker() will never use
				}); err != nil {
					err = fmt.Errorf("failed to send message: %w", err)
					return
				}
				// Send some silence to flush upstream buffer until we received back the stop marker
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						if err = sttc.send(&MessagePackAudio{
							Type: MessagePackTypeAudio,
							PCM:  oneSecondOfSilence,
						}); err != nil {
							err = fmt.Errorf("failed to send message: %w", err)
							return
						}
					case <-sttc.flushChan:
						// reader has received the end marker
						return
					}
				}
			}
		case <-sttc.workersCtx.Done():
			return
		}
	}
}

func (sttc *STTConnection) send(msg msgp.Marshaler) (err error) {
	var payload []byte
	if payload, err = msg.MarshalMsg(nil); err != nil {
		err = fmt.Errorf("failed to marshal message pack: %w", err)
		return
	}
	if err = sttc.conn.Write(sttc.workersCtx, websocket.MessageBinary, payload); err != nil {
		err = fmt.Errorf("failed to write message pack into the websocket connection: %w", err)
		return
	}
	return
}

func (sttc *STTConnection) reader() (err error) {
	var (
		msgType  websocket.MessageType
		payload  []byte
		msgPack  MessagePackHeader
		draining bool
	)
	for {
		// Read a message on the websocket connection
		if msgType, payload, err = sttc.conn.Read(sttc.workersCtx); err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) && ce.Code == websocket.StatusNoStatusRcvd {
				// regular close from the server
				err = nil
				// close chan when exiting to inform user we are done
				close(sttc.readerChan)
			}
			return
		}
		// Act based on websocket message type
		switch msgType {
		case websocket.MessageText:
			return fmt.Errorf("received an unexpected websocket text message: %s", string(payload))
		case websocket.MessageBinary:
			// Unmarsal binary as MessagePack on a identifier type structure
			if _, err = msgPack.UnmarshalMsg(payload); err != nil {
				err = fmt.Errorf("failed to unmarshal the message pack: %w", err)
				return
			}
			// Unmarshal the full payload into the correct type
			switch msgPack.Type {
			case MessagePackTypeReady:
				sttc.readerChan <- msgPack // ready does not have extra fields to parse
			case MessagePackTypeStep:
				var msgPackStep MessagePackStep
				if _, err = msgPackStep.UnmarshalMsg(payload); err != nil {
					err = fmt.Errorf("failed to unmarshal the message pack: %w", err)
					return
				}
				if draining {
					// draining silence sent by writer to flush upstream model buffer
					if msgPackStep.BufferedPCM == 0 {
						// finaly received all the upstream buffered silence, we can exit to allow conn to close
						return
					}
					// else there is still buffered upstream we need to drain, simply discard and wait for next step
				} else {
					// regular step before end marker, send it to user
					sttc.readerChan <- msgPackStep
				}
			case MessagePackTypeWord:
				var msgPackWord MessagePackWord
				if _, err = msgPackWord.UnmarshalMsg(payload); err != nil {
					err = fmt.Errorf("failed to unmarshal the message pack: %w", err)
					return
				}
				sttc.readerChan <- msgPackWord
			case MessagePackTypeEndWord:
				var msgPackWordEnd MessagePackWordEnd
				if _, err = msgPackWordEnd.UnmarshalMsg(payload); err != nil {
					err = fmt.Errorf("failed to unmarshal the message pack: %w", err)
					return
				}
				sttc.readerChan <- msgPackWordEnd
			case MessagePackTypeMarker:
				var msgPackMarker MessagePackMarker
				if _, err = msgPackMarker.UnmarshalMsg(payload); err != nil {
					err = fmt.Errorf("failed to unmarshal the message pack: %w", err)
					return
				}
				if msgPackMarker.ID == 0 {
					// stop signal received (back from writer)
					close(sttc.flushChan) // signal writer it can stop sending silence
					draining = true       // switch ourself to draining mode
				} else {
					// custom user marker, send it back
					sttc.readerChan <- msgPackMarker
				}
			default:
				return fmt.Errorf("unexpected message pack type identifier: %s", msgPack.Type)
			}
		default:
			return fmt.Errorf("unexpected websocket message type: %d", msgType)
		}
	}
}
