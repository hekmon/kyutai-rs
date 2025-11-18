package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/go-audio/audio"
	"github.com/go-audio/transforms"
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
	input := flag.String("input", "-", "Input text to synthesize. Use - for stdin.")
	inputWordRate := flag.Int("wordspersecond", 5, "Input text word sending rate (words per second). Use it to simulate a LLM input.")
	output := flag.String("output", "output.wav", "Output audio samples. Use - for stdout.")
	flag.Parse()
	if *output != "-" && !strings.HasSuffix(*output, ".wav") {
		fmt.Fprintln(os.Stderr, "When outputing to a file, you must use a .wav extension.")
		os.Exit(1)
	}

	// Create the Kyutai TTS client
	ttsClient, err := krs.NewTTSClient(&krs.TTSConfig{
		URL:    *server,
		APIKey: os.Getenv(EnvNameAPIKey),
		Voice:  "expresso/ex01-ex02_default_001_channel2_198s.wav",
	})
	if err != nil {
		panic(err)
	}

	// Open a connection
	fmt.Fprintf(os.Stderr, "Opening a connection...")
	ttsConn, err := ttsClient.Connect(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stderr, " connected.")

	// Send the input text to the TTS server...
	go sendInput(*input, *inputWordRate, ttsConn.GetWriteChan())

	// ...while reading the audio samples and processed text in return
	var (
		receivedMsgPack krs.PackMessage
		ok              bool
		audioSamples    []float32
	)
	for {
		if receivedMsgPack, ok = <-ttsConn.GetReadChan(); !ok {
			// End of server stream
			fmt.Fprintln(os.Stderr)
			break
		}
		switch receivedMsgPack.Type {
		case krs.PackMessageTypeText:
			fmt.Fprintf(os.Stderr, "%s ", receivedMsgPack.Text)
		case krs.PackMessageTypeAudio:
			if *output == "-" {
				if err = binary.Write(os.Stdout, binary.LittleEndian, receivedMsgPack.PCM); err != nil {
					panic(err)
				}
			} else {
				audioSamples = append(audioSamples, receivedMsgPack.PCM...)
			}
		}
	}

	// Wait for properly closing of the connection
	if err = ttsConn.Wait(); err != nil {
		panic(err)
	}

	// Write the audio samples to a WAV file
	if *output != "-" {
		if err = writeWAVE(*output, audioSamples); err != nil {
			panic(err)
		}
		fmt.Fprintf(os.Stderr, "\nAudio samples written to %q\n", *output)
	}
}

func sendInput(input string, wordsPerSecond int, sender chan<- string) {
	var err error
	// Create the rate limiter
	limiter := rate.NewLimiter(rate.Limit(wordsPerSecond), 1)
	// Process input
	var word string
	if input == "-" {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			for word = range strings.SplitSeq(scanner.Text(), " ") {
				if err = limiter.Wait(context.TODO()); err != nil {
					panic(err)
				}
				sender <- word
			}
		}
		if err = scanner.Err(); err != nil {
			panic(err)
		}
	} else {
		for word = range strings.SplitSeq(input, " ") {
			if err = limiter.Wait(context.TODO()); err != nil {
				panic(err)
			}
			sender <- word
		}
	}
	// We have finished submitting text
	close(sender)
}

func writeWAVE(filename string, kyutaiTTSSamples []float32) (err error) {
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
	// Samples from kyutai TTS are from -1 to 1, scale them to a standard bitdepth
	if err = transforms.PCMScaleF32(audioBuffer, 16); err != nil {
		return fmt.Errorf("failed to scale samples: %w", err)
	}
	// Create a standard wave encoder
	wavEncoder := wav.NewEncoder(
		file,
		audioBuffer.Format.SampleRate,
		audioBuffer.SourceBitDepth,
		audioBuffer.Format.NumChannels,
		1,
	)
	// Write the samples as wave now that we have scaled the samples to a bitdepth
	if err = wavEncoder.Write(audioBuffer.AsIntBuffer()); err != nil {
		return fmt.Errorf("failed to encode audio sample as wav file: %w", err)
	}
	if err = wavEncoder.Close(); err != nil {
		return fmt.Errorf("failed to flush wav encoder: %w", err)
	}
	return
}
