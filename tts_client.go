package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/coder/websocket"
	"github.com/tinylib/msgp/msgp"
)

const (
	// TTS server configuration
	ServerURL  = "ws://localhost:8998/api/tts_streaming"
	SampleRate = 24000 // 24kHz PCM audio
)

type TTSClient struct {
	conn        *websocket.Conn
	audioWriter io.Writer
}

// NewTTSClient creates and connects to TTS server
func NewTTSClient(ctx context.Context, serverURL string) (*TTSClient, error) {
	// Connect to WebSocket server
	conn, _, err := websocket.Dial(ctx, serverURL, &websocket.DialOptions{
		HTTPHeader: map[string][]string{
			"kyutai-api-key": {"public_token"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("websocket dial: %w", err)
	}

	return &TTSClient{conn: conn}, nil
}

// SetAudioWriter configures where to write PCM audio output
func (c *TTSClient) SetAudioWriter(w io.Writer) {
	c.audioWriter = w
}

// SendText sends text chunk to TTS server (streaming)
func (c *TTSClient) SendText(ctx context.Context, text string) error {
	msg := TextMessage{
		Type: "Text",
		Text: text,
	}

	// Marshal using generated MessagePack code
	data, err := msg.MarshalMsg(nil)
	if err != nil {
		return fmt.Errorf("marshal text: %w", err)
	}

	err = c.conn.Write(ctx, websocket.MessageBinary, data)
	if err != nil {
		return fmt.Errorf("write text: %w", err)
	}

	return nil
}

// SendEos signals end of text stream
func (c *TTSClient) SendEos(ctx context.Context) error {
	msg := EosMessage{Type: "Eos"}

	data, err := msg.MarshalMsg(nil)
	if err != nil {
		return fmt.Errorf("marshal eos: %w", err)
	}

	err = c.conn.Write(ctx, websocket.MessageBinary, data)
	if err != nil {
		return fmt.Errorf("write eos: %w", err)
	}

	return nil
}

// ReceiveAudio receives and processes audio from server
func (c *TTSClient) ReceiveAudio(ctx context.Context) error {
	var firstAudio time.Time
	var receivedFirstAudio bool

	for {
		// Read message from WebSocket
		msgType, data, err := c.conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return nil // Normal closure
			}
			return fmt.Errorf("read message: %w", err)
		}

		if msgType != websocket.MessageBinary {
			continue
		}

		// Decode MessagePack to determine message type
		var typeCheck struct {
			Type string `msg:"type"`
		}

		reader := msgp.NewReader(bytes.NewReader(data))
		err = reader.ReadIntf(&typeCheck)
		if err != nil {
			log.Printf("decode type: %v", err)
			continue
		}

		switch typeCheck.Type {
		case "Audio":
			if !receivedFirstAudio {
				firstAudio = time.Now()
				receivedFirstAudio = true
				log.Printf("First audio received")
			}

			// Decode full audio message
			var audioMsg AudioMessage
			_, err = audioMsg.UnmarshalMsg(data)
			if err != nil {
				log.Printf("decode audio: %v", err)
				continue
			}

			// Write PCM audio to output
			if c.audioWriter != nil {
				err = c.writePCM(audioMsg.PCM)
				if err != nil {
					log.Printf("write audio: %v", err)
				}
			}

		case "Marker":
			log.Printf("Marker received")

		default:
			log.Printf("Unknown message type: %s", typeCheck.Type)
		}
	}
}

// writePCM writes float32 PCM samples to output writer
func (c *TTSClient) writePCM(samples []float32) error {
	// Convert float32 to int16 PCM
	pcmData := make([]byte, len(samples)*2)
	for i, sample := range samples {
		// Clamp and convert to int16
		if sample > 1.0 {
			sample = 1.0
		} else if sample < -1.0 {
			sample = -1.0
		}
		pcm := int16(sample * 32767)
		binary.LittleEndian.PutUint16(pcmData[i*2:], uint16(pcm))
	}

	_, err := c.audioWriter.Write(pcmData)
	return err
}

// Close closes the WebSocket connection
func (c *TTSClient) Close() error {
	return c.conn.Close(websocket.StatusNormalClosure, "done")
}
