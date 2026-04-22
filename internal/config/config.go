package config

import (
	"flag"
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

// Settings holds all CLI configuration
type Settings struct {
	ShowVersion        bool
	ShowHelp           bool
	DebugAPI           bool
	AutoSummarize      bool
	AutoSave           bool
	PlainIO            bool
	NoCompact          bool
	SystemPrompt       string
	Skills             []string
	Session            string
	Proxy              string
	ModelConfig        string
	RuntimeConfig      string
	ThemesFolder       string
	CompactKeepSteps   int
	CompactTruncateLen int
	MaxSteps           int
}

// Parse parses CLI flags and returns settings
func Parse() *Settings {
	showVersion := flag.Bool("version", false, "Show version information")
	showHelp := flag.Bool("help", false, "Show help information")
	debugAPI := flag.Bool("debug-api", false, "Write raw API requests and responses to log file")
	autoSummarize := flag.Bool("auto-summarize", false, "Automatically summarize conversation when context exceeds 80% of limit")
	autoSave := flag.Bool("auto-save", true, "Automatically save session after each response (requires --session)")
	plainIO := flag.Bool("plainio", false, "Use plain stdin/stdout mode instead of terminal UI")
	noCompact := flag.Bool("no-compact", false, "Disable automatic history compaction (old tool results are kept in full)")
	compactKeepSteps := flag.Int("compact-keep-steps", 3, "Number of recent agent steps to preserve during compaction (default: 3)")
	compactTruncateLen := flag.Int("compact-truncate-len", 500, "Characters to keep when truncating old tool results (default: 500)")
	systemPrompt := &stringSlice{}
	flag.Var(systemPrompt, "system", "Extra system prompt (can be specified multiple times, will be appended to default)")
	skill := &stringSlice{}
	flag.Var(skill, "skill", "Skill path (can be specified multiple times)")
	session := flag.String("session", "", "Session file path to load/save conversations")
	proxy := flag.String("proxy", "", "HTTP proxy URL (e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:1080)")
	modelConfig := flag.String("model-config", "", "Model config file path (default: ~/.alayacore/model.conf)")
	runtimeConfig := flag.String("runtime-config", "", "Runtime config file path (default: ~/.alayacore/runtime.conf)")
	maxSteps := flag.Int("max-steps", 100, "Maximum agent loop steps")
	themesFolder := flag.String("themes", "", "Themes folder path (default: ~/.alayacore/themes)")
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
		ShowHelp:           *showHelp,
		DebugAPI:           *debugAPI,
		AutoSummarize:      *autoSummarize,
		AutoSave:           *autoSave,
		PlainIO:            *plainIO,
		NoCompact:          *noCompact,
		SystemPrompt:       mergedSystemPrompt,
		Skills:             skillPaths,
		Session:            *session,
		Proxy:              *proxy,
		ModelConfig:        *modelConfig,
		RuntimeConfig:      *runtimeConfig,
		CompactKeepSteps:   *compactKeepSteps,
		CompactTruncateLen: *compactTruncateLen,
		MaxSteps:           *maxSteps,
		ThemesFolder:       *themesFolder,
	}

	return s
}
