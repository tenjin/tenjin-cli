// Package session keeps the user signed in: it stores credentials, refreshes
// them when they expire, and hands the API a valid token.
package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

const keyringService = "tenjin-cli"

// Credentials are what one sign-in to one environment holds.
type Credentials struct {
	Environment string `json:"environment"`
	// ClientID identifies this CLI installation to the authorization server.
	// It is not a secret and survives sign-out so it can be reused.
	ClientID     string `json:"client_id"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// JWT is the short-lived credential the API accepts.
	JWT              string    `json:"jwt,omitempty"`
	JWTExpiry        time.Time `json:"jwt_expiry,omitempty"`
	UserID           string    `json:"user_id,omitempty"`
	OrganizationID   string    `json:"organization_id,omitempty"`
	OrganizationName string    `json:"organization_name,omitempty"`
}

// SignedIn reports whether the credentials can still obtain an API token.
func (c *Credentials) SignedIn() bool {
	return c != nil && c.RefreshToken != ""
}

// Store keeps credentials between runs, one set per environment.
type Store interface {
	// Load returns nil, nil when nothing is stored.
	Load(environment string) (*Credentials, error)
	// Save returns a description of where the credentials went.
	Save(c *Credentials) (location string, err error)
}

// keychainTimeout bounds each keychain call. A locked Linux keyring can wait
// for an unlock prompt that nobody will see, as over SSH; past this, the
// credentials go to the file.
const keychainTimeout = 5 * time.Second

// NewStore returns the store for this machine: the operating system's
// keychain, falling back to a private file where no keychain is available.
func NewStore() Store {
	dir, err := os.UserConfigDir()
	if err != nil {
		// No XDG_CONFIG_HOME and no HOME the usual way; the home folder still
		// outlasts a reboot, which the temporary folder does not.
		if home, homeErr := os.UserHomeDir(); homeErr == nil {
			dir = filepath.Join(home, ".config")
		} else {
			dir = os.TempDir()
		}
	}
	return newStoreIn(filepath.Join(dir, "tenjin"))
}

// newStoreIn is NewStore with the file kept in dir, for tests.
func newStoreIn(dir string) *systemStore {
	return &systemStore{dir: dir, get: keyring.Get, set: keyring.Set, timeout: keychainTimeout}
}

type systemStore struct {
	dir     string
	get     func(service, user string) (string, error)
	set     func(service, user, secret string) error
	timeout time.Duration
}

func (s *systemStore) file(environment string) string {
	return filepath.Join(s.dir, "credentials-"+environment+".json")
}

// The file is written only when the keychain failed, and removed whenever a
// save to the keychain succeeds, so when it exists it holds the latest
// credentials, and the keychain is not asked at all: where it is missing or
// locked, every command would otherwise wait on it first.
func (s *systemStore) Load(environment string) (*Credentials, error) {
	b, err := os.ReadFile(s.file(environment))
	var raw string
	switch {
	case err == nil:
		raw = string(b)
	case errors.Is(err, os.ErrNotExist):
		var keychainErr error
		raw, keychainErr = s.keychainGet(environment)
		if keychainErr != nil {
			// No entry, or no keychain at all: nothing is stored.
			return nil, nil
		}
	default:
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		// Unreadable credentials are the same as none: the user signs in again.
		return nil, nil
	}
	return &c, nil
}

func (s *systemStore) Save(c *Credentials) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	if err := s.keychainSet(c.Environment, string(raw)); err == nil {
		// A file left over from a time without a keychain must not shadow it.
		_ = os.Remove(s.file(c.Environment))
		return "the system keychain", nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	path := s.file(c.Environment)
	if err := writePrivate(path, raw); err != nil {
		return "", err
	}
	return path + " (no system keychain is available)", nil
}

var errKeychainTimeout = errors.New("the system keychain did not answer")

func (s *systemStore) keychainGet(environment string) (string, error) {
	type result struct {
		secret string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		secret, err := s.get(keyringService, environment)
		done <- result{secret, err}
	}()
	select {
	case r := <-done:
		return r.secret, r.err
	case <-time.After(s.timeout):
		return "", errKeychainTimeout
	}
}

func (s *systemStore) keychainSet(environment, secret string) error {
	done := make(chan error, 1)
	go func() { done <- s.set(keyringService, environment, secret) }()
	select {
	case err := <-done:
		return err
	case <-time.After(s.timeout):
		return errKeychainTimeout
	}
}

// writePrivate replaces a file with content only its owner can read, through
// a temporary file in the same folder: the file is never half written, and a
// file that was readable by others before is not any more.
func writePrivate(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// MemoryStore keeps credentials for the life of the process. Tests use it.
type MemoryStore struct {
	creds map[string]Credentials
}

func (m *MemoryStore) Load(environment string) (*Credentials, error) {
	c, ok := m.creds[environment]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

func (m *MemoryStore) Save(c *Credentials) (string, error) {
	if m.creds == nil {
		m.creds = map[string]Credentials{}
	}
	m.creds[c.Environment] = *c
	return "memory", nil
}
