package session

import (
	"context"
	"errors"
	"time"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/oauth"
)

// refreshMargin is how long before expiry a token is treated as expired, so
// that a request does not start with a token about to lapse.
const refreshMargin = time.Minute

// Authority is the part of the authorization server a session needs.
type Authority interface {
	Register(ctx context.Context) (clientID string, err error)
	Login(ctx context.Context, opts oauth.LoginOptions) (oauth.Tokens, error)
	Refresh(ctx context.Context, clientID, refreshToken string) (oauth.Tokens, error)
	Exchange(ctx context.Context, accessToken string) (oauth.Identity, error)
	Revoke(ctx context.Context, clientID, token string) error
}

// Manager owns the signed-in state for one environment.
type Manager struct {
	Environment string
	Store       Store
	Authority   Authority
	// APIToken, when set, is used as is and nothing is stored or refreshed.
	// It is how CI authenticates: a token created in the dashboard.
	APIToken string
	Now      func() time.Time
}

// Source says where the current credential comes from.
type Source string

const (
	SourceNone     Source = "none"
	SourceAPIToken Source = "TENJIN_API_TOKEN"
	SourceLogin    Source = "login"
)

// Status describes the signed-in state without contacting the server.
type Status struct {
	SignedIn         bool       `json:"signed_in"`
	Source           Source     `json:"source"`
	Environment      string     `json:"environment"`
	OrganizationID   string     `json:"organization_id,omitempty"`
	OrganizationName string     `json:"organization_name,omitempty"`
	UserID           string     `json:"user_id,omitempty"`
	TokenExpiry      *time.Time `json:"token_expiry,omitempty"`
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Status reports who is signed in.
func (m *Manager) Status() (Status, error) {
	s := Status{Source: SourceNone, Environment: m.Environment}
	if m.APIToken != "" {
		s.SignedIn, s.Source = true, SourceAPIToken
		return s, nil
	}
	c, err := m.Store.Load(m.Environment)
	if err != nil {
		return s, err
	}
	if c.SignedIn() {
		s.SignedIn, s.Source = true, SourceLogin
		s.OrganizationID, s.OrganizationName, s.UserID = c.OrganizationID, c.OrganizationName, c.UserID
		if !c.JWTExpiry.IsZero() {
			expiry := c.JWTExpiry
			s.TokenExpiry = &expiry
		}
	}
	return s, nil
}

// Login signs the user in through the browser and stores the result. It
// returns the credentials and where they were stored. pasted, when not nil,
// reads the address a browser on another machine ended on (see
// oauth.LoginOptions).
func (m *Manager) Login(ctx context.Context, open func(url string) error, pasted func() (string, error)) (*Credentials, string, error) {
	c, err := m.Store.Load(m.Environment)
	if err != nil {
		return nil, "", err
	}
	if c == nil {
		c = &Credentials{Environment: m.Environment}
	}
	if c.ClientID == "" {
		if c.ClientID, err = m.Authority.Register(ctx); err != nil {
			return nil, "", clierr.Wrap(clierr.Upstream, err, "%v", err)
		}
		// Keep the registration even if the sign-in is abandoned, so a retry
		// does not leave a second client behind on the server.
		if _, err := m.Store.Save(c); err != nil {
			return nil, "", err
		}
	}
	tokens, err := m.Authority.Login(ctx, oauth.LoginOptions{ClientID: c.ClientID, Open: open, Pasted: pasted})
	if err != nil {
		return nil, "", clierr.Wrap(clierr.NotSignedIn, err, "sign-in failed: %v", err)
	}
	identity, err := m.Authority.Exchange(ctx, tokens.AccessToken)
	if err != nil {
		return nil, "", clierr.Wrap(clierr.NotSignedIn, err, "sign-in failed: %v", err)
	}
	c.apply(tokens, identity)
	location, err := m.Store.Save(c)
	if err != nil {
		return nil, "", err
	}
	return c, location, nil
}

func (c *Credentials) apply(tokens oauth.Tokens, identity oauth.Identity) {
	if tokens.RefreshToken != "" {
		c.RefreshToken = tokens.RefreshToken
	}
	c.JWT, c.JWTExpiry = identity.JWT, identity.Expiry
	c.UserID, c.OrganizationID, c.OrganizationName = identity.UserID, identity.OrganizationID, identity.OrganizationName
}

// Token returns a credential the API accepts, refreshing it when needed.
func (m *Manager) Token(ctx context.Context) (string, error) {
	if m.APIToken != "" {
		return m.APIToken, nil
	}
	c, err := m.Store.Load(m.Environment)
	if err != nil {
		return "", err
	}
	if !c.SignedIn() {
		return "", notSignedIn("You are not signed in.")
	}
	if c.JWT != "" && m.now().Before(c.JWTExpiry.Add(-refreshMargin)) {
		return c.JWT, nil
	}

	tokens, err := m.Authority.Refresh(ctx, c.ClientID, c.RefreshToken)
	if err != nil {
		return "", m.refreshFailed(c, err)
	}
	// The refresh token rotates. Save the new one before anything else can
	// fail, or the stored one is dead and the user is signed out for no reason.
	if tokens.RefreshToken != "" {
		c.RefreshToken = tokens.RefreshToken
		if _, err := m.Store.Save(c); err != nil {
			return "", err
		}
	}
	identity, err := m.Authority.Exchange(ctx, tokens.AccessToken)
	if err != nil {
		return "", m.refreshFailed(c, err)
	}
	c.apply(tokens, identity)
	if _, err := m.Store.Save(c); err != nil {
		return "", err
	}
	return c.JWT, nil
}

func (m *Manager) refreshFailed(c *Credentials, err error) error {
	if errors.Is(err, oauth.ErrGrantInvalid) {
		// The grant was revoked or expired. Forget it so status is truthful.
		_, _ = m.Store.Save(&Credentials{Environment: c.Environment, ClientID: c.ClientID})
		return notSignedIn("Your sign-in has expired or was revoked.")
	}
	return clierr.Wrap(clierr.Upstream, err, "could not refresh the sign-in: %v", err)
}

func notSignedIn(message string) error {
	return clierr.New(clierr.NotSignedIn, "%s", message).WithHint("Run 'tenjin login', or set TENJIN_API_TOKEN to a token from the Tenjin dashboard.")
}

// Logout forgets the stored credentials and revokes the refresh token. It
// reports whether there was a sign-in to end. An API token already issued
// keeps working until it expires; the server offers no way to cancel it from
// here.
func (m *Manager) Logout(ctx context.Context) (wasSignedIn bool, err error) {
	c, err := m.Store.Load(m.Environment)
	if err != nil || !c.SignedIn() {
		return false, err
	}
	// Forget locally first: that must succeed even when the server is
	// unreachable.
	if _, err := m.Store.Save(&Credentials{Environment: c.Environment, ClientID: c.ClientID}); err != nil {
		return true, err
	}
	if err := m.Authority.Revoke(ctx, c.ClientID, c.RefreshToken); err != nil {
		return true, clierr.Wrap(clierr.Upstream, err, "signed out on this machine, but the server did not confirm the revocation: %v", err)
	}
	return true, nil
}
