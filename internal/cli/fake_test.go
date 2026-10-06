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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tenjin/tenjin-cli/internal/config"
	"github.com/tenjin/tenjin-cli/internal/session"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi"
)

// fakeTenjin stands in for the three services the CLI talks to: the
// authorization server, the API and the tracking service, answering as they do.
type fakeTenjin struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex

	registrations int
	challenge     string // PKCE challenge of the pending authorization
	refreshToken  string // the one currently valid; rotates on use
	accessTokens  map[string]bool
	jwts          map[string]bool
	issued        int

	apps       []tenjinapi.App
	pageSize   int
	denyCreate bool
	keyAnswers map[string]string // SDK key -> Tenjin's message; default accepts
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
}

func newFakeTenjin(t *testing.T) *fakeTenjin {
	t.Helper()
	f := &fakeTenjin{t: t, accessTokens: map[string]bool{}, jwts: map[string]bool{}, pageSize: 1000, keyAnswers: map[string]string{}, keyApps: map[string]string{}, installs: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", f.metadata)
	mux.HandleFunc("POST /oauth/registration", f.register)
	mux.HandleFunc("GET /oauth/authorize", f.authorize)
	mux.HandleFunc("POST /oauth/token", f.token)
	mux.HandleFunc("POST /oauth/revoke", f.revoke)
	mux.HandleFunc("GET /v2/apps", f.listApps)
	mux.HandleFunc("POST /v2/apps", f.createApp)
	mux.HandleFunc("GET /v2/apps/{id}", f.getApp)
	mux.HandleFunc("GET /v2/apps/{id}/sdk_keys", f.listKeys)
	mux.HandleFunc("POST /v0/event", f.event)
	mux.HandleFunc("GET /v0/user", f.user)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTenjin) endpoints() config.Endpoints {
	return config.Endpoints{Name: "test", Issuer: f.srv.URL, API: f.srv.URL + "/v2", Track: f.srv.URL, Resource: "https://mcp.test"}
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
		"registration_endpoint":  f.srv.URL + "/oauth/registration",
	})
}

func (f *fakeTenjin) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RedirectURIs []string `json:"redirect_uris"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if len(body.RedirectURIs) != 1 || body.RedirectURIs[0] != "http://127.0.0.1/callback" {
		f.t.Errorf("registered redirect URIs = %q", body.RedirectURIs)
	}
	f.mu.Lock()
	f.registrations++
	f.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]string{"client_id": "client-1"})
}

// authorize plays the browser and the consenting user: it sends the caller
// straight back to the loopback redirect with a code.
func (f *fakeTenjin) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	if q.Get("resource") != "https://mcp.test" || q.Get("code_challenge_method") != "S256" || q.Get("scope") != "mcp" ||
		!strings.HasPrefix(redirect, "http://127.0.0.1:") {
		http.Error(w, "invalid_target", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.challenge = q.Get("code_challenge")
	f.mu.Unlock()
	http.Redirect(w, r, redirect+"?"+url.Values{"code": {"auth-code"}, "state": {q.Get("state")}, "iss": {f.srv.URL}}.Encode(), http.StatusFound)
}

func (f *fakeTenjin) issue() (access, refresh string) {
	f.issued++
	access, refresh = fmt.Sprintf("access-%d", f.issued), fmt.Sprintf("refresh-%d", f.issued)
	f.accessTokens[access] = true
	f.refreshToken = refresh
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
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if r.PostForm.Get("code") != "auth-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			invalid("bad code or verifier")
			return
		}
		access, refresh := f.issue()
		writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 7200})
	case "refresh_token":
		if f.refreshToken == "" || r.PostForm.Get("refresh_token") != f.refreshToken {
			invalid("refresh token is revoked or unknown")
			return
		}
		access, refresh := f.issue()
		writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 7200})
	case "urn:ietf:params:oauth:grant-type:token-exchange":
		if !f.accessTokens[r.PostForm.Get("subject_token")] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request", "error_description": "subject_token is missing or invalid"})
			return
		}
		jwt := makeJWT(time.Now().Add(2*time.Hour), f.issued)
		f.jwts[jwt] = true
		writeJSON(w, http.StatusOK, map[string]string{
			"access_token": jwt, "user_id": "user-1", "organization_id": "org-1", "organization_name": "Acme Games",
		})
	default:
		invalid("unsupported grant")
	}
}

func makeJWT(expiry time.Time, n int) string {
	payload, _ := json.Marshal(map[string]any{"exp": expiry.Unix(), "n": n})
	return "eyJhbGciOiJFUzUxMiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

// revoke ends the ability to refresh; a JWT already exchanged keeps
// working until it expires.
func (f *fakeTenjin) revoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	if r.PostForm.Get("token") == f.refreshToken {
		f.refreshToken = ""
	}
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (f *fakeTenjin) authorized(w http.ResponseWriter, r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	ok := f.jwts[token]
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
		// The real API returns shared secrets here. They must never reach output.
		"ios_shared_secret": "TOP-SECRET-SHARED-SECRET",
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

// user answers the attribution lookup: the campaign for an install Tenjin has
// processed, and an empty object with a 404 for one it has not.
func (f *fakeTenjin) user(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("analytics_installation_id")
	f.mu.Lock()
	f.installsAsked = append(f.installsAsked, id)
	campaign, known := f.installs[id]
	f.mu.Unlock()
	if !known {
		writeJSON(w, http.StatusNotFound, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ad_network": "organic", "campaign_name": campaign})
}

// harness runs the CLI against a fakeTenjin.
type harness struct {
	t     *testing.T
	fake  *fakeTenjin
	env   *environment
	store *session.MemoryStore
	vars  map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, fake: newFakeTenjin(t), store: &session.MemoryStore{}, vars: map[string]string{}}
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
	return h
}

// connectDevice puts adb on the PATH with one device attached, whose log is
// the given text. It returns the adb calls made, for the test to inspect.
func (h *harness) connectDevice(log string) *[][]string {
	calls := &[][]string{}
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
		return []byte(log), nil
	}
	return calls
}

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
func (h *harness) signIn() {
	h.t.Helper()
	if code, _, stderr := h.run("login"); code != 0 {
		h.t.Fatalf("login failed (exit %d): %s", code, stderr)
	}
}
