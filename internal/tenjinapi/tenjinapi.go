// Package tenjinapi holds operations on Tenjin's Campaign Management API:
// typed functions that know nothing about terminals. They call the clients
// generated from the API's specs (cma, rma, openapi), which are the part of
// this repository meant to move into the module shared with the MCP server.
package tenjinapi

import (
	"bytes"
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

	"github.com/google/uuid"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi/cma"
)

// maxPageSize is the largest page the API serves. Its default is 25.
const maxPageSize = 1000

// maxResponse caps what is read of one response.
const maxResponse = 32 << 20

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

// App is a Tenjin app. One app is one bundle ID on one platform. It holds
// only what the CLI shows: the API also returns shared secrets on an app, and
// copying out these fields alone keeps them out of every output.
type App struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	BundleID      string `json:"bundle_id"`
	Platform      string `json:"platform"`
	StoreID       string `json:"store_id,omitempty"`
	SendingEvents bool   `json:"sending_events"`
	CreatedAt     string `json:"created_at,omitempty"`
}

func appFrom(r cma.App) App {
	var app App
	if r.Id != nil {
		app.ID = r.Id.String()
	}
	a := r.Attributes
	if a == nil {
		return app
	}
	app.Name, app.BundleID, app.StoreID = value(a.Name), value(a.BundleId), value(a.StoreId)
	if a.Platform != nil {
		app.Platform = string(*a.Platform)
	}
	app.SendingEvents = a.SendingEvents != nil && *a.SendingEvents
	if a.CreatedAt != nil {
		app.CreatedAt = a.CreatedAt.Format(time.RFC3339)
	}
	return app
}

// ListApps returns every app the user can see, following all pages. query
// filters by name; empty means all.
func (c *Client) ListApps(ctx context.Context, query string) ([]App, error) {
	api, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	apps := []App{}
	for page := 1; ; page++ {
		params := &cma.GetAppsParams{Page: &page, PerPage: new(maxPageSize)}
		if query != "" {
			params.Query = &query
		}
		resp, err := api.GetAppsWithResponse(ctx, params)
		if err != nil {
			return nil, callError(err)
		}
		if err := statusError(resp.HTTPResponse, resp.Body, resp.JSON200 != nil); err != nil {
			return nil, err
		}
		data := value(resp.JSON200.Data)
		for _, r := range data {
			apps = append(apps, appFrom(r))
		}
		if len(data) == 0 || !hasNext(resp.JSON200.Links) {
			return apps, nil
		}
	}
}

// hasNext reports whether a list response links to a further page. The spec
// allows links with or without pagination, so the link is read from the raw
// object.
func hasNext(links interface{ MarshalJSON() ([]byte, error) }) bool {
	if links == nil {
		return false
	}
	raw, err := links.MarshalJSON()
	if err != nil {
		return false
	}
	var l struct {
		Next string `json:"next"`
	}
	return json.Unmarshal(raw, &l) == nil && l.Next != ""
}

// FindApp returns the app with this bundle ID and platform, or nil. The API
// can only search by name, so this reads the full list.
func (c *Client) FindApp(ctx context.Context, bundleID, platform string) (*App, error) {
	apps, err := c.ListApps(ctx, "")
	if err != nil {
		return nil, err
	}
	for i := range apps {
		// Bundle IDs are matched as Tenjin matches them: exactly.
		if apps[i].BundleID == bundleID && apps[i].Platform == platform {
			return &apps[i], nil
		}
	}
	return nil, nil
}

// GetApp returns one app by ID.
func (c *Client) GetApp(ctx context.Context, id string) (*App, error) {
	appID, err := uuid.Parse(id)
	if err != nil {
		return nil, clierr.New(clierr.NotFound, "no app has the ID %q: app IDs look like 3f2a9c1e-5b7d-4e8a-9c21-7d4e5f6a8b90", id)
	}
	api, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := api.GetAppsIdWithResponse(ctx, appID)
	if err != nil {
		return nil, callError(err)
	}
	if err := statusError(resp.HTTPResponse, resp.Body, resp.JSON200 != nil && resp.JSON200.Data != nil); err != nil {
		return nil, err
	}
	app := appFrom(*resp.JSON200.Data)
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
	id, err := uuid.Parse(appID)
	if err != nil {
		return nil, clierr.New(clierr.NotFound, "no app has the ID %q", appID)
	}
	api, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := api.GetAppsAppIdSdkKeysWithResponse(ctx, id, &cma.GetAppsAppIdSdkKeysParams{PerPage: new(maxPageSize)})
	if err != nil {
		return nil, callError(err)
	}
	if err := statusError(resp.HTTPResponse, resp.Body, resp.JSON200 != nil); err != nil {
		if clierr.From(err).Kind == clierr.NotFound {
			return nil, ErrKeysNotServed
		}
		return nil, err
	}
	data := value(resp.JSON200.Data)
	keys := make([]SDKKey, 0, len(data))
	for _, r := range data {
		if r.Attributes == nil || value(r.Attributes.Key) == "" {
			continue
		}
		key := SDKKey{ID: value(r.Id), Name: value(r.Attributes.Name), Value: *r.Attributes.Key}
		if r.Attributes.CreatedAt != nil {
			key.CreatedAt = r.Attributes.CreatedAt.Format(time.RFC3339)
		}
		keys = append(keys, key)
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
	// The body is written here, not from the spec's type: the spec requires
	// a name, bundle ID and platform, and leaves out store_url, which the API
	// accepts instead of them.
	request := map[string]string{}
	for field, value := range map[string]string{"name": in.Name, "bundle_id": in.BundleID, "platform": in.Platform, "store_url": in.StoreURL} {
		if value != "" {
			request[field] = value
		}
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, false, err
	}
	api, err := c.api(ctx)
	if err != nil {
		return nil, false, err
	}
	resp, err := api.PostAppsWithBodyWithResponse(ctx, "application/json", bytes.NewReader(raw))
	if err != nil {
		return nil, false, callError(err)
	}
	if err := statusError(resp.HTTPResponse, resp.Body, resp.JSON201 != nil && resp.JSON201.Data != nil); err != nil {
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
	result := appFrom(*resp.JSON201.Data)
	return &result, true, nil
}

// api returns the generated client, signed in for this call.
func (c *Client) api(ctx context.Context) (*cma.ClientWithResponses, error) {
	token, err := c.Tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	httpClient := *c.HTTP
	httpClient.Transport = limitedBody{next: transportOf(c.HTTP)}
	return cma.NewClientWithResponses(c.BaseURL,
		cma.WithHTTPClient(&httpClient),
		cma.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Accept", "application/json")
			req.Header.Set("User-Agent", c.UserAgent)
			return nil
		}))
}

// callError is a call that got no usable response: the request failed, or
// the body did not decode as the spec says.
func callError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return clierr.Wrap(clierr.Upstream, err, "could not reach the Tenjin API: %v", err)
	}
	return clierr.Wrap(clierr.Upstream, err, "could not read the Tenjin API's response: %v", err)
}

// statusError classifies a response. decoded says whether the success body
// the call expects was there.
func statusError(resp *http.Response, body []byte, decoded bool) error {
	if resp.StatusCode/100 != 2 {
		return apiError(resp, body)
	}
	if !decoded {
		return clierr.New(clierr.Upstream, "the Tenjin API returned something that is not JSON (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// apiError classifies a failed response. The API's statuses need care: 403 is
// used both for an insufficient role and for a token it cannot decode, and
// 400 doubles as "not found".
func apiError(resp *http.Response, raw []byte) error {
	status := resp.StatusCode
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
	case status == http.StatusUnauthorized, status == 498,
		status == http.StatusForbidden && strings.Contains(message, "DecodeError"):
		return clierr.New(clierr.NotSignedIn, "Tenjin did not accept the saved sign-in").WithHint("%s", signIn)
	case status == http.StatusForbidden:
		return clierr.New(clierr.Permission, "your Tenjin role does not allow this").
			WithHint("Creating and changing apps needs a manager role in the organization. Ask a manager, or use an app that already exists.")
	case status == http.StatusNotFound,
		status == http.StatusBadRequest && strings.Contains(strings.ToLower(message), "not found"):
		return clierr.New(clierr.NotFound, "%s", firstNonEmpty(message, "not found"))
	case status == http.StatusTooManyRequests:
		e := clierr.New(clierr.RateLimited, "Tenjin is rate limiting this token")
		if after := resp.Header.Get("X-Retry-After"); after != "" {
			return e.WithHint("Retry in %s seconds.", after)
		}
		return e
	case status == http.StatusServiceUnavailable:
		return clierr.New(clierr.Upstream, "Tenjin is in read-only maintenance: %s", firstNonEmpty(message, "try again later"))
	case status >= 500:
		return clierr.New(clierr.Upstream, "the Tenjin API failed (HTTP %d)", status)
	}
	return clierr.New(clierr.Internal, "%s", firstNonEmpty(message, fmt.Sprintf("the Tenjin API refused the request (HTTP %d)", status)))
}

// limitedBody caps each response body at maxResponse.
type limitedBody struct{ next http.RoundTripper }

func (l limitedBody) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := l.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.LimitReader(resp.Body, maxResponse), resp.Body}
	return resp, nil
}

func transportOf(c *http.Client) http.RoundTripper {
	if c.Transport != nil {
		return c.Transport
	}
	return http.DefaultTransport
}

func value[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
