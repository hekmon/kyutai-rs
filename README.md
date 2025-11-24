# Kyutai Rust Server Golang bindings

[![Go Reference](https://pkg.go.dev/badge/github.com/hekmon/kyutai-rs.svg)](https://pkg.go.dev/github.com/hekmon/kyutai-rs)

This library allows simple interfacing to the Kyutai production rust server for both TTS and STT.

The rust server uses websocket connections with [message pack](https://msgpack.org/) payloads. The purpose of this library is to abstract the websocket connection and message pack serialization to provide a simple API to send text to the TTS server or send audio to the STT server using only go channels.

## Installation

```bash
go get github.com/hekmon/kyutai-rs
```

## Usage

1. Create a TTS or STT client
2. Use the client to create a connection with `Connect()`
3. The return connection object will have 3 importants methods to call after that:
    1. `GetWriteChan()`: to send data to the server
    2. `GetReadChan()`: to receive data from the server
    3. `GetContext()`: the connection context linked to the background websockets workers, only use the read and write channels while this context is valid.
4. Once you are done, you must close the write channel to inform the library to prepare a clean stop.
5. Wait for the read channel to be closed by its background worker.
6. Wait for the full stop of workers on the connection closure with the `Done()` connection's method. This will ensure all backgrounds workers are properly stopped and resources are freed. If any errors occured during the websocket connection (and caused the connection context to be canceled), this is where you will get the error.

## Examples

See the [TTS client](clients/tts) and the [STT client](clients/stt) for complete example on how to use the library.
