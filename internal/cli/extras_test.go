package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Site ID rules go to the Site ID Optimization service in Connect's JSON
// form, each change after a question or --yes.
func TestSiteIDs(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	if code, stdout, _ := h.run("site-ids", "list"); code != 0 || !strings.Contains(stdout, "No site ID rules.") {
		t.Fatalf("empty list: exit = %d, stdout = %q", code, stdout)
	}
	if code, _, stderr := h.run("site-ids", "block", "bad-site"); code != 2 || len(h.fake.siteIDCalls) != 1 || !strings.Contains(stderr, "--yes") {
		t.Errorf("block without --yes: exit = %d, calls = %v", code, h.fake.siteIDCalls)
	}
	code, stdout, stderr := h.run("site-ids", "block", "bad-site", "worse-site", "--app", testApp, "--channel", "0", "--yes")
	if code != 0 || !strings.Contains(stdout, "Blocked.") {
		t.Fatalf("block: exit = %d, stderr = %q", code, stderr)
	}
	var sent struct {
		Rules []map[string]any `json:"rules"`
	}
	call := h.fake.siteIDCalls[len(h.fake.siteIDCalls)-1]
	_ = json.Unmarshal([]byte(strings.TrimPrefix(call, "Block ")), &sent)
	if len(sent.Rules) != 2 || sent.Rules[0]["siteId"] != "bad-site" || sent.Rules[0]["appId"] != testApp || sent.Rules[0]["adNetworkId"] != float64(0) {
		t.Errorf("block sent %s", call)
	}
	if _, stdout, _ = h.run("site-ids", "list"); !strings.Contains(stdout, "worse-site") || !strings.Contains(stdout, testApp) {
		t.Errorf("list:\n%s", stdout)
	}
	if code, _, _ := h.run("site-ids", "reinstate", "bad-site", "--yes"); code != 0 || len(h.fake.siteIDRules) != 1 {
		t.Errorf("reinstate: exit = %d, rules = %v", code, h.fake.siteIDRules)
	}
	h.typing("")
	if code, _, stderr := h.run("site-ids", "clear"); code != 0 || len(h.fake.siteIDRules) != 1 || !strings.Contains(stderr, "[y/N]") || !strings.Contains(stderr, "Nothing was changed.") {
		t.Errorf("Enter on clear: exit = %d, rules = %v", code, h.fake.siteIDRules)
	}
	if code, _, _ := h.run("site-ids", "clear", "--yes"); code != 0 || len(h.fake.siteIDRules) != 0 {
		t.Errorf("clear: exit = %d, rules = %v", code, h.fake.siteIDRules)
	}
	signedOut := newHarness(t)
	if code, _, _ := signedOut.run("site-ids", "list"); code != 3 {
		t.Errorf("signed out: exit = %d, want 3", code)
	}
	calls := len(h.fake.siteIDCalls)
	code, stdout, stderr = h.run("site-ids", "reinstate", "bad-site", "--dry-run")
	if code != 0 || !strings.Contains(stdout, "bad-site") || !strings.Contains(stderr, "POST "+h.fake.srv.URL+"/management.v2.SiteIDFilter/Reinstate with this body") || len(h.fake.siteIDCalls) != calls {
		t.Errorf("reinstate --dry-run: exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

// A site ID the service would refuse is refused before the question.
func TestSiteIDsAreChecked(t *testing.T) {
	for _, args := range [][]string{
		{"site-ids", "block", ""},
		{"site-ids", "block", "bad\x07site"},
		{"site-ids", "block", "same", "same"},
		{"site-ids", "reinstate", ""},
		{"site-ids", "reinstate", "same", "same", "--dry-run"},
	} {
		h := newHarness(t)
		h.signIn()
		h.typing("y")
		if code, _, stderr := h.run(args...); code != 2 || len(h.fake.siteIDCalls) != 0 || strings.Contains(stderr, "[Y/n]") {
			t.Errorf("%q: exit = %d, calls = %v, stderr = %q", args, code, h.fake.siteIDCalls, stderr)
		}
	}
}

// The Site ID service's refusals read as the API's do: a refused token names
// where it came from, and a value it does not accept is refused (exit 11).
func TestSiteIDRefusals(t *testing.T) {
	h := newHarness(t)
	h.vars["TENJIN_API_TOKEN"] = "not-a-jwt"
	code, _, stderr := h.run("site-ids", "list", "--json")
	if body := errorBody(t, stderr); code != 3 || !strings.Contains(body["message"], "TENJIN_API_TOKEN") || strings.Contains(body["message"], "saved sign-in") {
		t.Errorf("refused token: exit = %d, stderr = %q", code, stderr)
	}

	h = newHarness(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_argument", "message": "site_id is too long"})
	}))
	defer srv.Close()
	h.env.endpoints.SiteIDs = srv.URL
	h.vars["TENJIN_API_TOKEN"] = "dashboard-token"
	code, _, stderr = h.run("site-ids", "block", "abc", "--yes", "--json")
	if body := errorBody(t, stderr); code != 11 || body["code"] != "refused" || !strings.Contains(body["message"], "site_id is too long") {
		t.Errorf("invalid argument: exit = %d, stderr = %q", code, stderr)
	}
}

// The documentation is searched without a token.
func TestDocsSearch(t *testing.T) {
	h := newHarness(t)
	h.fake.docs = []map[string]string{
		{"title": "SDK FAQ", "permalink": "https://tenjin.example/docs/sdk-faq/", "post_type": "docs"},
		{"title": "SDK news", "permalink": "https://tenjin.example/blog/", "post_type": "post"},
	}
	code, stdout, stderr := h.run("docs", "search", "sdk")
	if code != 0 || !strings.Contains(stdout, "https://tenjin.example/docs/sdk-faq/") || strings.Contains(stdout, "blog") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if code, _, _ := h.run("docs", "search"); code != 2 {
		t.Errorf("no words: exit = %d", code)
	}
}

func TestMetricsAndMacros(t *testing.T) {
	h := newHarness(t)
	if code, stdout, _ := h.run("reports", "metrics", "roi_7d"); code != 0 || !strings.HasPrefix(strings.Split(stdout, "\n")[1], "roi_Nd") {
		t.Errorf("metrics: exit = %d, stdout:\n%s", code, stdout)
	}
	code, stdout, _ := h.run("reports", "metrics", "--report", "skan", "--json")
	var metrics []map[string]any
	if err := json.Unmarshal([]byte(stdout), &metrics); err != nil || code != 0 || len(metrics) == 0 || metrics[0]["report"] != "skan" {
		t.Errorf("metrics --json: exit = %d, err = %v", code, err)
	}
	if code, stdout, stderr := h.run("reports", "metrics", "--report", "spend"); code != 2 || stdout != "" || !strings.Contains(stderr, "user_acquisition, ad_monetization or skan") {
		t.Errorf("--report spend: exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if code, stdout, _ := h.run("apps", "s2s-callbacks", "macros", "country"); code != 0 || !strings.Contains(stdout, "{{country}}") {
		t.Errorf("macros: exit = %d, stdout:\n%s", code, stdout)
	}
}

// The integration view marks each group with what it still needs.
func TestInspectIntegration(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	const template = "00000000-0000-4000-8000-0000000000cc"
	h.fake.data = map[string]func(*http.Request) (int, any){
		"/v2/integrations/31/callback_group_templates": func(*http.Request) (int, any) {
			return http.StatusOK, map[string]any{"data": []any{map[string]any{"id": template, "type": "callback_group_template", "attributes": map[string]any{
				"name": "Purchases", "event": "purchase", "required_settings": []string{"event_name", "event_type"},
			}}}}
		},
		"/v2/apps/" + testApp + "/integrations/31/callback_groups": func(*http.Request) (int, any) {
			return http.StatusOK, map[string]any{"data": []any{map[string]any{"id": testOther, "type": "callback_group", "attributes": map[string]any{
				"name": "My purchases", "active": false, "callback_group_template_id": template, "callback_settings": map[string]any{"event_name": "buy"},
			}}}}
		},
		"/v2/apps/" + testApp + "/integrations/31/callback_settings": func(*http.Request) (int, any) {
			return http.StatusOK, map[string]any{"data": map[string]any{"id": "s", "type": "callback_settings", "attributes": map[string]any{"callback_settings": map[string]any{}}}}
		},
	}
	code, stdout, stderr := h.run("apps", "callback-groups", "inspect", "--app", testApp, "--channel", "31", "--json")
	var got struct {
		Groups []map[string]any `json:"configured_groups"`
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || code != 0 || len(got.Groups) != 1 {
		t.Fatalf("exit = %d, err = %v, stderr = %q", code, err, stderr)
	}
	missing, _ := got.Groups[0]["missing_settings"].([]any)
	if len(missing) != 1 || missing[0] != "event_type" || got.Groups[0]["activatable"] != false {
		t.Errorf("group = %v", got.Groups[0])
	}
	if code, stdout, _ := h.run("apps", "callback-groups", "inspect", "--app", testApp, "--channel", "31"); code != 0 || !strings.Contains(stdout, "event_type") || !strings.Contains(stdout, "My purchases") {
		t.Errorf("table: exit = %d:\n%s", code, stdout)
	}
	if code, _, _ := h.run("apps", "callback-groups", "inspect", "--app", testApp); code != 2 {
		t.Errorf("no --channel: exit = %d", code)
	}
}

// The integration view reads every page of the templates, the groups and
// the events.
func TestInspectIntegrationReadsEveryPage(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	paged := func(path, key string) func(*http.Request) (int, any) {
		return func(r *http.Request) (int, any) {
			page := r.URL.Query().Get("page")
			doc := map[string]any{"data": []any{map[string]any{"id": key + page, "type": "thing", "attributes": map[string]any{"name": key + " " + page, "event": key + page}}}}
			if page == "1" {
				doc["links"] = map[string]string{"next": path + "?page=2"}
			}
			return http.StatusOK, doc
		}
	}
	templates := "/v2/integrations/31/callback_group_templates"
	groups := "/v2/apps/" + testApp + "/integrations/31/callback_groups"
	events := "/v2/apps/" + testApp + "/events"
	h.fake.data = map[string]func(*http.Request) (int, any){
		templates: paged(templates, "template"),
		groups:    paged(groups, "group"),
		events:    paged(events, "event"),
	}
	code, stdout, stderr := h.run("apps", "callback-groups", "inspect", "--app", testApp, "--channel", "31", "--json")
	var got struct {
		Templates []map[string]any `json:"available_templates"`
		Groups    []map[string]any `json:"configured_groups"`
		Events    []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || code != 0 {
		t.Fatalf("exit = %d, err = %v, stderr = %q", code, err, stderr)
	}
	if len(got.Templates) != 2 || len(got.Groups) != 2 || len(got.Events) != 2 {
		t.Errorf("%d templates, %d groups, %d events; want 2 of each", len(got.Templates), len(got.Groups), len(got.Events))
	}
	for _, u := range h.fake.dataAsked {
		if u.Path != "/v2/apps/"+testApp+"/integrations/31/callback_settings" && u.Query().Get("per_page") != "1000" {
			t.Errorf("read %s, not 1000 to a page", u)
		}
	}
}
