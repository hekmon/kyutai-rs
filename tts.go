package krs

import (
	"context"
	"errors"
	"fmt"
	"io"
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
			"kyutai-api-key": []string{client.apiKey},
		},
		// TODO
	}); err != nil {
		err = fmt.Errorf("failed to dial websocket: %w", err)
		return
	}
	// Prepare the channels
	ttsc.writerChan = make(chan string)
	ttsc.readerChan = make(chan MessagePack)
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
	readerChan chan MessagePack
}

func (ttsc *TTSConnection) GetContext() context.Context {
	return ttsc.workersCtx
}

func (ttsc *TTSConnection) GetWriteChan() chan<- string {
	return ttsc.writerChan
}

func (ttsc *TTSConnection) GetReadChan() <-chan MessagePack {
	return ttsc.readerChan
}

func (ttsc *TTSConnection) Done() (err error) {
	if err = ttsc.workers.Wait(); err != nil {
		var code websocket.StatusCode
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = websocket.StatusGoingAway
		} else {
			code = websocket.StatusInternalError
		}
		_ = ttsc.conn.Close(code, "") // discard any closing error as we want to keep the initial stop error
		return
	}
	if err = ttsc.conn.Close(websocket.StatusNormalClosure, ""); errors.Is(err, io.EOF) {
		// dunno why we can receive EOF here
		err = nil
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
			if open {
				msg := MessagePackText{
					Type: MessagePackTypeText,
					Text: input,
				}
				if payload, err = msg.MarshalMsg(nil); err != nil {
					err = fmt.Errorf("failed to marshal message pack: %w", err)
					return
				}
			} else {
				msg := MessagePackHeader{
					Type: MessagePackTypeEoS,
				}
				if payload, err = msg.MarshalMsg(nil); err != nil {
					err = fmt.Errorf("failed to marshal message pack: %w", err)
					return
				}
			}
			// Send the msg
			if err = ttsc.conn.Write(ttsc.workersCtx, websocket.MessageBinary, payload); err != nil {
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
	var (
		msgType websocket.MessageType
		payload []byte
		msgPack MessagePackHeader
	)
	for {
		// Read a message on the websocket connection
		if msgType, payload, err = ttsc.conn.Read(ttsc.workersCtx); err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) && ce.Code == websocket.StatusNoStatusRcvd {
				// regular close from the server
				err = nil
				// close chan when exiting to inform user we are done
				close(ttsc.readerChan)
			}
			return
		}
		// Act based on message
		switch msgType {
		case websocket.MessageText:
			return fmt.Errorf("received an unexpected text message: %s", string(payload))
		case websocket.MessageBinary:
			// Identify the payload
			if _, err = msgPack.UnmarshalMsg(payload); err != nil {
				err = fmt.Errorf("failed to unmarshal the message pack: %w", err)
				return
			}
			// Unmarshal in the correct type and send it
			switch msgPack.Type {
			case MessagePackTypeText:
				var msgPackText MessagePackText
				if _, err = msgPackText.UnmarshalMsg(payload); err != nil {
					err = fmt.Errorf("failed to unmarshal the message pack: %w", err)
					return
				}
				ttsc.readerChan <- msgPackText
			case MessagePackTypeAudio:
				var msgPackAudio MessagePackAudio
				if _, err = msgPackAudio.UnmarshalMsg(payload); err != nil {
					err = fmt.Errorf("failed to unmarshal the message pack: %w", err)
					return
				}
				ttsc.readerChan <- msgPackAudio
			default:
				return fmt.Errorf("unexpected message pack type identifier: %s", msgPack.Type)
			}
		default:
			return fmt.Errorf("unexpected websocket message type: %d", msgType)
		}
	}
}
