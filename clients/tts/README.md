

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

```bash
export KYUTAI_TTS_APIKEY="public_token"
./tts -input "Hello! My name is Bob Kelso. Guess who has two thumbs and doesn't care?"
```

```bash
export KYUTAI_TTS_APIKEY="public_token"
cat speech.txt | ./tts -server "ws://127.0.0.1:8081" -output "speech.wav"
```

```bash
export KYUTAI_TTS_APIKEY="public_token"
echo "Hello! My name is Bob Kelso. Guess who has two thumbs and doesn't care?" | ./tts -server "ws://127.0.0.1:8081" -input "-"  -wordspersecond 10 -output "-" | ffmpeg -hide_banner -loglevel error -y -f f32le -ar 24000 -ac 1 -i pipe: output.opus
```
