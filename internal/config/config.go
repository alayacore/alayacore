package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveConfigPath returns the provided path, or the default ~/.alayacore/<filename>
func ResolveConfigPath(providedPath, defaultFilename string) string {
	if providedPath != "" {
		return providedPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".alayacore", defaultFilename)
}

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

// Settings holds all CLI configuration
type Settings struct {
	// Core
	ShowVersion   bool
	PlainIO       bool
	DebugAPI      bool
	ModelConfig   string
	RuntimeConfig string
	ThemesFolder  string
	Skills        []string
	Session       string

	// I/O
	Proxy string

	// Agent behavior
	SystemPrompt  string
	MaxSteps      int
	AutoSummarize bool
	AutoSave      bool

	// Compaction
	NoCompact          bool
	CompactKeepSteps   int
	CompactTruncateLen int
}

// Parse parses CLI flags and returns settings
func Parse() *Settings {
	// Set custom usage function before any flag definitions
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), `AlayaCore - A minimal AI Agent

Usage:
  alayacore [flags]

Flags:
`)
		flag.PrintDefaults()
	}

	// Core
	showVersion := flag.Bool("version", false, "Show version information")
	plainIO := flag.Bool("plainio", false, "Use plain stdin/stdout mode instead of terminal UI")
	debugAPI := flag.Bool("debug-api", false, "Write raw API requests and responses to log file")
	modelConfig := flag.String("model-config", "", "Model config file path (default: ~/.alayacore/model.conf)")
	runtimeConfig := flag.String("runtime-config", "", "Runtime config file path (default: ~/.alayacore/runtime.conf)")
	themesFolder := flag.String("themes", "", "Themes folder path (default: ~/.alayacore/themes)")
	skill := &stringSlice{}
	flag.Var(skill, "skill", "Skill path (can be specified multiple times)")
	session := flag.String("session", "", "Session file path to load/save conversations")

	// I/O
	proxy := flag.String("proxy", "", "HTTP proxy URL (e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:1080)")

	// Agent behavior
	systemPrompt := &stringSlice{}
	flag.Var(systemPrompt, "system", "Extra system prompt (can be specified multiple times, will be appended to default)")
	maxSteps := flag.Int("max-steps", 100, "Maximum agent loop steps")
	autoSummarize := flag.Bool("auto-summarize", false, "Automatically summarize conversation when context exceeds 65% of limit")
	autoSave := flag.Bool("auto-save", true, "Automatically save session after each response (requires --session)")

	// Compaction
	noCompact := flag.Bool("no-compact", false, "Disable automatic history compaction (old tool results are kept in full)")
	compactKeepSteps := flag.Int("compact-keep-steps", 3, "Number of recent agent steps to preserve during compaction")
	compactTruncateLen := flag.Int("compact-truncate-len", 500, "Byte-equivalent length to keep when truncating old tool results")

	flag.Parse()

	// Collect skill paths
	skillPaths := skill.Get()

	// Merge system prompts with "\n\n" separator
	var mergedSystemPrompt string
	systemPrompts := systemPrompt.Get()
	if len(systemPrompts) > 0 {
		mergedSystemPrompt = strings.Join(systemPrompts, "\n\n")
	}

	s := &Settings{
		ShowVersion:        *showVersion,
		PlainIO:            *plainIO,
		DebugAPI:           *debugAPI,
		ModelConfig:        *modelConfig,
		RuntimeConfig:      *runtimeConfig,
		ThemesFolder:       *themesFolder,
		Skills:             skillPaths,
		Session:            *session,
		Proxy:              *proxy,
		SystemPrompt:       mergedSystemPrompt,
		MaxSteps:           *maxSteps,
		AutoSummarize:      *autoSummarize,
		AutoSave:           *autoSave,
		NoCompact:          *noCompact,
		CompactKeepSteps:   *compactKeepSteps,
		CompactTruncateLen: *compactTruncateLen,
	}

	return s
}
