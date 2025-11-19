package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-audio/wav"
	krs "github.com/hekmon/kyutai-rs"
	"golang.org/x/time/rate"
)

const (
	EnvNameAPIKey = "KYUTAI_TTS_APIKEY"
)

func main() {
	// Flags
	server := flag.String("server", "ws://127.0.0.1:8080", "The websocket URL of the Kyutai TTS server.")
	input := flag.String("input", "audio.wav", "Wav file to open. Use - for stdin.")
	flag.Parse()
	if *input != "-" && !strings.HasSuffix(*input, ".wav") {
		fmt.Fprintln(os.Stderr, "When outputing to a file, you must use a .wav extension.")
		os.Exit(1)
	}

	// Create the Kyutai TTS client
	sttClient, err := krs.NewSTTClient(&krs.STTConfig{
		URL:    *server,
		APIKey: os.Getenv(EnvNameAPIKey),
	})
	if err != nil {
		panic(err)
	}

	// Open a connection
	fmt.Fprintf(os.Stderr, "Opening a connection...")
	ttsConn, err := sttClient.Connect(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stderr, " connected.")

	// Send the input audio to the TTS server...
	go sendInput(ttsConn.GetContext(), ttsConn.GetWriteChan(), *input)

	// ... while receiving corresponding text
	go receiveOutput(ttsConn.GetContext(), ttsConn.GetReadChan())

	// Wait until the connection is done and collect error if any
	if err = ttsConn.Done(); err != nil {
		panic(err)
	}
}

func sendInput(ctx context.Context, sender chan<- []float32, input string) {
	var err error
	// Create the rate limiter, simulating realtime ingestion
	limiter := rate.NewLimiter(rate.Limit(krs.SampleRate), 1)
	// Process input
	var (
		point    float32
		nbPoints int
	)
	if input == "-" {
		for {
			if err = binary.Read(os.Stdin, binary.LittleEndian, &point); err != nil {
				break
			}
			if err = limiter.Wait(ctx); err != nil {
				panic(err)
			}
			select {
			case <-ctx.Done():
				// connection context canceled, stop using the sender channel
				return
			case sender <- []float32{point}:
				// inefficient but allows to respect the sample rate with the ratelimiter
				// simulating real time audio feed
				nbPoints++
			}
		}
		if !errors.Is(err, io.EOF) {
			panic(err)
		}
	} else {
		// open wave file
		audioSamples, err := readWaveFile(input)
		if err != nil {
			panic(err)
		}
		fmt.Println("audio samples:", len(audioSamples))
		for _, point = range audioSamples {
			if err = limiter.Wait(ctx); err != nil {
				panic(err)
			}
			select {
			case <-ctx.Done():
				// connection context canceled, stop using the sender channel
				return
			case sender <- []float32{point}:
				// inefficient but allows to respect the sample rate with the ratelimiter
				// simulating real time audio feed
				nbPoints++
			}
		}
	}
	// Signal the connection we have finished submitting text by closing the sender channel
	close(sender)
	fmt.Println("Audio entirely sent! (nb points:", nbPoints, ")")
}

func readWaveFile(filename string) (audioSamples []float32, err error) {
	// Open file
	fd, err := os.Open(filename)
	if err != nil {
		err = fmt.Errorf("failed to open file: %w", err)
		return
	}
	defer fd.Close()
	// Create the wav decoder and verify information
	waveDecoder := wav.NewDecoder(fd)
	if !waveDecoder.IsValidFile() {
		err = errors.New("invalid wav file")
		return
	}
	if waveDecoder.NumChans != krs.NumChannels {
		err = fmt.Errorf("wav file must have %d channel, current channels: %d",
			krs.NumChannels, waveDecoder.NumChans,
		)
		return
	}
	if waveDecoder.SampleRate != krs.SampleRate {
		err = fmt.Errorf("wav file must have a sample rate of %dHz, currently: %dHz",
			krs.SampleRate, waveDecoder.SampleRate,
		)
		return
	}
	duration, err := waveDecoder.Duration()
	if err != nil {
		err = fmt.Errorf("failed to read wav file duration: %w", err)
		return
	}
	fmt.Printf("input file duration: %s\n", duration)
	// Extract PCM
	buffer, err := waveDecoder.FullPCMBuffer()
	if err != nil {
		err = fmt.Errorf("failed to extract PCM from wav file: %w", err)
		return
	}
	audioSamples = buffer.AsFloat32Buffer().Data
	return
}

func receiveOutput(ctx context.Context, receiver <-chan krs.PackMessage) {
	var (
		receivedMsgPack krs.PackMessage
		open            bool
		// err             error
	)
	for {
		select {
		case <-ctx.Done():
			// connection context canceled, stop using the receiver channel
			return
		case receivedMsgPack, open = <-receiver:
			if !open {
				// End of server stream
				fmt.Fprintln(os.Stderr)
				return
			}
			switch msgPackTyped := receivedMsgPack.(type) {
			case krs.PackMessageStep:
				// fmt.Printf("remote audio ingestion buffer: %s\n", msgPackTyped.BufferDelay())
			case krs.PackMessageMarker:
			default:
				fmt.Printf("Received msg pack type %q\n", receivedMsgPack.MessageType())
			}
		}
	}
}
