//go:generate msgp

package krs

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/tinylib/msgp/msgp"
)

type MessagePackType string

const (
	// Can be received by the reader channel
	//// STT
	MessagePackTypeStep    MessagePackType = "Step"
	MessagePackTypeWord    MessagePackType = "Word"
	MessagePackTypeEndWord MessagePackType = "EndWord"
	//// TTS
	MessagePackTypeReady MessagePackType = "Ready"
	MessagePackTypeText  MessagePackType = "Text"
	MessagePackTypeAudio MessagePackType = "Audio"
	// Below are types handled automatically by the lib
	MessagePackTypeEoS    MessagePackType = "Eos"
	MessagePackTypeMarker MessagePackType = "Marker"
)

type MessagePack interface {
	MessageType() MessagePackType
}

type MessagePackHeader struct {
	Type MessagePackType `msg:"type"`
}

func (pmh MessagePackHeader) MessageType() MessagePackType {
	return pmh.Type
}

type MessagePackText struct {
	Type MessagePackType `msg:"type"`
	Text string          `msg:"text"`
}

func (pmt MessagePackText) MessageType() MessagePackType {
	return pmt.Type
}

type MessagePackAudio struct {
	Type MessagePackType `msg:"type"`
	PCM  []float32       `msg:"pcm"`
}

func (mpa MessagePackAudio) MessageType() MessagePackType {
	return mpa.Type
}

type MessagePackMarker struct {
	Type MessagePackType `msg:"type"`
	ID   int64           `msg:"id"`
}

func (mpm MessagePackMarker) MessageType() MessagePackType {
	return mpm.Type
}

type MessagePackStep struct {
	Type        MessagePackType `msg:"type"`
	Prs         []float32       `msg:"prs"`
	StepIndex   int             `msg:"step_idx"`
	BufferedPCM int             `msg:"buffered_pcm"`
}

func (mps MessagePackStep) MessageType() MessagePackType {
	return mps.Type
}

func (mps MessagePackStep) BufferDelay() time.Duration {
	return time.Duration(mps.BufferedPCM) * time.Second / SampleRate
}

type MessagePackWord struct {
	Type      MessagePackType `msg:"type"`
	Text      string          `msg:"text"`
	StartTime float64         `msg:"start_time"`
}

func (mpw MessagePackWord) MessageType() MessagePackType {
	return mpw.Type
}

func (mpw MessagePackWord) StartTimeDuration() time.Duration {
	return time.Duration(mpw.StartTime * float64(time.Second))
}

type MessagePackWordEnd struct {
	Type     MessagePackType `msg:"type"`
	StopTime float64         `msg:"stop_time"`
}

func (mpwe MessagePackWordEnd) MessageType() MessagePackType {
	return mpwe.Type
}

func (mpwe MessagePackWordEnd) StopTimeDuration() time.Duration {
	return time.Duration(mpwe.StopTime * float64(time.Second))
}

func QuickDebug(msgpackData []byte) string {
	r := msgp.NewReader(bytes.NewReader(msgpackData))
	v, _ := r.ReadIntf()
	j, _ := json.MarshalIndent(v, "", "  ")
	return string(j)
}
