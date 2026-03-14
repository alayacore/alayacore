# Web Server

`alayacore-web` runs a WebSocket server with a built-in chat UI.

## Usage

```sh
# Start WebSocket server (uses models from config file)
alayacore-web

# Custom address
alayacore-web --addr :9090

# With custom model config
alayacore-web --model-config ./my-models.conf

# With session persistence
alayacore-web --session ~/my-session.md

# With skills
alayacore-web --skill ./skills

# With proxy
alayacore-web --proxy socks5://127.0.0.1:1080
```

## Endpoints

- **Web UI**: Open `http://localhost:8080` in browser
- **WebSocket**: `ws://localhost:8080/ws`

## Features

- Each browser tab gets its own independent agent session
