package krs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"

	"github.com/coder/websocket"
	"golang.org/x/sync/errgroup"
)

type TTSConfig struct {
	URL    string
	APIKey string
	Voice  string
}

func NewTTSClient(config *TTSConfig) (client *TTSClient, err error) {
	// Create the client
	client = &TTSClient{
		apiKey: config.APIKey,
	}
	// Prepare the URL
	if client.url, err = url.Parse(config.URL); err != nil {
		err = fmt.Errorf("failed to parse the URL: %w", err)
		return
	}
	client.url.Path = path.Join(client.url.Path, "/api/tts_streaming")
	parameters := client.url.Query()
	if config.Voice != "" {
		parameters.Set("voice", config.Voice)
	}
	parameters.Set("format", "PcmMessagePack")
	client.url.RawQuery = parameters.Encode()
	// Preparations done
	return
}

type TTSClient struct {
	url    *url.URL
	apiKey string
}

func (client *TTSClient) Connect(ctx context.Context) (ttsc TTSConnection, err error) {
	// Prepare the websocket client
	if ttsc.conn, _, err = websocket.Dial(ctx, client.url.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"kyutai-api-key": {client.apiKey},
		},
		// TODO
	}); err != nil {
		err = fmt.Errorf("failed to dial websocket: %w", err)
		return
	}
	// Prepare the channels
	ttsc.writerChan = make(chan string)
	// Start workers
	ttsc.workers, ttsc.workersCtx = errgroup.WithContext(ctx)
	ttsc.workers.Go(ttsc.writer)
	ttsc.workers.Go(ttsc.reader)
	return
}

type TTSConnection struct {
	conn       *websocket.Conn
	workers    *errgroup.Group
	workersCtx context.Context
	writerChan chan string
}

func (ttsc *TTSConnection) GetWriteChan() chan<- string {
	return ttsc.writerChan
}

func (ttsc *TTSConnection) writer() (err error) {
	var (
		input   string
		open    bool
		payload []byte
	)
	for {
		select {
		case input, open = <-ttsc.writerChan:
			// Prepare the pack message
			var msg PackMessage
			if open {
				msg = PackMessage{
					Type: PackMessageTypeText,
					Text: input,
				}
			} else {
				msg = PackMessage{
					Type: PackMessageTypeEoS,
				}
			}
			// Send the msg
			if payload, err = msg.MarshalMsg(nil); err != nil {
				err = fmt.Errorf("failed to marshal message pack: %w", err)
				return
			}
			err = ttsc.conn.Write(ttsc.workersCtx, websocket.MessageBinary, payload)
			if err != nil {
				err = fmt.Errorf("failed to write message into the websocket connection: %w", err)
				return
			}
			// exit if end of stream
			if !open {
				return
			}
		case <-ttsc.workersCtx.Done():
			return
		}
	}
}

func (ttsc *TTSConnection) reader() (err error) {
	var (
		msgType           websocket.MessageType
		payload, leftover []byte
		msgPack           PackMessage
	)
	for {
		if msgType, payload, err = ttsc.conn.Read(ttsc.workersCtx); err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				switch ce.Code {

				}
			}
			return
		}
		switch msgType {
		case websocket.MessageText:
			// Handle text messages (e.g., status updates)
			fmt.Printf("Received text message: %s\n", string(payload))
		case websocket.MessageBinary:
			// Handle binary messages (e.g., audio data)
			fmt.Printf("Received binary message of length: %d\n", len(payload))
			if leftover, err = msgPack.UnmarshalMsg(payload); err != nil {
				err = fmt.Errorf("failed to unmarshal the message pack: %w", err)
				return
			}
			fmt.Printf("Data received: %s (leftover: %d)\n", msgPack.Type, len(leftover))
		default:
			return fmt.Errorf("unexpected websocket message type: %d", msgType)
		}
	}
}

func (ttsc *TTSConnection) Wait() (err error) {
	if err = ttsc.workers.Wait(); errors.Is(err, context.Canceled) {
		err = ttsc.conn.Close(websocket.StatusGoingAway, "")
	} else {
		_ = ttsc.conn.Close(websocket.StatusInternalError, "")
	}
	return
}
