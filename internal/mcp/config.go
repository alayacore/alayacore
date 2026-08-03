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
	errs := make([]string, 0)

	for _, block := range blocks {
		cleaned, ok := stripComments(block)
		if !ok {
			continue
		}
		srv, blockErrs, ok := parseServerBlock(cleaned)
		errs = append(errs, blockErrs...)
		if ok {
			configs = append(configs, srv)
		}
	}

	return dedupeServers(configs, errs)
}

// stripComments removes blank and comment lines from a config block and
// reports whether any real content remains. "#" is a line-level comment
// (ParseKeyValue skips such lines), but a block whose first line is a
// comment must still be parsed — only fully commented/blank blocks are
// dropped.
func stripComments(block string) (string, bool) {
	block = strings.TrimSpace(block)
	if block == "" {
		return "", false
	}

	kept := make([]string, 0)
	for _, line := range strings.Split(block, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		return "", false
	}
	return strings.Join(kept, "\n"), true
}

// parseServerBlock parses a single mcp.conf block into a ServerConfig.
// Returns ok=false when the block has no server name (reported via errs).
func parseServerBlock(block string) (ServerConfig, []string, bool) {
	var fileCfg ServerConfigFile
	parseErrors := config.ParseKeyValue(block, &fileCfg)

	errs := make([]string, 0, len(parseErrors))
	for _, e := range parseErrors {
		errs = append(errs, fmt.Sprintf("mcp.conf: %s", e.String()))
	}

	if fileCfg.Server == "" {
		errs = append(errs, "mcp.conf: skipping block with empty server name")
		return ServerConfig{}, errs, false
	}
	return fileCfg.ToServerConfig(), errs, true
}

// dedupeServers keeps the first occurrence of each server name and
// reports subsequent duplicates as errors.
func dedupeServers(configs []ServerConfig, errs []string) ([]ServerConfig, []string) {
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
