package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
)

// LoadConfigs reads mcp.conf from the config directory and parses it
// into a slice of ServerConfig. Returns any parse errors. mcp.conf is
// optional: a missing file yields no configs and no errors.
func LoadConfigs(cfg *config.Settings) ([]ServerConfig, []string) {
	data, err := os.ReadFile(cfg.MCPConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // mcp.conf is optional
		}
		return nil, []string{fmt.Sprintf("reading %s: %v", filepath.Base(cfg.MCPConfigPath), err)}
	}
	return parseServerConfigs(string(data), filepath.Base(cfg.MCPConfigPath))
}

// parseServerConfigs parses mcp.conf content into a slice of ServerConfig.
// file is the source file name used in error messages (e.g. "mcp.conf").
// Returns any parse errors.
func parseServerConfigs(content string, file string) ([]ServerConfig, []string) {
	blocks := config.ParseKeyValueBlocks(content)
	parsed := make([]parsedBlock, 0, len(blocks))
	errs := make([]string, 0)

	for i, block := range blocks {
		cleaned, ok := stripComments(block)
		if !ok {
			continue
		}
		srv, blockErrs, ok := parseServerBlock(cleaned, i, file)
		errs = append(errs, blockErrs...)
		if ok {
			parsed = append(parsed, parsedBlock{srv: srv, blockNo: i + 1})
		}
	}

	return dedupeServers(parsed, file, errs)
}

// parsedBlock pairs a parsed server config with its source block number
// (1-based) so duplicate errors can point at the offending block.
type parsedBlock struct {
	srv     ServerConfig
	blockNo int
}

// dedupeServers keeps the first occurrence of each server name and
// reports subsequent duplicates as errors.
func dedupeServers(parsed []parsedBlock, file string, errs []string) ([]ServerConfig, []string) {
	seenNames := make(map[string]bool)
	deduped := make([]ServerConfig, 0, len(parsed))
	for _, pb := range parsed {
		if seenNames[pb.srv.Name] {
			errs = append(errs, fmt.Sprintf("%s block %d: duplicate server name %q — skipped", file, pb.blockNo, pb.srv.Name))
			continue
		}
		seenNames[pb.srv.Name] = true
		deduped = append(deduped, pb.srv)
	}
	return deduped, errs
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
func parseServerBlock(block string, blockIdx int, file string) (ServerConfig, []string, bool) {
	var fileCfg ServerConfigFile
	parseErrors := config.ParseKeyValue(block, &fileCfg)

	errs := make([]string, 0, len(parseErrors))
	for _, e := range parseErrors {
		errs = append(errs, fmt.Sprintf("%s block %d: %s", file, blockIdx+1, e.String()))
	}

	if fileCfg.Server == "" {
		errs = append(errs, fmt.Sprintf("%s block %d: skipping block with empty server name", file, blockIdx+1))
		return ServerConfig{}, errs, false
	}
	return fileCfg.ToServerConfig(), errs, true
}
