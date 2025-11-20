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
	"time"

	"github.com/go-audio/wav"
	krs "github.com/hekmon/kyutai-rs"
	"golang.org/x/sync/errgroup"
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
		fmt.Println("When outputing to a file, you must use a .wav extension.")
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
	fmt.Printf("Opening a connection...")
	ttsConn, err := sttClient.Connect(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Println(" connected.")

	// Prepare our 2 independants write/read workers
	startSignal := make(chan any)
	workers := new(errgroup.Group)
	workers.Go(func() error {
		receiveOutput(ttsConn.GetContext(), ttsConn.GetReadChan(), startSignal)
		return nil
	})
	workers.Go(func() error {
		return sendInput(ttsConn.GetContext(), ttsConn.GetWriteChan(), *input, startSignal)
	})
	if err = workers.Wait(); err != nil {
		panic(err)
	}

	// Wait until the connection is done and collect error if any
	if err = ttsConn.Done(); err != nil {
		panic(err)
	}
}

func receiveOutput(ctx context.Context, receiver <-chan krs.MessagePack, sendSignal chan any) {
	var (
		receivedMsgPack krs.MessagePack
		open            bool
	)
	for {
		select {
		case <-ctx.Done():
			// connection context canceled, stop using the receiver channel
			return
		case receivedMsgPack, open = <-receiver:
			if !open {
				// End of server stream
				fmt.Println()
				return
			}
			switch typed := receivedMsgPack.(type) {
			case krs.MessagePackHeader:
				if typed.Type == krs.MessagePackTypeReady {
					close(sendSignal) // inform writer it can start sending audio
				}
			case krs.MessagePackStep:
				// fmt.Printf("remote audio ingestion buffer: %s\n", msgPackTyped.BufferDelay())
			case krs.MessagePackWord:
				fmt.Printf("%s ", typed.Text)
			case krs.MessagePackWordEnd:
			default:
				fmt.Printf("Received msg pack type %q\n", receivedMsgPack.MessageType())
			}
		}
	}
}

func sendInput(ctx context.Context, sender chan<- []float32, input string, startSignal chan any) (err error) {
	defer close(sender) // Signal the connection we have finished submitting text by closing the sender channel
	// Wait for the server to be ready to process audio
	select {
	case <-ctx.Done():
		return
	case <-startSignal:
		// continue
	}
	// Process input
	if input == "-" {
		return sendInputStdin(ctx, sender)
	}
	return sendInputFile(ctx, sender, input)
}

func sendInputStdin(ctx context.Context, sender chan<- []float32) (err error) {
	var (
		point float32
	)
	// Create the rate limiter, simulating realtime ingestion
	limiter := rate.NewLimiter(rate.Limit(krs.SampleRate), 1)
	for {
		if err = binary.Read(os.Stdin, binary.LittleEndian, &point); err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			} else {
				err = fmt.Errorf("failed to read binary float32 from stdin: %w", err)
			}
			return
		}
		if err = limiter.Wait(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				// the real error will be on Done()
				err = nil
			} else {
				err = fmt.Errorf("rate limiter wait failed: %w", err)
			}
			return
		}
		select {
		case <-ctx.Done():
			// connection context canceled, stop using the sender channel
			return
		case sender <- []float32{point}:
			// inefficient but allows to respect the sample rate with the ratelimiter
			// simulating real time audio feed
		}
	}
}

func sendInputFile(ctx context.Context, sender chan<- []float32, input string) (err error) {
	// open wave file
	var (
		audioSamples []float32
		duration     time.Duration
		point        float32
	)
	if audioSamples, duration, err = readWaveFileAt24kHz(input); err != nil {
		err = fmt.Errorf("failed to read wave file: %w", err)
		return
	}
	fmt.Printf("audio file duration: %s (%d samples @%dHz)\n",
		&duration, len(audioSamples), krs.SampleRate,
	)
	// Create the rate limiter, simulating realtime ingestion
	limiter := rate.NewLimiter(rate.Limit(krs.SampleRate), 1)
	for _, point = range audioSamples {
		if err = limiter.Wait(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				// the real error will be on Done()
				err = nil
			} else {
				err = fmt.Errorf("rate limiter wait failed: %w", err)
			}
			return
		}
		select {
		case <-ctx.Done():
			// connection context canceled, stop using the sender channel
			return
		case sender <- []float32{point}:
			// inefficient but allows to respect the sample rate with the ratelimiter
			// simulating real time audio feed
		}
	}
	return
}

func readWaveFileAt24kHz(filename string) (audioSamples []float32, duration time.Duration, err error) {
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
	if duration, err = waveDecoder.Duration(); err != nil {
		err = fmt.Errorf("failed to read wav file duration: %w", err)
		return
	}
	// Extract PCM
	buffer, err := waveDecoder.FullPCMBuffer()
	if err != nil {
		err = fmt.Errorf("failed to extract PCM from wav file: %w", err)
		return
	}
	audioSamples = buffer.AsFloat32Buffer().Data
	return
}
