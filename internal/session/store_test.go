package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/tenjin/tenjin-cli/internal/clierr"
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
	// With nothing in the file, a keychain that does not answer may still
	// hold a sign-in, so Load says it could not tell rather than "none".
	if got, err := store.Load("staging"); got != nil || !KeychainTimedOut(err) {
		t.Errorf("loaded %+v, %v", got, err)
	}
}

// Logout through a keychain that does not answer reports that, rather than
// "not signed in" with the sign-in left unrevoked.
func TestLogoutWhenTheKeychainDoesNotAnswer(t *testing.T) {
	store := newStoreIn(t.TempDir())
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	store.get = func(string, string) (string, error) { <-block; return "", nil }
	store.timeout = 10 * time.Millisecond
	m := &Manager{Environment: "production", Store: store}
	out, err := m.Logout(context.Background())
	if out.WasSignedIn || !KeychainTimedOut(err) {
		t.Errorf("logout = %+v, %v", out, err)
	}
	if _, err := m.Status(); !KeychainTimedOut(err) {
		t.Errorf("status: %v", err)
	}
	// Scripts see it as not being signed in, the hint saying what to do.
	if e := clierr.From(err); e.Kind != clierr.NotSignedIn || !strings.Contains(e.Hint, "Unlock the keychain") {
		t.Errorf("kind = %s, hint = %q", e.Kind, e.Hint)
	}
}

// Where the sign-in is kept is something a caller can ask: a file is
// readable by any program the user runs, the keychain is not.
func TestCredentialsFileSaysWhereTheSignInIs(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	dir := t.TempDir()
	store := newStoreIn(dir)
	if got := CredentialsFile(store, "production"); got != "" {
		t.Errorf("nothing stored: %q", got)
	}
	if _, err := store.Save(creds()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "credentials-production.json")
	if got := CredentialsFile(store, "production"); got != path {
		t.Errorf("signed in to a file: %q", got)
	}
	// Signed out, the file keeps only the client ID, which is no secret.
	if _, err := store.Save(&Credentials{Environment: "production", ClientID: "client"}); err != nil {
		t.Fatal(err)
	}
	if got := CredentialsFile(store, "production"); got != "" {
		t.Errorf("signed out: %q", got)
	}
	// A file that cannot be read as credentials may still hold a token.
	if err := os.WriteFile(path, []byte(`{"refresh_token":"tok`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := CredentialsFile(store, "production"); got != path {
		t.Errorf("an undecodable file: %q", got)
	}

	keyring.MockInit()
	if _, err := store.Save(creds()); err != nil {
		t.Fatal(err)
	}
	if got := CredentialsFile(store, "production"); got != "" {
		t.Errorf("signed in to the keychain: %q", got)
	}
	if got := CredentialsFile(&MemoryStore{}, "production"); got != "" {
		t.Errorf("memory: %q", got)
	}
}

// slowKeychain is a keychain whose first write is held until let go, as a
// locked keyring answers once someone unlocks it. Later calls answer at once.
type slowKeychain struct {
	mu      sync.Mutex
	secrets map[string]string
	writes  int
	letGo   chan struct{}
	// landed is closed when the held write has been made.
	landed chan struct{}
}

func newSlowKeychain() *slowKeychain {
	return &slowKeychain{secrets: map[string]string{}, letGo: make(chan struct{}), landed: make(chan struct{})}
}

func (k *slowKeychain) set(_, user, secret string) error {
	k.mu.Lock()
	k.writes++
	first := k.writes == 1
	k.mu.Unlock()
	if first {
		<-k.letGo
		defer close(k.landed)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.secrets[user] = secret
	return nil
}

func (k *slowKeychain) get(_, user string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if s, ok := k.secrets[user]; ok {
		return s, nil
	}
	return "", keyring.ErrNotFound
}

func withRefresh(token string) *Credentials {
	c := creds()
	c.RefreshToken = token
	return c
}

// A keychain write that outlives its wait lands before any later save's,
// never after it: the sign-in saved last is the one kept.
func TestALateKeychainWriteNeverReplacesALaterSave(t *testing.T) {
	keychain := newSlowKeychain()
	store := newStoreIn(t.TempDir())
	store.get, store.set = keychain.get, keychain.set
	store.timeout = 50 * time.Millisecond

	if where, err := store.Save(withRefresh("refresh-a")); err != nil || !strings.Contains(where, "no system keychain") {
		t.Fatalf("first save: %q, %v", where, err)
	}
	if _, err := store.Save(withRefresh("refresh-b")); err != nil {
		t.Fatal(err)
	}
	close(keychain.letGo)
	<-keychain.landed
	// Let any write queued behind the held one finish.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s, _ := keychain.get("", "production"); strings.Contains(s, "refresh-b") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got, err := store.Load("production"); err != nil || got.RefreshToken != "refresh-b" {
		t.Fatalf("loaded %+v, %v, want the later save", got, err)
	}
	if _, err := store.Save(withRefresh("refresh-c")); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load("production"); err != nil || got.RefreshToken != "refresh-c" {
		t.Errorf("loaded %+v, %v", got, err)
	}
	if s, _ := keychain.get("", "production"); !strings.Contains(s, "refresh-c") {
		t.Errorf("keychain holds %s", s)
	}
}

// Another tenjin cannot take the lock while a keychain write this one
// started is still on its way, so it cannot save under the lock and then
// have that write land over its sign-in. This tenjin takes it back at once.
func TestTheLockIsKeptUntilALateKeychainWriteLands(t *testing.T) {
	keychain := newSlowKeychain()
	dir := t.TempDir()
	store := newStoreIn(dir)
	store.get, store.set = keychain.get, keychain.set
	store.timeout = 50 * time.Millisecond

	unlock, err := store.Lock(context.Background(), "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(withRefresh("refresh-a")); err != nil {
		t.Fatal(err)
	}
	unlock()

	other := newStoreIn(dir)
	other.lockWait = 100 * time.Millisecond
	if _, err := other.Lock(context.Background(), "production"); !errors.Is(err, errLockBusy) {
		t.Fatalf("another tenjin took the lock with a write on its way: %v", err)
	}
	again, err := store.Lock(context.Background(), "production")
	if err != nil {
		t.Fatalf("this tenjin could not take its own lock back: %v", err)
	}
	again()

	close(keychain.letGo)
	<-keychain.landed
	other.lockWait = time.Second
	theirs, err := other.Lock(context.Background(), "production")
	if err != nil {
		t.Fatalf("the lock was not let go once the write landed: %v", err)
	}
	theirs()
}
