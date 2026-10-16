package cli

import (
	"encoding/json"
	"net/http"
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
	if code, _, stderr := h.run("site-ids", "clear"); code == 0 || len(h.fake.siteIDRules) != 1 || !strings.Contains(stderr, "[y/N]") {
		t.Errorf("Enter on clear: exit = %d, rules = %v", code, h.fake.siteIDRules)
	}
	if code, _, _ := h.run("site-ids", "clear", "--yes"); code != 0 || len(h.fake.siteIDRules) != 0 {
		t.Errorf("clear: exit = %d, rules = %v", code, h.fake.siteIDRules)
	}
	signedOut := newHarness(t)
	if code, _, _ := signedOut.run("site-ids", "list"); code != 3 {
		t.Errorf("signed out: exit = %d, want 3", code)
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
