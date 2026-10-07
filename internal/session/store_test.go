package session

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func creds() *Credentials {
	return &Credentials{Environment: "production", ClientID: "client", RefreshToken: "refresh", JWT: "jwt"}
}

// Where there is no keychain, as on a Linux server or in a container, the
// credentials go to a file only the user can read, and say so.
func TestWithoutAKeychainCredentialsGoToAPrivateFile(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	dir := t.TempDir()
	store := newStoreIn(dir)

	where, err := store.Save(creds())
	if err != nil || !strings.Contains(where, "no system keychain is available") {
		t.Fatalf("where = %q, err = %v", where, err)
	}
	path := filepath.Join(dir, "credentials-production.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	got, err := store.Load("production")
	if err != nil || got == nil || got.RefreshToken != "refresh" {
		t.Errorf("loaded %+v, %v", got, err)
	}
	if got, _ := store.Load("staging"); got != nil {
		t.Errorf("another environment's credentials: %+v", got)
	}
}

// A file that was readable by others is made private when it is rewritten.
func TestARewrittenCredentialsFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not Unix permissions on Windows")
	}
	keyring.MockInitWithError(errors.New("no secret service"))
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials-production.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newStoreIn(dir).Save(creds()); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

// With a keychain, the credentials go there, and a file left from a time
// without one is removed so that it cannot shadow them.
func TestWithAKeychainAStaleFileIsRemoved(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	stale := filepath.Join(dir, "credentials-production.json")
	if err := os.WriteFile(stale, []byte(`{"environment":"production","refresh_token":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newStoreIn(dir)
	where, err := store.Save(creds())
	if err != nil || where != "the system keychain" {
		t.Fatalf("where = %q, err = %v", where, err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stale file is still there: %v", err)
	}
	if got, _ := store.Load("production"); got == nil || got.RefreshToken != "refresh" {
		t.Errorf("loaded %+v", got)
	}
}

// Credentials that cannot be read are no credentials: the user signs in again.
func TestUnreadableCredentialsAreNone(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "credentials-production.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := newStoreIn(dir).Load("production"); got != nil || err != nil {
		t.Errorf("got %+v, %v", got, err)
	}
}

// A keychain that never answers, as a locked Linux keyring over SSH, costs
// one wait at sign-in, and none afterwards: the session is in the file.
func TestAKeychainThatDoesNotAnswerIsPassedOver(t *testing.T) {
	store := newStoreIn(t.TempDir())
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	var asked atomic.Int32
	store.set = func(string, string, string) error { asked.Add(1); <-block; return nil }
	store.get = func(string, string) (string, error) { asked.Add(1); <-block; return "", nil }
	store.timeout = 10 * time.Millisecond

	where, err := store.Save(creds())
	if err != nil || !strings.Contains(where, "no system keychain is available") {
		t.Fatalf("where = %q, err = %v", where, err)
	}
	asked.Store(0)
	got, err := store.Load("production")
	if err != nil || got == nil || got.RefreshToken != "refresh" {
		t.Fatalf("loaded %+v, %v", got, err)
	}
	if n := asked.Load(); n != 0 {
		t.Errorf("the keychain was asked %d times with the session in the file", n)
	}
	// With nothing saved, Load gives up on the keychain and reports nothing.
	if got, err := store.Load("staging"); got != nil || err != nil {
		t.Errorf("loaded %+v, %v", got, err)
	}
}
