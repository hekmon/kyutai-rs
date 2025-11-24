# Kyutai Rust Server Text To Speech client

This is an example client using the main library to connect to a Kyutai Rust server TTS server.

It sends text for voice synthesis. The server will return the audio samples in a wave format (mono at 24kHz).

## Usage

```text
Usage of ./tts:
  -input string
        Input text to synthesize. Use - for stdin. (default "-")
  -output string
        Output audio samples. Use - for stdout. (default "output.wav")
  -server string
        The websocket URL of the Kyutai TTS server. (default "ws://127.0.0.1:8080")
  -wordspersecond int
        Input text word sending rate (words per second). Use it to simulate a LLM input. (default 5)
```

### Simpliest form

Will create an `output.wav` file with the provided text.

```bash
export KYUTAI_TTS_APIKEY="public_token"
./tts -input "Hello! My name is Bob Kelso. Guess who has two thumbs and doesn't care?"
```

### Cutomized form

Take the text from a text file, specify the target server web socket URL and specify the output file.

```bash
export KYUTAI_TTS_APIKEY="public_token"
cat speech.txt | ./tts -server "ws://127.0.0.1:8081" -output "speech.wav"
```

### Advanced form

Take the text from stdin, customize the web socket URL, adjust the text rate to simulate a LLM output and output audio samples to stdout for conversion with ffmpeg to a custom format (here `opus` with defaul encoding options).

```bash
export KYUTAI_TTS_APIKEY="public_token"
echo "Hello! My name is Bob Kelso. Guess who has two thumbs and doesn't care?" | ./tts -server "ws://127.0.0.1:8081" -input "-"  -wordspersecond 10 -output "-" | ffmpeg -hide_banner -loglevel error -y -f f32le -ar 24000 -ac 1 -i pipe: output.opus
```
