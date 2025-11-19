package krs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"

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
	sttc.readerChan = make(chan PackMessage)
	// Start workers
	sttc.workers, sttc.workersCtx = errgroup.WithContext(ctx)
	sttc.workers.Go(sttc.writer)
	sttc.workers.Go(sttc.reader)
	return
}

type STTConnection struct {
	conn       *websocket.Conn
	workers    *errgroup.Group
	workersCtx context.Context
	writerChan chan []float32
	readerChan chan PackMessage
}

func (sttc *STTConnection) GetContext() context.Context {
	return sttc.workersCtx
}

func (sttc *STTConnection) GetWriteChan() chan<- []float32 {
	return sttc.writerChan
}

func (sttc *STTConnection) GetReadChan() <-chan PackMessage {
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
					if err = sttc.send(&PackMessage{
						Type: PackMessageTypeAudio,
						PCM:  oneSecondOfSilence,
					}); err != nil {
						err = fmt.Errorf("failed to send message: %w", err)
						return
					}
				}
				// Add input data to the buffer
				buffer = append(buffer, input...)
				// Send our buffer by respecting the frame size (there will be leftovers)
				for len(buffer) >= frameSize {
					// respect the frame size
					if err = sttc.send(&PackMessage{
						Type: PackMessageTypeAudio,
						PCM:  buffer[:frameSize],
					}); err != nil {
						err = fmt.Errorf("failed to send message: %w", err)
						return
					}
					buffer = buffer[frameSize:]
				}
			} else {
				// Flush our buffer
				for len(buffer) > 0 {
					if err = sttc.send(&PackMessage{
						Type: PackMessageTypeAudio,
						PCM:  buffer,
					}); err != nil {
						err = fmt.Errorf("failed to send message: %w", err)
						return
					}
				}
				// Send 5 seconds of silence
				for range 5 {
					if err = sttc.send(&PackMessage{
						Type: PackMessageTypeAudio,
						PCM:  oneSecondOfSilence,
					}); err != nil {
						err = fmt.Errorf("failed to send message: %w", err)
						return
					}
				}
				// Send the end marker
				if err = sttc.send(PackMarker{
					Type: PackMessageTypeMarker,
					ID:   0,
				}); err != nil {
					err = fmt.Errorf("failed to send message: %w", err)
					return
				}
				// Send some silence after the marker to flush the marker upstream
				for range 35 {
					if err = sttc.send(&PackMessage{
						Type: PackMessageTypeAudio,
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
		err = fmt.Errorf("failed to write message into the websocket connection: %w", err)
		return
	}
	return
}

func (sttc *STTConnection) reader() (err error) {
	var (
		msgType           websocket.MessageType
		payload, leftover []byte
		msgPack           PackMessage
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
		// Act based on message
		switch msgType {
		case websocket.MessageText:
			return fmt.Errorf("received an unexpected text message: %s", string(payload))
		case websocket.MessageBinary:
			if leftover, err = msgPack.UnmarshalMsg(payload); err != nil {
				err = fmt.Errorf("failed to unmarshal the message pack: %w", err)
				return
			}
			if len(leftover) > 0 {
				err = fmt.Errorf("unexpected data after unmarshaling '%s' type message: %d bytes",
					msgPack.Type, len(leftover),
				)
				return
			}
			sttc.readerChan <- msgPack
		default:
			return fmt.Errorf("unexpected websocket message type: %d", msgType)
		}
	}
}
