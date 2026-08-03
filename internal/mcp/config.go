package mcp

import (
	"fmt"
	"os"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
)

// LoadConfigs reads mcp.conf from the config directory and parses it
// into a slice of ServerConfig. Returns any parse errors.
func LoadConfigs(cfg *config.Settings) ([]ServerConfig, []string) {
	data, err := os.ReadFile(cfg.MCPConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // mcp.conf is optional
		}
		return nil, []string{fmt.Sprintf("reading mcp.conf: %v", err)}
	}

	blocks := config.ParseKeyValueBlocks(string(data))
	configs := make([]ServerConfig, 0, len(blocks))
	var errs []string

	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		// "#" is a line-level comment (ParseKeyValue skips # lines).
		// Strip comment lines first, then check whether any real content
		// remains — a block that starts with a comment must still be parsed.
		var kept []string
		for _, line := range strings.Split(block, "\n") {
			t := strings.TrimSpace(line)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			kept = append(kept, line)
		}
		block = strings.Join(kept, "\n")
		if strings.TrimSpace(block) == "" {
			continue
		}

		var fileCfg ServerConfigFile
		parseErrors := config.ParseKeyValue(block, &fileCfg)
		for _, e := range parseErrors {
			errs = append(errs, fmt.Sprintf("mcp.conf: %s", e.String()))
		}

		if fileCfg.Server == "" {
			errs = append(errs, "mcp.conf: skipping block with empty server name")
			continue
		}

		configs = append(configs, fileCfg.ToServerConfig())
	}

	// Check for duplicate server names.
	// First occurrence is kept; subsequent duplicates are reported as errors.
	seenNames := make(map[string]bool)
	deduped := make([]ServerConfig, 0, len(configs))
	for _, cfg := range configs {
		if seenNames[cfg.Name] {
			errs = append(errs, fmt.Sprintf("mcp.conf: duplicate server name %q — skipped", cfg.Name))
			continue
		}
		seenNames[cfg.Name] = true
		deduped = append(deduped, cfg)
	}

	return deduped, errs
}
