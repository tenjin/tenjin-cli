// Package session keeps the user signed in: it stores credentials, refreshes
// them when they expire, and hands the API a valid token.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/tenjin/tenjin-cli/internal/clierr"
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

// Locker is a Store that can keep other processes from refreshing the same
// sign-in at the same time. Lock waits for any other holder, a bounded time,
// and returns the function that lets go.
type Locker interface {
	Lock(ctx context.Context, environment string) (unlock func(), err error)
}

// lockWait bounds the wait for another tenjin's refresh, which takes a second
// or two. A holder that died lets go at once: the lock is the operating
// system's, on an open file, so it never outlives its process.
const lockWait = 30 * time.Second

var errLockBusy = errors.New("another tenjin is refreshing the sign-in")

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
	return &systemStore{dir: dir, get: keyring.Get, set: keyring.Set, timeout: keychainTimeout, lockWait: lockWait}
}

type systemStore struct {
	dir      string
	get      func(service, user string) (string, error)
	set      func(service, user, secret string) error
	timeout  time.Duration
	lockWait time.Duration
}

func (s *systemStore) file(environment string) string {
	return filepath.Join(s.dir, "credentials-"+environment+".json")
}

// Lock takes the lock file beside the credentials file, whether or not the
// credentials are in the keychain. The file stays; only the lock on it ends.
func (s *systemStore) Lock(ctx context.Context, environment string) (func(), error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "credentials-"+environment+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(s.lockWait)
	for {
		taken, err := tryLock(f)
		switch {
		case err != nil:
			_ = f.Close()
			return nil, err
		case taken:
			return func() {
				unlockFile(f)
				_ = f.Close()
			}, nil
		case time.Now().After(deadline):
			_ = f.Close()
			return nil, errLockBusy
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
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
		if errors.Is(keychainErr, errKeychainTimeout) {
			// A sign-in may be in there: saying there is none would have
			// logout leave it unrevoked. No session can be used, so a
			// script sees the exit status of not being signed in.
			return nil, clierr.Wrap(clierr.NotSignedIn, keychainErr, "the system keychain did not answer, so the saved sign-in could not be read").
				WithHint("Unlock the keychain and run this again: a locked keyring can wait on a password prompt nobody sees, as over SSH. Or set TENJIN_API_TOKEN to a token from the Tenjin dashboard.")
		}
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
	raw, err := json.Marshal(c) //nolint:gosec // G117: the session file is written 0600, below
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

// CredentialsFile returns the file that holds the sign-in to environment,
// or "" when it is in the system keychain or there is none. A program that
// can read the user's files, such as a coding agent, can read this one.
func CredentialsFile(s Store, environment string) string {
	if f, ok := s.(interface{ CredentialsFile(string) string }); ok {
		return f.CredentialsFile(environment)
	}
	return ""
}

// CredentialsFile is the store's side of the function above. A file that
// cannot be decoded counts, since it may still hold a token.
func (s *systemStore) CredentialsFile(environment string) string {
	path := s.file(environment)
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var c Credentials
	if json.Unmarshal(b, &c) == nil && c.RefreshToken == "" && c.JWT == "" {
		return ""
	}
	return path
}

var errKeychainTimeout = errors.New("the system keychain did not answer")

// KeychainTimedOut reports whether err is the keychain not answering, which
// leaves unknown whether anyone is signed in.
func KeychainTimedOut(err error) bool {
	return errors.Is(err, errKeychainTimeout)
}

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
	return replaceFile(tmp.Name(), path)
}

// replaceFile moves from over to. On Windows a file another process has
// open cannot be replaced, and another tenjin reading the session holds it
// only for a moment, so the move is tried again for about a second.
func replaceFile(from, to string) error {
	err := os.Rename(from, to)
	if runtime.GOOS != "windows" {
		return err
	}
	for wait := 5 * time.Millisecond; err != nil && wait <= 640*time.Millisecond; wait *= 2 {
		time.Sleep(wait)
		err = os.Rename(from, to)
	}
	return err
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
