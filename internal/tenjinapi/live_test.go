//go:build liveapi

// Calls every operation of the generated clients against a real Tenjin API
// and checks that each answers 2xx and decodes as its spec says. Opt-in:
//
//	TENJIN_API_TOKEN=<a dashboard API token> go test -tags liveapi -run Live -v ./internal/tenjinapi
//
// TENJIN_API_URL points it at another API (default production). It only
// reads, unless TENJIN_LIVE_WRITE=1: then it creates an app named
// "tenjin-cli live test", and on it a campaign, callbacks and a saved report,
// changes each, and deletes what the API can delete, the app last.
// Callbacks are created inactive and point at example.com. Bodies are never
// printed: SDK keys and shared secrets come back in them.
package tenjinapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/tenjin/tenjin-cli/internal/tenjinapi/cma"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi/rma"
)

// A User-Agent of its own, so these requests are not taken for the CLI's.
const liveUserAgent = "tenjin-cli-livetest/0"

type live struct {
	cma *cma.ClientWithResponses
	rma *rma.ClientWithResponses
	ctx context.Context
}

func newLive(t *testing.T) *live {
	token := os.Getenv("TENJIN_API_TOKEN")
	if token == "" {
		t.Skip("set TENJIN_API_TOKEN to a dashboard API token to run against a real API")
	}
	base := os.Getenv("TENJIN_API_URL")
	if base == "" {
		base = "https://api.tenjin.com/v2"
	}
	sign := func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", liveUserAgent)
		return nil
	}
	httpClient := &http.Client{Timeout: 60 * time.Second}
	c, err := cma.NewClientWithResponses(base, cma.WithHTTPClient(httpClient), cma.WithRequestEditorFn(sign))
	if err != nil {
		t.Fatal(err)
	}
	r, err := rma.NewClientWithResponses(base, rma.WithHTTPClient(httpClient), rma.WithRequestEditorFn(sign))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)
	return &live{cma: c, rma: r, ctx: ctx}
}

// ok checks a generated response: no transport or decode error, a 2xx, and a
// body that decoded into the spec's type. It returns the raw body.
func ok(t *testing.T, name string, r result) []byte {
	t.Helper()
	resp, err := r.resp, r.err
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	v := reflect.ValueOf(resp).Elem()
	httpResp, _ := v.FieldByName("HTTPResponse").Interface().(*http.Response)
	body := v.FieldByName("Body").Bytes()
	if httpResp == nil {
		t.Fatalf("%s: no response", name)
	}
	if httpResp.StatusCode/100 != 2 {
		t.Fatalf("%s: HTTP %d: %s", name, httpResp.StatusCode, errorText(body))
	}
	decoded := false
	for i := range v.NumField() {
		if strings.HasPrefix(v.Type().Field(i).Name, "JSON") && !v.Field(i).IsNil() {
			decoded = true
		}
	}
	if !decoded && len(bytes.TrimSpace(body)) > 0 {
		t.Errorf("%s: HTTP %d, but the body did not decode as the spec says (Content-Type %q)", name, httpResp.StatusCode, httpResp.Header.Get("Content-Type"))
	}
	t.Logf("%s: HTTP %d, %s", name, httpResp.StatusCode, describe(body))
	return body
}

// errorText is an error body's message, short, with nothing else from it.
func errorText(body []byte) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != nil {
		s := fmt.Sprint(e.Error)
		if len(s) > 200 {
			s = s[:200] + "…"
		}
		return s
	}
	return fmt.Sprintf("%d bytes", len(body))
}

func items(body []byte) []map[string]any {
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if json.Unmarshal(body, &list) == nil && list.Data != nil {
		return list.Data
	}
	var one struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal(body, &one) == nil && one.Data != nil {
		return []map[string]any{one.Data}
	}
	return nil
}

func describe(body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return "no body"
	}
	n := len(items(body))
	if n == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", n)
}

// firstID is the id of the first item, as text.
func firstID(body []byte) string {
	if list := items(body); len(list) > 0 {
		switch id := list[0]["id"].(type) {
		case string:
			return id
		case float64:
			return strconv.FormatInt(int64(id), 10)
		}
	}
	return ""
}

func mustUUID(t *testing.T, s string) openapi_types.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("not a UUID: %q", s)
	}
	return id
}

func jsonBody(v any) *bytes.Reader {
	raw, _ := json.Marshal(v)
	return bytes.NewReader(raw)
}

func TestLiveRead(t *testing.T) {
	l := newLive(t)
	page := &cma.GetAppsParams{PerPage: new(25)}
	apps := ok(t, "GET /apps", wrap(l.cma.GetAppsWithResponse(l.ctx, page)))
	appID := os.Getenv("TENJIN_LIVE_APP_ID")
	if appID == "" {
		appID = firstID(apps)
	}
	if appID == "" {
		t.Fatal("the account has no app to read")
	}
	app := mustUUID(t, appID)

	t.Run("apps", func(t *testing.T) {
		ok(t, "GET /apps/{id}", wrap(l.cma.GetAppsIdWithResponse(l.ctx, app)))
		ok(t, "GET /apps/{id}/events", wrap(l.cma.GetAppsAppIdEventsWithResponse(l.ctx, app, &cma.GetAppsAppIdEventsParams{PerPage: new(25)})))
		ok(t, "GET /apps/{id}/sdk_keys", wrap(l.cma.GetAppsAppIdSdkKeysWithResponse(l.ctx, app, nil)))
	})
	t.Run("callbacks", func(t *testing.T) {
		body := ok(t, "GET /apps/{id}/callbacks", wrap(l.cma.GetAppsAppIdCallbacksWithResponse(l.ctx, app, nil)))
		if id := firstID(body); id != "" {
			ok(t, "GET /apps/{id}/callbacks/{id}", wrap(l.cma.GetAppsAppIdCallbacksIdWithResponse(l.ctx, app, mustUUID(t, id))))
		}
		body = ok(t, "GET /apps/{id}/s2s_callbacks", wrap(l.cma.GetAppsAppIdS2sCallbacksWithResponse(l.ctx, app, nil)))
		if id := firstID(body); id != "" {
			ok(t, "GET /apps/{id}/s2s_callbacks/{id}", wrap(l.cma.GetAppsAppIdS2sCallbacksIdWithResponse(l.ctx, app, mustUUID(t, id))))
		}
		body = ok(t, "GET /apps/{id}/callback_templates", wrap(l.cma.GetAppsAppIdCallbackTemplatesWithResponse(l.ctx, app, nil)))
		if id := firstID(body); id != "" {
			ok(t, "GET /apps/{id}/callback_templates/{id}", wrap(l.cma.GetAppsAppIdCallbackTemplatesIdWithResponse(l.ctx, app, mustUUID(t, id))))
		}
	})
	t.Run("channels", func(t *testing.T) {
		body := ok(t, "GET /channels", wrap(l.cma.GetChannelsWithResponse(l.ctx, &cma.GetChannelsParams{PerPage: new(25)})))
		if id := firstID(body); id != "" {
			n, _ := strconv.Atoi(id)
			ok(t, "GET /channels/{id}", wrap(l.cma.GetChannelsIdWithResponse(l.ctx, n)))
		}
	})
	t.Run("ad accounts", func(t *testing.T) {
		body := ok(t, "GET /ad_accounts", wrap(l.cma.GetAdAccountsWithResponse(l.ctx, nil)))
		if id := firstID(body); id != "" {
			ok(t, "GET /ad_accounts/{id}", wrap(l.cma.GetAdAccountsIdWithResponse(l.ctx, mustUUID(t, id))))
		}
	})
	t.Run("campaigns", func(t *testing.T) {
		body := ok(t, "GET /campaigns", wrap(l.cma.GetCampaignsWithResponse(l.ctx, &cma.GetCampaignsParams{PerPage: new(25)})))
		if id := firstID(body); id != "" {
			ok(t, "GET /campaigns/{id}", wrap(l.cma.GetCampaignsIdWithResponse(l.ctx, mustUUID(t, id))))
		}
	})
	t.Run("skadnetworks", func(t *testing.T) {
		body := ok(t, "GET /sk_ad_networks", wrap(l.cma.GetSkAdNetworksWithResponse(l.ctx)))
		if id := firstID(body); id != "" {
			ok(t, "GET /sk_ad_networks/{id}", wrap(l.cma.GetSkAdNetworksIdWithResponse(l.ctx, id)))
		}
	})
	t.Run("callback groups", func(t *testing.T) {
		channel := channelWithGroupTemplates(t, l)
		if channel == 0 {
			t.Skip("no channel among the first ones has callback group templates")
		}
		ok(t, "GET /apps/{id}/integrations/{id}/callback_groups", wrap(l.cma.GetAppsAppIdIntegrationsIntegrationIdCallbackGroupsWithResponse(l.ctx, app, channel)))
		ok(t, "GET /apps/{id}/integrations/{id}/callback_settings", wrap(l.cma.GetAppsAppIdIntegrationsIntegrationIdCallbackSettingsWithResponse(l.ctx, app, channel)))
	})
	t.Run("reports", func(t *testing.T) {
		end := time.Now().UTC()
		from, to := openapi_types.Date{Time: end.AddDate(0, 0, -30)}, openapi_types.Date{Time: end}
		// Each grouping by one name and by several: meta.group_by is a string
		// for one and a list for several.
		for _, group := range []string{"app", "channel,app"} {
			t.Run(group, func(t *testing.T) {
				spend := rma.GetReportsSpendParamsGroupBy(group)
				ok(t, "GET /reports/spend", wrap(l.rma.GetReportsSpendWithResponse(l.ctx, &rma.GetReportsSpendParams{StartDate: from, EndDate: to, PerPage: new(5), GroupBy: &spend})))
				revenue := rma.GetReportsAdRevenueParamsGroupBy(group)
				ok(t, "GET /reports/ad_revenue", wrap(l.rma.GetReportsAdRevenueWithResponse(l.ctx, &rma.GetReportsAdRevenueParams{StartDate: from, EndDate: to, PerPage: new(5), GroupBy: &revenue})))
			})
		}
		for _, group := range []string{"app", "app,channel"} {
			t.Run("skadnetwork "+group, func(t *testing.T) {
				sk := rma.GetReportsSkAdNetworkParamsGroupBy(group)
				ok(t, "GET /reports/sk_ad_network", wrap(l.rma.GetReportsSkAdNetworkWithResponse(l.ctx, &rma.GetReportsSkAdNetworkParams{StartDate: from, EndDate: to, PerPage: new(5), GroupBy: &sk})))
				skan := rma.GetReportsSkanAppleParamsGroupBy(group)
				ok(t, "GET /reports/skan_apple", wrap(l.rma.GetReportsSkanAppleWithResponse(l.ctx, &rma.GetReportsSkanAppleParams{StartDate: from, EndDate: to, PerPage: new(5), GroupBy: &skan})))
			})
		}
		body := ok(t, "GET /saved_reports", wrap(l.rma.GetSavedReportsWithResponse(l.ctx, nil)))
		if id := firstID(body); id != "" {
			ok(t, "GET /saved_reports/{id}", wrap(l.rma.GetSavedReportsIdWithResponse(l.ctx, mustUUID(t, id))))
			ok(t, "GET /reports/{id}", wrap(l.rma.GetReportsIdWithResponse(l.ctx, mustUUID(t, id))))
		}
	})
}

// channelWithGroupTemplates is the first channel, of the first few, with
// callback group templates; 0 if none.
func channelWithGroupTemplates(t *testing.T, l *live) int {
	t.Helper()
	resp, err := l.cma.GetChannelsWithResponse(l.ctx, &cma.GetChannelsParams{PerPage: new(25)})
	if err != nil || resp.HTTPResponse.StatusCode != http.StatusOK {
		return 0
	}
	for _, c := range items(resp.Body) {
		id, _ := c["id"].(float64)
		templates, err := l.cma.GetIntegrationsIntegrationIdCallbackGroupTemplatesWithResponse(l.ctx, int(id))
		if err == nil && templates.HTTPResponse.StatusCode == http.StatusOK && len(items(templates.Body)) > 0 {
			ok(t, "GET /integrations/{id}/callback_group_templates", result{templates, nil})
			return int(id)
		}
	}
	return 0
}

func TestLiveWrite(t *testing.T) {
	if os.Getenv("TENJIN_LIVE_WRITE") != "1" {
		t.Skip("set TENJIN_LIVE_WRITE=1 to create, change and delete test data")
	}
	l := newLive(t)
	stamp := time.Now().UTC().Format("20060102150405")
	name := "tenjin-cli live test " + stamp

	body := ok(t, "POST /apps", wrap(l.cma.PostAppsWithBodyWithResponse(l.ctx, contentJSON, jsonBody(map[string]string{
		"name": name, "bundle_id": "com.tenjin.cli.livetest.t" + stamp, "platform": "android",
	}))))
	app := mustUUID(t, firstID(body))
	t.Cleanup(func() {
		ok(t, "DELETE /apps/{id}", wrap(l.cma.DeleteAppsIdWithResponse(context.Background(), app)))
	})
	ok(t, "PATCH /apps/{id}", wrap(l.cma.PatchAppsIdWithBodyWithResponse(l.ctx, app, contentJSON, jsonBody(map[string]string{"name": name + " (renamed)"}))))

	t.Run("campaign", func(t *testing.T) {
		channels := ok(t, "GET /channels", wrap(l.cma.GetChannelsWithResponse(l.ctx, &cma.GetChannelsParams{PerPage: new(25)})))
		channel, _ := strconv.Atoi(firstID(channels))
		body := ok(t, "POST /campaigns", wrap(l.cma.PostCampaignsWithBodyWithResponse(l.ctx, contentJSON, jsonBody(map[string]any{
			"name": name, "app_id": app.String(), "channel_id": channel,
		}))))
		campaign := mustUUID(t, firstID(body))
		deleted := false
		t.Cleanup(func() {
			if !deleted {
				_, _ = l.cma.DeleteCampaignsIdWithResponse(context.Background(), campaign)
			}
		})
		ok(t, "GET /campaigns/{id}", wrap(l.cma.GetCampaignsIdWithResponse(l.ctx, campaign)))
		// The attribution window is the only field a campaign lets change.
		ok(t, "PATCH /campaigns/{id}", wrap(l.cma.PatchCampaignsIdWithBodyWithResponse(l.ctx, campaign, contentJSON, jsonBody(map[string]int{"attribution_window": 86400}))))
		ok(t, "DELETE /campaigns/{id}", wrap(l.cma.DeleteCampaignsIdWithResponse(l.ctx, campaign)))
		deleted = true
	})
	t.Run("s2s callback", func(t *testing.T) {
		body := ok(t, "POST /apps/{id}/s2s_callbacks", wrap(l.cma.PostAppsAppIdS2sCallbacksWithBodyWithResponse(l.ctx, app, contentJSON, jsonBody(map[string]any{
			"name": name, "event": "open", "url": "https://example.com/tenjin-cli-live-test", "http_method": "GET", "action": "ping", "active": false,
		}))))
		id := mustUUID(t, firstID(body))
		ok(t, "GET /apps/{id}/s2s_callbacks/{id}", wrap(l.cma.GetAppsAppIdS2sCallbacksIdWithResponse(l.ctx, app, id)))
		ok(t, "PATCH /apps/{id}/s2s_callbacks/{id}", wrap(l.cma.PatchAppsAppIdS2sCallbacksIdWithBodyWithResponse(l.ctx, app, id, contentJSON, jsonBody(map[string]any{"name": name + " (renamed)", "active": false}))))
	})
	t.Run("callback", func(t *testing.T) {
		templates := ok(t, "GET /apps/{id}/callback_templates", wrap(l.cma.GetAppsAppIdCallbackTemplatesWithResponse(l.ctx, app, nil)))
		template := firstID(templates)
		if template == "" {
			t.Skip("the new app has no callback templates")
		}
		// A template names the customer parameters a callback must give.
		params := map[string]string{}
		if required, ok := items(templates)[0]["attributes"].(map[string]any)["customer_parameters"].(map[string]any); ok {
			for key := range required {
				params[key] = "tenjin-cli-live-test"
			}
		}
		body := ok(t, "POST /apps/{id}/callbacks", wrap(l.cma.PostAppsAppIdCallbacksWithBodyWithResponse(l.ctx, app, contentJSON, jsonBody(map[string]any{
			"callback_template_id": template, "name": name, "event": "open", "active": false, "customer_parameters": params,
		}))))
		id := mustUUID(t, firstID(body))
		ok(t, "GET /apps/{id}/callbacks/{id}", wrap(l.cma.GetAppsAppIdCallbacksIdWithResponse(l.ctx, app, id)))
		ok(t, "PATCH /apps/{id}/callbacks/{id}", wrap(l.cma.PatchAppsAppIdCallbacksIdWithBodyWithResponse(l.ctx, app, id, contentJSON, jsonBody(map[string]any{"name": name + " (renamed)", "active": false}))))
	})
	t.Run("callback group", func(t *testing.T) {
		channel := channelWithGroupTemplates(t, l)
		if channel == 0 {
			t.Skip("no channel among the first ones has callback group templates")
		}
		templates := ok(t, "GET /integrations/{id}/callback_group_templates", wrap(l.cma.GetIntegrationsIntegrationIdCallbackGroupTemplatesWithResponse(l.ctx, channel)))
		body := ok(t, "POST /apps/{id}/integrations/{id}/callback_groups", wrap(l.cma.PostAppsAppIdIntegrationsIntegrationIdCallbackGroupsWithBodyWithResponse(l.ctx, app, channel, contentJSON, jsonBody(map[string]any{
			"callback_group_template_id": firstID(templates), "event": "open", "name": name, "active": false,
		}))))
		id := mustUUID(t, firstID(body))
		ok(t, "GET /apps/{id}/integrations/{id}/callback_groups/{id}", wrap(l.cma.GetAppsAppIdIntegrationsIntegrationIdCallbackGroupsIdWithResponse(l.ctx, app, channel, id)))
		ok(t, "PATCH /apps/{id}/integrations/{id}/callback_groups/{id}", wrap(l.cma.PatchAppsAppIdIntegrationsIntegrationIdCallbackGroupsIdWithBodyWithResponse(l.ctx, app, channel, id, contentJSON, jsonBody(map[string]any{"name": name + " (renamed)", "active": false}))))
		// The settings go back as they were read.
		settings := ok(t, "GET /apps/{id}/integrations/{id}/callback_settings", wrap(l.cma.GetAppsAppIdIntegrationsIntegrationIdCallbackSettingsWithResponse(l.ctx, app, channel)))
		var current struct {
			Data struct {
				Attributes struct {
					CallbackSettings map[string]any `json:"callback_settings"`
				} `json:"attributes"`
			} `json:"data"`
		}
		_ = json.Unmarshal(settings, &current)
		ok(t, "PATCH /apps/{id}/integrations/{id}/callback_settings", wrap(l.cma.PatchAppsAppIdIntegrationsIntegrationIdCallbackSettingsWithBodyWithResponse(l.ctx, app, channel, contentJSON, jsonBody(map[string]any{
			"callback_settings": current.Data.Attributes.CallbackSettings,
		}))))
	})
	t.Run("saved report", func(t *testing.T) {
		body := ok(t, "POST /saved_reports", wrap(l.rma.PostSavedReportsWithBodyWithResponse(l.ctx, contentJSON, jsonBody(map[string]any{
			"name": name, "report_type": "user_acquisition", "granularity": "daily", "group_by": "app", "past_number_days": 7,
		}))))
		id := mustUUID(t, firstID(body))
		deleted := false
		t.Cleanup(func() {
			if !deleted {
				_, _ = l.rma.DeleteSavedReportsIdWithResponse(context.Background(), id)
			}
		})
		ok(t, "GET /saved_reports/{id}", wrap(l.rma.GetSavedReportsIdWithResponse(l.ctx, id)))
		ok(t, "GET /reports/{id}", wrap(l.rma.GetReportsIdWithResponse(l.ctx, id)))
		ok(t, "PATCH /saved_reports/{id}", wrap(l.rma.PatchSavedReportsIdWithBodyWithResponse(l.ctx, id, contentJSON, jsonBody(map[string]any{"name": name + " (renamed)"}))))
		ok(t, "DELETE /saved_reports/{id}", wrap(l.rma.DeleteSavedReportsIdWithResponse(l.ctx, id)))
		deleted = true
	})
}

// result is a generated call's two results, as one value for ok.
type result struct {
	resp any
	err  error
}

func wrap[R any](resp *R, err error) result {
	return result{resp, err}
}
