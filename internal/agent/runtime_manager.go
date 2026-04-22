package agent

// RuntimeManager owns the small, writable runtime.conf file that stores
// state which can change while the program is running (currently only
// the active model name). Unlike ModelManager, it is allowed to write
// its file and is used by the session layer to remember the last active
// model across process restarts.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alayacore/alayacore/internal/config"
)

// RuntimeConfig holds runtime configuration that can change during execution
type RuntimeConfig struct {
	ActiveModel string `json:"active_model" config:"active_model"` // Model name (from model.conf)
	ActiveTheme string `json:"active_theme" config:"active_theme"` // Theme name (without .conf extension)
}

// RuntimeManager manages runtime configuration
type RuntimeManager struct {
	config RuntimeConfig
	mu     sync.RWMutex
	path   string
}

// NewRuntimeManager creates a new runtime manager
// If runtimePath is empty, it defaults to ~/.alayacore/runtime.conf
func NewRuntimeManager(runtimePath, _ string) *RuntimeManager {
	rm := &RuntimeManager{}

	if runtimePath != "" {
		rm.path = runtimePath
	} else {
		// Use default ~/.alayacore/runtime.conf
		home, err := os.UserHomeDir()
		if err != nil {
			return rm
		}
		rm.path = filepath.Join(home, ".alayacore", "runtime.conf")
	}

	// Load if path is set
	if rm.path != "" {
		_ = rm.Load() //nolint:errcheck // best-effort load on init
	}

	return rm
}

// Load reads the runtime config from file
func (rm *RuntimeManager) Load() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.path == "" {
		return nil
	}

	data, err := os.ReadFile(rm.path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create the file with default content (already holding lock)
			return rm.saveLocked()
		}
		return err
	}

	rm.config = parseRuntimeConfig(string(data))
	return nil
}

// Save writes the runtime config to file
func (rm *RuntimeManager) Save() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.saveLocked()
}

// saveLocked writes the runtime config to file (caller must hold lock)
func (rm *RuntimeManager) saveLocked() error {
	if rm.path == "" {
		return nil
	}

	// Ensure directory exists
	dir := filepath.Dir(rm.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	content := formatRuntimeConfig(rm.config)
	return os.WriteFile(rm.path, []byte(content), 0600)
}

// parseRuntimeConfig parses the key-value runtime config format
func parseRuntimeConfig(content string) RuntimeConfig {
	var cfg RuntimeConfig
	config.ParseKeyValue(content, &cfg)
	return cfg
}

// formatRuntimeConfig formats the runtime config as key-value text
func formatRuntimeConfig(config RuntimeConfig) string {
	var sb strings.Builder
	sb.WriteString("# AlayaCore runtime configuration\n")
	sb.WriteString("# This file is automatically updated when you switch models or themes\n")
	sb.WriteString("\n")
	sb.WriteString("active_model: \"")
	sb.WriteString(config.ActiveModel)
	sb.WriteString("\"\n")
	sb.WriteString("active_theme: \"")
	sb.WriteString(config.ActiveTheme)
	sb.WriteString("\"\n")
	return sb.String()
}

// GetActiveModel returns the active model name
func (rm *RuntimeManager) GetActiveModel() string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.config.ActiveModel
}

// SetActiveModel sets the active model name and saves to file
func (rm *RuntimeManager) SetActiveModel(name string) error {
	rm.mu.Lock()
	rm.config.ActiveModel = name
	rm.mu.Unlock()
	return rm.Save()
}

// GetActiveTheme returns the active theme name
func (rm *RuntimeManager) GetActiveTheme() string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.config.ActiveTheme
}

// SetActiveTheme sets the active theme name and saves to file
func (rm *RuntimeManager) SetActiveTheme(name string) error {
	rm.mu.Lock()
	rm.config.ActiveTheme = name
	rm.mu.Unlock()
	return rm.Save()
}

// GetPath returns the runtime config file path
func (rm *RuntimeManager) GetPath() string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.path
}
