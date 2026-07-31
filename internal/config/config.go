// Package config parses CLI flags and configuration files.
package config

import (
	"flag"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/tools"
)

// Reasoning level constants.
// 0 = off (no reasoning), 1 = normal, 2 = max.
const (
	ReasoningLevelOff     = 0
	ReasoningLevelNormal  = 1
	ReasoningLevelMax     = 2
	DefaultReasoningLevel = ReasoningLevelNormal
)

// Agent behavior defaults.
const (
	DefaultMaxSteps = 0 // 0 means no limit; only bounded when user passes --max-steps

	// boolFalse is used for flag default comparison in printDefaults.
	boolFalse = "false"
)

// defaultConfigDir returns the default configuration directory (~/.alayacore).
func defaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".alayacore")
}

// ExpandPath expands ~ to the user's home directory.
func ExpandPath(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	usr, err := user.Current()
	if err != nil {
		return path
	}
	if path == "~" {
		return usr.HomeDir
	}
	return filepath.Join(usr.HomeDir, path[1:])
}

// printDefaults prints all command-line flags with -- prefix instead of the default -
func printDefaults() {
	flag.CommandLine.VisitAll(func(f *flag.Flag) {
		var placeholder string
		usage := f.Usage
		if s, _ := flag.UnquoteUsage(f); s != "" {
			placeholder = " " + s
		}
		usage = strings.ReplaceAll(usage, "`", "")
		if f.DefValue != "" && f.DefValue != boolFalse {
			fmt.Fprintf(flag.CommandLine.Output(), "\t--%s%s (default: %s)\n", f.Name, placeholder, f.DefValue)
		} else {
			fmt.Fprintf(flag.CommandLine.Output(), "\t--%s%s\n", f.Name, placeholder)
		}
		fmt.Fprintf(flag.CommandLine.Output(), "\t\t%s\n", usage)
	})
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
	RawIO         bool
	TerseIO       bool // read all stdin as one prompt; print only the final answer
	PlainIO       bool
	DebugLogDir   string // "" = disabled (when flag not set), "." = write to CWD, or any path (set by --debug-log)
	ModelConfig   string // derived from config-path + "model.conf"
	RuntimeConfig string // derived from config-path + "runtime.conf"
	MCPConfigPath string // derived from config-path + "mcp.conf"
	ThemesFolder  string // derived from config-path + "themes"
	Skills        []string
	Session       string

	// Model selection
	ModelName string

	// I/O
	Proxy string

	// Agent behavior
	SystemPrompt  string
	MaxSteps      int
	AutoSummarize int              // 0 = disabled, 1-100 = enabled with this threshold percentage
	ToolConfirm   []string         // tool names requiring user confirmation
	BuiltinTools  tools.ToolFilter // built-in tools to enable

	// Command execution
	CommandTimeout int // max duration for execute_command in seconds (default 120)

	// Delta streaming
	NoDelta bool // If true, suppress delta frames (At, Ar, Af); use complete frames only
}

// Parse parses CLI flags and returns settings
func Parse() *Settings {
	// Set custom usage function before any flag definitions
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), "AlayaCore - A minimal AI Agent\n\nUsage:\n\talayacore [flags]\n\nFlags:\n")
		printDefaults()
	}

	// Pre-compute default paths so they appear in --help output
	defaultConfigPath := defaultConfigDir()

	// Core
	showVersion := flag.Bool("version", false, "Show version information")
	rawIO := flag.Bool("rawio", false, "Use raw TLV stdin/stdout mode instead of terminal UI (pipe TLV frames directly)")
	terseIO := flag.Bool("terseio", false, "Read all of stdin as a single prompt and print only the final answer (stdout stays clean; errors go to stderr)")
	plainIO := flag.Bool("plainio", false, "Use plain stdin/stdout mode instead of terminal UI")
	debugLog := flag.String("debug-log", "", "Debug log `directory` (`.` = CWD, or any path; omitted = disabled). Enables both API and MCP debug logging.")
	configPath := flag.String("config-path", defaultConfigPath, "Config directory `path` (contains model.conf, runtime.conf, themes/)")
	modelName := flag.String("model", "", "Model `name` to activate (must exist in model config; overrides runtime config)")
	skill := &stringSlice{}
	flag.Var(skill, "skill", "Skill `path` (can be specified multiple times)")
	session := flag.String("session", "", "Session file `path` to load/save conversations")

	// I/O
	proxy := flag.String("proxy", "", "HTTP proxy URL (e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:1080)")

	// Agent behavior
	systemPrompt := &stringSlice{}
	flag.Var(systemPrompt, "system", "Extra `system-prompt` (can be specified multiple times, will be appended to default)")
	maxSteps := flag.Int("max-steps", DefaultMaxSteps, "Maximum agent loop steps (0 = no limit)")
	autoSummarize := flag.Int("auto-summarize", 0, "Enable auto-summarization at given threshold percentage (e.g. --auto-summarize=65, 0 = disabled)")
	toolConfirm := flag.String("tool-confirm", "", "Comma-separated tool `names` requiring user confirmation (e.g. execute_command,search_content)")
	noDelta := flag.Bool("no-delta", false, "Disable delta frames (At, Ar, Af); use complete frames only")
	flag.String("builtin-tools", "", "Comma-separated built-in tool `names` to enable (empty = no builtin tools, unspecified = all tools)")
	commandTimeout := flag.Int("command-timeout", 120,
		"Maximum duration in seconds for shell command execution (default 120)")

	flag.Parse()

	// Derive config file paths from config directory
	cp := *configPath

	// Detect if --builtin-tools was explicitly provided (even if empty).
	var builtinToolsFilter tools.ToolFilter
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "builtin-tools" {
			raw := f.Value.String()
			if raw != "" {
				builtinToolsFilter = tools.ToolFilter{
					AllBuiltins: false,
					Selected:    parseToolConfirm(raw),
				}
			}
			// --builtin-tools= (empty) keeps the zero value: no tools.
		}
	})
	// If --builtin-tools was never visited, AllBuiltins remains true.
	if !flagHasBeenVisited("builtin-tools") {
		builtinToolsFilter = tools.ToolFilter{AllBuiltins: true}
	}
	s := &Settings{
		ShowVersion:    *showVersion,
		RawIO:          *rawIO,
		TerseIO:        *terseIO,
		PlainIO:        *plainIO,
		DebugLogDir:    *debugLog,
		ModelConfig:    filepath.Join(cp, "model.conf"),
		RuntimeConfig:  filepath.Join(cp, "runtime.conf"),
		MCPConfigPath:  filepath.Join(cp, "mcp.conf"),
		ThemesFolder:   filepath.Join(cp, "themes"),
		Skills:         skill.Get(),
		Session:        *session,
		ModelName:      *modelName,
		Proxy:          *proxy,
		SystemPrompt:   mergedSystemPrompt(systemPrompt),
		MaxSteps:       *maxSteps,
		AutoSummarize:  *autoSummarize,
		ToolConfirm:    parseToolConfirm(*toolConfirm),
		BuiltinTools:   builtinToolsFilter,
		CommandTimeout: resolveCommandTimeout(*commandTimeout),
		NoDelta:        *noDelta,
	}

	return s
}

// mergedSystemPrompt joins multiple --system values with "\n\n".
func mergedSystemPrompt(sp *stringSlice) string {
	prompts := sp.Get()
	if len(prompts) == 0 {
		return ""
	}
	return strings.Join(prompts, "\n\n")
}

// parseToolConfirm splits a comma-separated tool-confirm value.
func parseToolConfirm(raw string) []string {
	if raw == "" {
		return nil
	}
	var names []string
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// resolveCommandTimeout resolves the command timeout in seconds using the
// following precedence (highest first):
//  1. --command-timeout CLI flag (if explicitly provided)
//  2. ALAYACORE_COMMAND_TIMEOUT environment variable (integer seconds)
//  3. The flag's default value (120 seconds)
//
// This allows users to set a persistent default via their shell profile
// while still overriding per-invocation via --command-timeout.
func resolveCommandTimeout(flagSec int) int {
	if flagHasBeenVisited("command-timeout") {
		return flagSec
	}
	if env := os.Getenv("ALAYACORE_COMMAND_TIMEOUT"); env != "" {
		if sec, err := strconv.Atoi(env); err == nil && sec > 0 {
			return sec
		}
		fmt.Fprintf(os.Stderr, "warning: invalid ALAYACORE_COMMAND_TIMEOUT=%q, using default\n", env)
	}
	return flagSec
}

// flagHasBeenVisited returns true if the named flag was explicitly set
// on the command line (including with an empty value).
func flagHasBeenVisited(name string) bool {
	var found bool
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
