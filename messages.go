package main

// Message types for Kyutai TTS WebSocket protocol
// Generate MessagePack code with: go generate

//go:generate msgp

// TextMessage sends text to TTS server
type TextMessage struct {
	Type string `msg:"type"`
	Text string `msg:"text"`
}

// EosMessage signals end of text stream
type EosMessage struct {
	Type string `msg:"type"`
}

// AudioMessage contains PCM audio data from server
type AudioMessage struct {
	Type string    `msg:"type"`
	PCM  []float32 `msg:"pcm"`
}

// MarkerMessage indicates stream markers
type MarkerMessage struct {
	Type string `msg:"type"`
}
