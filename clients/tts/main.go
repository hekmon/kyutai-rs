package main

import (
	"context"
	"fmt"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/transforms"
	"github.com/go-audio/wav"
	krs "github.com/hekmon/kyutai-rs"
)

const (
	EnvNameURL    = "KYUTAI_TTS_URL"
	EnvNameAPIKey = "KYUTAI_TTS_APIKEY"
)

func main() {
	ttsClient, err := krs.NewTTSClient(&krs.TTSConfig{
		URL:    os.Getenv(EnvNameURL),
		APIKey: os.Getenv(EnvNameAPIKey),
		Voice:  "expresso/ex01-ex02_default_001_channel2_198s.wav",
	})
	if err != nil {
		panic(err)
	}

	fmt.Fprintf(os.Stderr, "Opening a connection...")
	ttsConn, err := ttsClient.Connect(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stderr, " connected.")

	fmt.Fprintf(os.Stderr, "Sending msg...")
	ttsSend := ttsConn.GetWriteChan()
	ttsSend <- "Hello!"
	ttsSend <- "My name is Bob Kelso."
	ttsSend <- "Guess who has two thumbs and doesn't care?"
	close(ttsSend)
	fmt.Fprintln(os.Stderr, " sent.")

	var (
		receivedMsgPack krs.PackMessage
		ok              bool
		audioSamples    []float32
	)
	for {
		if receivedMsgPack, ok = <-ttsConn.GetReadChan(); !ok {
			fmt.Fprintln(os.Stderr)
			break
		}
		switch receivedMsgPack.Type {
		case krs.PackMessageTypeText:
			fmt.Fprintf(os.Stderr, "%s ", receivedMsgPack.Text)
		case krs.PackMessageTypeAudio:
			audioSamples = append(audioSamples, receivedMsgPack.PCM...)
		}
	}

	if err = ttsConn.Wait(); err != nil {
		panic(err)
	}

	// Write the audio samples to a WAV file
	if err = writeWAVE("output.wav", audioSamples); err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stderr, "\nAudio samples written to output.wav\n")
}

func writeWAVE(filename string, kyutaiTTSSamples []float32) (err error) {
	audiobitDepth := 16
	// Create the file
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create %q file: %w", filename, err)
	}
	defer file.Close()
	// Create the raw buffer
	audioBuffer := &audio.Float32Buffer{
		Format: &audio.Format{
			NumChannels: krs.TTSNumChannels,
			SampleRate:  krs.TTSSampleRate,
		},
		Data: kyutaiTTSSamples,
	}
	// Samples from kyutai TTS are from -1 to 1, scale them to a given bitdepth
	if err = transforms.PCMScaleF32(audioBuffer, audiobitDepth); err != nil {
		return fmt.Errorf("failed to scale samples: %w", err)
	}
	// Create a standard wave encoder and write the buffer into int
	wavEncoder := wav.NewEncoder(file, audioBuffer.Format.SampleRate, audioBuffer.SourceBitDepth, audioBuffer.Format.NumChannels, 1)
	if err = wavEncoder.Write(audioBuffer.AsIntBuffer()); err != nil {
		return fmt.Errorf("failed to encode audio sample as wav file: %w", err)
	}
	if err = wavEncoder.Close(); err != nil {
		return fmt.Errorf("failed to flush wav encoder: %w", err)
	}
	return
}
