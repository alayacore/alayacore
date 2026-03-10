# CLI Reference

## Usage

All configuration must be specified via command line flags:

```sh
# Local Ollama OpenAI-compatible server
coreclaw --type openai --base-url http://localhost:11434/v1 --api-key xxx --model llama3

# Local Ollama Anthropic-compatible server
coreclaw --type anthropic --base-url http://localhost:11434 --api-key=xxx --model gpt-oss:20b

# MiniMax (Anthropic-compatible)
coreclaw --type anthropic --base-url $MINIMAXI_API_URL --api-key $MINIMAXI_API_KEY --model MiniMax-M2.5

# DeepSeek (OpenAI-compatible)
coreclaw --type openai --base-url $DEEPSEEK_API_URL --api-key $DEEPSEEK_API_KEY --model deepseek-chat

# ZAI (OpenAI-compatible)
coreclaw --type openai --base-url $ZAI_API_URL --api-key $ZAI_API_KEY --model GLM-4.7
```

Running with skills:
```sh
coreclaw --type anthropic --base-url http://localhost:11434 --api-key=xxx --model gpt-oss:20b --skill ~/playground/coreclaw/misc/samples/skills/
```


## CLI Flags

| Flag | Description |
|------|-------------|
| `-type string` | Provider type: `anthropic` or `openai` (required) |
| `-base-url string` | API endpoint URL (required) |
| `-api-key string` | API key (required) |
| `-model string` | Model name to use |
| `-version` | Show version information |
| `-help` | Show help information |
| `-debug-api` | Write raw API requests and responses to log file |
| `-system string` | Override system prompt |
| `-skill string` | Skills directory path (can be specified multiple times) |
| `-session string` | Session file path to load/save conversations |
| `-proxy string` | HTTP proxy URL (supports HTTP, HTTPS, and SOCKS5 proxies, e.g., `http://127.0.0.1:7890` or `socks5://127.0.0.1:1080`) |
| `-context-limit string` | Provider context window size in tokens. Supports K/M suffixes (e.g., `200K`, `1M`). When set, status bar shows `Context: N / LIMIT (X.X%)` instead of just `Context: N` |
