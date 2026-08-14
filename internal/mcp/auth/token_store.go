package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alayacore/alayacore/internal/config"
)

// TokenStore defines the interface for persisting and loading OAuth tokens.
// Implementations must be safe for concurrent use.
type TokenStore interface {
	// LoadToken returns a previously saved token for the given server ID,
	// or nil if no token exists.
	LoadToken(serverID string) (*Token, error)

	// SaveToken persists a token for the given server ID.
	SaveToken(serverID string, token *Token) error

	// DeleteToken removes a persisted token for the given server ID.
	DeleteToken(serverID string) error
}

// FileTokenStore persists OAuth tokens as key-value config files on disk.
// Tokens are stored in a single directory, one file per server.
// The directory is created on first use if it does not exist.
//
// Format: key-value with JSON values for complex types (same as mcp.conf).
type FileTokenStore struct {
	dir string
	mu  sync.RWMutex
}

// NewFileTokenStore creates a FileTokenStore rooted at dir.
// The directory will be created on the first write if it does not exist.
func NewFileTokenStore(dir string) *FileTokenStore {
	return &FileTokenStore{dir: dir}
}

// tokenFilePath returns the file path for a given server ID.
func (s *FileTokenStore) tokenFilePath(serverID string) string {
	return filepath.Join(s.dir, sanitizeFileTokenName(serverID)+".conf")
}

// sanitizeFileTokenName encodes a server name into a safe, unique file
// name. The encoding is injective: bytes from [A-Za-z0-9_-] are kept
// as-is, and every other byte is escaped as "~hh" (tilde + 2 hex digits).
// A literal '~' is itself escaped, so the mapping is reversible — two
// different server names can never map to the same file (e.g. "a/b" and
// "a_b" produce "a~2fb" and "a_b"; the old mapping collapsed both to
// "a_b", cross-contaminating their tokens).
func sanitizeFileTokenName(name string) string {
	const hex = "0123456789abcdef"
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_':
			result = append(result, c)
		default:
			result = append(result, '~', hex[c>>4], hex[c&0xf])
		}
	}
	return string(result)
}

// tokenFilePayload mirrors Token for serialization.
type tokenFilePayload struct {
	AccessToken      string   `config:"access_token"`
	TokenType        string   `config:"token_type"`
	RefreshToken     string   `config:"refresh_token,omitempty"`
	ExpiresAt        int64    `config:"expires_at,omitempty"` // Unix timestamp (seconds), 0 = no expiration
	Scopes           []string `config:"scopes,omitempty"`
	TokenEndpoint    string   `config:"token_endpoint,omitempty"`
	ClientID         string   `config:"client_id,omitempty"`
	ClientAuthMethod string   `config:"client_auth_method,omitempty"`
}

// LoadToken loads a persisted token for the given server ID.
// Returns nil if no token file exists.
func (s *FileTokenStore) LoadToken(serverID string) (*Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := s.tokenFilePath(serverID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read token file %s: %w", path, err)
	}

	var payload tokenFilePayload
	if errs := config.ParseKeyValue(string(data), &payload); len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.String())
		}
		return nil, fmt.Errorf("parse token file %s: %s", path, strings.Join(msgs, "; "))
	}

	return payloadToToken(&payload), nil
}

// payloadToToken converts a tokenFilePayload to a Token.
func payloadToToken(payload *tokenFilePayload) *Token {
	token := &Token{
		AccessToken:      payload.AccessToken,
		TokenType:        payload.TokenType,
		RefreshToken:     payload.RefreshToken,
		Scopes:           payload.Scopes,
		TokenEndpoint:    payload.TokenEndpoint,
		ClientID:         payload.ClientID,
		ClientAuthMethod: payload.ClientAuthMethod,
	}
	if payload.ExpiresAt > 0 {
		token.ExpiresAt = unixTime(payload.ExpiresAt)
	}
	return token
}

// SaveToken persists a token for the given server ID.
func (s *FileTokenStore) SaveToken(serverID string, token *Token) error {
	if token == nil {
		return s.DeleteToken(serverID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("create token dir %s: %w", s.dir, err)
	}

	payload := tokenFilePayload{
		AccessToken:      token.AccessToken,
		TokenType:        token.TokenType,
		RefreshToken:     token.RefreshToken,
		Scopes:           token.Scopes,
		TokenEndpoint:    token.TokenEndpoint,
		ClientID:         token.ClientID,
		ClientAuthMethod: token.ClientAuthMethod,
	}
	if !token.ExpiresAt.IsZero() {
		payload.ExpiresAt = token.ExpiresAt.Unix()
	}

	data := config.FormatKeyValue(&payload)

	path := s.tokenFilePath(serverID)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		return fmt.Errorf("write token file %s: %w", path, err)
	}

	return nil
}

// DeleteToken removes a persisted token file for the given server ID.
func (s *FileTokenStore) DeleteToken(serverID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.tokenFilePath(serverID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete token file %s: %w", path, err)
	}
	return nil
}

// unixTime converts a Unix timestamp (seconds) to time.Time.
func unixTime(sec int64) time.Time {
	return time.Unix(sec, 0)
}
