package krs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sync/atomic"

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
	writerChan   chan []float32
	readerChan   chan MessagePack
	markerIDsGen atomic.Int64
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
		err = fmt.Errorf("failed to marked ID %d: %w", markerID, err)
		return
	}
	return
}

func (sttc *STTConnection) GetReadChan() <-chan MessagePack {
	return sttc.readerChan
}

func (sttc *STTConnection) Done() (err error) {
	if err = sttc.workers.Wait(); errors.Is(err, context.Canceled) {
		err = sttc.conn.Close(websocket.StatusGoingAway, "")
	} else {
		_ = sttc.conn.Close(websocket.StatusInternalError, "")
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
			// Send logic is a bit weird, but taken from an official script:
			// https://github.com/kyutai-labs/delayed-streams-modeling/blob/main/scripts/stt_from_file_rust_server.py
			if open {
				// If this is the first data we send, start with 1 second if silence
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
				// Flush our buffer
				for len(buffer) > 0 {
					if err = sttc.send(&MessagePackAudio{
						Type: MessagePackTypeAudio,
						PCM:  buffer,
					}); err != nil {
						err = fmt.Errorf("failed to send message: %w", err)
						return
					}
				}
				// Send 5 seconds of silence
				for range 5 {
					if err = sttc.send(&MessagePackAudio{
						Type: MessagePackTypeAudio,
						PCM:  oneSecondOfSilence,
					}); err != nil {
						err = fmt.Errorf("failed to send message: %w", err)
						return
					}
				}
				// Send the end marker
				if err = sttc.send(MessagePackMarker{
					Type: MessagePackTypeMarker,
					ID:   0, // special ID the SendMarker() will never send
				}); err != nil {
					err = fmt.Errorf("failed to send message: %w", err)
					return
				}
				// Send some silence after the marker to flush the marker upstream
				for range 35 {
					if err = sttc.send(&MessagePackAudio{
						Type: MessagePackTypeAudio,
						PCM:  oneSecondOfSilence,
					}); err != nil {
						err = fmt.Errorf("failed to send message: %w", err)
						return
					}
				}
				return
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
		msgType websocket.MessageType
		payload []byte
		msgPack MessagePackHeader
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
				sttc.readerChan <- msgPackStep
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
					// stop signal, writer has exited and server have processed everything writer sent
					return
				}
				sttc.readerChan <- msgPackMarker
			default:
				return fmt.Errorf("unexpected message pack type identifier: %s", msgPack.Type)
			}
		default:
			return fmt.Errorf("unexpected websocket message type: %d", msgType)
		}
	}
}
