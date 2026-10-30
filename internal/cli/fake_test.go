package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tenjin/tenjin-cli/internal/config"
	"github.com/tenjin/tenjin-cli/internal/session"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi"
)

// fakeTenjin stands in for the three services the CLI talks to: the
// authorization server, the API and Tenjin, answering as they do.
type fakeTenjin struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex

	challenge    string            // PKCE challenge of the pending authorization
	refreshToken string            // the one currently valid; rotates on use
	refreshOwner string            // the client refreshToken was issued to
	accessTokens map[string]string // access token -> client
	jwts         map[string]string // API token -> the client it came from
	// revocations records each revocation asked for, "client token";
	// revokeFails makes the server answer 503 instead.
	revocations []string
	revokeFails bool
	issued      int

	apps       []tenjinapi.App
	pageSize   int
	denyCreate bool
	// exchangeExpiresIn is the exchange's expires_in, in seconds; 0 leaves
	// it out, as an older server does.
	exchangeExpiresIn int
	keyAnswers        map[string]string // SDK key -> Tenjin's message; default accepts
	// keyApps ties an SDK key to one app, "bundle|platform"; it is refused
	// for any other app.
	keyApps map[string]string
	// sdkKeys are the keys the API serves, by app ID. Nil means the API has
	// no keys endpoint.
	sdkKeys   map[string][]string
	denyKeys  bool
	keysAsked []string
	// installs maps an installation ID Tenjin has processed to the campaign
	// it was attributed to.
	installs      map[string]string
	installsAsked []string
	// data answers the data commands' reads, by path; a path it has no
	// answer for gets a one-item list. dataAsked records each read.
	data      map[string]func(r *http.Request) (int, any)
	dataAsked []*url.URL
	// writes records the API writes the data commands make; writeAnswers
	// answers them by "METHOD path", else with the body sent back as the
	// resource (and {} for a delete).
	writes       []apiWrite
	writeAnswers map[string]func(r *http.Request) (int, any)
	// callbacks says whether each app callback is active, by ID; one not
	// listed is. As the API does, an update that does not send active
	// switches the callback on.
	callbacks map[string]bool
	// siteIDRules are the Site ID Optimization rules; siteIDCalls records
	// each call as "Method body".
	siteIDRules []map[string]any
	siteIDCalls []string
	// docs answers the documentation search.
	docs []map[string]string
}

type apiWrite struct {
	method, path string
	body         map[string]any
}

func newFakeTenjin(t *testing.T) *fakeTenjin {
	t.Helper()
	f := &fakeTenjin{t: t, accessTokens: map[string]string{}, jwts: map[string]string{}, pageSize: 1000, exchangeExpiresIn: 7200, keyAnswers: map[string]string{}, keyApps: map[string]string{}, installs: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", f.metadata)
	mux.HandleFunc("GET /oauth/authorize", f.authorize)
	mux.HandleFunc("POST /oauth/token", f.token)
	mux.HandleFunc("POST /oauth/revoke", f.revoke)
	mux.HandleFunc("GET /v2/apps", f.listApps)
	mux.HandleFunc("POST /v2/apps", f.createApp)
	mux.HandleFunc("GET /v2/apps/{id}", f.getApp)
	mux.HandleFunc("GET /v2/apps/{id}/sdk_keys", f.listKeys)
	mux.HandleFunc("GET /v2/", f.dataRead)
	mux.HandleFunc("PATCH /v2/", f.dataWrite)
	mux.HandleFunc("DELETE /v2/", f.dataWrite)
	mux.HandleFunc("POST /v2/", f.dataWrite)
	mux.HandleFunc("POST /management.v2.SiteIDFilter/{method}", f.siteIDs)
	mux.HandleFunc("GET /wp-json/betterdocs/v1/search", f.searchDocs)
	mux.HandleFunc("POST /v0/event", f.event)
	mux.HandleFunc("GET /v0/user", f.user)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTenjin) endpoints() config.Endpoints {
	return config.Endpoints{Name: "test", Issuer: f.srv.URL, API: f.srv.URL + "/v2", Track: f.srv.URL, Resource: "https://mcp.test", SiteIDs: f.srv.URL, Docs: f.srv.URL}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeTenjin) metadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"issuer":                 f.srv.URL,
		"authorization_endpoint": f.srv.URL + "/oauth/authorize",
		"token_endpoint":         f.srv.URL + "/oauth/token",
		"revocation_endpoint":    f.srv.URL + "/oauth/revoke",
	})
}

// authorize plays the browser and the consenting user: it sends the caller
// straight back to the loopback redirect with a code.
func (f *fakeTenjin) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	if q.Get("client_id") != "tenjin-cli" || q.Get("resource") != "https://mcp.test" || q.Get("code_challenge_method") != "S256" || q.Get("scope") != "mcp" ||
		!strings.HasPrefix(redirect, "http://127.0.0.1:") || !strings.HasSuffix(redirect, "/callback") {
		http.Error(w, "invalid_target", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.challenge = q.Get("code_challenge")
	f.mu.Unlock()
	http.Redirect(w, r, redirect+"?"+url.Values{"code": {"auth-code"}, "state": {q.Get("state")}, "iss": {f.srv.URL}}.Encode(), http.StatusFound)
}

func (f *fakeTenjin) issue(client string) (access, refresh string) {
	f.issued++
	access, refresh = fmt.Sprintf("access-%d", f.issued), fmt.Sprintf("refresh-%d", f.issued)
	f.accessTokens[access] = client
	f.refreshToken, f.refreshOwner = refresh, client
	return access, refresh
}

func (f *fakeTenjin) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	invalid := func(description string) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": description})
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		if r.PostForm.Get("client_id") != "tenjin-cli" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
			return
		}
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if r.PostForm.Get("code") != "auth-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			invalid("bad code or verifier")
			return
		}
		access, refresh := f.issue("tenjin-cli")
		writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 7200})
	case "refresh_token":
		// A refresh token is good only with the client it was issued to.
		if f.refreshToken == "" || r.PostForm.Get("refresh_token") != f.refreshToken || r.PostForm.Get("client_id") != f.refreshOwner {
			invalid("refresh token is revoked or unknown")
			return
		}
		access, refresh := f.issue(f.refreshOwner)
		writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 7200})
	case "urn:ietf:params:oauth:grant-type:token-exchange":
		client := f.accessTokens[r.PostForm.Get("subject_token")]
		if client == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request", "error_description": "subject_token is missing or invalid"})
			return
		}
		jwt := makeJWT(time.Now().Add(2*time.Hour), f.issued)
		f.jwts[jwt] = client
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": jwt, "user_id": "user-1", "organization_id": "org-1", "organization_name": "Acme Games", "expires_in": f.exchangeExpiresIn,
		})
	default:
		invalid("unsupported grant")
	}
}

func makeJWT(expiry time.Time, n int) string {
	payload, _ := json.Marshal(map[string]any{"exp": expiry.Unix(), "n": n})
	return "eyJhbGciOiJFUzUxMiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

// revoke ends the ability to refresh. For the CLI's own client it also ends
// the API access exchanged from it; for an older version's registered client,
// a JWT already exchanged keeps working until it expires.
func (f *fakeTenjin) revoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	f.revocations = append(f.revocations, r.PostForm.Get("client_id")+" "+r.PostForm.Get("token"))
	if f.revokeFails {
		f.mu.Unlock()
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporarily_unavailable"})
		return
	}
	if token := r.PostForm.Get("token"); token == f.refreshToken {
		if r.PostForm.Get("client_id") != f.refreshOwner {
			f.mu.Unlock()
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "unauthorized_client"})
			return
		}
		f.refreshToken = ""
		if f.refreshOwner == "tenjin-cli" {
			for jwt, client := range f.jwts {
				if client == "tenjin-cli" {
					delete(f.jwts, jwt)
				}
			}
		}
	}
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{})
}

// dataRead answers the API's other reads.
func (f *fakeTenjin) dataRead(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(w, r) {
		return
	}
	f.mu.Lock()
	f.dataAsked = append(f.dataAsked, r.URL)
	answer := f.data[r.URL.Path]
	id, active, isCallback := f.callback(r.URL.Path)
	f.mu.Unlock()
	if answer == nil && isCallback {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"id": id, "type": "callback", "attributes": map[string]any{"name": "First", "active": active}}})
		return
	}
	if answer == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"data":  []any{map[string]any{"id": "00000000-0000-4000-8000-0000000000aa", "type": "thing", "attributes": map[string]any{"name": "First"}}},
			"links": map[string]string{"self": f.srv.URL + r.URL.Path},
		})
		return
	}
	status, body := answer(r)
	writeJSON(w, status, body)
}

// siteIDs answers the Site ID Optimization service, in Connect's JSON form.
func (f *fakeTenjin) siteIDs(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Connect-Protocol-Version") != "1" || r.Header.Get("Content-Type") != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"code": "unknown", "message": "not Connect JSON"})
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	signedIn := f.jwts[token] != "" || token == "dashboard-token"
	f.mu.Unlock()
	if !signedIn {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthenticated", "message": "invalid token"})
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Rules   []map[string]any `json:"rules"`
		Clear   bool             `json:"clear"`
		SiteIDs []string         `json:"siteIds"`
	}
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.siteIDCalls = append(f.siteIDCalls, r.PathValue("method")+" "+string(raw))
	switch r.PathValue("method") {
	case "Get":
		if len(f.siteIDRules) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rules": f.siteIDRules})
		return
	case "Block":
		if body.Clear {
			f.siteIDRules = nil
		}
		f.siteIDRules = append(f.siteIDRules, body.Rules...)
	case "Reinstate":
		var kept []map[string]any
		for _, rule := range f.siteIDRules {
			if !contains(body.SiteIDs, fmt.Sprint(rule["siteId"])) {
				kept = append(kept, rule)
			}
		}
		f.siteIDRules = kept
	case "Clear":
		f.siteIDRules = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (f *fakeTenjin) searchDocs(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "" {
		f.t.Errorf("a token was sent to the public documentation")
	}
	out := []map[string]string{}
	for _, d := range f.docs {
		if strings.Contains(strings.ToLower(d["title"]), strings.ToLower(r.URL.Query().Get("s"))) {
			out = append(out, d)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// dataWrite answers the API's writes.
func (f *fakeTenjin) dataWrite(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(w, r) {
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.writes = append(f.writes, apiWrite{method: r.Method, path: r.URL.Path, body: body})
	answer := f.writeAnswers[r.Method+" "+r.URL.Path]
	if id, _, ok := f.callback(r.URL.Path); ok && r.Method == http.MethodPatch && answer == nil {
		active, sent := body["active"].(bool)
		if f.callbacks == nil {
			f.callbacks = map[string]bool{}
		}
		f.callbacks[id] = active || !sent
	}
	f.mu.Unlock()
	switch {
	case answer != nil:
		status, out := answer(r)
		writeJSON(w, status, out)
	case r.Method == http.MethodDelete:
		writeJSON(w, http.StatusOK, map[string]any{})
	case r.Method == http.MethodPost:
		writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"id": "00000000-0000-4000-8000-0000000000bb", "type": "thing", "attributes": body}})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"id": "00000000-0000-4000-8000-0000000000bb", "type": "thing", "attributes": body}})
	}
}

// callback reads the callback a path names, if it names one, under f.mu.
func (f *fakeTenjin) callback(path string) (id string, active, ok bool) {
	rest, found := strings.CutPrefix(path, "/v2/apps/")
	if !found {
		return "", false, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[1] != "callbacks" {
		return "", false, false
	}
	active, listed := f.callbacks[parts[2]]
	return parts[2], active || !listed, true
}

func (f *fakeTenjin) authorized(w http.ResponseWriter, r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	ok := f.jwts[token] != ""
	f.mu.Unlock()
	switch {
	case ok || token == "dashboard-token":
		return true
	case strings.Count(token, ".") != 2:
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "AuthHandler::DecodeError"})
	default:
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized. Invalid access token provided."})
	}
	return false
}

func appResource(a tenjinapi.App) map[string]any {
	return map[string]any{"id": a.ID, "type": "app", "attributes": map[string]any{
		"name": a.Name, "bundle_id": a.BundleID, "platform": a.Platform, "sending_events": a.SendingEvents,
		"created_at": "2026-10-06T12:34:56.123Z",
		// The real API returns shared secrets here. They must never reach output.
		"ios_shared_secret": "TOP-SECRET-SHARED-SECRET",
		// Fields the CLI does not show, with values the spec does not expect:
		// they must not fail the app, or the list it is in.
		"price": 0.99, "rating": 4.5, "released_on": "",
	}}
}

func (f *fakeTenjin) listApps(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(w, r) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	page := 1
	_, _ = fmt.Sscan(r.URL.Query().Get("page"), &page)
	start := (page - 1) * f.pageSize
	data := []map[string]any{}
	for i := start; i < len(f.apps) && i < start+f.pageSize; i++ {
		data = append(data, appResource(f.apps[i]))
	}
	links := map[string]string{}
	if start+f.pageSize < len(f.apps) {
		links["next"] = fmt.Sprintf("%s/v2/apps?page=%d", f.srv.URL, page+1)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "links": links, "meta": map[string]int{"count": len(f.apps)}})
}

func (f *fakeTenjin) getApp(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(w, r) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.apps {
		if a.ID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, map[string]any{"data": appResource(a)})
			return
		}
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "App not found"})
}

func (f *fakeTenjin) createApp(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(w, r) {
		return
	}
	var in struct {
		Name     string `json:"name"`
		BundleID string `json:"bundle_id"`
		Platform string `json:"platform"`
		StoreURL string `json:"store_url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	// From a store URL, Tenjin reads the app's details from the store.
	if in.StoreURL != "" {
		u, err := url.Parse(in.StoreURL)
		if err != nil || u.Host != "play.google.com" || u.Query().Get("id") == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "store_url could not be resolved to a supported (ios or android) app store URL"})
			return
		}
		in.Name, in.BundleID, in.Platform = "From The Store", u.Query().Get("id"), "android"
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.denyCreate {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Unauthorized. You do not have proper access. Contact your administrator."})
		return
	}
	for _, a := range f.apps {
		if a.BundleID == in.BundleID && a.Platform == in.Platform {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "An app already exists with this bundle_id and platform: " + a.ID})
			return
		}
	}
	app := tenjinapi.App{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", len(f.apps)+1), Name: in.Name, BundleID: in.BundleID, Platform: in.Platform}
	f.apps = append(f.apps, app)
	writeJSON(w, http.StatusCreated, map[string]any{"data": appResource(app)})
}

func (f *fakeTenjin) addApp(name, bundleID, platform string) tenjinapi.App {
	f.mu.Lock()
	defer f.mu.Unlock()
	app := tenjinapi.App{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", len(f.apps)+1), Name: name, BundleID: bundleID, Platform: platform}
	f.apps = append(f.apps, app)
	return app
}

// serveKeys deploys the SDK keys endpoint, with these keys for the app,
// oldest first.
func (f *fakeTenjin) serveKeys(appID string, keys ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sdkKeys == nil {
		f.sdkKeys = map[string][]string{}
	}
	f.sdkKeys[appID] = keys
}

func (f *fakeTenjin) listKeys(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(w, r) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.sdkKeys == nil:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not Found"})
		return
	case f.denyKeys:
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Unauthorized. You do not have proper access. Contact your administrator."})
		return
	}
	data := []map[string]any{}
	for i, key := range f.sdkKeys[r.PathValue("id")] {
		data = append(data, map[string]any{"id": fmt.Sprintf("key-%d", i+1), "type": "sdk_key", "attributes": map[string]any{
			"name": fmt.Sprintf("SDK Key %d", i+1), "key": key, "created_at": "2026-10-02T15:07:34Z",
		}})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

func (f *fakeTenjin) event(w http.ResponseWriter, r *http.Request) {
	key, _, _ := r.BasicAuth()
	_ = r.ParseForm()
	f.mu.Lock()
	f.keysAsked = append(f.keysAsked, key)
	message, known := f.keyAnswers[key]
	if app, tied := f.keyApps[key]; tied && app != r.PostForm.Get("bundle_id")+"|"+r.PostForm.Get("platform") {
		message, known = "unauthorized", true
	}
	f.mu.Unlock()
	switch {
	case !known:
		writeJSON(w, http.StatusAccepted, map[string]any{"code": 202, "message": "invalid device identifier"})
	case message == "unauthorized":
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": 401, "message": message})
	case message == "no such app":
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": message})
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"code": 202, "message": message})
	}
}

// user answers the attribution lookup: the device's IDs and campaign for an
// install Tenjin has processed, an empty object with a 404 for one it has
// not, and a 401 for a key tied to another app.
func (f *fakeTenjin) user(w http.ResponseWriter, r *http.Request) {
	key, _, _ := r.BasicAuth()
	q := r.URL.Query()
	id := q.Get("analytics_installation_id")
	f.mu.Lock()
	f.installsAsked = append(f.installsAsked, id)
	campaign, known := f.installs[id]
	app, tied := f.keyApps[key]
	f.mu.Unlock()
	switch {
	case tied && app != q.Get("bundle_id")+"|"+q.Get("platform"):
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": 401, "message": "unauthorized"})
	case !known:
		writeJSON(w, http.StatusNotFound, map[string]any{})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"advertising_id": "", "analytics_installation_id": id, "developer_device_id": "",
			"ad_network": "organic", "campaign_name": campaign})
	}
}

// harness runs the CLI against a fakeTenjin.
type harness struct {
	t     *testing.T
	fake  *fakeTenjin
	env   *environment
	store *session.MemoryStore
	vars  map[string]string
	// device is the Android device connectDevice connected, if any.
	device *fakeDevice
}

// withoutData turns the data commands off, as they are in a release.
func (h *harness) withoutData() *harness {
	delete(h.vars, dataCommandsVar)
	return h
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	// The data commands are on, so their tests reach them; withoutData turns
	// them off, as a release has them.
	h := &harness{t: t, fake: newFakeTenjin(t), store: &session.MemoryStore{}, vars: map[string]string{dataCommandsVar: "1"}}
	h.env = &environment{
		getenv:       func(name string) string { return h.vars[name] },
		endpoints:    h.fake.endpoints(),
		store:        h.store,
		stdin:        bufio.NewReader(strings.NewReader("")),
		loginTimeout: 5 * time.Second,
		// No coding agent unless a test installs one.
		lookPath:   func(name string) (string, error) { return "", fmt.Errorf("%s: not found", name) },
		executable: func() (string, error) { return "/usr/local/bin/tenjin", nil },
		wait:       func(time.Duration) {},
		// Never the real configuration folder's.
		telemetryState: filepath.Join(t.TempDir(), "tenjin", "telemetry.json"),
		// No adb either, unless a test connects a device.
		run: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			return nil, fmt.Errorf("%s: not found", name)
		},
		// The "browser": follow the authorize redirect back to the CLI.
		openBrowser: func(authorizeURL string) error {
			go func() {
				resp, err := http.Get(authorizeURL)
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}()
			return nil
		},
	}
	// What this build sends, as for any build but a release; telemetry turns it on.
	h.env.telemetryUnsent = telemetryUnsent(h.env.endpoints, h.env.telemetryState)
	return h
}

// connectDevice puts adb on the PATH with one device attached, whose log is
// the given text. It returns the adb calls made, for the test to inspect.
func (h *harness) connectDevice(log string) *[][]string {
	calls := &[][]string{}
	device := &fakeDevice{launch: log}
	h.device = device
	h.env.lookPath = func(name string) (string, error) {
		if name == "adb" {
			return "/opt/android/platform-tools/adb", nil
		}
		return "", fmt.Errorf("%s: not found", name)
	}
	h.env.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		*calls = append(*calls, args)
		if len(args) == 1 && args[0] == "devices" {
			return []byte("List of devices attached\nemulator-5554\tdevice\n\n"), nil
		}
		return device.logcat(args), nil
	}
	return calls
}

// fakeDevice is an Android device's log buffer, served as logcat serves it.
type fakeDevice struct {
	// earlier is what the buffer held before the app's launch, and launch
	// what the launch logged. The launch comes right after the first look
	// at the newest line, as when setup asks for one; a read made without
	// that look finds it there already, as doctor --device does.
	earlier, launch string
	marked          bool
}

// logcat answers `logcat -d [-t n] -v time [-s tag...]` as the real one
// does: each dump starts with the buffer's divider, and -t takes the newest
// lines of the whole buffer before the tag filter leaves out other tags.
func (d *fakeDevice) logcat(args []string) []byte {
	buffer := d.earlier + d.launch
	tail, tags := 0, []string(nil)
	for i, a := range args {
		switch {
		case a == "-t" && i+1 < len(args):
			tail, _ = strconv.Atoi(args[i+1])
		case a == "-s" && i > 0 && containsString(args[:i], "logcat"):
			tags = args[i+1:]
		}
	}
	if tail > 0 {
		if !d.marked {
			buffer = d.earlier
		}
		d.marked = true
	}
	lines := strings.Split(strings.TrimRight(buffer, "\n"), "\n")
	if buffer == "" {
		lines = nil
	}
	if tail > 0 && tail < len(lines) {
		lines = lines[len(lines)-tail:]
	}
	out := "--------- beginning of main\n"
	for _, line := range lines {
		if m := logcatTag.FindStringSubmatch(line); len(tags) > 0 && m != nil && !containsString(tags, m[1]) {
			continue
		}
		out += line + "\n"
	}
	return []byte(out)
}

// logcatTag reads the tag of a line in logcat's "time" format.
var logcatTag = regexp.MustCompile(`^\d{2}-\d{2} [\d:.]+ [VDIWEF]/([^(]+)\(`)

// bootSimulator puts xcrun on the PATH with one iOS simulator booted, whose
// log is the given text. It returns the simctl calls made.
func (h *harness) bootSimulator(log string) *[][]string {
	calls := &[][]string{}
	h.env.lookPath = func(name string) (string, error) {
		if name == "xcrun" {
			return "/usr/bin/xcrun", nil
		}
		return "", fmt.Errorf("%s: not found", name)
	}
	h.env.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "/usr/bin/xcrun" {
			return nil, fmt.Errorf("%s: not found", name)
		}
		*calls = append(*calls, args)
		if len(args) > 1 && args[1] == "list" {
			return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-5":[{"udid":"243AACFD-275B-4332-8D06-D4B06EFEA171","name":"iPhone 16","state":"Booted"}]}}`), nil
		}
		return []byte(log), nil
	}
	return calls
}

// connectIPhone puts xcrun on the PATH with these iPhones connected; the
// app launched on the first answers with the given console output. It
// returns the launches made, each as its arguments.
func (h *harness) connectIPhone(console string, phones ...string) *[][]string {
	if len(phones) == 0 {
		phones = []string{`{"connectionProperties":{"tunnelState":"connected"},"deviceProperties":{"name":"Test iPhone","developerModeStatus":"enabled"},"hardwareProperties":{"udid":"00008130-000000000000000A","reality":"physical","platform":"iOS"}}`}
	}
	launches := &[][]string{}
	h.env.lookPath = func(name string) (string, error) {
		if name == "xcrun" {
			return "/usr/bin/xcrun", nil
		}
		return "", fmt.Errorf("%s: not found", name)
	}
	h.env.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "devicectl" && args[1] == "list" {
			return []byte(`{"info":{},"result":{"devices":[` + strings.Join(phones, ",") + `]}}`), nil
		}
		if len(args) > 1 && args[0] == "simctl" && args[1] == "list" {
			return []byte(`{"devices":{}}`), nil
		}
		return nil, fmt.Errorf("%s %v: unexpected", name, args)
	}
	h.env.stream = func(_ context.Context, _ string, args []string, stop func(string) bool) ([]byte, error) {
		*launches = append(*launches, args)
		var out strings.Builder
		for _, line := range strings.Split(console, "\n") {
			out.WriteString(line + "\n")
			if stop(line) {
				break
			}
		}
		if !strings.Contains(console, "[Tenjin]") {
			return []byte(console), fmt.Errorf("exit status 1")
		}
		return []byte(out.String()), nil
	}
	return launches
}

// typing makes the harness interactive, with the given lines as the user's
// answers.
func (h *harness) typing(lines ...string) *harness {
	h.env.interactive = true
	h.env.stdin = bufio.NewReader(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	return h
}

func (h *harness) run(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = execute(args, &out, &errOut, h.env)
	return code, out.String(), errOut.String()
}

// signIn logs the harness in and fails the test if that does not work.
// signInAsAnEarlierClient stores a session as an earlier build left it:
// signed in with a client it registered for itself, here "client-1".
func (h *harness) signInAsAnEarlierClient() {
	h.t.Helper()
	f := h.fake
	f.mu.Lock()
	access, refresh := f.issue("client-1")
	jwt := makeJWT(time.Now().Add(2*time.Hour), f.issued)
	f.jwts[jwt] = f.accessTokens[access]
	f.mu.Unlock()
	if _, err := h.store.Save(&session.Credentials{
		Environment: "test", ClientID: "client-1", RefreshToken: refresh, JWT: jwt, JWTExpiry: time.Now().Add(2 * time.Hour),
		UserID: "user-1", OrganizationID: "org-1", OrganizationName: "Acme Games",
	}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) signIn() {
	h.t.Helper()
	if code, _, stderr := h.run("login"); code != 0 {
		h.t.Fatalf("login failed (exit %d): %s", code, stderr)
	}
}
