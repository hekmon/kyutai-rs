package main

import (
	"context"
	"fmt"
	"os"

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

	fmt.Println("opening a connection")
	ttsConn, err := ttsClient.Connect(context.Background())
	if err != nil {
		panic(err)
	}

	fmt.Printf("sending msg...")
	ttsSend := ttsConn.GetWriteChan()
	ttsSend <- "Hello!"
	ttsSend <- "My name is Bob Kelso."
	ttsSend <- "Guess What has two thumbs and doesn't care?"
	close(ttsSend)
	fmt.Println(" sent.")

	var (
		receivedMsgPack krs.PackMessage
		ok              bool
	)
	for {
		if receivedMsgPack, ok = <-ttsConn.GetReadChan(); !ok {
			fmt.Println("channel closed")
			break
		}
		switch receivedMsgPack.Type {
		case krs.PackMessageTypeText:
			fmt.Printf("Text received: %q\n", receivedMsgPack.Text)
		case krs.PackMessageTypeAudio:
			fmt.Printf("Audio received: %d bytes\n", len(receivedMsgPack.PCM))
		default:
			fmt.Printf("%s received\n", receivedMsgPack.Type)
		}
	}

	if err = ttsConn.Wait(); err != nil {
		panic(err)
	}
}
