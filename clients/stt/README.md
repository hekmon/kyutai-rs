# Kyutai Rust Server Speech To Text client

This is an example client using the main library to connect to a Kyutai Rust server STT server.

It reads a wave file and send it to the server for transcription.

## Usage

```text
Usage of ./stt:
  -input string
        Wav file to open. Use - for stdin. (default "audio.wav")
  -server string
        The websocket URL of the Kyutai STT server. (default "ws://127.0.0.1:8080")
```

### Simple form

If you have a wave in the correct format (for example the output of the TTS client) you can directly pass it to the client.

```bash
export KYUTAI_TTS_APIKEY="public_token"
./stt -input 'speech_mono_24kHz.wav'
```

### Convertion using with ffmpeg

For any other format (others codecs or wav files that are not mono 24kHz) you can use ffmpeg to convert it (the client will read from stdin if you pass '-' as input file):

```bash
export KYUTAI_TTS_APIKEY="public_token"
ffmpeg -hide_banner -loglevel 'error' -i "speech.opus" -f 'f32le' -ar '24000' -ac '1' 'pipe:' | ./stt -input '-'
```
