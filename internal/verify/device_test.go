package verify_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

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
	known map[string]bool // by installation ID
	err   error
	asked []string
}

func (f *fakeLookup) LookupDevice(_ context.Context, key, bundleID, platform, installationID string) (bool, error) {
	f.asked = append(f.asked, key+"|"+bundleID+"|"+platform+"|"+installationID)
	if f.err != nil {
		return false, f.err
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
		{"not Tenjin's JSON", `<html>502</html>`, verify.Warning, "unexpected answer"},
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

	lookup := &fakeLookup{known: map[string]bool{"install-1": true}}
	r := withDevice(t, accepted, lookup)
	expect(t, r, "device.processed", project.Android, verify.Pass)
	// The lookup uses the key from the source and the IDs from the log.
	if want := testproject.Key + "|com.example.sample|android|install-1"; len(lookup.asked) != 1 || lookup.asked[0] != want {
		t.Errorf("asked = %q", lookup.asked)
	}

	// Accepted but not attributed yet is worth saying, but it is not a fault.
	r = withDevice(t, accepted, &fakeLookup{})
	expect(t, r, "device.processed", project.Android, verify.Info)

	// A lookup that fails says so, and why; it is not "not processed yet".
	r = withDevice(t, accepted, &fakeLookup{err: errors.New(`Tenjin answered HTTP 404 "no such app"`)})
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
		// Another app on the device with the SDK: its requests are its own.
		deviceLog("https://track.tenjin.com/v0/purchase", "event=purchase, bundle_id=com.other.app", accepted) +
		deviceLog("https://track.tenjin.com/v0/ad_impressions", "bundle_id=com.other.app", `{"code":401,"message":"bad key"}`) +
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

// unknownIDAndroid is AndroidNative with an applicationId that is not a
// string, so the project's ID cannot be read.
func unknownIDAndroid() testproject.Files {
	base := testproject.AndroidNative()
	return base.With(testproject.Files{
		"app/build.gradle.kts": strings.Replace(base["app/build.gradle.kts"], `applicationId = "com.example.sample"`, `applicationId = appId`, 1),
	})
}

// When the project's application ID could not be read, no app on the device
// can be told to be this one: another app's accepted connect, identifiers
// and purchases do not pass as this app's, Tenjin is not asked about its
// device, and the report says why.
func TestADeviceLogIsNotThisAppsWhenTheProjectsIDIsUnknown(t *testing.T) {
	other := strings.Replace(connectParams, "bundle_id=com.example.sample", "bundle_id=com.other.game", 1)
	text := "10-02 15:07:34.121 " + deviceLog(connectURL, other, `{"code":200,"success":true}`) +
		deviceLog("https://track.tenjin.com/v0/purchase", "event=purchase, bundle_id=com.other.game", `{"code":200}`)
	log, err := devicelog.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	lookup := &fakeLookup{known: map[string]bool{"install-1": true}}
	r := run(t, unknownIDAndroid(), verify.Options{Device: &verify.DeviceInput{Log: log, Lookup: lookup, Platform: project.Android}})

	f := expect(t, r, "device.connect", project.Android, verify.Info)
	if f.Title != "The log shows a connect from com.other.game, but whether that is this project's app could not be told" ||
		f.Detail != "The project's application ID could not be read, so the app cannot be told from other apps with the Tenjin SDK on this device. Tenjin answered 200. Sent at 10-02 15:07:34.121." ||
		f.Fix != "Write applicationId as a string in app/build.gradle.kts, then run this again." {
		t.Errorf("finding = %+v", f)
	}
	for _, id := range []string{"device.identifiers", "device.advertising_id", "device.requests", "device.processed", "device.processed.unchecked", "device.bundle_id"} {
		if found := find(r, id, project.Android); found != nil {
			t.Errorf("%s was reported for an app that may be another's: %+v", id, found)
		}
	}
	if len(lookup.asked) != 0 {
		t.Errorf("Tenjin was asked about a device that may be another app's: %q", lookup.asked)
	}
}

// iosRequest is one request as the iOS SDK logs it with debug logs on, from
// the process pid: the body names the key the app sends.
func iosRequest(pid int, bundle, key, path, body, response string) string {
	p := "Sample[" + strconv.Itoa(pid) + ":a1] [Tenjin] - "
	return p + "LOG: Sending request - https://track.tenjin.com/v0/" + path + "\n" +
		p + "DEBUG: request body bundle_id=" + bundle + "&platform=ios&api_key=" + key + "&" + body + "\n" +
		p + "LOG: Got http response 200\n" +
		p + "DEBUG: Response body " + response + "\n"
}

// The iOS SDK logs the key with each request. When the project's bundle ID
// could not be read, the app whose key the project holds is this one, and
// one sending another key cannot be told; neither is another app's for sure.
func TestTheKeyTellsThisAppWhenTheProjectsBundleIDIsUnknown(t *testing.T) {
	base := testproject.IOSNative()
	files := base.With(testproject.Files{
		"Sample.xcodeproj/project.pbxproj": testproject.PBXProj(testproject.PBXTarget{
			Name: "Sample", ProductType: "com.apple.product-type.application", BundleID: `"$(APP_BUNDLE_ID)"`, InfoPlist: "Sample/Info.plist",
		}),
	})
	ours := iosRequest(1, "com.example.shipped", testproject.Key, "event", "advertising_id=A1&developer_device_id=V1&tracking_status=3&analytics_installation_id=install-1", `{"code":200,"success":true}`)
	theirs := iosRequest(2, "com.other.game", "OTHERKEY1234567890ABCDEFGHIJKLMNOP", "event", "advertising_id=A2&developer_device_id=V2&tracking_status=3&analytics_installation_id=install-2", `{"code":200,"success":true}`) +
		iosRequest(2, "com.other.game", "OTHERKEY1234567890ABCDEFGHIJKLMNOP", "purchase", "event=purchase", `{"code":200}`)
	check := func(text string) *verify.Report {
		log, err := devicelog.Parse(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		return run(t, files, verify.Options{Device: &verify.DeviceInput{Log: log, Platform: project.IOS}})
	}

	r := check(theirs)
	f := expect(t, r, "device.connect", project.IOS, verify.Info)
	if !strings.Contains(f.Title, "a connect from com.other.game, but whether that is this project's app could not be told") ||
		!strings.Contains(f.Detail, "The project's bundle ID could not be read, so the app cannot be told from other apps with the Tenjin SDK on this device, and the connect carries a key the project does not hold (…MNOP).") ||
		f.Fix != "Set PRODUCT_BUNDLE_IDENTIFIER to the bundle ID itself in Sample.xcodeproj, then run this again." {
		t.Errorf("finding = %+v", f)
	}
	if find(r, "device.requests", project.IOS) != nil || find(r, "device.identifiers", project.IOS) != nil {
		t.Errorf("another key's requests were reported as this app's: %s", ids(r))
	}

	r = check(theirs + ours)
	expect(t, r, "device.connect", project.IOS, verify.Pass)
	if f := expect(t, r, "device.identifiers", project.IOS, verify.Info); f.Detail != "IDFA A1, IDFV V1." {
		t.Errorf("identifiers = %q", f.Detail)
	}
	if find(r, "device.requests", project.IOS) != nil || find(r, "device.bundle_id", project.IOS) != nil {
		t.Errorf("the other key's purchase, or a bundle ID that could not be compared, was reported: %s", ids(r))
	}
}

// setup reads a device again and again while Tenjin processes the install.
// The project's checks run once (Prepare), and each read adds the device's:
// the report is the one a full run with the device gives, finding for
// finding, and a later read starts afresh.
func TestPreparedChecksGiveTheReportOfAFullRun(t *testing.T) {
	android := deviceLog(connectURL, connectParams, `{"code":200,"success":true}`)
	ios := iosRequest(1, "com.example.sample", testproject.Key, "event", "advertising_id=A1&developer_device_id=V1&tracking_status=3&analytics_installation_id=install-1", `{"code":200,"success":true}`)
	cases := map[string]struct {
		files    testproject.Files
		platform project.Platform
		log      string
	}{
		"android": {testproject.AndroidNative(), project.Android, android},
		"ios":     {testproject.IOSNative(), project.IOS, ios},
		"flutter": {testproject.Flutter(), project.Android, android},
		"unity":   {testproject.Unity(), project.IOS, ios},
		// A platform the project does not build for.
		"unsupported": {testproject.AndroidNative(), project.IOS, ios},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := project.Detect(testproject.Write(t, tc.files))
			if err != nil {
				t.Fatal(err)
			}
			log, err := devicelog.Parse(strings.NewReader(tc.log))
			if err != nil {
				t.Fatal(err)
			}
			device := func(lookup verify.DeviceLookup) *verify.DeviceInput {
				return &verify.DeviceInput{Log: log, Lookup: lookup, Platform: tc.platform}
			}
			ctx := context.Background()
			full := verify.Run(ctx, p, verify.Options{Device: device(&fakeLookup{known: map[string]bool{"install-1": true}})})
			prepared := verify.Prepare(ctx, p, verify.Options{})
			// Not processed on the first read, processed on the second: the
			// second must not carry the first's findings.
			first := prepared.Device(device(&fakeLookup{}))
			second := prepared.Device(device(&fakeLookup{known: map[string]bool{"install-1": true}}))

			sorted := func(r *verify.Report) []verify.Finding {
				findings := append([]verify.Finding{}, r.Findings...)
				sort.SliceStable(findings, func(i, j int) bool { return findings[i].ID < findings[j].ID })
				return findings
			}
			if !reflect.DeepEqual(sorted(full), sorted(second)) || full.Summary != second.Summary {
				t.Errorf("prepared:\n%s\nfull run:\n%s", ids(second), ids(full))
			}
			if strings.Contains(ids(first), "pass:device.processed") || strings.Count(ids(second), "device.connect") != strings.Count(ids(full), "device.connect") {
				t.Errorf("first read:\n%s\nsecond read:\n%s", ids(first), ids(second))
			}
			if len(full.Findings) == 0 || (name != "unsupported" && find(full, "device.connect", tc.platform) == nil) {
				t.Errorf("the full run checked no device: %s", ids(full))
			}
		})
	}
}

// A connect the SDK skipped after the last one its process sent is what that
// process last did, and the earlier refusal is not reported as its answer.
func TestADedupAfterTheConnectWins(t *testing.T) {
	log := deviceLog(connectURL, connectParams, `{"code":401,"message":"unauthorized"}`) +
		"D/TenjinSDK( 1): Connect deduped by persisted timestamp\n"
	r := withDevice(t, log, nil)
	expect(t, r, "device.connect.deduped", project.Android, verify.Info)
	if find(r, "device.connect", project.Android) != nil {
		t.Error("the earlier connect was reported")
	}
}

// A connect skipped in another process, another app's with the SDK or a
// later launch's, does not cancel this app's accepted connect: the skip
// does not say which app skipped, and the log is paired per process.
func TestAConnectSkippedInAnotherProcessDoesNotCancelThisOnes(t *testing.T) {
	accepted := deviceLog(connectURL, connectParams, `{"code":200,"success":true}`)
	cases := map[string]string{
		"another app":    accepted + "D/TenjinSDK( 2): Connect deduped by persisted timestamp\n",
		"a later launch": accepted + "D/TenjinSDK( 3): Connecting...\nD/TenjinSDK( 3): Connect deduped by persisted timestamp, flushing queue\n",
	}
	for name, log := range cases {
		t.Run(name, func(t *testing.T) {
			r := withDevice(t, log, &fakeLookup{known: map[string]bool{"install-1": true}})
			expect(t, r, "device.connect", project.Android, verify.Pass)
			expect(t, r, "device.processed", project.Android, verify.Pass)
			if find(r, "device.connect.deduped", project.Android) != nil {
				t.Error("another process's skipped connect was reported as this app's")
			}
		})
	}

	// The same on iOS, where the other app's process skips its connect
	// after this app's was accepted.
	text := "Sample[100:a] [Tenjin] - LOG: Sending request - https://track.tenjin.com/v0/event\n" +
		"Sample[100:a] [Tenjin] - DEBUG: request body bundle_id=com.example.sample&platform=ios&advertising_id=A1&developer_device_id=V1&tracking_status=3\n" +
		"Sample[100:a] [Tenjin] - LOG: Got http response 200\n" +
		"Sample[100:a] [Tenjin] - DEBUG: Response body {\"code\":200,\"success\":true}\n" +
		"Other[200:a] [Tenjin] - DEBUG: Connect sent 4.2s ago (interval: 30.0s), ignoring duplicate ping\n"
	log, err := devicelog.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	r := run(t, testproject.IOSNative(), verify.Options{Device: &verify.DeviceInput{Log: log, Platform: project.IOS}})
	expect(t, r, "device.connect", project.IOS, verify.Pass)
	if find(r, "device.connect.deduped", project.IOS) != nil {
		t.Error("another process's skipped connect was reported as this app's")
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
