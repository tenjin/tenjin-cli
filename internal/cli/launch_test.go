package cli

import (
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
