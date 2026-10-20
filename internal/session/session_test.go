package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/oauth"
)

// rotating is an authorization server whose refresh tokens can be used once.
type rotating struct {
	valid string
	n     int
	// onExchange runs during each exchange; exchangeErr fails it.
	onExchange  func()
	exchangeErr error
}

func (a *rotating) Register(context.Context) (string, error) { return "client", nil }
func (a *rotating) Login(context.Context, oauth.LoginOptions) (oauth.Tokens, error) {
	return oauth.Tokens{}, nil
}
func (a *rotating) Revoke(context.Context, string, string) error { return nil }
func (a *rotating) Exchange(_ context.Context, access string) (oauth.Identity, error) {
	if a.onExchange != nil {
		a.onExchange()
	}
	if a.exchangeErr != nil {
		return oauth.Identity{}, a.exchangeErr
	}
	return oauth.Identity{JWT: "jwt-" + access, Expiry: time.Now().Add(2 * time.Hour)}, nil
}
func (a *rotating) Refresh(_ context.Context, _, refresh string) (oauth.Tokens, error) {
	if refresh != a.valid {
		return oauth.Tokens{}, oauth.ErrGrantInvalid
	}
	a.n++
	a.valid = "refresh-" + string(rune('0'+a.n))
	return oauth.Tokens{AccessToken: "access", RefreshToken: a.valid}, nil
}

// Two commands refreshing at once spend the same refresh token: the one that
// loses takes the winner's sign-in instead of signing the user out.
func TestARefreshLostToAnotherProcessKeepsTheSignIn(t *testing.T) {
	store := &MemoryStore{}
	expired := &Credentials{Environment: "production", ClientID: "client", RefreshToken: "refresh-0", JWT: "old", JWTExpiry: time.Now().Add(-time.Hour)}
	if _, err := store.Save(expired); err != nil {
		t.Fatal(err)
	}
	auth := &rotating{valid: "refresh-0"}
	winner := &Manager{Environment: "production", Store: store, Authority: auth}
	// The loser read the store before the winner refreshed.
	loser := &Manager{Environment: "production", Store: &staleOnce{Store: store, first: *expired}, Authority: auth}

	if _, err := winner.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if jwt, err := loser.Token(context.Background()); err != nil || jwt == "" {
		t.Fatalf("loser: %q, %v", jwt, err)
	}
	if c, _ := store.Load("production"); !c.SignedIn() {
		t.Error("the user was signed out")
	}
}

// staleOnce answers the first Load with what the store held earlier.
type staleOnce struct {
	Store
	first  Credentials
	served bool
}

func (s *staleOnce) Load(environment string) (*Credentials, error) {
	if !s.served {
		s.served = true
		c := s.first
		return &c, nil
	}
	return s.Store.Load(environment)
}

// fileStoreIn is the system store in dir with no keychain: the credentials
// go to a file, and the lock file beside it is real. Two of them on one
// folder are two processes.
func fileStoreIn(dir string) *systemStore {
	store := newStoreIn(dir)
	none := errors.New("no keychain")
	store.get = func(string, string) (string, error) { return "", none }
	store.set = func(string, string, string) error { return none }
	return store
}

func signedIn(t *testing.T, store Store, refresh, jwt string, expiry time.Time) {
	t.Helper()
	if _, err := store.Save(&Credentials{Environment: "production", ClientID: "client", RefreshToken: refresh, JWT: jwt, JWTExpiry: expiry}); err != nil {
		t.Error(err)
	}
}

// A token this machine's clock still thinks current can be one the API has
// let lapse. Renewing it refreshes the sign-in, whatever the clock says.
func TestARefusedTokenIsRenewedEvenWhenItLooksCurrent(t *testing.T) {
	store := fileStoreIn(t.TempDir())
	signedIn(t, store, "refresh-0", "lapsed", time.Now().Add(time.Hour))
	auth := &rotating{valid: "refresh-0"}
	m := &Manager{Environment: "production", Store: store, Authority: auth}

	if jwt, err := m.Token(context.Background()); err != nil || jwt != "lapsed" {
		t.Fatalf("Token = %q, %v", jwt, err)
	}
	jwt, err := m.Renew(context.Background(), "lapsed")
	if err != nil || jwt != "jwt-access" || auth.n != 1 {
		t.Fatalf("Renew = %q, %v, after %d refreshes", jwt, err, auth.n)
	}
	if c, _ := store.Load("production"); c.RefreshToken != "refresh-1" || c.JWT != "jwt-access" {
		t.Errorf("stored %+v", c)
	}
	// Another request renewing the same refused token takes the new one.
	if jwt, err := m.Renew(context.Background(), "lapsed"); err != nil || jwt != "jwt-access" || auth.n != 1 {
		t.Errorf("second Renew = %q, %v, after %d refreshes", jwt, err, auth.n)
	}
}

// A token from TENJIN_API_TOKEN is not renewed, and its refusal names it.
func TestAnAPITokenIsNotRenewed(t *testing.T) {
	m := &Manager{Environment: "production", Store: &MemoryStore{}, Authority: &rotating{}, APIToken: "dashboard-token"}
	_, err := m.Renew(context.Background(), "dashboard-token")
	e := clierr.From(err)
	if e.Kind != clierr.NotSignedIn || !strings.Contains(e.Message, "TENJIN_API_TOKEN") || strings.Contains(e.Message, "saved sign-in") ||
		!strings.Contains(e.Hint, "TENJIN_API_TOKEN") || strings.Contains(e.Message+e.Hint, "dashboard-token") {
		t.Errorf("message %q, hint %q", e.Message, e.Hint)
	}
}

// A token with a space or a line break inside cannot be sent; it is refused
// before any request, without being printed.
func TestAnAPITokenThatCannotBeSentIsRefused(t *testing.T) {
	for _, token := range []string{"xy zzy", "xy\nzzy", "xy\x1bzzy", "xyøzzy"} {
		m := &Manager{Environment: "production", Store: &MemoryStore{}, APIToken: token}
		_, err := m.Token(context.Background())
		e := clierr.From(err)
		if e.Kind != clierr.Usage || !strings.Contains(e.Message, "TENJIN_API_TOKEN") || strings.Contains(e.Message+e.Hint, "zzy") {
			t.Errorf("%q: %v", token, err)
		}
		if _, err := m.Status(); clierr.From(err).Kind != clierr.Usage {
			t.Errorf("%q: status err = %v", token, err)
		}
	}
}

// While another tenjin refreshes, this one waits for it and takes its
// result, instead of spending the same refresh token.
func TestARefreshWaitsForAnotherProcessAndTakesItsResult(t *testing.T) {
	dir := t.TempDir()
	store := fileStoreIn(dir)
	signedIn(t, store, "refresh-0", "old", time.Now().Add(-time.Hour))
	auth := &rotating{valid: "refresh-0"}
	m := &Manager{Environment: "production", Store: store, Authority: auth}

	other := fileStoreIn(dir)
	unlock, err := other.Lock(context.Background(), "production")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		jwt, err := m.Token(context.Background())
		if err != nil {
			jwt = err.Error()
		}
		done <- jwt
	}()
	signedIn(t, other, "refresh-9", "theirs", time.Now().Add(time.Hour))
	unlock()

	if jwt := <-done; jwt != "theirs" {
		t.Errorf("Token = %q", jwt)
	}
	if auth.n != 0 {
		t.Errorf("refreshed %d times", auth.n)
	}
}

// A lock held past the wait does not hold the command forever.
func TestALockHeldTooLongIsNotWaitedOnForever(t *testing.T) {
	dir := t.TempDir()
	store := fileStoreIn(dir)
	store.lockWait = 100 * time.Millisecond
	signedIn(t, store, "refresh-0", "old", time.Now().Add(-time.Hour))
	unlock, err := fileStoreIn(dir).Lock(context.Background(), "production")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := store.Lock(context.Background(), "production"); !errors.Is(err, errLockBusy) {
		t.Errorf("Lock = %v, want busy", err)
	}
	m := &Manager{Environment: "production", Store: store, Authority: &rotating{valid: "refresh-0"}}
	if jwt, err := m.Token(context.Background()); err != nil || jwt != "jwt-access" {
		t.Errorf("Token = %q, %v", jwt, err)
	}
}

// A lock is let go when its holder ends, so the next one takes it at once.
func TestALockIsLetGo(t *testing.T) {
	dir := t.TempDir()
	unlock, err := fileStoreIn(dir).Lock(context.Background(), "production")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	next := fileStoreIn(dir)
	next.lockWait = 0
	unlock, err = next.Lock(context.Background(), "production")
	if err != nil {
		t.Fatalf("Lock after unlock = %v", err)
	}
	unlock()
}

// A tenjin that does not take the lock, such as an older version, may
// refresh in the middle of this one's refresh. What it saved is newer and
// stays.
func TestARefreshNeverOverwritesANewerSignIn(t *testing.T) {
	store := fileStoreIn(t.TempDir())
	signedIn(t, store, "refresh-0", "old", time.Now().Add(-time.Hour))
	auth := &rotating{valid: "refresh-0"}
	auth.onExchange = func() { signedIn(t, store, "refresh-9", "theirs", time.Now().Add(time.Hour)) }
	m := &Manager{Environment: "production", Store: store, Authority: auth}

	if jwt, err := m.Token(context.Background()); err != nil || jwt != "jwt-access" {
		t.Fatalf("Token = %q, %v", jwt, err)
	}
	if c, _ := store.Load("production"); c.RefreshToken != "refresh-9" || c.JWT != "theirs" {
		t.Errorf("stored %+v", c)
	}
}

// An exchange that fails after a good refresh leaves the new refresh token
// in place: the sign-in still works, and the next command tries again.
func TestAFailedExchangeKeepsTheNewRefreshToken(t *testing.T) {
	store := fileStoreIn(t.TempDir())
	signedIn(t, store, "refresh-0", "old", time.Now().Add(-time.Hour))
	auth := &rotating{valid: "refresh-0", exchangeErr: oauth.ErrGrantInvalid}
	m := &Manager{Environment: "production", Store: store, Authority: auth}

	if _, err := m.Token(context.Background()); clierr.From(err).Kind != clierr.Upstream {
		t.Errorf("err = %v", err)
	}
	c, _ := store.Load("production")
	if !c.SignedIn() || c.RefreshToken != "refresh-1" {
		t.Fatalf("stored %+v", c)
	}
	auth.exchangeErr = nil
	if jwt, err := m.Token(context.Background()); err != nil || jwt != "jwt-access" {
		t.Errorf("next Token = %q, %v", jwt, err)
	}
}

// A grant refused while another tenjin had already moved the sign-in on
// does not erase what that one saved.
func TestARevokedGrantDoesNotEraseANewerSignIn(t *testing.T) {
	store := fileStoreIn(t.TempDir())
	signedIn(t, store, "refresh-0", "old", time.Now().Add(-time.Hour))
	m := &Manager{Environment: "production", Store: store, Authority: &rotating{valid: "revoked"}}
	c, _ := store.Load("production")
	signedIn(t, store, "refresh-9", "theirs", time.Now().Add(time.Hour))
	_ = m.refreshFailed(c, "refresh-0", oauth.ErrGrantInvalid)
	if c, _ := store.Load("production"); c.RefreshToken != "refresh-9" {
		t.Errorf("stored %+v", c)
	}
}
