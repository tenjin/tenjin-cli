package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// correctAndroid is a signed-in harness and a correct Android project, whose
// app exists in Tenjin.
func correctAndroid(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	return h, testproject.Write(t, testproject.AndroidNative())
}

// setup ends on a real launch: what Tenjin answered the device, and that it
// processed the install.
func TestSetupEndsWhenTenjinProcessesTheInstall(t *testing.T) {
	h, dir := correctAndroid(t)
	h.fake.installs[capturedInstall] = "Organic"
	h.connectDevice(readCapturedLog(t))

	code, stdout, stderr := h.typing("", "").run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout,
		"Step 7 of 7 · Check a real launch",
		"Launch a debug build of the app on an Android device or emulator, then press Enter",
		"Tenjin accepted the app's connect request (SDK 1.23.0)",
		"✓ Tenjin received this device's install and processed it. The integration works end to end.",
		"/dashboard/debug_app_users",
		"/dashboard/sdk_diagnostics",
	)
	// Only the device's findings: the project's were shown in step 6.
	if strings.Count(stdout, "Tenjin accepts the SDK key") != 1 {
		t.Errorf("the project checks were repeated:\n%s", stdout)
	}
	// The device's identifiers are said once, where registering it is.
	if strings.Contains(stdout, "for registering it as a test device") || strings.Count(stdout, "test device") != 2 ||
		!strings.Contains(stdout, "register this device (Advertising ID ") {
		t.Errorf("the test device is not said once, with its identifiers:\n%s", stdout)
	}
}

// Tenjin can take a moment to process an install it accepted; setup waits for
// it, a bounded number of times.
func TestSetupWaitsForTheInstallToBeProcessed(t *testing.T) {
	h, dir := correctAndroid(t)
	h.connectDevice(readCapturedLog(t))
	waits := 0
	h.env.wait = func(time.Duration) {
		if waits++; waits == 3 {
			h.fake.installs[capturedInstall] = "Organic"
		}
	}

	code, stdout, _ := h.typing("", "").run("setup", dir)
	lacks(t, stdout, "Waiting for it to process the install", "The integration works end to end.")
	if code != 0 || waits != 3 {
		t.Errorf("exit = %d, waits = %d", code, waits)
	}
}

func TestSetupSaysWhenTheInstallIsNotProcessedYet(t *testing.T) {
	h, dir := correctAndroid(t)
	h.connectDevice(readCapturedLog(t))

	code, stdout, _ := h.typing("", "").run("setup", dir)
	lacks(t, stdout, "has not processed the install yet. Run tenjin doctor --device again in a minute.")
	if strings.Contains(stdout, "works end to end") {
		t.Errorf("setup claimed the install was processed:\n%s", stdout)
	}
	if code != 0 || len(h.fake.installsAsked) != processingPolls+1 {
		t.Errorf("exit = %d, lookups = %d", code, len(h.fake.installsAsked))
	}
}

// While setup waits for Tenjin to process the install, the project's source
// is scanned once, not at every poll. Each poll asks Tenjin with every key
// the source holds, so a key added to the source during the wait is asked
// with only if the source is scanned again: the distinct keys asked count the
// scans of one launch. Before, 13 (the read, and each of the 12 polls); now 1.
func TestSetupScansTheSourceOnceWhileWaiting(t *testing.T) {
	h, dir := correctAndroid(t)
	h.connectDevice(readCapturedLog(t))
	polls := 0
	h.env.wait = func(time.Duration) {
		polls++
		name := fmt.Sprintf("Poll%02d", polls)
		key := fmt.Sprintf("%sKEY34567890ABCDEFGHIJKLMNOPQRS", strings.ToUpper(name))
		source := "package com.example.sample\n\nimport android.app.Activity\nimport com.tenjin.android.TenjinSDK\n\nclass " + name +
			" {\n    fun start(activity: Activity) {\n        TenjinSDK.getInstance(activity, \"" + key + "\").connect()\n    }\n}\n"
		if err := os.WriteFile(filepath.Join(dir, "app", "src", "main", "java", "com", "example", "sample", name+".kt"), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	code, stdout, _ := h.typing("", "").run("setup", dir)
	lacks(t, stdout, "has not processed the install yet. Run tenjin doctor --device again in a minute.")
	if code != 0 || polls != processingPolls {
		t.Errorf("exit = %d, polls = %d", code, polls)
	}
	scans := map[string]bool{}
	for _, key := range h.fake.installKeys {
		scans[key] = true
	}
	if len(scans) != 1 || !scans[testproject.Key] {
		t.Errorf("the source was scanned %d times during the launch, want 1: %d lookups", len(scans), len(h.fake.installKeys))
	}
}

// A launch that never reached Tenjin is read again after the next launch.
func TestSetupReadsTheDeviceAgainAfterAFailedLaunch(t *testing.T) {
	h, dir := correctAndroid(t)
	calls := h.connectDevice("")

	_, stdout, _ := h.typing("", "", "s").run("setup", dir)
	lacks(t, stdout,
		"Fix what is marked above, rebuild and relaunch the app",
		"Skipped. After launching the app, run tenjin doctor --device",
	)
	if reads := logReads(*calls); reads != 1 {
		t.Errorf("device read %d times, want 1", reads)
	}
}

func TestSetupLaunchCanBeSkipped(t *testing.T) {
	h, dir := correctAndroid(t)
	calls := h.connectDevice(readCapturedLog(t))

	code, stdout, _ := h.typing("", "s").run("setup", dir)
	lacks(t, stdout, "Skipped. After launching the app, run tenjin doctor --device to see Tenjin's answer.")
	if code != 0 || logReads(*calls) != 0 {
		t.Errorf("exit = %d, adb calls = %q", code, *calls)
	}
}

// A device named with --serial that is not there is a mistake to say, not a
// step to skip: a UDID names no device of an Android project, and a serial
// that no connected device has reads nothing.
func TestSetupRefusesASerialThatNamesNoDevice(t *testing.T) {
	h, dir := correctAndroid(t)
	h.connectDevice(readCapturedLog(t))
	code, stdout, stderr := h.typing("", "").run("setup", dir, "--serial", "243AACFD-275B-4332-8D06-D4B06EFEA171")
	if code != 2 || !strings.Contains(stderr, "--serial 243AACFD-275B-4332-8D06-D4B06EFEA171 names an iPhone or an iOS simulator, and this project does not build for iOS") {
		t.Errorf("UDID: exit = %d, stderr = %q", code, stderr)
	}
	// Said at once, not after six steps of work.
	if strings.Contains(stdout, "Step 2 of 7") {
		t.Errorf("setup went on with a --serial it cannot use:\n%s", stdout)
	}

	h, dir = correctAndroid(t)
	h.connectDevice(readCapturedLog(t))
	code, stdout, stderr = h.typing("", "", "", "s").run("setup", dir, "--serial", "emulator-9999")
	if code != 5 || !strings.Contains(stderr, "No connected Android device has the serial emulator-9999") || !strings.Contains(stderr, "Connected: emulator-5554.") {
		t.Errorf("not connected: exit = %d, stderr = %q", code, stderr)
	}
	if strings.Count(stdout, "then press Enter (s to skip):") != 1 || strings.Contains(stdout, "Skipped.") {
		t.Errorf("setup asked for another launch of a device that is not there:\n%s", stdout)
	}
}

// Without a terminal there is nobody to launch the app, so the step says how
// to check it later instead of reading a stale log.
func TestSetupWithoutATerminalLeavesTheLaunchToTheDeveloper(t *testing.T) {
	h, dir := correctAndroid(t)
	calls := h.connectDevice(readCapturedLog(t))

	code, stdout, _ := h.run("setup", dir)
	lacks(t, stdout, "Launch a debug build on an Android device or emulator, then run tenjin doctor --device")
	if code != 0 || logReads(*calls) != 0 {
		t.Errorf("exit = %d, adb calls = %q", code, *calls)
	}
}

// Step 7 reads only the launch it asked for: an earlier launch's refused
// connect, still in the device's log, does not stand for it. Other apps have
// logged since that launch, as they do on any device.
func TestSetupReadsOnlyTheNewLaunch(t *testing.T) {
	h, dir := correctAndroid(t)
	h.fake.installs[capturedInstall] = "Organic"
	launch := readCapturedLog(t)
	h.connectDevice(launch)
	h.device.earlier = strings.ReplaceAll(strings.ReplaceAll(launch, "10-01 21:", "10-01 20:"),
		`{"code":200,"success":true}`, `{"code":401,"message":"invalid api key"}`) +
		"10-01 21:30:00.000 I/ActivityManager(  612): Displayed com.android.settings/.Settings\n"

	code, stdout, stderr := h.typing("", "").run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout, "Tenjin accepted the app's connect request (SDK 1.23.0)", "The integration works end to end.")
	if strings.Contains(stdout, "invalid api key") {
		t.Errorf("the earlier launch was read:\n%s", stdout)
	}
}

// A launch that sent nothing is not stood in for by an earlier one that
// worked, still in the device's log.
func TestSetupDoesNotTakeAnEarlierLaunchForTheNewOne(t *testing.T) {
	h, dir := correctAndroid(t)
	h.fake.installs[capturedInstall] = "Organic"
	h.connectDevice("10-01 22:10:00.000 I/ActivityManager(  612): Start proc 4242:com.example.sample/u0a190\n")
	h.device.earlier = readCapturedLog(t) + "10-01 22:00:00.000 I/ActivityManager(  612): Displayed com.android.settings/.Settings\n"

	_, stdout, _ := h.typing("", "", "s").run("setup", dir)
	lacks(t, stdout, "Fix what is marked above, rebuild and relaunch the app")
	if strings.Contains(stdout, "works end to end") || strings.Contains(stdout, "Tenjin accepted the app's connect request") {
		t.Errorf("the earlier launch was read for this one:\n%s", stdout)
	}
}

// A phone plugged in only after the launch was asked for was not there when
// setup looked at its log's newest line, so where the log stood is unknown.
// Its log is yesterday's, and yesterday's accepted connect is not this
// launch's: the read goes by the phone's own clock, and says why.
func TestSetupDoesNotTakeAStaleLogForALaunchOnAPhonePluggedInLate(t *testing.T) {
	h, dir := correctAndroid(t)
	h.fake.installs[capturedInstall] = "Organic"
	h.connectDevice("")
	h.device.earlier = readCapturedLog(t)
	h.device.pluggedInLater = true
	// The phone's clock, the next morning.
	h.device.clock = "10-02 09:00:00"

	code, stdout, _ := h.typing("", "", "s").run("setup", dir)
	lacks(t, stdout,
		"! The device could not be read when the launch was asked for (No Android device or emulator is connected), so what it logged since then is read by its own clock.",
		"The device log has no output from the Tenjin SDK",
		"Fix what is marked above, rebuild and relaunch the app",
	)
	if code != 0 || strings.Contains(stdout, "works end to end") || strings.Contains(stdout, "Tenjin accepted the app's connect request") {
		t.Errorf("exit = %d; yesterday's launch was taken for this one:\n%s", code, stdout)
	}
}

// A phone plugged in after the launch was asked for, then launched: the
// launch is read, by the phone's own clock, and what the phone logged before
// is not.
func TestSetupReadsTheLaunchOnAPhonePluggedInLate(t *testing.T) {
	h, dir := correctAndroid(t)
	h.fake.installs[capturedInstall] = "Organic"
	launch := readCapturedLog(t)
	h.connectDevice(launch)
	// An hour earlier, every request was refused.
	h.device.earlier = strings.ReplaceAll(strings.ReplaceAll(launch, "10-01 21:", "10-01 20:"), `{"code":200`, `{"code":401,"message":"invalid api key"`)
	h.device.pluggedInLater = true
	// The phone's clock, just before the launch.
	h.device.clock = "10-01 21:55:01"

	code, stdout, stderr := h.typing("", "").run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout,
		"! The device could not be read when the launch was asked for (No Android device or emulator is connected), so what it logged since then is read by its own clock.",
		"Tenjin accepted the app's connect request (SDK 1.23.0)",
		"The integration works end to end.",
	)
	if strings.Contains(stdout, "invalid api key") || strings.Contains(stdout, "not accepted") {
		t.Errorf("the earlier launch was read:\n%s", stdout)
	}
}

// A phone plugged in late whose clock cannot be read leaves the launch
// unread, and setup asks for it again: the whole log must not stand for it.
func TestSetupAsksAgainWhenAPhonePluggedInLateHasNoClock(t *testing.T) {
	h, dir := correctAndroid(t)
	h.fake.installs[capturedInstall] = "Organic"
	h.connectDevice("")
	h.device.earlier = readCapturedLog(t)
	h.device.pluggedInLater = true

	code, stdout, _ := h.typing("", "", "s").run("setup", dir)
	lacks(t, stdout,
		"! The clock of emulator-5554 could not be read, so what it logged since the launch was asked for cannot be told from what it held before: exit status 1",
		"  Launch the app again.",
		"Skipped.",
	)
	if code != 0 || strings.Contains(stdout, "Tenjin accepted") {
		t.Errorf("exit = %d; the whole log was read:\n%s", code, stdout)
	}
}

// logReads counts the reads of a device's whole log among adb calls, leaving
// out the look at its newest line that setup takes before each launch.
func logReads(calls [][]string) int {
	n := 0
	for _, args := range calls {
		read, mark := false, false
		for _, a := range args {
			read = read || a == "logcat"
			mark = mark || a == "-t"
		}
		if read && !mark {
			n++
		}
	}
	return n
}

// Offline, a key already written in the project settles its build: setup
// does not ask for one.
func TestSetupOfflineUsesTheKeyInTheProject(t *testing.T) {
	h, dir := correctAndroid(t)
	_, stdout, _ := h.typing("", "s").run("setup", dir, "--offline")
	if !strings.Contains(stdout, "the project holds a key, not checked (--offline)") || strings.Contains(stdout, "Paste the") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// connectBoth puts an Android device and a booted iOS simulator side by
// side, each answering with its own log.
func (h *harness) connectBoth(androidLog, iosLog string) {
	h.connectDevice(androidLog)
	adbPath, adbRun := h.env.lookPath, h.env.run
	h.bootSimulator(iosLog)
	xcrunPath, xcrunRun := h.env.lookPath, h.env.run
	h.env.lookPath = func(name string) (string, error) {
		if name == "xcrun" {
			return xcrunPath(name)
		}
		return adbPath(name)
	}
	h.env.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "/usr/bin/xcrun" {
			return xcrunRun(ctx, name, args...)
		}
		return adbRun(ctx, name, args...)
	}
}

// A launch on each platform names each real device's identifiers, and the
// dashboard's pages once; a simulator is not offered as a test device.
func TestSetupNamesEachDeviceItReads(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.fake.addApp("Sample", "com.example.sample", "ios")
	h.signIn()
	h.fake.installs[capturedInstall] = "Organic"
	h.connectBoth(readCapturedLog(t), readCapturedIOSLog(t))
	dir := testproject.Write(t, testproject.Flutter())

	code, stdout, stderr := h.typing("", "", "", "").run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout, "Optional, and not needed for the integration", "register this device (Advertising ID ")
	if strings.Contains(stdout, "IDFV AAAABBBB") {
		t.Errorf("the simulator was offered as a test device:\n%s", stdout)
	}
	if n := strings.Count(stdout, "/dashboard/debug_app_users"); n != 1 {
		t.Errorf("the test device pages are named %d times:\n%s", n, stdout)
	}
}
