package cli

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const (
	testApp     = "00000000-0000-4000-8000-000000000001"
	testOther   = "00000000-0000-4000-8000-000000000002"
	unknownUUID = "00000000-0000-4000-8000-999999999999"
)

// Each data command reads one endpoint, with its flags as the API's
// parameters.
func TestDataCommandsReadTheirEndpoints(t *testing.T) {
	for _, tc := range []struct {
		args  string
		path  string
		query map[string]string
	}{
		{"campaigns list --app " + testApp + " --channel 5 --platform ios --query summer --per-page 10 --page 2", "/v2/campaigns",
			map[string]string{"app_id": testApp, "channel_id": "5", "platform": "ios", "query": "summer", "per_page": "10", "page": "2"}},
		{"campaigns get " + testOther, "/v2/campaigns/" + testOther, nil},
		{"channels list --query face", "/v2/channels", map[string]string{"query": "face"}},
		{"channels get 18", "/v2/channels/18", nil},
		{"channels callback-group-templates 18", "/v2/integrations/18/callback_group_templates", nil},
		{"ad-accounts list --channel 3 --query main", "/v2/ad_accounts", map[string]string{"channel_id": "3", "query": "main"}},
		{"ad-accounts get " + testOther, "/v2/ad_accounts/" + testOther, nil},
		{"skadnetworks list", "/v2/sk_ad_networks", nil},
		{"skadnetworks get 4fzdc2evr5.skadnetwork", "/v2/sk_ad_networks/4fzdc2evr5.skadnetwork", nil},
		{"apps events list --app " + testApp, "/v2/apps/" + testApp + "/events", nil},
		{"apps callbacks list --app " + testApp + " --channel 2", "/v2/apps/" + testApp + "/callbacks", map[string]string{"channel_id": "2"}},
		{"apps callbacks get " + testOther + " --app " + testApp, "/v2/apps/" + testApp + "/callbacks/" + testOther, nil},
		{"apps s2s-callbacks list --app " + testApp, "/v2/apps/" + testApp + "/s2s_callbacks", nil},
		{"apps s2s-callbacks get " + testOther + " --app " + testApp, "/v2/apps/" + testApp + "/s2s_callbacks/" + testOther, nil},
		{"apps callback-templates list --app " + testApp + " --channel 7", "/v2/apps/" + testApp + "/callback_templates", map[string]string{"channel_id": "7"}},
		{"apps callback-templates get " + testOther + " --app " + testApp, "/v2/apps/" + testApp + "/callback_templates/" + testOther, nil},
		{"apps callback-groups list --app " + testApp + " --channel 18", "/v2/apps/" + testApp + "/integrations/18/callback_groups", nil},
		{"apps callback-groups get " + testOther + " --app " + testApp + " --channel 18", "/v2/apps/" + testApp + "/integrations/18/callback_groups/" + testOther, nil},
		{"apps callback-settings get --app " + testApp + " --channel 18", "/v2/apps/" + testApp + "/integrations/18/callback_settings", nil},
		{"reports spend --from 2026-10-01 --to 2026-10-07 --granularity totals-daily --group-by channel,app --apps " + testApp + " --channels 1,2 --metrics spend,installs", "/v2/reports/spend",
			map[string]string{"start_date": "2026-10-01", "end_date": "2026-10-07", "granularity": "totals-daily", "group_by": "channel,app", "app_ids": testApp, "channel_ids": "1,2", "metrics": "spend,installs"}},
		{"reports ad-revenue --from 2026-10-01 --to 2026-10-02", "/v2/reports/ad_revenue", map[string]string{"start_date": "2026-10-01", "end_date": "2026-10-02"}},
		{"reports skadnetwork --from 2026-10-01 --to 2026-10-02 --group-by conversion_value", "/v2/reports/sk_ad_network", map[string]string{"group_by": "conversion_value"}},
		{"reports skan --from 2026-10-01 --to 2026-10-02 --granularity daily", "/v2/reports/skan_apple", map[string]string{"granularity": "daily"}},
		{"saved-reports list --type ad_revenue", "/v2/saved_reports", map[string]string{"report_type": "ad_revenue"}},
		{"saved-reports get " + testOther, "/v2/saved_reports/" + testOther, nil},
		{"saved-reports run " + testOther, "/v2/reports/" + testOther, nil},
	} {
		t.Run(tc.args, func(t *testing.T) {
			h := newHarness(t)
			h.signIn()
			code, stdout, stderr := h.run(strings.Fields(tc.args)...)
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr)
			}
			if len(h.fake.dataAsked) != 1 {
				t.Fatalf("%d reads, want 1", len(h.fake.dataAsked))
			}
			asked := h.fake.dataAsked[0]
			if asked.Path != tc.path {
				t.Errorf("read %s, want %s", asked.Path, tc.path)
			}
			for k, want := range tc.query {
				if got := asked.Query().Get(k); got != want {
					t.Errorf("%s = %q, want %q (query %s)", k, got, want, asked.RawQuery)
				}
			}
			if !strings.Contains(stdout, "First") {
				t.Errorf("the answer is not shown:\n%s", stdout)
			}
		})
	}
}

// With --json, a data command prints the API's answer as it is.
func TestDataJSONIsTheAPIsAnswer(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	answer := map[string]any{
		"data": []any{map[string]any{"id": testOther, "type": "campaign", "attributes": map[string]any{
			"name": "Summer", "platform": "ios", "click_tracking_url": "https://track.example/c", "hidden": false,
		}}},
		"links": map[string]any{"self": "x"},
		"meta":  map[string]any{"total": 1},
	}
	h.fake.data = map[string]func(*http.Request) (int, any){"/v2/campaigns": func(*http.Request) (int, any) { return http.StatusOK, answer }}

	code, stdout, stderr := h.run("campaigns", "list", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	var got, want any
	raw, _ := json.Marshal(answer)
	_ = json.Unmarshal(raw, &want)
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("stdout is not the API's answer (err %v):\n%s", err, stdout)
	}
}

// At a terminal, a list shows its view's columns, a report every column the
// API sent in its order, and one resource all its fields.
func TestDataTables(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.data = map[string]func(*http.Request) (int, any){
		"/v2/campaigns": func(*http.Request) (int, any) {
			return http.StatusOK, map[string]any{"data": []any{map[string]any{"id": testOther, "type": "campaign", "attributes": map[string]any{
				"name": "Summer", "platform": "ios", "channel_id": 5, "app_id": testApp, "click_tracking_url": "https://track.example/c",
			}}}}
		},
		"/v2/reports/spend": func(*http.Request) (int, any) {
			return http.StatusOK, json.RawMessage(`{"data":[{"type":"report","attributes":{"date":"2026-10-01","name":"Sample","spend":12.5,"installs":40}}]}`)
		},
		"/v2/campaigns/" + testOther: func(*http.Request) (int, any) {
			return http.StatusOK, json.RawMessage(`{"data":{"id":"` + testOther + `","type":"campaign","attributes":{"name":"Summer","hidden":false,"click_tracking_url":"https://track.example/c"}}}`)
		},
		"/v2/ad_accounts": func(*http.Request) (int, any) { return http.StatusOK, map[string]any{"data": []any{}} },
	}

	_, stdout, _ := h.run("campaigns", "list")
	if !strings.Contains(stdout, "NAME") || !strings.Contains(stdout, "CHANNEL ID") || !strings.Contains(stdout, "Summer") || strings.Contains(stdout, "track.example") {
		t.Errorf("campaigns table:\n%s", stdout)
	}
	_, stdout, _ = h.run("reports", "spend", "--from", "2026-10-01", "--to", "2026-10-01")
	header := strings.Fields(strings.SplitN(stdout, "\n", 2)[0])
	if want := []string{"DATE", "NAME", "SPEND", "INSTALLS"}; !reflect.DeepEqual(header, want) || !strings.Contains(stdout, "12.5") {
		t.Errorf("report header = %v, want %v:\n%s", header, want, stdout)
	}
	_, stdout, _ = h.run("campaigns", "get", testOther)
	if !strings.Contains(stdout, "click tracking url") || !strings.Contains(stdout, "https://track.example/c") || !strings.Contains(stdout, "hidden") {
		t.Errorf("one campaign:\n%s", stdout)
	}
	if _, stdout, _ = h.run("ad-accounts", "list"); !strings.Contains(stdout, "No ad accounts.") {
		t.Errorf("empty list:\n%s", stdout)
	}
}

// A list shows one page and says when there are more; --all reads them all.
func TestDataPaging(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.data = map[string]func(*http.Request) (int, any){"/v2/channels": func(r *http.Request) (int, any) {
		page := r.URL.Query().Get("page")
		doc := map[string]any{"data": []any{map[string]any{"id": 1, "type": "channel", "attributes": map[string]any{"name": "Channel page " + page}}}}
		if page != "2" {
			doc["links"] = map[string]string{"next": "/v2/channels?page=2"}
		}
		return http.StatusOK, doc
	}}

	code, stdout, stderr := h.run("channels", "list")
	if code != 0 || !strings.Contains(stderr, "--page 2") || strings.Contains(stdout, "--page") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	h.fake.dataAsked = nil
	code, stdout, stderr = h.run("channels", "list", "--all", "--json")
	var got struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || code != 0 || len(got.Data) != 2 || stderr != "" {
		t.Errorf("--all: exit = %d, %d items, stderr = %q", code, len(got.Data), stderr)
	}
	if len(h.fake.dataAsked) != 2 || h.fake.dataAsked[0].Query().Get("per_page") != "1000" {
		t.Errorf("--all read %d pages, the first as %q", len(h.fake.dataAsked), h.fake.dataAsked[0].RawQuery)
	}
}

// A command missing what its endpoint needs, or given something that cannot
// be an ID, fails as a usage error before any request.
func TestDataUsageErrors(t *testing.T) {
	for _, args := range []string{
		"apps events list",
		"apps callback-groups list --app " + testApp,
		"apps callback-settings get --channel 18",
		"campaigns get summer",
		"campaigns list --app com.example.sample",
		"channels get facebook",
		"reports spend --from 2026-10-01",
		"reports spend --from yesterday --to 2026-10-02",
		"channels list --all --page 2",
		"saved-reports run",
	} {
		t.Run(args, func(t *testing.T) {
			h := newHarness(t)
			h.signIn()
			code, _, stderr := h.run(strings.Fields(args)...)
			if code != 2 || len(h.fake.dataAsked) != 0 {
				t.Errorf("exit = %d, %d reads, stderr = %q", code, len(h.fake.dataAsked), stderr)
			}
		})
	}
}

// The API's refusals keep their meaning.
func TestDataErrors(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.data = map[string]func(*http.Request) (int, any){
		"/v2/campaigns/" + unknownUUID: func(*http.Request) (int, any) {
			return http.StatusBadRequest, map[string]string{"error": "Campaign not found"}
		},
		"/v2/reports/spend": func(*http.Request) (int, any) {
			return http.StatusForbidden, map[string]string{"error": "Not authorized"}
		},
	}
	if code, _, stderr := h.run("campaigns", "get", unknownUUID, "--json"); code != 5 || errorBody(t, stderr)["code"] != "not_found" {
		t.Errorf("unknown campaign: exit = %d, stderr = %q", code, stderr)
	}
	if code, _, stderr := h.run("reports", "spend", "--from", "2026-10-01", "--to", "2026-10-02", "--json"); code == 0 || errorBody(t, stderr)["code"] != "permission_denied" {
		t.Errorf("refused report: exit = %d, stderr = %q", code, stderr)
	}
	signedOut := newHarness(t)
	if code, _, _ := signedOut.run("campaigns", "list"); code != 3 {
		t.Errorf("signed out: exit = %d, want 3", code)
	}
}
