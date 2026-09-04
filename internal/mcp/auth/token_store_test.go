package auth

import (
	"os"
	"testing"
	"time"
)

func TestFileTokenStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	token := &Token{
		AccessToken:  "test-access-token",
		TokenType:    "Bearer",
		RefreshToken: "test-refresh-token",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
		Scopes:       []string{"read", "write"},
	}

	// Save
	if err := store.SaveToken("test-server", token); err != nil {
		t.Fatalf("SaveToken() error = %v", err)
	}

	// Verify file exists
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}

	// Load
	loaded, err := store.LoadToken("test-server")
	if err != nil {
		t.Fatalf("LoadToken() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadToken() returned nil")
	}
	if loaded.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, token.AccessToken)
	}
	if loaded.RefreshToken != token.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", loaded.RefreshToken, token.RefreshToken)
	}
	if loaded.TokenType != token.TokenType {
		t.Errorf("TokenType = %q, want %q", loaded.TokenType, token.TokenType)
	}
	// Compare with second precision (Unix timestamp truncation).
	if !loaded.ExpiresAt.Equal(token.ExpiresAt.Truncate(time.Second)) &&
		!loaded.ExpiresAt.Equal(token.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v (or truncated)", loaded.ExpiresAt, token.ExpiresAt)
	}
	if len(loaded.Scopes) != len(token.Scopes) {
		t.Errorf("Scopes = %v, want %v", loaded.Scopes, token.Scopes)
	}
}

func TestFileTokenStore_LoadNonExistent(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	token, err := store.LoadToken("nonexistent")
	if err != nil {
		t.Fatalf("LoadToken() error = %v", err)
	}
	if token != nil {
		t.Errorf("expected nil, got %v", token)
	}
}

func TestFileTokenStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	// Save and delete
	token := &Token{AccessToken: "tok", TokenType: "Bearer", ExpiresAt: time.Now().Add(1 * time.Hour)}
	if err := store.SaveToken("test-server", token); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteToken("test-server"); err != nil {
		t.Fatalf("DeleteToken() error = %v", err)
	}

	// Verify deleted
	loaded, err := store.LoadToken("test-server")
	if err != nil {
		t.Fatalf("LoadToken() error = %v", err)
	}
	if loaded != nil {
		t.Error("expected nil after delete")
	}
}

func TestFileTokenStore_SanitizeTokenName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"with-dashes", "with-dashes"},
		{"with_underscores", "with_underscores"},
		{"path/with/slashes", "path~2fwith~2fslashes"},
		{"dots.are.ok", "dots~2eare~2eok"},
		{"a b", "a~20b"},
		{"tilde~here", "tilde~7ehere"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := sanitizeFileTokenName(tt.input); got != tt.want {
				t.Errorf("sanitizeFileTokenName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestFileTokenStore_SanitizeTokenNameUnique verifies the encoding is
// injective: server names that collided under the old mapping (e.g. "a/b"
// vs "a_b" both became "a_b") must map to distinct token files, so two
// servers' tokens can never cross-contaminate.
func TestFileTokenStore_SanitizeTokenNameUnique(t *testing.T) {
	pairs := [][2]string{
		{"a/b", "a_b"},
		{"a.b", "a_b"},
		{"a\\b", "a_b"},
		{"a_2f", "a/"},
		{"a~2fb", "a/b"},
		{"my server", "my_server"},
	}
	for _, p := range pairs {
		x, y := sanitizeFileTokenName(p[0]), sanitizeFileTokenName(p[1])
		if x == y {
			t.Errorf("sanitizeFileTokenName(%q) == sanitizeFileTokenName(%q) == %q", p[0], p[1], x)
		}
	}
}

func TestFileTokenStore_Overwrite(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	// Save first token
	token1 := &Token{AccessToken: "token1", TokenType: "Bearer", ExpiresAt: time.Now().Add(1 * time.Hour)}
	if err := store.SaveToken("test", token1); err != nil {
		t.Fatal(err)
	}

	// Overwrite with second
	token2 := &Token{AccessToken: "token2", TokenType: "Bearer", ExpiresAt: time.Now().Add(2 * time.Hour)}
	if err := store.SaveToken("test", token2); err != nil {
		t.Fatal(err)
	}

	// Load should return token2
	loaded, err := store.LoadToken("test")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AccessToken != "token2" {
		t.Errorf("got %q, want %q", loaded.AccessToken, "token2")
	}
}

func TestFileTokenStore_NilTokenDeletes(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	// Save then save nil (should delete)
	token := &Token{AccessToken: "tok", TokenType: "Bearer", ExpiresAt: time.Now().Add(1 * time.Hour)}
	_ = store.SaveToken("test", token)
	_ = store.SaveToken("test", nil)

	loaded, err := store.LoadToken("test")
	if err != nil {
		t.Fatal(err)
	}
	if loaded != nil {
		t.Error("expected nil after saving nil")
	}
}

func TestFileTokenStore_NonExpiringToken(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	// Token with no expiration (ExpiresAt is zero value).
	token := &Token{
		AccessToken:  "non-expiring",
		TokenType:    "Bearer",
		RefreshToken: "refresh-token",
		// ExpiresAt is zero value → non-expiring
	}

	if err := store.SaveToken("test-server", token); err != nil {
		t.Fatalf("SaveToken() error = %v", err)
	}

	// Verify file content — should NOT have expires_at field
	data, err := os.ReadFile(store.tokenFilePath("test-server"))
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(data), "expires_at") {
		t.Errorf("non-expiring token should not have expires_at field, got: %s", data)
	}

	// Load and verify
	loaded, err := store.LoadToken("test-server")
	if err != nil {
		t.Fatalf("LoadToken() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadToken() returned nil")
	}
	if !loaded.Valid() {
		t.Error("non-expiring token should be valid")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestToken_Valid_NonExpiring(t *testing.T) {
	// Token with zero ExpiresAt should be valid (non-expiring).
	tok := &Token{AccessToken: "tok", TokenType: "Bearer"}
	if !tok.Valid() {
		t.Error("non-expiring token should be valid")
	}

	// Token with ExpiresAt in the future should be valid.
	tok = &Token{AccessToken: "tok", TokenType: "Bearer", ExpiresAt: time.Now().Add(1 * time.Hour)}
	if !tok.Valid() {
		t.Error("future-expiring token should be valid")
	}

	// Token with ExpiresAt in the past should be invalid.
	tok = &Token{AccessToken: "tok", TokenType: "Bearer", ExpiresAt: time.Now().Add(-1 * time.Hour)}
	if tok.Valid() {
		t.Error("past-expiring token should be invalid")
	}

	// Empty access token should be invalid.
	tok = &Token{}
	if tok.Valid() {
		t.Error("empty token should be invalid")
	}
}
