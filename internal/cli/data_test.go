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
		{"reports spend --from 2026-10-01 --to 2026-10-07 --granularity totals-daily --group-by channel,app --app " + testApp + " --channel 1 --channel 2 --metrics spend,installs", "/v2/reports/spend",
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

// A table and CSV leave hidden campaigns out, as the dashboard does, and say
// so; --json keeps them.
func TestHiddenCampaignsAreLeftOutOfTables(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.data = map[string]func(*http.Request) (int, any){"/v2/campaigns": func(*http.Request) (int, any) {
		return http.StatusOK, json.RawMessage(`{"data":[
			{"id":"` + testOther + `","type":"campaign","attributes":{"name":"Summer","hidden":false}},
			{"id":"` + testApp + `","type":"campaign","attributes":{"name":"Spring @abc","hidden":true}}]}`)
	}}
	for _, format := range []string{"table", "csv"} {
		code, stdout, stderr := h.run("campaigns", "list", "-o", format)
		if code != 0 || !strings.Contains(stdout, "Summer") || strings.Contains(stdout, "Spring") || !strings.Contains(stderr, "Hidden campaigns left out, as the dashboard does: 1") {
			t.Errorf("%s: exit = %d\n%s\n%s", format, code, stdout, stderr)
		}
	}
	code, stdout, stderr := h.run("campaigns", "list", "--json")
	if code != 0 || !strings.Contains(stdout, "Spring @abc") || strings.Contains(stderr, "left out") {
		t.Errorf("--json: exit = %d\n%s\n%s", code, stdout, stderr)
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
			return http.StatusOK, json.RawMessage(`{"data":[{"type":"report","attributes":{"date":"2026-10-01","name":"Sample","app_icon_url":"https://img.example/a.png","spend":12.5,"installs":40}}]}`)
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

// rows is a page of n report rows.
func rows(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{"type": "report", "attributes": map[string]any{"date": "2026-10-01", "spend": i}}
	}
	return out
}

// --all --json is the first page's document, its meta kept and its page
// links left out, with every page's data.
func TestAllKeepsTheFirstPagesDocument(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.data = map[string]func(*http.Request) (int, any){"/v2/channels": func(r *http.Request) (int, any) {
		doc := map[string]any{
			"data":  []any{map[string]any{"id": "1", "type": "channel", "attributes": map[string]any{"name": "Page " + r.URL.Query().Get("page")}}},
			"meta":  map[string]any{"count": 2, "per_page": 1000},
			"links": map[string]string{"self": r.URL.String()},
		}
		if r.URL.Query().Get("page") == "1" {
			doc["links"] = map[string]string{"self": r.URL.String(), "next": "/v2/channels?page=2"}
		}
		return http.StatusOK, doc
	}}
	code, stdout, stderr := h.run("channels", "list", "--all", "--json")
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || code != 0 {
		t.Fatalf("exit = %d, err = %v, stderr = %q", code, err, stderr)
	}
	data, _ := got["data"].([]any)
	meta, _ := got["meta"].(map[string]any)
	if _, hasLinks := got["links"]; len(data) != 2 || meta["count"] != float64(2) || hasLinks {
		t.Errorf("--all --json = %s", stdout)
	}
}

// A report with no next link is read on while its pages come back full and
// short of meta.count; a full page that reaches the count is the last.
func TestReportsPageByTheirCount(t *testing.T) {
	for _, tc := range []struct {
		name         string
		count        any
		pages, total int
	}{
		{"short of the count", 1500, 2, 1500},
		{"at the count", 1000, 1, 1000},
		{"no count", nil, 2, 1500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.signIn()
			h.fake.data = map[string]func(*http.Request) (int, any){"/v2/reports/spend": func(r *http.Request) (int, any) {
				n := 1000
				if r.URL.Query().Get("page") != "1" {
					n = 500
				}
				doc := map[string]any{"data": rows(n), "links": map[string]string{"self": r.URL.String()}}
				if tc.count != nil {
					doc["meta"] = map[string]any{"count": tc.count, "per_page": 1000}
				}
				return http.StatusOK, doc
			}}
			code, stdout, stderr := h.run("reports", "spend", "--from", "2026-10-01", "--to", "2026-10-02", "--json")
			var got struct {
				Data []json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal([]byte(stdout), &got); err != nil || code != 0 {
				t.Fatalf("exit = %d, err = %v, stderr = %q", code, err, stderr)
			}
			if len(h.fake.dataAsked) != tc.pages || len(got.Data) != tc.total {
				t.Errorf("%d reads, %d rows; want %d and %d", len(h.fake.dataAsked), len(got.Data), tc.pages, tc.total)
			}
		})
	}
}

// One page of a report says there is more when its count does.
func TestOnePageOfAReportSaysThereIsMore(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.data = map[string]func(*http.Request) (int, any){"/v2/reports/spend": func(*http.Request) (int, any) {
		return http.StatusOK, map[string]any{"data": rows(10), "meta": map[string]any{"count": 25, "per_page": 10}}
	}}
	code, _, stderr := h.run("reports", "spend", "--from", "2026-10-01", "--to", "2026-10-02", "--per-page", "10", "--page", "2")
	if code != 0 || !strings.Contains(stderr, "--page 3") {
		t.Errorf("page 2 of 3: exit = %d, stderr = %q", code, stderr)
	}
	_, _, stderr = h.run("reports", "spend", "--from", "2026-10-01", "--to", "2026-10-02", "--per-page", "10", "--page", "3")
	if strings.Contains(stderr, "There is more") {
		t.Errorf("the last page: stderr = %q", stderr)
	}
}

// A server that never stops offering a next page is read up to a cap, and
// then the command fails rather than show part of the list.
func TestEndlessPagesFailLoudly(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.data = map[string]func(*http.Request) (int, any){"/v2/channels": func(*http.Request) (int, any) {
		return http.StatusOK, map[string]any{
			"data":  []any{map[string]any{"id": "1", "type": "channel", "attributes": map[string]any{"name": "Again"}}},
			"links": map[string]string{"next": "/v2/channels?page=again"},
		}
	}}
	code, stdout, stderr := h.run("channels", "list", "--all")
	if code != 7 || stdout != "" || !strings.Contains(stderr, "more than 1000 pages") || len(h.fake.dataAsked) != 1000 {
		t.Errorf("exit = %d, %d reads, stdout = %q, stderr = %q", code, len(h.fake.dataAsked), stdout, stderr)
	}
}

// The lists the API pages without its specs saying so are paged like the
// others; a saved report's data, like a report, is every row.
func TestEveryPagedListPages(t *testing.T) {
	for _, tc := range []struct{ args, path string }{
		{"skadnetworks list", "/v2/sk_ad_networks"},
		{"channels callback-group-templates 18", "/v2/integrations/18/callback_group_templates"},
		{"apps callback-groups list --app " + testApp + " --channel 18", "/v2/apps/" + testApp + "/integrations/18/callback_groups"},
	} {
		t.Run(tc.args, func(t *testing.T) {
			h := newHarness(t)
			h.signIn()
			h.fake.data = map[string]func(*http.Request) (int, any){tc.path: func(r *http.Request) (int, any) {
				doc := map[string]any{"data": []any{map[string]any{"id": "x", "type": "thing", "attributes": map[string]any{"name": "Page " + r.URL.Query().Get("page")}}}}
				if r.URL.Query().Get("page") != "2" {
					doc["links"] = map[string]string{"next": tc.path + "?page=2"}
				}
				return http.StatusOK, doc
			}}
			code, _, stderr := h.run(strings.Fields(tc.args + " --page 1 --per-page 10")...)
			if q := h.fake.dataAsked[0].Query(); code != 0 || q.Get("page") != "1" || q.Get("per_page") != "10" || !strings.Contains(stderr, "--page 2") {
				t.Errorf("one page: exit = %d, query %q, stderr = %q", code, h.fake.dataAsked[0].RawQuery, stderr)
			}
			h.fake.dataAsked = nil
			code, stdout, _ := h.run(strings.Fields(tc.args + " --all")...)
			if code != 0 || len(h.fake.dataAsked) != 2 || !strings.Contains(stdout, "Page 2") {
				t.Errorf("--all: exit = %d, %d reads:\n%s", code, len(h.fake.dataAsked), stdout)
			}
		})
	}

	h := newHarness(t)
	h.signIn()
	path := "/v2/reports/" + testOther
	h.fake.data = map[string]func(*http.Request) (int, any){path: func(r *http.Request) (int, any) {
		doc := map[string]any{"data": []any{map[string]any{"type": "report", "attributes": map[string]any{"date": "2026-10-0" + r.URL.Query().Get("page")}}}}
		if r.URL.Query().Get("page") != "2" {
			doc["links"] = map[string]string{"next": path + "?page=2"}
		}
		return http.StatusOK, doc
	}}
	code, stdout, _ := h.run("saved-reports", "run", testOther)
	if code != 0 || len(h.fake.dataAsked) != 2 || !strings.Contains(stdout, "2026-10-01") || !strings.Contains(stdout, "2026-10-02") {
		t.Errorf("saved-reports run: exit = %d, %d reads:\n%s", code, len(h.fake.dataAsked), stdout)
	}
}

// A value or an attribute name from the API cannot add rows, columns or
// escape codes to a table.
func TestTablesCannotBeSpoofed(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.data = map[string]func(*http.Request) (int, any){
		"/v2/reports/spend": func(*http.Request) (int, any) {
			return http.StatusOK, json.RawMessage(`{"data":[{"type":"report","attributes":{"na\u001b[31mme\nFAKE":"Evil\nFAKE ROW\t9","spend":1}}]}`)
		},
	}
	code, stdout, _ := h.run("reports", "spend", "--from", "2026-10-01", "--to", "2026-10-01")
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if code != 0 || len(lines) != 2 || strings.Contains(stdout, "\x1b") || !strings.Contains(lines[1], "Evil FAKE ROW 9") {
		t.Errorf("exit = %d:\n%s", code, stdout)
	}
	h.fake.siteIDRules = []map[string]any{{"siteId": "s", "appId": "a\nFAKE", "createTime": "t\x1b[2K"}}
	_, stdout, _ = h.run("site-ids", "list")
	if lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n"); len(lines) != 2 || strings.Contains(stdout, "\x1b") {
		t.Errorf("site-ids list:\n%s", stdout)
	}
}

// --channel 0 asks for Organic; a list given no --channel is not narrowed.
func TestChannelZeroIsOrganic(t *testing.T) {
	for _, list := range []string{"campaigns list", "ad-accounts list", "apps callbacks list --app " + testApp, "apps callback-templates list --app " + testApp} {
		t.Run(list, func(t *testing.T) {
			h := newHarness(t)
			h.signIn()
			if code, _, stderr := h.run(strings.Fields(list + " --channel 0")...); code != 0 || h.fake.dataAsked[0].Query().Get("channel_id") != "0" {
				t.Errorf("--channel 0: exit = %d, query %q, stderr = %q", code, h.fake.dataAsked[0].RawQuery, stderr)
			}
			if code, _, _ := h.run(strings.Fields(list)...); code != 0 || h.fake.dataAsked[1].Query().Has("channel_id") {
				t.Errorf("no --channel: exit = %d, query %q", code, h.fake.dataAsked[1].RawQuery)
			}
			if code, _, _ := h.run(strings.Fields(list + " --channel organic")...); code != 2 {
				t.Errorf("--channel organic: exit = %d", code)
			}
		})
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
		"reports spend --from 2026-10-07 --to 2026-10-01",
		"reports spend --from 2026-10-01 --to 2026-10-02 --granularity hourly",
		"reports ad-revenue --from 2026-10-01 --to 2026-10-02 --group-by site",
		"reports skan --from 2026-10-01 --to 2026-10-02 --group-by campaign",
		"reports skadnetwork --from 2026-10-01 --to 2026-10-02 --granularity weekly",
		"reports spend --from 2026-10-01 --to 2026-10-02 --app com.example.sample",
		"reports spend --from 2026-10-01 --to 2026-10-02 --channel facebook",
		"skadnetworks get .",
		"skadnetworks get ..",
		"skadnetworks get 4fzdc2evr5",
		"skadnetworks get ../sk_ad_networks",
		"saved-reports list --type weekly",
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

// A report shows every row unless a page is asked for.
func TestReportsReadEveryPage(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.data = map[string]func(*http.Request) (int, any){"/v2/reports/spend": func(r *http.Request) (int, any) {
		doc := map[string]any{"data": []any{map[string]any{"type": "report", "attributes": map[string]any{"date": "2026-10-0" + r.URL.Query().Get("page")}}}}
		if r.URL.Query().Get("page") != "2" {
			doc["links"] = map[string]string{"next": "/v2/reports/spend?page=2"}
		}
		return http.StatusOK, doc
	}}
	code, stdout, _ := h.run("reports", "spend", "--from", "2026-10-01", "--to", "2026-10-02")
	if code != 0 || !strings.Contains(stdout, "2026-10-01") || !strings.Contains(stdout, "2026-10-02") {
		t.Errorf("exit = %d:\n%s", code, stdout)
	}
	h.fake.dataAsked = nil
	_, _, stderr := h.run("reports", "spend", "--from", "2026-10-01", "--to", "2026-10-02", "--per-page", "1")
	if len(h.fake.dataAsked) != 1 || !strings.Contains(stderr, "--page 2") {
		t.Errorf("--per-page: %d reads, stderr = %q", len(h.fake.dataAsked), stderr)
	}
	if code, _, _ := h.run("channels", "list", "--per-page", "5000"); code != 2 {
		t.Errorf("--per-page 5000: exit = %d", code)
	}
}

// -o csv writes a data command's rows for a spreadsheet; other commands
// refuse it.
func TestDataCSV(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.data = map[string]func(*http.Request) (int, any){"/v2/reports/spend": func(*http.Request) (int, any) {
		return http.StatusOK, json.RawMessage(`{"data":[{"type":"report","attributes":{"date":"2026-10-01","name":"Sample, Inc.","spend":12.5}}]}`)
	}}
	code, stdout, _ := h.run("reports", "spend", "--from", "2026-10-01", "--to", "2026-10-01", "-o", "csv")
	if want := "date,name,spend\n2026-10-01,\"Sample, Inc.\",12.5\n"; code != 0 || stdout != want {
		t.Errorf("exit = %d, stdout = %q, want %q", code, stdout, want)
	}
	if code, _, stderr := h.run("doctor", "-o", "csv"); code != 2 || !strings.Contains(stderr, "data commands") {
		t.Errorf("doctor -o csv: exit = %d, stderr = %q", code, stderr)
	}
}
