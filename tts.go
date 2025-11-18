package krs

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"

	"github.com/coder/websocket"
)

type TTSConfig struct {
	URL    string
	APIKey string
	Voice  string
}

func NewTTSClient(ctx context.Context, config *TTSConfig) (client *TTSClient, err error) {
	client = new(TTSClient)
	// Prepare the URL
	endpoint, err := url.Parse(config.URL)
	if err != nil {
		err = fmt.Errorf("failed to parse the URL: %w", err)
		return
	}
	endpoint.Path = path.Join(endpoint.Path, "/api/tts_streaming")
	parameters := endpoint.Query()
	if config.Voice != "" {
		parameters.Set("voice", config.Voice)
	}
	parameters.Set("format", "PcmMessagePack")
	endpoint.RawQuery = parameters.Encode()
	fmt.Println(endpoint.String())
	// Prepare the websocket client
	if client.conn, _, err = websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"kyutai-api-key": {config.APIKey},
		},
		// TODO
	}); err != nil {
		err = fmt.Errorf("failed to dial websocket: %w", err)
		return
	}
	return
}

type TTSClient struct {
	conn *websocket.Conn
}

func (client *TTSClient) Close() error {
	return client.conn.Close(websocket.StatusNormalClosure, "")
}
