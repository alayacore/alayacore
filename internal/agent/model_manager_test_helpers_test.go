package agent

import (
	"fmt"
)

// Reload reloads models from the config file (test helper).
func (mm *modelManager) Reload() error {
	if mm.filePath == "" {
		return fmt.Errorf("model: no config file path set")
	}
	return mm.LoadFromFile(mm.filePath)
}

// AddModel adds a new model to the runtime list (test helper).
func (mm *modelManager) AddModel(m modelConfig) int {
	m.ID = mm.nextID
	mm.nextID++
	mm.models = append(mm.models, m)
	return m.ID
}

// ModelCount returns the number of models (test helper).
func (mm *modelManager) ModelCount() int {
	return len(mm.models)
}
