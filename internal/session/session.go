package session

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/oauth"
)

// refreshMargin is how long before expiry a token is treated as expired, so
// that a request does not start with a token about to lapse.
const refreshMargin = time.Minute

// Authority is the part of the authorization server a session needs.
type Authority interface {
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
		if _, err := m.apiToken(); err != nil {
			return s, err
		}
		s.SignedIn, s.Source = true, SourceAPIToken
		return s, nil
	}
	c, err := m.Store.Load(m.Environment)
	if err != nil {
		return s, err
	}
	return m.StatusOf(c), nil
}

// StatusOf describes a saved session, whatever TENJIN_API_TOKEN holds.
func (m *Manager) StatusOf(c *Credentials) Status {
	s := Status{Source: SourceNone, Environment: m.Environment}
	if c.SignedIn() {
		s.SignedIn, s.Source = true, SourceLogin
		s.OrganizationID, s.OrganizationName, s.UserID = c.OrganizationID, c.OrganizationName, c.UserID
		if !c.JWTExpiry.IsZero() {
			expiry := c.JWTExpiry
			s.TokenExpiry = &expiry
		}
	}
	return s
}

// Login signs the user in through the browser and stores the result. It
// returns the credentials and where they were stored. pasted, when not nil,
// reads the address a browser on another machine ended on (see
// oauth.LoginOptions).
func (m *Manager) Login(ctx context.Context, open func(url string) error, pasted func() (string, error)) (*Credentials, string, error) {
	// The browser's code is redeemed under the refresh lock, so that the
	// session replaced is the current one: another tenjin may have refreshed
	// it while the browser was open. The lock is taken before the code is
	// spent, so a lock held past the wait ends the sign-in with no grant
	// issued. Ending a grant afterwards is no way out: revoking one of the
	// CLI's own ends every CLI session of that user in the organization,
	// the holder's included (see outlived).
	var unlock func()
	locked := error(nil)
	tokens, err := m.Authority.Login(ctx, oauth.LoginOptions{
		ClientID: oauth.ClientID, Open: open, Pasted: pasted,
		BeforeRedeem: func(ctx context.Context) error {
			release, err := m.lock(ctx)
			if err != nil {
				locked = err
				return err
			}
			unlock = release
			return nil
		},
	})
	if unlock != nil {
		defer unlock()
	}
	if errors.Is(locked, errLockBusy) {
		return nil, "", lockBusy(locked, "this sign-in was not completed",
			"Nothing was changed. Run 'tenjin login' again once the other command has finished.")
	}
	if locked != nil {
		return nil, "", locked
	}
	if err != nil {
		return nil, "", clierr.Wrap(clierr.NotSignedIn, err, "sign-in failed: %v", err)
	}
	if unlock == nil {
		// Saved without the lock, the sign-in could be lost to another
		// tenjin's refresh: an Authority must run BeforeRedeem.
		return nil, "", clierr.New(clierr.Internal, "the sign-in was redeemed without taking the sign-in lock, so it was not saved")
	}
	identity, err := m.Authority.Exchange(ctx, tokens.AccessToken)
	if err != nil {
		return nil, "", clierr.Wrap(clierr.NotSignedIn, err, "sign-in failed: %v", err)
	}
	c := &Credentials{Environment: m.Environment, ClientID: oauth.ClientID}
	c.apply(tokens, identity)
	// A keychain that does not answer hides the previous session, not the
	// new one, which goes to the file.
	previous, err := m.Store.Load(m.Environment)
	if err != nil && !KeychainTimedOut(err) {
		return nil, "", err
	}
	location, err := m.Store.Save(c)
	if err != nil {
		return nil, "", err
	}
	if outlived(previous, c) {
		_ = m.Authority.Revoke(ctx, previous.ClientID, previous.RefreshToken)
	}
	return c, location, nil
}

// outlived reports whether the previous session's grant stays open on the
// server once c replaces it, with nothing here able to end it: a grant of a
// client an earlier build registered for itself, or one for another user or
// organization. A session of the CLI's own client for the same user and
// organization is not revoked: the server would end the new session with it.
func outlived(previous, c *Credentials) bool {
	if !previous.SignedIn() || previous.ClientID == "" {
		return false
	}
	differs := func(before, now string) bool { return before != "" && now != "" && before != now }
	return previous.ClientID != c.ClientID ||
		differs(previous.OrganizationID, c.OrganizationID) || differs(previous.UserID, c.UserID)
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
		return m.apiToken()
	}
	c, err := m.Store.Load(m.Environment)
	if err != nil {
		return "", err
	}
	if !c.SignedIn() {
		return "", notSignedIn("You are not signed in.")
	}
	if m.current(c) {
		return c.JWT, nil
	}
	return m.refresh(ctx, "")
}

// Renew returns a token to use in place of one the API refused. The expiry
// is judged by this machine's clock, which can be wrong, so a refused token
// is refreshed even when it looks current. A token from TENJIN_API_TOKEN
// cannot be renewed.
func (m *Manager) Renew(ctx context.Context, refused string) (string, error) {
	if m.APIToken != "" {
		return "", m.Refused()
	}
	return m.refresh(ctx, refused)
}

// Refused is what to tell the user when the API refuses the token, naming
// where it came from.
func (m *Manager) Refused() *clierr.Error {
	if m.APIToken != "" {
		return clierr.New(clierr.NotSignedIn, "Tenjin did not accept the token in TENJIN_API_TOKEN").
			WithHint("Set TENJIN_API_TOKEN to a current access token from the Tenjin dashboard, or unset it and run 'tenjin login'.")
	}
	return clierr.New(clierr.NotSignedIn, "Tenjin did not accept the saved sign-in").WithHint("Run 'tenjin login'.")
}

// apiToken returns TENJIN_API_TOKEN, refusing one that could not be sent: a
// token is printable ASCII, with no spaces.
func (m *Manager) apiToken() (string, error) {
	if strings.ContainsFunc(m.APIToken, func(r rune) bool { return r <= ' ' || r > '~' }) {
		return "", clierr.New(clierr.Usage, "TENJIN_API_TOKEN holds a space, a line break or another character no Tenjin token has").
			WithHint("Set it to the access token alone, copied from the Tenjin dashboard.")
	}
	return m.APIToken, nil
}

func (m *Manager) current(c *Credentials) bool {
	return c.JWT != "" && m.now().Before(c.JWTExpiry.Add(-refreshMargin))
}

// refresh trades the refresh token for a new API token, one tenjin at a
// time: each refresh spends the refresh token, so two at once would leave one
// of them holding a dead one. refused, when not empty, is a token the API
// turned down, which must not be handed out again.
func (m *Manager) refresh(ctx context.Context, refused string) (string, error) {
	unlock, err := m.lock(ctx)
	if errors.Is(err, errLockBusy) {
		// Spending the refresh token now could spend it twice: the holder may
		// have spent it and not yet saved the one it got back.
		return "", lockBusy(err, "this command could not renew it",
			"Nothing was changed. Run this command again once the other one has finished.")
	}
	if err != nil {
		return "", err
	}
	defer unlock()
	// Another tenjin may have refreshed while this one waited: its result is
	// in the store.
	c, err := m.Store.Load(m.Environment)
	if err != nil {
		return "", err
	}
	if !c.SignedIn() {
		return "", notSignedIn("You are not signed in.")
	}
	if c.JWT != refused && m.current(c) {
		return c.JWT, nil
	}

	spent := c.RefreshToken
	tokens, err := m.Authority.Refresh(ctx, c.ClientID, spent)
	if errors.Is(err, oauth.ErrGrantInvalid) {
		// A tenjin that does not take the lock, such as an older version,
		// may have refreshed at the same moment and spent the token this one
		// used. Its result is in the store.
		if again, reloadErr := m.Store.Load(m.Environment); reloadErr == nil && again.SignedIn() && again.RefreshToken != spent {
			if again.JWT != refused && m.current(again) {
				return again.JWT, nil
			}
			c, spent = again, again.RefreshToken
			tokens, err = m.Authority.Refresh(ctx, c.ClientID, spent)
		}
	}
	if err != nil {
		return "", m.refreshFailed(c, spent, err)
	}
	// The refresh token rotates. Save the new one before anything else can
	// fail, or the stored one is dead and the user is signed out for no reason.
	if tokens.RefreshToken != "" {
		c.RefreshToken = tokens.RefreshToken
		if err := m.saveOver(spent, c); err != nil {
			return "", err
		}
	}
	identity, err := m.Authority.Exchange(ctx, tokens.AccessToken)
	if err != nil {
		// The refresh token just saved is good: keep it for the next try.
		return "", clierr.Wrap(clierr.Upstream, err, "Tenjin renewed the sign-in but did not give an API token for it: %v", err).
			WithHint("Try again. If it keeps failing, run 'tenjin login'.")
	}
	c.apply(tokens, identity)
	if err := m.saveOver(spent, c); err != nil {
		return "", err
	}
	return c.JWT, nil
}

// saveOver saves c, refreshed from the refresh token spent, unless the store
// has since moved on to another: then a tenjin that did not wait for the lock
// signed in, out or refreshed meanwhile, and what it saved stands.
func (m *Manager) saveOver(spent string, c *Credentials) error {
	stored, err := m.Store.Load(m.Environment)
	if err != nil {
		return err
	}
	if stored == nil || (stored.RefreshToken != spent && stored.RefreshToken != c.RefreshToken) {
		return nil
	}
	_, err = m.Store.Save(c)
	return err
}

// lock keeps other tenjin processes from refreshing, signing in or out
// until unlock is called. Where the store cannot lock, it goes ahead
// without. When another process holds the lock past the wait, it returns
// errLockBusy with a no-op unlock, and nothing may go ahead: the holder may
// have spent the refresh token and not saved the next, or have a keychain
// write still on its way that would land over a save made now.
func (m *Manager) lock(ctx context.Context) (unlock func(), err error) {
	locker, ok := m.Store.(Locker)
	if !ok {
		return func() {}, nil
	}
	unlock, err = locker.Lock(ctx, m.Environment)
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.Is(err, errLockBusy):
		return func() {}, errLockBusy
	case err != nil:
		return func() {}, nil
	}
	return unlock, nil
}

// lockBusy is the error for a lock another tenjin has held past the wait:
// outcome says what did not happen, hint what to do about it.
func lockBusy(err error, outcome, hint string) error {
	return clierr.Wrap(clierr.Busy, err, "another tenjin command has been using the sign-in for more than %d seconds, so %s", int(lockWait.Seconds()), outcome).
		WithHint("%s", hint)
}

func (m *Manager) refreshFailed(c *Credentials, spent string, err error) error {
	if errors.Is(err, oauth.ErrGrantInvalid) {
		// The grant was revoked or expired. Forget it so status is truthful.
		_ = m.saveOver(spent, &Credentials{Environment: c.Environment, ClientID: c.ClientID})
		return notSignedIn("Your sign-in has expired or was revoked.")
	}
	return clierr.Wrap(clierr.Upstream, err, "could not refresh the sign-in: %v", err)
}

func notSignedIn(message string) error {
	return clierr.New(clierr.NotSignedIn, "%s", message).WithHint("Run 'tenjin login', or set TENJIN_API_TOKEN to a token from the Tenjin dashboard.")
}

// Signout is what Logout did.
type Signout struct {
	// WasSignedIn is false when there was no sign-in to end.
	WasSignedIn bool
	// AccessEnded is true when the session's API access has ended: Tenjin
	// confirmed the revocation of a sign-in with the CLI's own client. A
	// session of a client an earlier build registered keeps its API token
	// until it expires.
	AccessEnded bool
}

// Logout forgets the stored credentials and revokes the refresh token. An
// error with WasSignedIn set means the session was forgotten here but its
// revocation was not confirmed.
func (m *Manager) Logout(ctx context.Context) (Signout, error) {
	// Under the refresh lock, so that the refresh token revoked is the
	// current one, not one another tenjin is about to spend.
	unlock, err := m.lock(ctx)
	if errors.Is(err, errLockBusy) {
		return Signout{}, lockBusy(err, "the sign-in was not ended",
			"Nothing was changed. Run 'tenjin logout' again once the other command has finished.")
	}
	if err != nil {
		return Signout{}, err
	}
	defer unlock()
	c, err := m.Store.Load(m.Environment)
	if err != nil || !c.SignedIn() {
		return Signout{}, err
	}
	// Forget locally first: that must succeed even when the server is
	// unreachable.
	if _, err := m.Store.Save(&Credentials{Environment: c.Environment}); err != nil {
		return Signout{}, err
	}
	// Signed out here; an error now means Tenjin did not confirm it.
	if err := m.Authority.Revoke(ctx, c.ClientID, c.RefreshToken); err != nil {
		return Signout{WasSignedIn: true}, err
	}
	return Signout{WasSignedIn: true, AccessEnded: c.ClientID == oauth.ClientID}, nil
}
