//go:generate msgp

package krs

// Message types for Kyutai TTS WebSocket protocol
// Generate MessagePack code with: go generate

type PackMessageType string

const (
	PackMessageTypeReady PackMessageType = "Ready"
	PackMessageTypeText  PackMessageType = "Text"
	PackMessageTypeAudio PackMessageType = "Audio"
	PackMessageTypeEoS   PackMessageType = "Eos" // automatically sent when closing writer chan, should not be seen on the reader channel
)

// TextMessage sends text to TTS server
type PackMessage struct {
	Type PackMessageType `msg:"type"`
	Text string          `msg:"text,omitempty"` // for PackMessageTypeText
	PCM  []float32       `msg:"pcm,omitempty"`  // for PackMessageTypeAudio
}
