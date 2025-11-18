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
	var input string
	for {
		select {
		case input = <-ttsc.writerChan:
			err = ttsc.conn.Write(ttsc.workersCtx, websocket.MessageText, []byte(input))
			if err != nil {
				err = fmt.Errorf("failed to write message into the websocket connection: %w", err)
				return
			}
		case <-ttsc.workersCtx.Done():
			return
		}
	}
}

func (ttsc *TTSConnection) reader() (err error) {
	var (
		msgType websocket.MessageType
		payload []byte
	)
	for {
		if msgType, payload, err = ttsc.conn.Read(ttsc.workersCtx); err != nil {
			return
		}
		switch msgType {
		case websocket.MessageText:
			// Handle text messages (e.g., status updates)
			fmt.Printf("Received text message: %s\n", string(payload))
		case websocket.MessageBinary:
			// Handle binary messages (e.g., audio data)
			fmt.Printf("Received binary message of length: %d\n", len(payload))
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
