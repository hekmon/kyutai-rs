//go:generate msgp

package krs

// Message types for Kyutai TTS WebSocket protocol
// Generate MessagePack code with: go generate

type PackMessageType string

const (
	PackMessageTypeText  PackMessageType = "Text"
	PackMessageTypeReady PackMessageType = "Ready"
	PackMessageTypeAudio PackMessageType = "Audio"
	PackMessageTypeEoS   PackMessageType = "Eos"
)

// TextMessage sends text to TTS server
type PackMessage struct {
	Type PackMessageType `msg:"type"`
	Text string          `msg:"text,omitempty"` // for PackMessageTypeText
	PCM  []float32       `msg:"pcm,omitempty"`  // for PackMessageTypeAudio
}
