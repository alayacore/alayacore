package main

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/adaptors/terminal"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/config"
)

func main() {
	cfg := config.Parse()

	if cfg.ShowVersion {
		fmt.Printf("alayacore version %s\n", config.Version)
		os.Exit(0)
	}

	if cfg.ShowHelp {
		printHelp()
		os.Exit(0)
	}

	appCfg, err := app.Setup(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	adaptor := terminal.NewAdaptor(appCfg)
	adaptor.Start()
}

func printHelp() {
	fmt.Print(`AlayaCore - A minimal AI Agent

Usage:
  alayacore [flags]

Flags:
  --model-config string   Model config file path (default: ~/.alayacore/model.conf)
  --runtime-config string Runtime config file path (default: ~/.alayacore/runtime.conf)
  --system string         Extra system prompt (can be specified multiple times)
  --skill strings         Skill path (can be specified multiple times)
  --session string        Session file path to load/save conversations
  --proxy string          HTTP proxy URL (e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:1080)
  --debug-api             Write raw API requests and responses to log file
  --version               Show version information
  --help                  Show help information

Examples:
  # Using model config file
  alayacore

  # With optional flags
  alayacore --session ~/mysession.md
  alayacore --skill ./skills1 --skill ./skills2
  alayacore --proxy http://127.0.0.1:7890
  alayacore --model-config ./my-model.conf

Model config example (~/.alayacore/model.conf):
  name: "Claude 3.5 Sonnet"
  protocol_type: "anthropic"
  base_url: "https://api.anthropic.com"
  api_key: "your-api-key"
  model_name: "claude-3-5-sonnet-20241022"
  context_limit: 200000
  prompt_cache: true
  ---
  name: "OpenAI GPT-4o"
  protocol_type: "openai"
  base_url: "https://api.openai.com/v1"
  api_key: "your-api-key"
  model_name: "gpt-4o"
  context_limit: 128000
`)
}
