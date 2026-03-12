package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// stringSlice implements flag.Value for multiple string flags
type stringSlice struct {
	slice []string
}

func (s *stringSlice) String() string {
	return strings.Join(s.slice, ",")
}

func (s *stringSlice) Set(value string) error {
	s.slice = append(s.slice, value)
	return nil
}

func (s *stringSlice) Get() []string {
	return s.slice
}

// parseContextLimit parses a context limit string with optional K/M suffix.
// Examples: "200K" -> 200000, "1M" -> 1000000, "128000" -> 128000
func parseContextLimit(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	s = strings.TrimSpace(strings.ToUpper(s))

	multiplier := int64(1)
	if strings.HasSuffix(s, "K") {
		multiplier = 1000
		s = s[:len(s)-1]
	} else if strings.HasSuffix(s, "M") {
		multiplier = 1000000
		s = s[:len(s)-1]
	}

	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid context limit: %q", s)
	}
	return val * multiplier, nil
}

// Settings holds all CLI configuration
type Settings struct {
	ShowVersion   bool
	ShowHelp      bool
	DebugAPI      bool
	SystemPrompt  string
	Skills        []string
	Addr          string
	Session       string
	Proxy         string
	ContextLimit  int64
	ModelConfig   string
	RuntimeConfig string
}

// Parse parses CLI flags and returns settings
func Parse() *Settings {
	showVersion := flag.Bool("version", false, "Show version information")
	showHelp := flag.Bool("help", false, "Show help information")
	debugAPI := flag.Bool("debug-api", false, "Write raw API requests and responses to log file")
	systemPrompt := flag.String("system", "", "Override system prompt")
	skill := &stringSlice{}
	flag.Var(skill, "skill", "Skill path (can be specified multiple times)")
	addr := flag.String("addr", ":8080", "Server address to listen on (for web server)")
	session := flag.String("session", "", "Session file path to load/save conversations")
	proxy := flag.String("proxy", "", "HTTP proxy URL (e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:1080)")
	contextLimitStr := flag.String("context-limit", "0", "Provider context window size in tokens (supports K/M suffix, e.g., 200K, 1M; 0 = unknown)")
	modelConfig := flag.String("model-config", "", "Model config file path (default: ~/.alayacore/models.conf)")
	runtimeConfig := flag.String("runtime-config", "", "Runtime config file path (default: <model-config-dir>/runtime.conf, or ~/.alayacore/runtime.conf)")
	flag.Parse()

	// Parse context limit with optional K/M suffix
	contextLimit, err := parseContextLimit(*contextLimitStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Collect skill paths
	skillPaths := skill.Get()

	s := &Settings{
		ShowVersion:   *showVersion,
		ShowHelp:      *showHelp,
		DebugAPI:      *debugAPI,
		SystemPrompt:  *systemPrompt,
		Skills:        skillPaths,
		Addr:          *addr,
		Session:       *session,
		Proxy:         *proxy,
		ContextLimit:  contextLimit,
		ModelConfig:   *modelConfig,
		RuntimeConfig: *runtimeConfig,
	}

	return s
}
