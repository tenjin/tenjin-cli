package cli

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Each write command makes one API write, with only the fields given.
func TestWriteCommandsSendTheirBodies(t *testing.T) {
	app, other := testApp, testOther
	for _, tc := range []struct {
		args         []string
		method, path string
		body         string
	}{
		{[]string{"apps", "update", app, "--name", "New name", "--protocol", "fb123"}, "PATCH", "/v2/apps/" + app, `{"name":"New name","protocol":"fb123"}`},
		{[]string{"apps", "delete", app}, "DELETE", "/v2/apps/" + app, ``},
		{[]string{"campaigns", "create", "--app", app, "--name", "Summer", "--channel", "0"}, "POST", "/v2/campaigns", `{"app_id":"` + app + `","name":"Summer","channel_id":0}`},
		{[]string{"campaigns", "create", "--app", app, "--name", "Summer", "--ad-account", other}, "POST", "/v2/campaigns", `{"app_id":"` + app + `","name":"Summer","ad_account_id":"` + other + `"}`},
		{[]string{"campaigns", "update", other, "--attribution-window", "86400"}, "PATCH", "/v2/campaigns/" + other, `{"attribution_window":86400}`},
		{[]string{"campaigns", "delete", other}, "DELETE", "/v2/campaigns/" + other, ``},
		{[]string{"apps", "callbacks", "create", "--app", app, "--template", other, "--event", "open", "--active=false", "--param", "token=abc", "--param", "url=a=b"}, "POST", "/v2/apps/" + app + "/callbacks",
			`{"callback_template_id":"` + other + `","event":"open","active":false,"customer_parameters":{"token":"abc","url":"a=b"}}`},
		{[]string{"apps", "callbacks", "update", other, "--app", app, "--active"}, "PATCH", "/v2/apps/" + app + "/callbacks/" + other, `{"active":true}`},
		{[]string{"apps", "s2s-callbacks", "create", "--app", app, "--url", "https://example.com/hook", "--event", "open", "--name", "Opens", "--action", "ping", "--user-filter", "1,2"}, "POST", "/v2/apps/" + app + "/s2s_callbacks",
			`{"url":"https://example.com/hook","event":"open","name":"Opens","action":"ping","user_filter":[1,2]}`},
		{[]string{"apps", "s2s-callbacks", "update", other, "--app", app, "--http-method", "POST"}, "PATCH", "/v2/apps/" + app + "/s2s_callbacks/" + other, `{"http_method":"POST"}`},
		{[]string{"apps", "callback-groups", "create", "--app", app, "--channel", "18", "--template", other, "--event", "open", "--setting", "pixel=42"}, "POST", "/v2/apps/" + app + "/integrations/18/callback_groups",
			`{"callback_group_template_id":"` + other + `","event":"open","callback_settings":{"pixel":"42"}}`},
		{[]string{"apps", "callback-groups", "update", other, "--app", app, "--channel", "18", "--name", "Installs"}, "PATCH", "/v2/apps/" + app + "/integrations/18/callback_groups/" + other, `{"name":"Installs"}`},
		{[]string{"apps", "callback-settings", "update", "--app", app, "--channel", "18", "--setting", "pixel=42"}, "PATCH", "/v2/apps/" + app + "/integrations/18/callback_settings", `{"callback_settings":{"pixel":"42"}}`},
		{[]string{"saved-reports", "create", "--type", "user_acquisition", "--name", "Weekly", "--granularity", "daily", "--group-by", "app", "--past-days", "7"}, "POST", "/v2/saved_reports",
			`{"report_type":"user_acquisition","name":"Weekly","granularity":"daily","group_by":"app","past_number_days":7}`},
		{[]string{"saved-reports", "update", other, "--name", "Monthly"}, "PATCH", "/v2/saved_reports/" + other, `{"name":"Monthly"}`},
		{[]string{"saved-reports", "delete", other}, "DELETE", "/v2/saved_reports/" + other, ``},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			h := newHarness(t)
			h.signIn()
			code, _, stderr := h.run(append(tc.args, "--yes")...)
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr)
			}
			if len(h.fake.writes) != 1 {
				t.Fatalf("%d writes, want 1", len(h.fake.writes))
			}
			got := h.fake.writes[0]
			if got.method != tc.method || got.path != tc.path {
				t.Errorf("wrote %s %s, want %s %s", got.method, got.path, tc.method, tc.path)
			}
			if tc.body != "" {
				var want map[string]any
				if err := json.Unmarshal([]byte(tc.body), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.body, want) {
					t.Errorf("body = %v, want %v", got.body, want)
				}
			}
		})
	}
}

// A write asks first at a terminal, needs --yes elsewhere, and a delete
// defaults to no.
func TestWritesAskFirst(t *testing.T) {
	create := []string{"campaigns", "create", "--app", testApp, "--name", "Summer", "--channel", "5"}

	h := newHarness(t)
	h.signIn()
	if code, _, stderr := h.run(create...); code != 2 || len(h.fake.writes) != 0 || !strings.Contains(stderr, "--yes") {
		t.Errorf("without a terminal or --yes: exit = %d, %d writes, stderr = %q", code, len(h.fake.writes), stderr)
	}

	h = newHarness(t)
	h.signIn()
	h.typing("")
	if code, _, stderr := h.run(create...); code != 0 || len(h.fake.writes) != 1 || !strings.Contains(stderr, `Create the campaign "Summer"`) {
		t.Errorf("Enter on a create: exit = %d, %d writes, stderr = %q", code, len(h.fake.writes), stderr)
	}

	h = newHarness(t)
	h.signIn()
	h.typing("")
	if code, _, stderr := h.run("campaigns", "delete", testOther); code != 0 || len(h.fake.writes) != 0 || !strings.Contains(stderr, "[y/N]") || !strings.Contains(stderr, "Nothing was changed.") {
		t.Errorf("Enter on a delete: exit = %d, %d writes, stderr = %q", code, len(h.fake.writes), stderr)
	}

	h = newHarness(t)
	h.signIn()
	h.typing("y")
	if code, stdout, _ := h.run("campaigns", "delete", testOther); code != 0 || len(h.fake.writes) != 1 || !strings.Contains(stdout, "Deleted.") {
		t.Errorf("yes on a delete: exit = %d, %d writes, stdout = %q", code, len(h.fake.writes), stdout)
	}
}

// The API creates a callback active whatever it is sent: one asked for
// inactive is switched off straight after.
func TestInactiveCallbackIsSwitchedOff(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.writeAnswers = map[string]func(*http.Request) (int, any){
		"POST /v2/apps/" + testApp + "/callbacks": func(*http.Request) (int, any) {
			return http.StatusCreated, map[string]any{"data": map[string]any{"id": testOther, "type": "callback", "attributes": map[string]any{"active": true}}}
		},
	}
	code, _, stderr := h.run("apps", "callbacks", "create", "--app", testApp, "--template", testOther, "--active=false", "--yes")
	if code != 0 || len(h.fake.writes) != 2 {
		t.Fatalf("exit = %d, %d writes, stderr = %q", code, len(h.fake.writes), stderr)
	}
	if w := h.fake.writes[1]; w.method != "PATCH" || w.path != "/v2/apps/"+testApp+"/callbacks/"+testOther || w.body["active"] != false {
		t.Errorf("follow-up = %s %s %v", w.method, w.path, w.body)
	}
}

// --dry-run prints the body and sends nothing.
func TestWriteDryRun(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, stdout, _ := h.run("apps", "s2s-callbacks", "create", "--app", testApp, "--url", "https://example.com/hook", "--event", "open", "--name", "Opens", "--action", "ping", "--dry-run")
	var body map[string]any
	if err := json.Unmarshal([]byte(stdout), &body); err != nil || code != 0 || body["url"] != "https://example.com/hook" || len(h.fake.writes) != 0 {
		t.Errorf("exit = %d, %d writes, stdout = %q", code, len(h.fake.writes), stdout)
	}
}

// What a write needs is checked before anything is sent.
func TestWriteUsageErrors(t *testing.T) {
	for _, args := range []string{
		"apps update " + testApp,
		"campaigns create --app " + testApp + " --name Summer",
		"campaigns create --app " + testApp + " --name Summer --channel 5 --ad-account " + testOther,
		"campaigns create --name Summer --channel 5",
		"campaigns update " + testOther,
		"apps callbacks create --app " + testApp + " --event open",
		"apps callbacks create --app " + testApp + " --template " + testOther + " --param nokey",
		"apps s2s-callbacks create --app " + testApp + " --event open --name Opens --action ping",
		"apps s2s-callbacks create --app " + testApp + " --url https://example.com/hook --event open",
		"apps callback-groups create --app " + testApp + " --channel 18 --template " + testOther,
		"apps callback-settings update --app " + testApp + " --setting a=b",
		"saved-reports create --name Weekly --granularity daily --group-by app",
		"campaigns delete not-an-id",
	} {
		t.Run(args, func(t *testing.T) {
			h := newHarness(t)
			h.signIn()
			code, _, stderr := h.run(append(strings.Fields(args), "--yes")...)
			if code != 2 || len(h.fake.writes) != 0 {
				t.Errorf("exit = %d, %d writes, stderr = %q", code, len(h.fake.writes), stderr)
			}
		})
	}
}

// The API's answer is what is shown: the resource as a table, or as it is
// with --json; a refusal keeps its message.
func TestWriteAnswers(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	code, stdout, _ := h.run("saved-reports", "update", testOther, "--name", "Monthly", "--yes")
	if code != 0 || !strings.Contains(stdout, "name") || !strings.Contains(stdout, "Monthly") {
		t.Errorf("update: exit = %d, stdout:\n%s", code, stdout)
	}
	code, stdout, _ = h.run("saved-reports", "delete", testOther, "--yes", "--json")
	if code != 0 || strings.TrimSpace(stdout) != "{}" {
		t.Errorf("delete --json: exit = %d, stdout = %q", code, stdout)
	}
	h.fake.writeAnswers = map[string]func(*http.Request) (int, any){
		"DELETE /v2/campaigns/" + testOther: func(*http.Request) (int, any) {
			return http.StatusBadRequest, map[string]string{"error": "Campaign cannot be deleted because it has started tracking installs"}
		},
	}
	code, _, stderr := h.run("campaigns", "delete", testOther, "--yes")
	if code == 0 || !strings.Contains(stderr, "started tracking installs") {
		t.Errorf("refused delete: exit = %d, stderr = %q", code, stderr)
	}
}
