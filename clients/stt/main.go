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

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	krs "github.com/hekmon/kyutai-rs"
	"github.com/hekmon/liveprogress/v2"
	"github.com/zeozeozeo/gomplerate"
)

const (
	EnvNameAPIKey = "KYUTAI_TTS_APIKEY"
)

func main() {
	// Flags
	server := flag.String("server", "ws://127.0.0.1:8080", "The websocket URL of the Kyutai STT server.")
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

	// Gather the audio samples
	var audioSamples []float32
	if *input == "-" {
		if audioSamples, err = readAudioSamplesFromStdin(); err != nil {
			panic(err)
		}
	} else {
		if audioSamples, err = readAudioSamplesFromWaveFile(*input); err != nil {
			panic(err)
		}
	}

	// Open a connection
	fmt.Printf("Opening a connection...")
	sttConn, err := sttClient.Connect(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Println(" connected")

	// Prepare the dynamic output
	if err = liveprogress.Start(); err != nil {
		panic(err)
	}
	defer func() {
		if err = liveprogress.Stop(true); err != nil {
			panic(err)
		}
	}()

	// Start processing input and output independently
	startSignal := make(chan any)
	go receiveOutput(sttConn.GetContext(), sttConn.GetReadChan(), startSignal)
	if err = sendInput(sttConn.GetContext(), sttConn.GetWriteChan(), audioSamples, startSignal); err != nil {
		panic(err)
	}

	// Wait until the connection is done and collect error if any
	if err = sttConn.Done(); err != nil {
		panic(err)
	}
}

func readAudioSamplesFromStdin() (audioSamples []float32, err error) {
	var point float32
	fmt.Print("Reading audio samples from stdin...")
	for {
		if err = binary.Read(os.Stdin, binary.LittleEndian, &point); err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
				break
			}
			fmt.Println()
			err = fmt.Errorf("failed to read binary float32 from stdin: %w", err)
			return
		}
		audioSamples = append(audioSamples, point)
	}
	fmt.Printf(" %d samples read\n", len(audioSamples))
	return
}

func readAudioSamplesFromWaveFile(filename string) (audioSamples []float32, err error) {
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
	duration, err := waveDecoder.Duration()
	if err != nil {
		err = fmt.Errorf("failed to read wav file duration: %w", err)
		return
	}
	// Extract PCM
	buffer, err := waveDecoder.FullPCMBuffer()
	if err != nil {
		err = fmt.Errorf("failed to extract PCM from wav file: %w", err)
		return
	}
	// We need mono
	switch buffer.Format.NumChannels {
	case 0:
		err = errors.New("no channels found")
		return
	case krs.NumChannels:
		// ok
	default:
		// too many channels, let's keep the first one (mono needed)
		filteredSamples := make([]int, len(buffer.Data)/buffer.Format.NumChannels)
		for i := range len(buffer.Data) / buffer.Format.NumChannels {
			filteredSamples[i] = buffer.Data[i*buffer.Format.NumChannels]
		}
		// done
		buffer.Data = filteredSamples
		buffer.Format.NumChannels = krs.NumChannels
	}
	// Resample if necessary
	if buffer.Format.SampleRate != krs.SampleRate {
		var resampler *gomplerate.Resampler
		if resampler, err = gomplerate.NewResampler(
			buffer.Format.NumChannels,
			buffer.Format.SampleRate,
			krs.SampleRate,
		); err != nil {
			err = fmt.Errorf("failed to create resampler: %w", err)
			return
		}
		audioSamples = make([]float32, 0, int((float64(buffer.Format.SampleRate)*duration.Seconds())/float64(krs.SampleRate)))
		for _, sample := range resampler.ResampleFloat64(buffer.AsFloatBuffer().Data) {
			audioSamples = append(audioSamples, float32(sample))
		}
		// if err = writeConvertedWaveFile("converted.wav", audioSamples, 16); err != nil {
		// 	err = fmt.Errorf("failed to write converted file: %w", err)
		// 	return
		// }
	} else {
		audioSamples = buffer.AsFloat32Buffer().Data
	}
	fmt.Printf("Audio file duration: %s (%d samples @%dHz)\n",
		duration, len(audioSamples), krs.SampleRate,
	)
	return
}

func writeConvertedWaveFile(filename string, audioSamples []float32, bitdepth int) (err error) {
	// output resampled file for debug
	var fd *os.File
	if fd, err = os.Create(filename); err != nil {
		return
	}
	defer fd.Close()
	encoder := wav.NewEncoder(fd, krs.SampleRate, bitdepth, krs.NumChannels, 1)
	newBuff := audio.Float32Buffer{
		Format: &audio.Format{
			NumChannels: krs.NumChannels,
			SampleRate:  krs.SampleRate,
		},
		Data:           audioSamples,
		SourceBitDepth: bitdepth,
	}
	if err = encoder.Write(newBuff.AsIntBuffer()); err != nil {
		err = fmt.Errorf("write error: %w", err)
		return
	}
	if err = encoder.Close(); err != nil {
		err = fmt.Errorf("wave encoder flush error: %w", err)
		return
	}
	return
}

func receiveOutput(ctx context.Context, receiver <-chan krs.MessagePack, sendSignal chan any) {
	// Prepare the dynamic lines
	//// Stats
	var (
		bufferDelay      time.Duration
		currentTimestamp time.Duration
		steps            int
	)
	statsLine := liveprogress.AddCustomLine(func() string {
		return fmt.Sprintf("Current timestamp: %s | Upstream buffer delay: %s | Server steps: %d",
			currentTimestamp, bufferDelay, steps,
		)
	})
	defer liveprogress.RemoveCustomLine(statsLine)
	//// Text
	var text strings.Builder
	textLine := liveprogress.AddCustomLine(func() string {
		return text.String()
	})
	defer liveprogress.RemoveCustomLine(textLine)
	//// final
	defer func() {
		// Final print before removing live line
		fmt.Fprintln(liveprogress.Bypass(), text.String())
	}()
	// Process output
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
				// Actually there is high chance we will exit because of ctx.Done():
				// Once the connection sender and receiver are both done, the connection context is canceled
				// So this is a race within the go runtime:
				// is the channel will be closed and read here first
				// or the connection context canceled and read here?
				return
			}
			switch msgPackTyped := receivedMsgPack.(type) {
			case krs.MessagePackHeader:
				if msgPackTyped.Type == krs.MessagePackTypeReady {
					close(sendSignal) // inform writer it can start sending audio
				}
			case krs.MessagePackStep:
				bufferDelay = msgPackTyped.BufferDelay()
				steps = msgPackTyped.StepIndex
			case krs.MessagePackWord:
				if text.Len() > 0 {
					text.WriteRune(' ')
				}
				text.WriteString(msgPackTyped.Text)
				currentTimestamp = msgPackTyped.StartTimeDuration()
			case krs.MessagePackWordEnd:
				currentTimestamp = msgPackTyped.StopTimeDuration()
			default:
				fmt.Fprintf(liveprogress.Bypass(), "Received msg pack type %q\n", receivedMsgPack.MessageType())
			}
		}
	}
}

func sendInput(ctx context.Context, sender chan<- []float32, audioSamples []float32, startSignal chan any) (err error) {
	defer close(sender) // Signal the connection we have finished submitting text by closing the sender channel
	// Wait for the server to be ready to process audio
	select {
	case <-ctx.Done():
		return
	case <-startSignal:
		// continue
	}
	// Show progress
	sendingBar := liveprogress.AddBar(
		liveprogress.WithTotal(uint64(len(audioSamples))),
		liveprogress.WithAppendPercent(liveprogress.BaseStyle()),
		liveprogress.WithPrependDecorator(func(bar *liveprogress.Bar) string {
			return "Sending audio "
		}),
		liveprogress.WithAppendDecorator(func(bar *liveprogress.Bar) string {
			return fmt.Sprintf(" | %d/%d samples sent", bar.Current(), bar.Total())
		}),
	)
	defer liveprogress.RemoveBar(sendingBar)
	// Send 0.1 second worth of audio samples every 0.1 seconds
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var (
		bufferSize int
		buffer     []float32
	)
	for {
		// Extract 0.1 second of audio samples maximum
		if bufferSize = min(krs.SampleRate/10, len(audioSamples)); bufferSize == 0 {
			break
		}
		buffer = audioSamples[:bufferSize]
		audioSamples = audioSamples[bufferSize:]
		// Wait for the ticker
		select {
		case <-ctx.Done():
			// connection context canceled, no need to wait for the tick
			return
		case <-ticker.C:
			// it's time, send the audio samples
			select {
			case <-ctx.Done():
				// connection context canceled, stop using the sender channel
				return
			case sender <- buffer:
				sendingBar.CurrentAdd(uint64(bufferSize))
			}
		}
	}
	fmt.Fprintln(liveprogress.Bypass(), "Audio fully sent")
	return
}
