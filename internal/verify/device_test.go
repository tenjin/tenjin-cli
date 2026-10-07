package verify_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/collector"
	"github.com/tenjin/tenjin-cli/internal/devicelog"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// deviceLog builds the three lines the SDK logs for one request.
func deviceLog(url, params, response string) string {
	return "D/HttpConnection( 1): Tenjin request URL: " + url + "\n" +
		"D/HttpConnection( 1): Tenjin::connect params: {" + params + "}\n" +
		"D/HttpConnection( 1): Tenjin::connect response: " + response + "\n"
}

const (
	connectURL    = "https://track.tenjin.com/v0/event"
	connectParams = "bundle_id=com.example.sample, platform=android, sdk_version=1.24.0, advertising_id=11111111-2222-3333-4444-555555555555, analytics_installation_id=install-1, developer_device_id=dev-1"
)

func withDevice(t *testing.T, text string, lookup verify.DeviceLookup) *verify.Report {
	t.Helper()
	log, err := devicelog.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return run(t, testproject.AndroidNative(), verify.Options{Device: &verify.DeviceInput{Log: log, Lookup: lookup, Platform: project.Android}})
}

type fakeLookup struct {
	known map[string]collector.Attribution // by installation ID
	err   error
	asked []string
}

func (f *fakeLookup) LookupDevice(_ context.Context, key, bundleID, platform, installationID string) (collector.Attribution, error) {
	f.asked = append(f.asked, key+"|"+bundleID+"|"+platform+"|"+installationID)
	if f.err != nil {
		return collector.Attribution{}, f.err
	}
	return f.known[installationID], nil
}

func TestDeviceConnectAnswers(t *testing.T) {
	cases := []struct {
		name     string
		response string
		severity verify.Severity
		title    string
	}{
		{"accepted", `{"code":200,"success":true}`, verify.Pass, "Tenjin accepted the app's connect request (SDK 1.24.0)"},
		{"key rejected", `{"code":401,"message":"unauthorized"}`, verify.Error, "does not accept this SDK key for com.example.sample"},
		{"no such app", `{"code":404,"message":"no such app"}`, verify.Error, "Tenjin has no android app with the bundle ID com.example.sample"},
		{"no device identifier", `{"code":202,"message":"invalid device identifier"}`, verify.Error, "carried no device identifier"},
		{"key disabled", `{"code":202,"message":"not logged"}`, verify.Error, "the SDK key is disabled"},
		{"version filtered", `{"code":202,"message":"ignored"}`, verify.Warning, "app-version rule"},
		{"not the collector's JSON", `<html>502</html>`, verify.Warning, "unexpected answer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := withDevice(t, deviceLog(connectURL, connectParams, tc.response), nil)
			f := expect(t, r, "device.connect", project.Android, tc.severity)
			if !strings.Contains(f.Title, tc.title) {
				t.Errorf("title = %q, want it to contain %q", f.Title, tc.title)
			}
		})
	}
}

func TestDeviceProcessedByTenjin(t *testing.T) {
	accepted := deviceLog(connectURL, connectParams, `{"code":200,"success":true}`)

	lookup := &fakeLookup{known: map[string]collector.Attribution{"install-1": {Known: true, CampaignName: "Organic"}}}
	r := withDevice(t, accepted, lookup)
	f := expect(t, r, "device.processed", project.Android, verify.Pass)
	if !strings.Contains(f.Title, "attributed to Organic") {
		t.Errorf("title = %q", f.Title)
	}
	// The lookup uses the key from the source and the IDs from the log.
	if want := testproject.Key + "|com.example.sample|android|install-1"; len(lookup.asked) != 1 || lookup.asked[0] != want {
		t.Errorf("asked = %q", lookup.asked)
	}

	// Accepted is only queued; no attribution yet is worth saying, but it is
	// not a fault.
	r = withDevice(t, accepted, &fakeLookup{})
	expect(t, r, "device.processed", project.Android, verify.Info)

	// A lookup that fails says so, and why; it is not "not processed yet".
	r = withDevice(t, accepted, &fakeLookup{err: errors.New(`the collector answered HTTP 404 "no such app"`)})
	if f := expect(t, r, "device.lookup", project.Android, verify.Warning); !strings.Contains(f.Detail, "no such app") {
		t.Errorf("detail = %q", f.Detail)
	}
	if find(r, "device.processed", project.Android) != nil {
		t.Error("a failed lookup was reported as an answer")
	}

	// Without a lookup, the report says the install was not checked.
	r = withDevice(t, accepted, nil)
	expect(t, r, "device.processed.unchecked", project.Android, verify.Info)

	// A refused connect is not looked up at all.
	refused := &fakeLookup{}
	withDevice(t, deviceLog(connectURL, connectParams, `{"code":401,"message":"unauthorized"}`), refused)
	if len(refused.asked) != 0 {
		t.Errorf("looked up a device whose connect was refused: %q", refused.asked)
	}
}

func TestDeviceRunningBuildDiffersFromTheProject(t *testing.T) {
	params := strings.Replace(connectParams, "com.example.sample", "com.example.sample.debug", 1)
	r := withDevice(t, deviceLog(connectURL, params, `{"code":404,"message":"no such app"}`), nil)
	f := expect(t, r, "device.bundle_id", project.Android, verify.Warning)
	if !strings.Contains(f.Title, "com.example.sample.debug") || !strings.Contains(f.Title, "com.example.sample") {
		t.Errorf("title = %q", f.Title)
	}
}

func TestDeviceAdvertisingID(t *testing.T) {
	accepted := `{"code":200,"success":true}`

	r := withDevice(t, deviceLog(connectURL, connectParams, accepted), nil)
	f := expect(t, r, "device.identifiers", project.Android, verify.Info)
	if !strings.Contains(f.Detail, "11111111-2222-3333-4444-555555555555") || !strings.Contains(f.Detail, "dev-1") {
		t.Errorf("detail = %q", f.Detail)
	}

	zeros := strings.Replace(connectParams, "11111111-2222-3333-4444-555555555555", "00000000-0000-0000-0000-000000000000", 1)
	r = withDevice(t, deviceLog(connectURL, zeros, accepted), nil)
	expect(t, r, "device.advertising_id", project.Android, verify.Warning)

	// With ad tracking limited on the device, zeros are what to expect.
	r = withDevice(t, deviceLog(connectURL, zeros+", limit_ad_tracking=true", accepted), nil)
	expect(t, r, "device.advertising_id", project.Android, verify.Info)
}

func TestDeviceWithoutAConnect(t *testing.T) {
	r := withDevice(t, "I/ActivityManager: Start proc com.example.sample\n", nil)
	expect(t, r, "device.no_sdk_output", project.Android, verify.Warning)

	r = withDevice(t, "D/TenjinSDK( 1): Connecting...\nD/TenjinSDK( 1): Connect deduped by persisted timestamp, flushing queue\n", nil)
	expect(t, r, "device.connect.deduped", project.Android, verify.Info)

	r = withDevice(t, "D/TenjinSDK( 1): Stored initialized_at timestamp: 1\n", nil)
	expect(t, r, "device.no_connect", project.Android, verify.Warning)
}

func TestDeviceOtherRequests(t *testing.T) {
	accepted := `{"code":200}`
	text := deviceLog(connectURL, connectParams, `{"code":200,"success":true}`) +
		deviceLog("https://track.tenjin.com/v0/purchase", "event=purchase, bundle_id=com.example.sample", accepted) +
		deviceLog("https://track.tenjin.com/v0/purchase", "event=purchase, bundle_id=com.example.sample", accepted) +
		deviceLog(connectURL, "event=level_up, bundle_id=com.example.sample", accepted)
	r := withDevice(t, text, nil)
	f := expect(t, r, "device.requests", project.Android, verify.Pass)
	if !strings.Contains(f.Title, "3 other requests were accepted: 1 event, 2 purchase") {
		t.Errorf("title = %q", f.Title)
	}

	text += deviceLog("https://track.tenjin.com/v1/subscriptions", "bundle_id=com.example.sample", `{"code":202,"message":"not logged"}`)
	r = withDevice(t, text, nil)
	f = expect(t, r, "device.requests", project.Android, verify.Warning)
	if !strings.Contains(f.Title, "1 other request was not accepted") || !strings.Contains(f.Detail, "subscriptions: 202 not logged") {
		t.Errorf("finding = %+v", f)
	}
}

func TestDeviceThatCannotBeRead(t *testing.T) {
	r := run(t, testproject.AndroidNative(), verify.Options{Device: &verify.DeviceInput{
		Platform: project.Android,
		Problem:  &devicelog.Unavailable{Reason: "No Android device or emulator is connected", Fix: "Start an emulator."},
	}})
	f := expect(t, r, "device.unavailable", project.Android, verify.Warning)
	if f.Title != "No Android device or emulator is connected" || f.Fix != "Start an emulator." {
		t.Errorf("finding = %+v", f)
	}
	if r.Summary.Errors != 0 {
		t.Error("an unreadable device was counted as an integration error")
	}
}

func TestADeviceLogNeedsATargetOfItsPlatform(t *testing.T) {
	log, _ := devicelog.Parse(strings.NewReader(""))
	r := run(t, testproject.IOSNative(), verify.Options{Device: &verify.DeviceInput{Log: log, Platform: project.Android}})
	f := expect(t, r, "device.unsupported", "", verify.Info)
	if !strings.Contains(f.Title, "does not build for Android") {
		t.Errorf("title = %q", f.Title)
	}
}

// On iOS an all-zeros IDFA is what the user's tracking choice produces; only
// a connect sent before the prompt was answered is a problem.
func TestIOSAdvertisingID(t *testing.T) {
	launch := func(params string) *verify.Report {
		log, _ := devicelog.Parse(strings.NewReader(`[Tenjin] - LOG: Sending request - https://track.tenjin.com/v0/event
[Tenjin] - DEBUG: request body bundle_id=com.example.sample&platform=ios&` + params + `
[Tenjin] - LOG: Got http response 200
[Tenjin] - DEBUG: Response body {"code":200,"success":true}
`))
		return run(t, testproject.IOSNative(), verify.Options{Device: &verify.DeviceInput{Log: log, Platform: project.IOS}})
	}
	zeros := "advertising_id=00000000-0000-0000-0000-000000000000&developer_device_id=V&tracking_status="
	expect(t, launch(zeros+"0"), "device.advertising_id", project.IOS, verify.Warning)
	expect(t, launch(zeros+"2"), "device.advertising_id", project.IOS, verify.Info)
	expect(t, launch(zeros+"3"), "device.advertising_id", project.IOS, verify.Warning)

	r := launch("advertising_id=A1&developer_device_id=V1&tracking_status=3")
	if f := find(r, "device.advertising_id", project.IOS); f != nil {
		t.Errorf("a real IDFA was reported: %+v", f)
	}
	if f := expect(t, r, "device.identifiers", project.IOS, verify.Info); f.Detail != "IDFA A1, IDFV V1." {
		t.Errorf("identifiers = %q", f.Detail)
	}
	expect(t, r, "device.connect", project.IOS, verify.Pass)
}

// The connect that counts is this project's app's, the newest of them; one
// from another app on the device stands for nothing.
func TestTheConnectThatCountsIsThisApps(t *testing.T) {
	other := strings.Replace(connectParams, "bundle_id=com.example.sample", "bundle_id=com.other.game", 1)
	debug := strings.Replace(connectParams, "bundle_id=com.example.sample", "bundle_id=com.example.sample.debug", 1)
	cases := []struct {
		name, log string
		severity  verify.Severity
		title     string
	}{
		{"refused here, accepted elsewhere",
			deviceLog(connectURL, connectParams, `{"code":401,"message":"unauthorized"}`) + deviceLog(connectURL, other, `{"code":200,"success":true}`),
			verify.Error, "does not accept this SDK key"},
		{"only another app",
			deviceLog(connectURL, other, `{"code":200,"success":true}`),
			verify.Warning, "connects only from other apps (com.other.game)"},
		{"a debug suffix is this app",
			deviceLog(connectURL, debug, `{"code":200,"success":true}`),
			verify.Pass, "Tenjin accepted the app's connect request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := expect(t, withDevice(t, c.log, nil), "device.connect", project.Android, c.severity)
			if !strings.Contains(f.Title, c.title) {
				t.Errorf("title = %q", f.Title)
			}
		})
	}
}

// A connect the SDK skipped after the last one it sent is what the newest
// launch did, and an earlier refusal is not reported as this launch's.
func TestADedupAfterTheConnectWins(t *testing.T) {
	log := deviceLog(connectURL, connectParams, `{"code":401,"message":"unauthorized"}`) +
		"D/TenjinSDK( 1): Connect deduped by persisted timestamp\n"
	r := withDevice(t, log, nil)
	expect(t, r, "device.connect.deduped", project.Android, verify.Info)
	if find(r, "device.connect", project.Android) != nil {
		t.Error("the earlier connect was reported")
	}
}

// When the device logged it is said, so an old launch shows as old.
func TestAConnectSaysWhenItWasSent(t *testing.T) {
	log := "10-02 15:07:34.121 " + strings.TrimSuffix(deviceLog(connectURL, connectParams, `{"code":401,"message":"unauthorized"}`), "\n")
	f := expect(t, withDevice(t, log+"\n", nil), "device.connect", project.Android, verify.Error)
	if !strings.Contains(f.Detail, "Sent at 10-02 15:07:34.121.") {
		t.Errorf("detail = %q", f.Detail)
	}
}

// Without debug logs an iOS request is only its URL and status: it is not
// taken for a connect.
func TestAnIOSRequestWithoutItsBodyIsNotAConnect(t *testing.T) {
	text := "[Tenjin] - LOG: Sending request - https://track.tenjin.com/v0/event\n[Tenjin] - LOG: Got http response 200\n"
	log, err := devicelog.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	r := run(t, testproject.IOSNative(), verify.Options{Device: &verify.DeviceInput{Log: log, Platform: project.IOS}})
	expect(t, r, "device.no_debug_logs", project.IOS, verify.Warning)
	if find(r, "device.connect", project.IOS) != nil {
		t.Error("an unread request was taken for a connect")
	}
}
