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

const (
	TTSSampleRate  = 24_000
	TTSNumChannels = 1
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
			"kyutai-api-key": []string{client.apiKey},
		},
		// TODO
	}); err != nil {
		err = fmt.Errorf("failed to dial websocket: %w", err)
		return
	}
	// Prepare the channels
	ttsc.writerChan = make(chan string)
	ttsc.readerChan = make(chan PackMessage)
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
	readerChan chan PackMessage
}

func (ttsc *TTSConnection) GetContext() context.Context {
	return ttsc.workersCtx
}

func (ttsc *TTSConnection) GetWriteChan() chan<- string {
	return ttsc.writerChan
}

func (ttsc *TTSConnection) GetReadChan() <-chan PackMessage {
	return ttsc.readerChan
}

func (ttsc *TTSConnection) Done() (err error) {
	if err = ttsc.workers.Wait(); errors.Is(err, context.Canceled) {
		err = ttsc.conn.Close(websocket.StatusGoingAway, "")
	} else {
		_ = ttsc.conn.Close(websocket.StatusInternalError, "")
	}
	return
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
			// exit if end of user input
			if !open {
				return
			}
		case <-ttsc.workersCtx.Done():
			return
		}
	}
}

func (ttsc *TTSConnection) reader() (err error) {
	defer close(ttsc.readerChan) // close chan when exiting to inform user we are done
	var (
		msgType           websocket.MessageType
		payload, leftover []byte
		msgPack           PackMessage
	)
	for {
		// Read a message on the websocket connection
		if msgType, payload, err = ttsc.conn.Read(ttsc.workersCtx); err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				switch ce.Code {
				case websocket.StatusNoStatusRcvd:
					// regular close from the server
					err = nil
				}
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
			ttsc.readerChan <- msgPack
		default:
			return fmt.Errorf("unexpected websocket message type: %d", msgType)
		}
	}
}
