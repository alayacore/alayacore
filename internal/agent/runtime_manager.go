package agent

// runtimeManager owns the small, writable runtime.conf file that stores
// state which can change while the program is running (the active model and
// theme names). Unlike modelManager, it is allowed to write
// its file and is used by the session layer to remember the last active
// model across process restarts.
//
// All methods are called from the session's run() goroutine only, so no
// synchronization is needed inside the process. Other processes are a
// different matter: the file is per user, not per instance, so every write
// re-reads it and changes only its own field (see update), and commits with
// internal/atomicfile so a reader never catches a half-written file.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alayacore/alayacore/internal/atomicfile"
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

	// Load if path is set. A missing file is created (first run); a file that
	// is there but cannot be read is reported, because the fallback to the
	// first model would otherwise be indistinguishable from a machine that has
	// no saved preference yet.
	if rm.path != "" {
		if err := rm.load(); err != nil {
			rm.loadErrs = append(rm.loadErrs, fmt.Sprintf("%s: %v", filepath.Base(rm.path), err))
		}
	}

	return rm
}

// Load reads the runtime config from file
func (rm *runtimeManager) load() error {
	if rm.path == "" {
		return nil
	}

	data, err := os.ReadFile(rm.path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create the file with default content
			return rm.write(rm.config)
		}
		return err
	}

	rm.config, rm.loadErrs = parseRuntimeConfig(string(data), filepath.Base(rm.path))
	return nil
}

// write replaces the file with cfg.
//
// The commit is atomic (internal/atomicfile): another instance, or a sync
// client, reading runtime.conf while this one writes sees either the old file or
// the new one — never an empty file to fall back from, and never two contents
// mixed into one that does not parse.
func (rm *runtimeManager) write(cfg runtimeConfig) error {
	if rm.path == "" {
		return nil
	}

	// Ensure directory exists
	dir := filepath.Dir(rm.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return atomicfile.Write(rm.path, []byte(formatRuntimeConfig(cfg)), 0600)
}

// update applies one change to the file on disk, after re-reading it.
//
// The in-memory copy is loaded once, at startup, and writing that whole copy
// back is what let two instances overwrite each other wholesale: switching a
// theme in one terminal reverted the model another had chosen hours earlier,
// because the writer's snapshot predated it. Re-reading first shrinks that window
// from the lifetime of a process to the few syscalls below. What is left is two
// instances changing different fields in the same instant, which costs one of the
// two switches — the same outcome as two instances changing the same field, and
// not worth a cross-process lock on a file that only records a preference.
func (rm *runtimeManager) update(mutate func(*runtimeConfig)) error {
	if rm.path == "" {
		mutate(&rm.config)
		return nil
	}

	current, err := rm.readCurrent()
	if err != nil {
		// The file cannot be read, so there is nothing to merge with; writing
		// this process's copy is the behavior every write had before the merge.
		current = rm.config
	}
	mutate(&current)

	if err := rm.write(current); err != nil {
		return err
	}
	rm.config = current
	return nil
}

// readCurrent returns the file as another process last left it, or the zero
// value when there is no file yet.
//
// Parse errors are dropped rather than reported: load() already reported the
// file it read at startup, a merge is not the place to repeat it, and the write
// that follows replaces the file with well-formed content built from whatever
// could be recovered.
func (rm *runtimeManager) readCurrent() (runtimeConfig, error) {
	data, err := os.ReadFile(rm.path)
	if err != nil {
		if os.IsNotExist(err) {
			return runtimeConfig{}, nil
		}
		return runtimeConfig{}, err
	}
	cfg, _ := parseRuntimeConfig(string(data), filepath.Base(rm.path))
	return cfg, nil
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
func (rm *runtimeManager) getLoadErrors() []string {
	return rm.loadErrs
}

func (rm *runtimeManager) getActiveModel() string {
	return rm.config.ActiveModel
}

// setActiveModel records name as the active model and saves it to file.
func (rm *runtimeManager) setActiveModel(name string) error {
	return rm.update(func(cfg *runtimeConfig) { cfg.ActiveModel = name })
}

func (rm *runtimeManager) getActiveTheme() string {
	return rm.config.ActiveTheme
}

// setActiveTheme records name as the active theme and saves it to file.
func (rm *runtimeManager) setActiveTheme(name string) error {
	return rm.update(func(cfg *runtimeConfig) { cfg.ActiveTheme = name })
}
