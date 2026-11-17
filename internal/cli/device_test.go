package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// The log of a real launch, scrubbed. Its bundle ID is the test project's.
const capturedLog = "../devicelog/testdata/android-accepted.log"

// The installation ID in that log.
const capturedInstall = "aaaabbbbccccddddeeeeffff00001111"

func readCapturedLog(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(capturedLog)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDoctorReadsAConnectedDevice(t *testing.T) {
	h := newHarness(t)
	h.fake.installs[capturedInstall] = "Organic"
	calls := h.connectDevice(readCapturedLog(t))
	dir := testproject.Write(t, testproject.AndroidNative())

	code, stdout, stderr := h.run("doctor", dir, "--device")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"On the device",
		"Tenjin accepted the app's connect request (SDK 1.23.0)",
		"Tenjin has processed this device's install",
		"4 other requests were accepted",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q:\n%s", want, stdout)
		}
	}
	if len(*calls) != 2 || (*calls)[1][1] != "emulator-5554" {
		t.Errorf("adb calls = %q", *calls)
	}
	if got := strings.Join(h.fake.installsAsked, ","); got != capturedInstall {
		t.Errorf("installs looked up = %q", got)
	}
	// The project's own key is what the lookup is made with, and it stays
	// out of the report.
	if strings.Contains(stdout, testproject.Key) {
		t.Error("the report prints the whole SDK key")
	}
}

func TestDoctorReadsASavedLog(t *testing.T) {
	h := newHarness(t)
	dir := testproject.Write(t, testproject.AndroidNative())

	// From a file, as JSON: the findings carry stable IDs.
	code, stdout, stderr := h.run("doctor", dir, "--logs", capturedLog, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	var report struct {
		Findings []struct{ ID, Severity, Platform string }
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	got := map[string]string{}
	for _, f := range report.Findings {
		if strings.HasPrefix(f.ID, "device.") {
			got[f.ID] = f.Severity
		}
	}
	// Tenjin has not processed this install in the fake, which is not a fault.
	want := map[string]string{"device.connect": "pass", "device.identifiers": "info", "device.processed": "info", "device.requests": "pass"}
	for id, severity := range want {
		if got[id] != severity {
			t.Errorf("%s = %q, want %q (all: %v)", id, got[id], severity, got)
		}
	}

	// From standard input.
	h.env.stdin = bufio.NewReader(strings.NewReader(readCapturedLog(t)))
	code, stdout, _ = h.run("doctor", dir, "--logs", "-")
	if code != 0 || !strings.Contains(stdout, "Tenjin accepted the app's connect request") {
		t.Errorf("exit = %d, stdout:\n%s", code, stdout)
	}
}

func TestDoctorOfflineReadsTheLogWithoutAskingTenjin(t *testing.T) {
	h := newHarness(t)
	dir := testproject.Write(t, testproject.AndroidNative())
	code, stdout, _ := h.run("doctor", dir, "--logs", capturedLog, "--offline")
	if code != 0 || len(h.fake.installsAsked) != 0 || len(h.fake.keysAsked) != 0 {
		t.Errorf("exit = %d, lookups = %q, key checks = %q", code, h.fake.installsAsked, h.fake.keysAsked)
	}
	if !strings.Contains(stdout, "Tenjin accepted the app's connect request") || !strings.Contains(stdout, "was not checked: the check was offline") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// What the device says can fail the check on its own: the source is right,
// but Tenjin refused the build that ran.
func TestDoctorFailsWhenTenjinRefusedTheDevice(t *testing.T) {
	h := newHarness(t)
	dir := testproject.Write(t, testproject.AndroidNative())
	log := filepath.Join(t.TempDir(), "logcat.txt")
	text := strings.Replace(readCapturedLog(t), `Tenjin::connect response: {"code":200,"success":true}`, `Tenjin::connect response: {"code":401,"message":"unauthorized"}`, 1)
	if err := os.WriteFile(log, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := h.run("doctor", dir, "--logs", log)
	if code != 9 || !strings.Contains(stdout, "does not accept this SDK key") {
		t.Errorf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	if len(h.fake.installsAsked) != 0 {
		t.Error("looked up a device whose connect was refused")
	}
}

// A device that cannot be read is a finding, so the rest of the report still
// comes out and the command does not fail for it.
func TestDoctorDeviceProblemsAreFindings(t *testing.T) {
	dir := testproject.Write(t, testproject.AndroidNative())

	t.Run("no adb", func(t *testing.T) {
		code, stdout, _ := newHarness(t).run("doctor", dir, "--device")
		if code != 0 || !strings.Contains(stdout, "adb was not found") || !strings.Contains(stdout, "Tenjin accepts the SDK key") {
			t.Errorf("exit = %d, stdout:\n%s", code, stdout)
		}
	})

	t.Run("no device", func(t *testing.T) {
		h := newHarness(t)
		h.connectDevice("")
		run := h.env.run
		h.env.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if len(args) == 1 && args[0] == "devices" {
				return []byte("List of devices attached\n\n"), nil
			}
			return run(ctx, name, args...)
		}
		code, stdout, _ := h.run("doctor", dir, "--device")
		if code != 0 || !strings.Contains(stdout, "No Android device or emulator is connected") {
			t.Errorf("exit = %d, stdout:\n%s", code, stdout)
		}
		// With --strict a warning fails the run, this one included.
		if code, _, _ := h.run("doctor", dir, "--device", "--strict"); code != 9 {
			t.Errorf("exit with --strict = %d, want 9", code)
		}
	})

	t.Run("app not launched", func(t *testing.T) {
		h := newHarness(t)
		h.connectDevice("")
		code, stdout, _ := h.run("doctor", dir, "--device")
		if code != 0 || !strings.Contains(stdout, "On the device") {
			t.Errorf("exit = %d, stdout:\n%s", code, stdout)
		}
	})
}

func TestDoctorDeviceFlagErrors(t *testing.T) {
	h := newHarness(t)
	dir := testproject.Write(t, testproject.AndroidNative())
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"serial without device", []string{"--serial", "emulator-5554"}, 2, "--serial only applies with --device"},
		{"both sources", []string{"--device", "--logs", capturedLog}, 2, "either --device or --logs"},
		{"missing file", []string{"--logs", "nope.txt"}, 5, "cannot read nope.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := h.run(append([]string{"doctor", dir}, tc.args...)...)
			if code != tc.code || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Errorf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
			}
		})
	}
}

// A device named with --serial that is not there is the user's mistake, as a
// --logs file that cannot be read is: an error that says so, not a finding
// about a device that could not be read.
func TestDoctorRefusesASerialThatNamesNoDevice(t *testing.T) {
	h := newHarness(t)
	h.connectDevice(readCapturedLog(t))
	dir := testproject.Write(t, testproject.AndroidNative())
	code, stdout, stderr := h.run("doctor", dir, "--device", "--serial", "243AACFD-275B-4332-8D06-D4B06EFEA171")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "--serial 243AACFD-275B-4332-8D06-D4B06EFEA171 names an iPhone or an iOS simulator, and this project does not build for iOS") {
		t.Errorf("UDID: exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	code, stdout, stderr = h.run("doctor", dir, "--device", "--serial", "emulator-9999")
	if code != 5 || stdout != "" || !strings.Contains(stderr, "No connected Android device has the serial emulator-9999") || !strings.Contains(stderr, "Connected: emulator-5554.") {
		t.Errorf("not connected: exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	// An Android serial on an iOS project is the same mistake the other way.
	dir = testproject.Write(t, testproject.IOSNative())
	code, _, stderr = h.run("doctor", dir, "--device", "--serial", "emulator-5554")
	if code != 2 || !strings.Contains(stderr, "--serial emulator-5554 names an Android device, and this project does not build for Android") {
		t.Errorf("Android serial: exit = %d, stderr = %q", code, stderr)
	}
}

// With a key per store, the install is looked up with each. The Google Play
// app's answer, not processed yet, is the one that counts: the Amazon key's
// refusal after it says nothing about this install.
func TestTheLookupKeepsTheMostTellingAnswer(t *testing.T) {
	h, dir := multiStore(t, testproject.AndroidMultiStore())
	code, stdout, stderr := h.run("doctor", dir, "--logs", capturedLog, "--json")
	var report struct {
		Findings []struct{ ID, Severity string }
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("exit = %d, stderr = %q: %v", code, stderr, err)
	}
	got := map[string]string{}
	for _, f := range report.Findings {
		got[f.ID] = f.Severity
	}
	if got["device.processed"] != "info" || got["device.lookup"] != "" || len(h.fake.installsAsked) != 2 {
		t.Errorf("findings = %v, lookups = %d", got, len(h.fake.installsAsked))
	}
}

// An emulator the SDK recognises marks its requests device_info=true, and
// Tenjin does not process them: doctor says so and does not ask.
func TestDoctorOnAMarkedEmulator(t *testing.T) {
	h := newHarness(t)
	h.fake.installs[capturedInstall] = "Organic"
	h.connectDevice(strings.ReplaceAll(readCapturedLog(t), "platform=android, ", "platform=android, device_info=true, "))
	dir := testproject.Write(t, testproject.AndroidNative())
	_, stdout, _ := h.run("doctor", dir, "--device")
	lacks(t, stdout, "Tenjin does not process installs from a simulator or emulator", "a physical Android device")
	if len(h.fake.installsAsked) != 0 {
		t.Errorf("an emulator's install was looked up: %q", h.fake.installsAsked)
	}
}

// Tenjin's answer about a device names what it was attributed to. The CLI
// asks only whether the install was processed, so none of that is shown.
func TestADevicesAttributionIsNotShown(t *testing.T) {
	const campaign = "Campaign-Of-One-Device"
	h := newHarness(t)
	h.fake.installs[capturedInstall] = campaign
	h.connectDevice(readCapturedLog(t))
	dir := testproject.Write(t, testproject.AndroidNative())
	for _, args := range [][]string{{"doctor", dir, "--device"}, {"doctor", dir, "--device", "--json"}} {
		_, stdout, stderr := h.run(args...)
		if !strings.Contains(stdout, "Tenjin has processed this device's install") || strings.Contains(stdout+stderr, campaign) || strings.Contains(stdout+stderr, "organic") {
			t.Errorf("%q:\nstdout:\n%s\nstderr:\n%s", args, stdout, stderr)
		}
	}
}
