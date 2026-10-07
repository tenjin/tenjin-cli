package session

import (
	"context"
	"testing"
	"time"

	"github.com/tenjin/tenjin-cli/internal/oauth"
)

// rotating is an authorization server whose refresh tokens can be used once.
type rotating struct {
	valid string
	n     int
}

func (a *rotating) Register(context.Context) (string, error) { return "client", nil }
func (a *rotating) Login(context.Context, oauth.LoginOptions) (oauth.Tokens, error) {
	return oauth.Tokens{}, nil
}
func (a *rotating) Revoke(context.Context, string, string) error { return nil }
func (a *rotating) Exchange(_ context.Context, access string) (oauth.Identity, error) {
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
