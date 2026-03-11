# Web Server

`alayacore-web` runs a WebSocket server with a built-in chat UI.

## Usage

```sh
# Start WebSocket server
alayacore-web --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o

# Custom address
alayacore-web --type anthropic --base-url https://api.anthropic.com --api-key $ANTHROPIC_API_KEY --model claude-sonnet-4 --addr :9090
```

## Endpoints

- **Web UI**: Open `http://localhost:8080` in browser
- **WebSocket**: `ws://localhost:8080/ws`

## Features

- Each browser tab gets its own independent agent session
