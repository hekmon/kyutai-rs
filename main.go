package main

import (
	"bufio"
	"context"
	"flag"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	var (
		serverURL  = flag.String("server", ServerURL, "TTS server WebSocket URL")
		inputFile  = flag.String("input", "-", "Input text file (- for stdin)")
		outputFile = flag.String("output", "-", "Output audio file (- for stdout)")
		streaming  = flag.Bool("streaming", true, "Stream text word-by-word")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Read input text
	text, err := readInput(*inputFile)
	if err != nil {
		log.Fatalf("Read input: %v", err)
	}

	// Setup audio output
	audioOut, closeOut, err := setupOutput(*outputFile)
	if err != nil {
		log.Fatalf("Setup output: %v", err)
	}
	defer closeOut()

	// Create TTS client
	client, err := NewTTSClient(ctx, *serverURL)
	if err != nil {
		log.Fatalf("Create client: %v", err)
	}
	defer client.Close()

	client.SetAudioWriter(audioOut)

	// Start receiving audio in background
	audioDone := make(chan error, 1)
	go func() {
		audioDone <- client.ReceiveAudio(ctx)
	}()

	// Send text to server
	startTime := time.Now()
	if *streaming {
		// Stream word-by-word (good for LLM integration)
		words := strings.Fields(text)
		for _, word := range words {
			err = client.SendText(ctx, word+" ")
			if err != nil {
				log.Fatalf("Send text: %v", err)
			}
			time.Sleep(50 * time.Millisecond) // Simulate streaming
		}
	} else {
		// Send all text at once
		err = client.SendText(ctx, text)
		if err != nil {
			log.Fatalf("Send text: %v", err)
		}
	}

	// Signal end of stream
	err = client.SendEos(ctx)
	if err != nil {
		log.Fatalf("Send EOS: %v", err)
	}

	log.Printf("Text sent in %.2fs, waiting for audio...",
		time.Since(startTime).Seconds())

	// Wait for audio reception to complete
	err = <-audioDone
	if err != nil {
		log.Fatalf("Receive audio: %v", err)
	}

	log.Printf("Total time: %.2fs", time.Since(startTime).Seconds())
}

func readInput(filename string) (string, error) {
	var input io.Reader = os.Stdin
	if filename != "-" {
		f, err := os.Open(filename)
		if err != nil {
			return "", err
		}
		defer f.Close()
		input = f
	}

	scanner := bufio.NewScanner(input)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	return strings.Join(lines, " "), scanner.Err()
}

func setupOutput(filename string) (io.Writer, func(), error) {
	if filename == "-" {
		return os.Stdout, func() {}, nil
	}

	f, err := os.Create(filename)
	if err != nil {
		return nil, nil, err
	}

	return f, func() { f.Close() }, nil
}
