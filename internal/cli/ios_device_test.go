package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// The log of a real launch on an iOS simulator, scrubbed, and the
// installation ID in it.
const (
	capturedIOSLog     = "../devicelog/testdata/ios-accepted.log"
	capturedIOSInstall = "AAAABBBB-CCCC-DDDD-EEEE-000000000003"
)

func readCapturedIOSLog(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(capturedIOSLog)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDoctorReadsAnIOSSimulator(t *testing.T) {
	h := newHarness(t)
	h.fake.installs[capturedIOSInstall] = "Organic"
	calls := h.bootSimulator(readCapturedIOSLog(t))
	dir := testproject.Write(t, testproject.IOSNative())

	code, stdout, stderr := h.run("doctor", dir, "--device")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout,
		"On the device",
		"Tenjin accepted the app's connect request (SDK 1.19.1)",
		"Tenjin has processed this device's install, attributed to Organic",
		// The capture's app never asked for tracking permission.
		"connect() ran before the user was asked for tracking permission",
		"IDFV AAAABBBB-CCCC-DDDD-EEEE-000000000002",
	)
	read := (*calls)[len(*calls)-1]
	if read[0] != "simctl" || read[2] != "243AACFD-275B-4332-8D06-D4B06EFEA171" {
		t.Errorf("simctl calls = %q", *calls)
	}
	if got := strings.Join(h.fake.installsAsked, ","); got != capturedIOSInstall {
		t.Errorf("installs looked up = %q", got)
	}
}

// A project for both platforms reads the simulator when no Android device
// can be read, and says why for each when neither can.
func TestDoctorDeviceOnACrossPlatformProject(t *testing.T) {
	h := newHarness(t)
	h.bootSimulator(readCapturedIOSLog(t))
	dir := testproject.Write(t, testproject.Flutter())
	_, stdout, _ := h.run("doctor", dir, "--device", "--offline")
	lacks(t, stdout, "Tenjin accepted the app's connect request (SDK 1.19.1)")

	h = newHarness(t)
	_, stdout, _ = h.run("doctor", dir, "--device", "--offline")
	lacks(t, stdout, "adb was not found, so the device log could not be read; Xcode was not found", "Android: Install the Android platform tools", "iOS: Install Xcode")
}

func TestDoctorReadsASavedIOSLog(t *testing.T) {
	h := newHarness(t)
	dir := testproject.Write(t, testproject.IOSNative())
	_, stdout, _ := h.run("doctor", dir, "--logs", capturedIOSLog, "--offline")
	lacks(t, stdout, "Tenjin accepted the app's connect request (SDK 1.19.1)")
}

// Without debug logs the iOS SDK writes nothing, and doctor says that is
// the likely reason.
func TestDoctorExplainsASilentIOSSimulator(t *testing.T) {
	h := newHarness(t)
	h.bootSimulator("Timestamp               Ty Process[PID:TID]\n")
	dir := testproject.Write(t, testproject.IOSNative())
	_, stdout, _ := h.run("doctor", dir, "--device", "--offline")
	lacks(t, stdout, "The device log has no output from the Tenjin SDK", "TenjinSDK.debugLogs()")
}

func TestSetupChecksALaunchOnTheIOSSimulator(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "ios")
	h.signIn()
	h.fake.installs[capturedIOSInstall] = "Organic"
	h.bootSimulator(readCapturedIOSLog(t))
	dir := testproject.Write(t, testproject.IOSNative())

	code, stdout, stderr := h.typing("", "").run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout,
		"On an iPhone connected by cable, the Tenjin CLI launches the app itself",
		"Install a debug build on the iPhone, or launch one on a simulator, then press Enter",
		"✓ Tenjin received this device's install and processed it. The integration works end to end.",
	)
}

// The log of a real launch on an iPhone, scrubbed, and its installation ID.
const (
	capturedIPhoneLog     = "../devicelog/testdata/ios-device-accepted.log"
	capturedIPhoneInstall = "AAAABBBB-CCCC-DDDD-EEEE-000000000013"
)

// A connected iPhone is preferred to a simulator: tenjin launches the app
// on it, reads its console until Tenjin has answered, and leaves it running.
func TestDoctorLaunchesTheAppOnAConnectedIPhone(t *testing.T) {
	h := newHarness(t)
	h.fake.installs[capturedIPhoneInstall] = "Organic"
	raw, err := os.ReadFile(capturedIPhoneLog)
	if err != nil {
		t.Fatal(err)
	}
	launches := h.connectIPhone(string(raw))
	dir := testproject.Write(t, testproject.IOSNative())

	code, stdout, stderr := h.run("doctor", dir, "--device")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout,
		"Tenjin accepted the app's connect request (SDK 1.19.1)",
		"Tenjin has processed this device's install, attributed to Organic",
	)
	if len(*launches) != 1 || strings.Join((*launches)[0], " ") != "devicectl device process launch --device 00008130-000000000000000A --terminate-existing --console com.example.sample" {
		t.Errorf("launches = %q", *launches)
	}
}

func TestIPhonesThatCannotBeRead(t *testing.T) {
	dir := testproject.Write(t, testproject.IOSNative())
	for _, c := range []struct {
		name, console string
		phones        []string
		want          []string
	}{
		{"developer mode off", "", []string{`{"connectionProperties":{"tunnelState":"connected"},"deviceProperties":{"name":"Test iPhone","developerModeStatus":"disabled"},"hardwareProperties":{"udid":"00008130-000000000000000A","reality":"physical","platform":"iOS"}}`},
			[]string{"Developer Mode is off on Test iPhone", "Settings → Privacy & Security → Developer Mode"}},
		{"app not installed", "ERROR: The requested application com.example.sample is not installed.", nil,
			[]string{"Could not launch com.example.sample on Test iPhone: ERROR: The requested application com.example.sample is not installed.", "run it once from Xcode"}},
		{"two phones", "", []string{
			`{"connectionProperties":{"tunnelState":"connected"},"deviceProperties":{"name":"One","developerModeStatus":"enabled"},"hardwareProperties":{"udid":"00008130-000000000000000A","reality":"physical","platform":"iOS"}}`,
			`{"connectionProperties":{"tunnelState":"connected"},"deviceProperties":{"name":"Two","developerModeStatus":"enabled"},"hardwareProperties":{"udid":"00008130-000000000000000B","reality":"physical","platform":"iOS"}}`},
			[]string{"More than one iPhone is connected: One (00008130-000000000000000A), Two (00008130-000000000000000B)", "--serial"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.connectIPhone(c.console, c.phones...)
			_, stdout, _ := h.run("doctor", dir, "--device", "--offline")
			lacks(t, stdout, c.want...)
		})
	}
	// --serial with an iPhone's UDID picks it.
	h := newHarness(t)
	raw, _ := os.ReadFile(capturedIPhoneLog)
	launches := h.connectIPhone(string(raw),
		`{"connectionProperties":{"tunnelState":"connected"},"deviceProperties":{"name":"One","developerModeStatus":"enabled"},"hardwareProperties":{"udid":"00008130-000000000000000A","reality":"physical","platform":"iOS"}}`,
		`{"connectionProperties":{"tunnelState":"connected"},"deviceProperties":{"name":"Two","developerModeStatus":"enabled"},"hardwareProperties":{"udid":"00008130-000000000000000B","reality":"physical","platform":"iOS"}}`)
	h.run("doctor", dir, "--device", "--offline", "--serial", "00008130-000000000000000B")
	if len(*launches) != 1 || (*launches)[0][5] != "00008130-000000000000000B" {
		t.Errorf("launches = %q", *launches)
	}
}

func TestSetupChecksALaunchOnAnIPhone(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "ios")
	h.signIn()
	h.fake.installs[capturedIPhoneInstall] = "Organic"
	raw, _ := os.ReadFile(capturedIPhoneLog)
	h.connectIPhone(string(raw))
	dir := testproject.Write(t, testproject.IOSNative())
	code, stdout, _ := h.typing("", "").run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, stdout)
	}
	lacks(t, stdout, "✓ Tenjin received this device's install and processed it. The integration works end to end.")
}

// On a simulator, an accepted install that is not processed is not a
// matter of waiting: say so, and what confirms it.
func TestSetupSaysWhatConfirmsASimulatorLaunch(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "ios")
	h.signIn()
	h.bootSimulator(readCapturedIOSLog(t))
	dir := testproject.Write(t, testproject.IOSNative())
	_, stdout, _ := h.typing("", "").run("setup", dir)
	lacks(t, stdout, "Installs from an iOS simulator have not been seen to be processed; a launch on an iPhone connected by cable confirms it.")
}
