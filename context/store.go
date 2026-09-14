package context

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	configDirName    = "sukko"
	contextsDirName  = "contexts"
	activeFileName   = "active-context"
	contextFilePerms = 0o600
	contextDirPerms  = 0o700
)

// ErrContextNotFound is returned when a named context does not exist.
var ErrContextNotFound = errors.New("context not found")

// ErrNoActiveContext is returned when no active context is set or the active context file is empty.
var ErrNoActiveContext = errors.New("no active context")

// validateContextName rejects names that could escape the contexts directory.
func validateContextName(name string) error {
	if name == "" {
		return errors.New("context name is required")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return fmt.Errorf("invalid context name %q: must not contain path separators or '..'", name)
	}
	if name != filepath.Base(name) {
		return fmt.Errorf("invalid context name %q: must be a plain filename", name)
	}
	return nil
}

// Store manages context files in ~/.config/sukko/contexts/.
type Store struct {
	dir string // path to contexts directory
	key []byte // machine-derived encryption key
}

// NewStore creates a Store, initializing the config directory and deriving the encryption key.
func NewStore() (*Store, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("user config dir: %w", err)
	}

	return NewStoreWithDir(filepath.Join(configDir, configDirName, contextsDirName))
}

// NewStoreWithDir creates a Store with a custom directory (for testing).
func NewStoreWithDir(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, contextDirPerms); err != nil {
		return nil, fmt.Errorf("create contexts dir: %w", err)
	}

	key, err := DeriveKey()
	if err != nil {
		return nil, fmt.Errorf("derive encryption key: %w", err)
	}

	return &Store{dir: dir, key: key}, nil
}

// Add writes a context to disk as <name>.json.
func (s *Store) Add(ctx *Context) error {
	if err := validateContextName(ctx.Name); err != nil {
		return err
	}

	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal context: %w", err)
	}

	path := s.contextPath(ctx.Name)
	if err := os.WriteFile(path, data, contextFilePerms); err != nil {
		return fmt.Errorf("write context file: %w", err)
	}

	return nil
}

// Get reads and returns a context by name.
func (s *Store) Get(name string) (*Context, error) {
	if err := validateContextName(name); err != nil {
		return nil, err
	}

	path := s.contextPath(name)
	data, err := os.ReadFile(path) //nolint:gosec // G304: path constructed from validated context name + fixed directory
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrContextNotFound, name)
		}
		return nil, fmt.Errorf("read context file: %w", err)
	}

	var ctx Context
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, fmt.Errorf("unmarshal context: %w", err)
	}

	return &ctx, nil
}

// List returns all stored contexts.
func (s *Store) List() ([]Context, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read contexts dir: %w", err)
	}

	var contexts []Context
	for _, entry := range entries {
		name, isJSON := strings.CutSuffix(entry.Name(), ".json")
		if entry.IsDir() || !isJSON {
			continue
		}
		ctx, err := s.Get(name)
		if err != nil {
			// Skip unreadable context files (permissions, I/O, corrupt JSON) — best-effort enumeration
			continue
		}
		contexts = append(contexts, *ctx)
	}

	return contexts, nil
}

// Remove deletes a context file. Also clears active context if it matches.
func (s *Store) Remove(name string) error {
	if err := validateContextName(name); err != nil {
		return err
	}

	path := s.contextPath(name)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrContextNotFound, name)
		}
		return fmt.Errorf("remove context file: %w", err)
	}

	// Best-effort: clear active context pointer if it referenced the removed context.
	active, err := s.ActiveName()
	if err == nil && active == name {
		_ = os.Remove(s.activePath())
	}

	return nil
}

// Active returns the currently active context.
func (s *Store) Active() (*Context, error) {
	name, err := s.ActiveName()
	if err != nil {
		return nil, err
	}
	return s.Get(name)
}

// ActiveName returns the name of the active context.
func (s *Store) ActiveName() (string, error) {
	data, err := os.ReadFile(s.activePath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: no active context set (use 'sukko context use <name>')", ErrNoActiveContext)
		}
		return "", fmt.Errorf("read active context: %w", err)
	}

	name := strings.TrimSpace(string(data))
	if name == "" {
		return "", fmt.Errorf("%w: active context file is empty", ErrNoActiveContext)
	}

	return name, nil
}

// SetActive sets the named context as active.
func (s *Store) SetActive(name string) error {
	if err := validateContextName(name); err != nil {
		return err
	}

	// Verify the context exists
	if _, err := s.Get(name); err != nil {
		return err
	}

	if err := os.WriteFile(s.activePath(), []byte(name), contextFilePerms); err != nil {
		return fmt.Errorf("write active context: %w", err)
	}

	return nil
}

// EncryptSecret encrypts a plaintext secret for storage in a context file.
func (s *Store) EncryptSecret(plaintext string) (string, error) {
	return Encrypt(s.key, plaintext)
}

// DecryptSecret decrypts an encrypted secret from a context file.
func (s *Store) DecryptSecret(ciphertext string) (string, error) {
	return Decrypt(s.key, ciphertext)
}

// Key returns a copy of the encryption key (for Context decrypt methods).
func (s *Store) Key() []byte {
	cp := make([]byte, len(s.key))
	copy(cp, s.key)
	return cp
}

// FindLocalContext returns the first context with Type=="local", or nil if none exists.
func (s *Store) FindLocalContext() (*Context, error) {
	contexts, err := s.List()
	if err != nil {
		return nil, fmt.Errorf("find local context: %w", err)
	}
	for _, ctx := range contexts {
		if ctx.Type == "local" {
			return &ctx, nil
		}
	}
	return nil, nil
}

// Dir returns the base contexts directory path.
func (s *Store) Dir() string {
	return s.dir
}

func (s *Store) contextPath(name string) string {
	return filepath.Join(s.dir, name+".json")
}

func (s *Store) activePath() string {
	return filepath.Join(filepath.Dir(s.dir), activeFileName)
}
