package devicelog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture is a real capture of the Android SDK (1.23.0) launching the
// demo app, with every identifier replaced.
func parseFixture(t *testing.T) *Log {
	t.Helper()
	f, err := os.Open("testdata/android-accepted.log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	log, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return log
}

func TestParseARealCapture(t *testing.T) {
	log := parseFixture(t)
	if !log.SawSDK || len(log.Requests) != 5 {
		t.Fatalf("saw SDK = %v, requests = %d", log.SawSDK, len(log.Requests))
	}
	var endpoints []string
	for _, r := range log.Requests {
		endpoints = append(endpoints, r.Endpoint)
		if !r.Accepted() {
			t.Errorf("%s: code = %d", r.Endpoint, r.Code)
		}
	}
	if got := strings.Join(endpoints, ","); got != "event,purchase,ad_impressions,purchase,purchase" {
		t.Errorf("endpoints = %s", got)
	}

	connect := log.LastConnect()
	if connect == nil || connect.URL != "https://track.tenjin.com/v0/event" {
		t.Fatalf("connect = %+v", connect)
	}
	want := map[string]string{
		"bundle_id":                 "com.example.sample",
		"platform":                  "android",
		"sdk_version":               "1.23.0",
		"advertising_id":            "11111111-2222-3333-4444-555555555555",
		"analytics_installation_id": "aaaabbbbccccddddeeeeffff00001111",
		"developer_device_id":       "99999999-8888-7777-6666-555555555555",
		// The last parameter, with no trailing comma.
		"total_ilrd_revenue_usd": "0.0",
	}
	for key, value := range want {
		if connect.Params[key] != value {
			t.Errorf("%s = %q, want %q", key, connect.Params[key], value)
		}
	}
	// The purchases go to a different endpoint and carry an event name, so
	// neither is mistaken for the connect.
	if log.Requests[1].IsConnect() {
		t.Error("a purchase was taken for a connect")
	}
}

func TestParseOtherLogcatFormatsAndRefusals(t *testing.T) {
	// threadtime format, as Android Studio and `adb logcat` print by default.
	text := `10-02 09:00:00.000  4242  4260 D HttpConnection: Tenjin request URL: https://track.tenjin.com/v0/event
10-02 09:00:00.000  4242  4260 D HttpConnection: Tenjin::connect params: {name=Acme, Inc., bundle_id=com.example.app.debug, platform=android}
10-02 09:00:00.001  4242  4260 D HttpConnection: Tenjin::connect response: {"code":404,"message":"no such app"}
10-02 09:00:01.000  4242  4260 D HttpConnection: Tenjin request URL: https://track.tenjin.com/v0/event
10-02 09:00:01.000  4242  4260 D HttpConnection: Tenjin::connect params: {event=level_up, bundle_id=com.example.app.debug}
10-02 09:00:01.001  4242  4260 D HttpConnection: Tenjin::connect response: <html>502 Bad Gateway</html>
`
	log, err := Parse(strings.NewReader(text))
	if err != nil || len(log.Requests) != 2 {
		t.Fatalf("requests = %d, err = %v", len(log.Requests), err)
	}
	connect := log.LastConnect()
	if connect == nil || connect.Code != 404 || connect.Message != "no such app" || connect.Line != 1 {
		t.Fatalf("connect = %+v", connect)
	}
	// A value with a comma in it is kept whole.
	if connect.Params["name"] != "Acme, Inc." || connect.Params["bundle_id"] != "com.example.app.debug" {
		t.Errorf("params = %v", connect.Params)
	}
	event := log.Requests[1]
	if event.IsConnect() || event.Code != 0 || event.Accepted() {
		t.Errorf("custom event = %+v", event)
	}
}

func TestParseDedupAndSilence(t *testing.T) {
	deduped, _ := Parse(strings.NewReader("D/TenjinSDK( 1): Connecting...\nD/TenjinSDK( 1): Connect deduped by persisted timestamp, flushing queue\n"))
	if !deduped.SawSDK || deduped.Deduped != 1 || deduped.LastConnect() != nil {
		t.Errorf("deduped = %+v", deduped)
	}
	// "HttpConnection" alone is not the SDK: other libraries use that tag.
	silent, _ := Parse(strings.NewReader("D/HttpConnection( 1): GET https://example.com\nI/ActivityManager: Start proc\n"))
	if silent.SawSDK || len(silent.Requests) != 0 {
		t.Errorf("silent = %+v", silent)
	}
}

// fakeADB answers `adb devices` and records the logcat call.
type fakeADB struct {
	devices string
	err     error
	calls   [][]string
}

func (f *fakeADB) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if f.err != nil {
		return nil, f.err
	}
	if len(args) == 1 && args[0] == "devices" {
		return []byte(f.devices), nil
	}
	return []byte("the log"), nil
}

func TestReadAndroid(t *testing.T) {
	one := "List of devices attached\nemulator-5554\tdevice\n\n"
	two := "List of devices attached\nemulator-5554\tdevice\nR58M123\tdevice\nZY22\tunauthorized\n"

	t.Run("reads the only device", func(t *testing.T) {
		adb := &fakeADB{devices: one}
		out, err := ReadAndroid(context.Background(), adb.run, "adb", "")
		if err != nil || string(out) != "the log" {
			t.Fatalf("out = %q, err = %v", out, err)
		}
		got := strings.Join(adb.calls[1], " ")
		if !strings.HasPrefix(got, "-s emulator-5554 logcat -d -v time -s TenjinSDK HttpConnection") {
			t.Errorf("logcat call = %q", got)
		}
	})

	cases := []struct {
		name, devices, serial, want string
	}{
		{"nothing connected", "List of devices attached\n\n", "", "No Android device or emulator is connected"},
		{"only an unauthorized device", "List of devices attached\nZY22\tunauthorized\n", "", "No Android device or emulator is connected"},
		{"several and no serial", two, "", "More than one Android device is connected: emulator-5554, R58M123"},
		{"unknown serial", two, "nope", "No connected Android device has the serial nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adb := &fakeADB{devices: tc.devices}
			_, err := ReadAndroid(context.Background(), adb.run, "adb", tc.serial)
			var unavailable *Unavailable
			if !errors.As(err, &unavailable) || unavailable.Reason != tc.want {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}

	t.Run("picks the named one of several", func(t *testing.T) {
		adb := &fakeADB{devices: two}
		if _, err := ReadAndroid(context.Background(), adb.run, "adb", "R58M123"); err != nil {
			t.Fatal(err)
		}
		if adb.calls[1][1] != "R58M123" {
			t.Errorf("logcat call = %q", adb.calls[1])
		}
	})

	t.Run("adb fails", func(t *testing.T) {
		adb := &fakeADB{err: errors.New("exec: adb: permission denied")}
		_, err := ReadAndroid(context.Background(), adb.run, "adb", "")
		var unavailable *Unavailable
		if !errors.As(err, &unavailable) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestFindADB(t *testing.T) {
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }

	if path, ok := FindADB(func(string) (string, error) { return "/usr/bin/adb", nil }, func(string) string { return "" }); !ok || path != "/usr/bin/adb" {
		t.Errorf("on PATH: %q, %v", path, ok)
	}

	sdk := t.TempDir()
	if err := os.MkdirAll(sdk+"/platform-tools", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sdk+"/platform-tools/adb", []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := func(name string) string {
		if name == "ANDROID_HOME" {
			return sdk
		}
		return ""
	}
	if path, ok := FindADB(notOnPath, env); !ok || !strings.HasSuffix(path, "platform-tools/adb") {
		t.Errorf("in the SDK: %q, %v", path, ok)
	}
	if _, ok := FindADB(notOnPath, func(string) string { return "" }); ok {
		t.Error("found adb where there is none")
	}
}

// The iOS fixture is a real capture of the iOS SDK (1.19.1) connecting from a
// simulator with debug logs on, every identifier and location replaced.
func TestParseARealIOSCapture(t *testing.T) {
	f, err := os.Open("testdata/ios-accepted.log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	log, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if log.Platform != "ios" || !log.SawSDK || len(log.Requests) != 1 {
		t.Fatalf("platform = %q, saw SDK = %v, requests = %d", log.Platform, log.SawSDK, len(log.Requests))
	}
	c := log.LastConnect()
	if c == nil || !c.Accepted() || c.Response != `{"code":200,"success":true}` {
		t.Fatalf("connect = %+v", c)
	}
	for key, want := range map[string]string{
		"bundle_id":                 "com.example.sample",
		"platform":                  "ios",
		"sdk_version":               "1.19.1",
		"analytics_installation_id": "AAAABBBB-CCCC-DDDD-EEEE-000000000003",
		"developer_device_id":       "AAAABBBB-CCCC-DDDD-EEEE-000000000002",
		"advertising_id":            "00000000-0000-0000-0000-000000000000",
		"tracking_status":           "0",
		"timezone":                  "America/New_York",
	} {
		if got := c.Params[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestParseIOSWithoutDebugLogs(t *testing.T) {
	// verboseLogs() logs the URL and the status, not the bodies.
	log, _ := Parse(strings.NewReader(`2026-10-06 10:04:49.416 Df Sample[1:2] (TenjinSDK) [Tenjin] - LOG: Sending request - https://track.tenjin.com/v0/event
2026-10-06 10:04:49.903 Df Sample[1:3] (TenjinSDK) [Tenjin] - LOG: Got http response 401
`))
	c := log.LastConnect()
	if c == nil || c.Code != 401 || c.Accepted() {
		t.Fatalf("connect = %+v", c)
	}
}

func TestParseIOSSkippedConnect(t *testing.T) {
	log, _ := Parse(strings.NewReader(`2026-10-06 10:04:49.416 Df Sample[1:2] (TenjinSDK) [Tenjin] - DEBUG: Connect sent 4.2s ago (interval: 30.0s), ignoring duplicate ping
`))
	if log.Platform != "ios" || log.Deduped != 1 || log.LastConnect() != nil {
		t.Errorf("log = %+v", log)
	}
}

func TestReadIOSSimulator(t *testing.T) {
	booted := func(names ...string) []byte {
		var devices []string
		for i, n := range names {
			devices = append(devices, `{"udid":"0000000`+string(rune('1'+i))+`-0000-0000-0000-000000000000","name":"`+n+`","state":"Booted"}`)
		}
		return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-5":[` + strings.Join(devices, ",") + `],"com.apple.CoreSimulator.SimRuntime.iOS-26-0":[]}}`)
	}
	fake := func(list []byte) (Runner, *[][]string) {
		calls := &[][]string{}
		return func(_ context.Context, _ string, args ...string) ([]byte, error) {
			*calls = append(*calls, args)
			if args[1] == "list" {
				return list, nil
			}
			return []byte("the log"), nil
		}, calls
	}

	run, calls := fake(booted("iPhone 16"))
	out, err := ReadIOSSimulator(context.Background(), run, "xcrun", "")
	if err != nil || string(out) != "the log" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	read := strings.Join((*calls)[1], " ")
	if !strings.Contains(read, "spawn 00000001-0000-0000-0000-000000000000 log show --last 10m") || !strings.Contains(read, `eventMessage CONTAINS "[Tenjin]"`) {
		t.Errorf("read with %q", read)
	}

	for _, c := range []struct {
		name, udid, reason string
		list               []byte
	}{
		{"none", "", "No iOS simulator is running", booted()},
		{"several", "", "More than one iOS simulator is running: iPhone 16 (00000001", booted("iPhone 16", "iPhone 17")},
		{"wrong udid", "00000009-0000-0000-0000-000000000000", "No running iOS simulator has the UDID", booted("iPhone 16")},
	} {
		run, _ := fake(c.list)
		_, err := ReadIOSSimulator(context.Background(), run, "xcrun", c.udid)
		var unavailable *Unavailable
		if !errors.As(err, &unavailable) || !strings.Contains(unavailable.Reason, c.reason) {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}

// adb is found in Android Studio's default place on each system, with the
// name it has there.
func TestFindADBInTheSDKsUsualPlaces(t *testing.T) {
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }
	for _, c := range []struct{ goos, home, local, rel string }{
		{"darwin", "home", "", "home/Library/Android/sdk/platform-tools/adb"},
		{"linux", "home", "", "home/Android/Sdk/platform-tools/adb"},
		{"windows", "", "local", "local/Android/Sdk/platform-tools/adb.exe"},
	} {
		root := t.TempDir()
		path := filepath.Join(root, filepath.FromSlash(c.rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
		vars := map[string]string{}
		if c.home != "" {
			vars["HOME"] = filepath.Join(root, c.home)
		}
		if c.local != "" {
			vars["LOCALAPPDATA"] = filepath.Join(root, c.local)
		}
		got, ok := findADB(notOnPath, func(n string) string { return vars[n] }, c.goos)
		if !ok || got != path {
			t.Errorf("%s: found %q, %v; want %q", c.goos, got, ok, path)
		}
	}
}
