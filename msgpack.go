//go:generate msgp

package krs

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/tinylib/msgp/msgp"
)

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

type PackMessage interface {
	MessageType() PackMessageType
}

type PackMessageHeader struct {
	Type PackMessageType `msg:"type"`
}

func (pmh PackMessageHeader) MessageType() PackMessageType {
	return pmh.Type
}

type PackMessageText struct {
	Type PackMessageType `msg:"type"`
	Text string          `msg:"text"`
}

func (pmt PackMessageText) MessageType() PackMessageType {
	return pmt.Type
}

type PackMessageAudio struct {
	Type PackMessageType `msg:"type"`
	PCM  []float32       `msg:"pcm"`
}

func (pma PackMessageAudio) MessageType() PackMessageType {
	return pma.Type
}

type PackMessageMarker struct {
	Type PackMessageType `msg:"type"`
	ID   int             `msg:"id"`
}

func (pmm PackMessageMarker) MessageType() PackMessageType {
	return pmm.Type
}

type PackMessageStep struct {
	Type        PackMessageType `msg:"type"`
	Prs         []float32       `msg:"prs"`
	StepIndex   int             `msg:"step_idx"`
	BufferedPCM int             `msg:"buffered_pcm"`
}

func (pms PackMessageStep) MessageType() PackMessageType {
	return pms.Type
}

func (pms PackMessageStep) BufferDelay() time.Duration {
	return time.Duration(pms.BufferedPCM) * time.Second / SampleRate
}

func QuickDebug(msgpackData []byte) string {
	r := msgp.NewReader(bytes.NewReader(msgpackData))
	v, _ := r.ReadIntf()
	j, _ := json.MarshalIndent(v, "", "  ")
	return string(j)
}
