//go:generate msgp

package krs

// Message types for Kyutai TTS WebSocket protocol
// Generate MessagePack code with: go generate

type PackMessageType string

const (
	// Can be received by the reader channel
	//// STT
	PackMessageTypeStep    PackMessageType = "Step"
	PackMessageTypeWord    PackMessageType = "Word"
	PackMessageTypeEndWord PackMessageType = "EndWord"
	//// TTS
	PackMessageTypeReady PackMessageType = "Ready"
	PackMessageTypeText  PackMessageType = "Text"
	PackMessageTypeAudio PackMessageType = "Audio"
	// Below are types handled automatically by the lib
	PackMessageTypeEoS    PackMessageType = "Eos"
	PackMessageTypeMarker PackMessageType = "Marker"
)

// TextMessage sends text to TTS server
type PackMessage struct {
	Type PackMessageType `msg:"type"`
	Text string          `msg:"text,omitempty"` // for PackMessageTypeText
	PCM  []float32       `msg:"pcm,omitempty"`  // for PackMessageTypeAudio
}

type PackMarker struct {
	Type PackMessageType `msg:"type"`
	ID   int             `msg:"id"`
}
