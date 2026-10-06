// Package tenjinapi holds operations on Tenjin's Campaign Management API:
// typed functions that know nothing about terminals. It is the part of this
// repository meant to move into the module shared with the MCP server.
package tenjinapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tenjin/tenjin-cli/internal/clierr"
)

// maxPageSize is the largest page the API serves. Its default is 25.
const maxPageSize = 1000

// TokenSource supplies the bearer token for each request.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Client calls the API.
type Client struct {
	BaseURL   string
	UserAgent string
	Tokens    TokenSource
	HTTP      *http.Client
}

// New returns a client for the API at baseURL.
func New(baseURL, userAgent string, tokens TokenSource) *Client {
	return &Client{
		BaseURL:   strings.TrimSuffix(baseURL, "/"),
		UserAgent: userAgent,
		Tokens:    tokens,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
	}
}

// App is a Tenjin app. One app is one bundle ID on one platform.
type App struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	BundleID      string `json:"bundle_id"`
	Platform      string `json:"platform"`
	StoreID       string `json:"store_id,omitempty"`
	SendingEvents bool   `json:"sending_events"`
	CreatedAt     string `json:"created_at,omitempty"`
}

// resource is one JSON:API object. Only the attributes the CLI shows are
// decoded: the API also returns shared secrets on an app, and leaving them
// out here keeps them out of every output.
type resource struct {
	ID         string `json:"id"`
	Attributes struct {
		Name          string `json:"name"`
		BundleID      string `json:"bundle_id"`
		Platform      string `json:"platform"`
		StoreID       string `json:"store_id"`
		SendingEvents bool   `json:"sending_events"`
		CreatedAt     string `json:"created_at"`
	} `json:"attributes"`
}

func (r resource) app() App {
	a := r.Attributes
	return App{ID: r.ID, Name: a.Name, BundleID: a.BundleID, Platform: a.Platform, StoreID: a.StoreID, SendingEvents: a.SendingEvents, CreatedAt: a.CreatedAt}
}

// ListApps returns every app the user can see, following all pages. query
// filters by name; empty means all.
func (c *Client) ListApps(ctx context.Context, query string) ([]App, error) {
	apps := []App{}
	for page := 1; ; page++ {
		params := url.Values{"page": {fmt.Sprint(page)}, "per_page": {fmt.Sprint(maxPageSize)}}
		if query != "" {
			params.Set("query", query)
		}
		var body struct {
			Data  []resource `json:"data"`
			Links struct {
				Next string `json:"next"`
			} `json:"links"`
		}
		if err := c.do(ctx, http.MethodGet, "/apps?"+params.Encode(), nil, &body); err != nil {
			return nil, err
		}
		for _, r := range body.Data {
			apps = append(apps, r.app())
		}
		if body.Links.Next == "" || len(body.Data) == 0 {
			return apps, nil
		}
	}
}

// FindApp returns the app with this bundle ID and platform, or nil. The API
// can only search by name, so this reads the full list.
func (c *Client) FindApp(ctx context.Context, bundleID, platform string) (*App, error) {
	apps, err := c.ListApps(ctx, "")
	if err != nil {
		return nil, err
	}
	for i := range apps {
		// Bundle IDs are matched as the collector matches them: exactly.
		if apps[i].BundleID == bundleID && apps[i].Platform == platform {
			return &apps[i], nil
		}
	}
	return nil, nil
}

// GetApp returns one app by ID.
func (c *Client) GetApp(ctx context.Context, id string) (*App, error) {
	var body struct {
		Data resource `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/apps/"+url.PathEscape(id), nil, &body); err != nil {
		return nil, err
	}
	app := body.Data.app()
	return &app, nil
}

// SDKKey is one of an app's SDK keys. Value is the key itself. It ships
// inside the app, so it is not a secret the way a token is, but the CLI still
// never prints it whole.
type SDKKey struct {
	ID        string
	Name      string
	Value     string
	CreatedAt string
}

// ErrKeysNotServed means the API has no SDK keys endpoint: it is not deployed
// yet, or not on this server.
var ErrKeysNotServed = errors.New("the Tenjin API does not serve SDK keys")

// ListSDKKeys returns an app's enabled, app-level SDK keys, oldest first. An
// app with none returns an empty list. The app must be one that exists, so
// "not found" here means the endpoint is missing, and is ErrKeysNotServed.
func (c *Client) ListSDKKeys(ctx context.Context, appID string) ([]SDKKey, error) {
	var body struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Name      string `json:"name"`
				Key       string `json:"key"`
				CreatedAt string `json:"created_at"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/apps/"+url.PathEscape(appID)+"/sdk_keys", nil, &body); err != nil {
		if clierr.From(err).Kind == clierr.NotFound {
			return nil, ErrKeysNotServed
		}
		return nil, err
	}
	keys := make([]SDKKey, 0, len(body.Data))
	for _, r := range body.Data {
		if r.Attributes.Key == "" {
			continue
		}
		keys = append(keys, SDKKey{ID: r.ID, Name: r.Attributes.Name, Value: r.Attributes.Key, CreatedAt: r.Attributes.CreatedAt})
	}
	return keys, nil
}

var existingAppID = regexp.MustCompile(`already exists with this bundle_id and platform: ([0-9a-fA-F-]{36})`)

// NewApp describes an app to create: either a store URL, from which Tenjin
// reads the name and bundle ID, or all of name, bundle ID and platform.
type NewApp struct {
	Name     string
	BundleID string
	Platform string
	StoreURL string
}

// CreateApp creates an app. A bundle ID and platform cannot be changed
// afterwards. If the app already exists, the existing one is returned with
// created false: creation is not idempotent on the server, so this makes it
// so.
func (c *Client) CreateApp(ctx context.Context, in NewApp) (app *App, created bool, err error) {
	request := map[string]string{}
	for field, value := range map[string]string{"name": in.Name, "bundle_id": in.BundleID, "platform": in.Platform, "store_url": in.StoreURL} {
		if value != "" {
			request[field] = value
		}
	}
	var body struct {
		Data resource `json:"data"`
	}
	err = c.do(ctx, http.MethodPost, "/apps", request, &body)
	if err != nil {
		// The server reports a duplicate as a plain 400 with the existing
		// app's ID inside the message.
		if m := existingAppID.FindStringSubmatch(err.Error()); m != nil {
			existing, getErr := c.GetApp(ctx, m[1])
			if getErr != nil {
				return nil, false, getErr
			}
			return existing, false, nil
		}
		return nil, false, err
	}
	created = true
	result := body.Data.app()
	return &result, created, nil
}

// do sends a request and decodes a JSON response into out.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	token, err := c.Tokens.Token(ctx)
	if err != nil {
		return err
	}
	var payload io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		payload = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return clierr.Wrap(clierr.Upstream, err, "could not reach the Tenjin API: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return clierr.Wrap(clierr.Upstream, err, "could not read the Tenjin API's response: %v", err)
	}
	if resp.StatusCode/100 != 2 {
		return apiError(resp, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return clierr.Wrap(clierr.Upstream, err, "the Tenjin API returned something that is not JSON (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// apiError classifies a failed response. The API's statuses need care: 403 is
// used both for an insufficient role and for a token it cannot decode, and
// 400 doubles as "not found".
func apiError(resp *http.Response, raw []byte) error {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	message := body.Error
	if message == "" {
		message = strings.TrimSpace(string(raw))
		if len(message) > 300 {
			message = message[:300] + "…"
		}
	}
	signIn := "Run 'tenjin login'."

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == 498,
		resp.StatusCode == http.StatusForbidden && strings.Contains(message, "DecodeError"):
		return clierr.New(clierr.NotSignedIn, "Tenjin did not accept the saved sign-in").WithHint("%s", signIn)
	case resp.StatusCode == http.StatusForbidden:
		return clierr.New(clierr.Permission, "your Tenjin role does not allow this").
			WithHint("Creating and changing apps needs a manager role in the organization. Ask a manager, or use an app that already exists.")
	case resp.StatusCode == http.StatusNotFound,
		resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(message), "not found"):
		return clierr.New(clierr.NotFound, "%s", firstNonEmpty(message, "not found"))
	case resp.StatusCode == http.StatusTooManyRequests:
		e := clierr.New(clierr.RateLimited, "Tenjin is rate limiting this token")
		if after := resp.Header.Get("X-Retry-After"); after != "" {
			return e.WithHint("Retry in %s seconds.", after)
		}
		return e
	case resp.StatusCode == http.StatusServiceUnavailable:
		return clierr.New(clierr.Upstream, "Tenjin is in read-only maintenance: %s", firstNonEmpty(message, "try again later"))
	case resp.StatusCode >= 500:
		return clierr.New(clierr.Upstream, "the Tenjin API failed (HTTP %d)", resp.StatusCode)
	}
	return clierr.New(clierr.Internal, "%s", firstNonEmpty(message, fmt.Sprintf("the Tenjin API refused the request (HTTP %d)", resp.StatusCode)))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
