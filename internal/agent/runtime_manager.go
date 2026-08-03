package agent

// runtimeManager owns the small, writable runtime.conf file that stores
// state which can change while the program is running (currently only
// the active model name). Unlike modelManager, it is allowed to write
// its file and is used by the session layer to remember the last active
// model across process restarts.
//
// All methods are called from the session's run() goroutine only, so no
// synchronization is needed.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alayacore/alayacore/internal/config"
)

// runtimeConfig holds runtime configuration that can change during execution
type runtimeConfig struct {
	ActiveModel string `json:"active_model" config:"active_model"` // Model name (from model.conf)
	ActiveTheme string `json:"active_theme" config:"active_theme"` // Theme name (without .conf extension)
}

// runtimeManager manages runtime configuration
type runtimeManager struct {
	config   runtimeConfig
	path     string
	loadErrs []string // parse errors from last Load()
}

func newRuntimeManager(runtimePath string) *runtimeManager {
	rm := &runtimeManager{}
	rm.path = runtimePath

	// Load if path is set
	if rm.path != "" {
		_ = rm.Load() // best-effort load on init
	}

	return rm
}

// Load reads the runtime config from file
func (rm *runtimeManager) Load() error {
	if rm.path == "" {
		return nil
	}

	data, err := os.ReadFile(rm.path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create the file with default content
			return rm.save()
		}
		return err
	}

	rm.config, rm.loadErrs = parseRuntimeConfig(string(data), filepath.Base(rm.path))
	return nil
}

// save writes the runtime config to file
func (rm *runtimeManager) save() error {
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

// parseRuntimeConfig parses the key-value runtime config format.
// file is the source file name used in error messages (e.g. "runtime.conf").
func parseRuntimeConfig(content string, file string) (runtimeConfig, []string) {
	var cfg runtimeConfig
	if errs := config.ParseKeyValue(content, &cfg); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = fmt.Sprintf("%s: %s", file, e.String())
		}
		return cfg, msgs
	}
	return cfg, nil
}

// formatRuntimeConfig formats the runtime config as key-value text
func formatRuntimeConfig(cfg runtimeConfig) string {
	return config.FormatKeyValue(cfg)
}

// GetLoadErrors returns any parse errors from the last Load() call.
func (rm *runtimeManager) GetLoadErrors() []string {
	return rm.loadErrs
}

func (rm *runtimeManager) GetActiveModel() string {
	return rm.config.ActiveModel
}

// SetActiveModel sets the active model name and saves to file
func (rm *runtimeManager) SetActiveModel(name string) error {
	rm.config.ActiveModel = name
	return rm.save()
}

func (rm *runtimeManager) GetActiveTheme() string {
	return rm.config.ActiveTheme
}

// SetActiveTheme sets the active theme name and saves to file
func (rm *runtimeManager) SetActiveTheme(name string) error {
	rm.config.ActiveTheme = name
	return rm.save()
}
