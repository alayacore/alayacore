package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/config"
)

func TestLoadConfigs_FileNotFound(t *testing.T) {
	cfg := &config.Settings{MCPConfigPath: "/nonexistent/mcp.conf"}
	configs, errs := LoadConfigs(cfg)
	if configs != nil {
		t.Errorf("expected nil configs, got %v", configs)
	}
	if errs != nil {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestLoadConfigs_ValidFile(t *testing.T) {
	content := `---
server: myapi
url: "https://mcp.example.com"
auth-type: static
auth-token: "test-token"
auth-scopes: ["read", "write"]
---
server: filesystem
command: "npx"
args: ["@anthropic/mcp-fs-server"]
env: {"TOKEN": "abc123"}
---
server: public
url: "https://public.example.com/mcp"
---
`

	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.conf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Settings{MCPConfigPath: path}
	configs, errs := LoadConfigs(cfg)
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(configs) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(configs))
	}

	// Check first config (myapi)
	c0 := configs[0]
	if c0.Name != "myapi" {
		t.Errorf("expected name 'myapi', got %q", c0.Name)
	}
	if c0.URL != "https://mcp.example.com" {
		t.Errorf("expected URL 'https://mcp.example.com', got %q", c0.URL)
	}
	if c0.Auth == nil {
		t.Fatal("expected auth config")
	}
	if c0.Auth.Type != "static" {
		t.Errorf("expected auth type 'static', got %q", c0.Auth.Type)
	}
	if c0.Auth.Token != "test-token" {
		t.Errorf("expected token 'test-token', got %q", c0.Auth.Token)
	}
	if c0.Auth.TokenEndpoint != "" {
		t.Errorf("expected empty token_endpoint for static auth, got %q", c0.Auth.TokenEndpoint)
	}
	if len(c0.Auth.Scopes) != 2 || c0.Auth.Scopes[0] != "read" || c0.Auth.Scopes[1] != "write" {
		t.Errorf("expected scopes [read write], got %v", c0.Auth.Scopes)
	}

	// Check second config (filesystem — stdio)
	c1 := configs[1]
	if c1.Name != "filesystem" {
		t.Errorf("expected name 'filesystem', got %q", c1.Name)
	}
	if c1.Command != "npx" {
		t.Errorf("expected command 'npx', got %q", c1.Command)
	}
	if len(c1.Args) != 1 || c1.Args[0] != "@anthropic/mcp-fs-server" {
		t.Errorf("expected args ['@anthropic/mcp-fs-server'], got %v", c1.Args)
	}
	if c1.Env["TOKEN"] != "abc123" {
		t.Errorf("expected env TOKEN=abc123, got %q", c1.Env["TOKEN"])
	}
	if c1.Auth != nil {
		t.Errorf("expected no auth for filesystem, got %v", c1.Auth)
	}

	// Check third config (public — no auth)
	c2 := configs[2]
	if c2.Name != "public" {
		t.Errorf("expected name 'public', got %q", c2.Name)
	}
	if c2.Auth != nil {
		t.Errorf("expected no auth for public, got %v", c2.Auth)
	}
}

func TestLoadConfigs_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.conf")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Settings{MCPConfigPath: path}
	configs, errs := LoadConfigs(cfg)
	if len(configs) != 0 {
		t.Errorf("expected 0 configs, got %d", len(configs))
	}
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestLoadConfigs_CommentsOnly(t *testing.T) {
	content := `# This is a comment
# Another comment
`
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.conf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Settings{MCPConfigPath: path}
	configs, errs := LoadConfigs(cfg)
	if len(configs) != 0 {
		t.Errorf("expected 0 configs, got %d", len(configs))
	}
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestLoadConfigs_EmptyServerName(t *testing.T) {
	content := `---
url: "https://example.com"
---
server: valid
url: "https://valid.example.com"
---
`
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.conf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Settings{MCPConfigPath: path}
	configs, errs := LoadConfigs(cfg)
	if len(configs) != 1 {
		t.Errorf("expected 1 config, got %d", len(configs))
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 error for empty server name, got %d", len(errs))
	}
}

func TestLoadConfigs_BlockWithLeadingComment(t *testing.T) {
	// A "#" comment is line-level: a block that starts with a comment line
	// but contains real config must still be parsed (not discarded whole).
	content := `---
server: my-db
command: npx
args: ["@anthropic/mcp-db-server"]
---
#server: my-db-deep
server: my-db
url: "https://example.com/mcp/deep"
---
#server: github
#url: "https://api.githubcopilot.com/mcp/"
---
server: other
url: "https://other.example.com/mcp"
---
`
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.conf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Settings{MCPConfigPath: path}
	configs, errs := LoadConfigs(cfg)

	// The leading-comment block must be parsed: 2 configs (my-db, other).
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs (comment-prefixed block must be parsed), got %d: %v", len(configs), configs)
	}

	// ...which means the duplicate my-db is now detected.
	found := false
	for _, w := range errs {
		if strings.Contains(w, "duplicate server name") && strings.Contains(w, "my-db") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected duplicate error for 'my-db' (comment-prefixed block dropped before fix), got: %v", errs)
	}

	// Pure-comment blocks are still skipped entirely.
	for _, c := range configs {
		if c.Name == "github" {
			t.Errorf("pure-comment block 'github' should be skipped, got config: %+v", c)
		}
	}
}

func TestLoadConfigs_DuplicateServerName(t *testing.T) {
	content := `---
server: my-db
command: npx
args: ["@anthropic/mcp-db-server"]
---
server: my-db
url: "https://example.com/mcp"
---
server: other
url: "https://other.example.com/mcp"
---
`
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.conf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Settings{MCPConfigPath: path}
	configs, errs := LoadConfigs(cfg)
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs (first occurrence of my-db + other), got %d", len(configs))
	}
	if configs[0].Name != "my-db" {
		t.Errorf("expected first config name 'my-db', got %q", configs[0].Name)
	}
	if configs[1].Name != "other" {
		t.Errorf("expected second config name 'other', got %q", configs[1].Name)
	}

	// Verify the first my-db is the stdio one (first occurrence preserved)
	if configs[0].Command != "npx" {
		t.Errorf("expected first my-db to be stdio (npx), got command=%q url=%q", configs[0].Command, configs[0].URL)
	}

	// Check duplicate error
	found := false
	for _, w := range errs {
		if strings.Contains(w, "duplicate server name") && strings.Contains(w, "my-db") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about duplicate server name 'my-db', got: %v", errs)
	}
}
