package devicelog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Runner runs a program and returns its standard output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Unavailable explains why a device's log could not be read. It is something
// for the developer to act on, not a fault in the CLI.
type Unavailable struct{ Reason, Fix string }

func (u *Unavailable) Error() string { return u.Reason }

// logcatTags limits the dump to the tags the SDK logs under.
var logcatTags = []string{"TenjinSDK", "HttpConnection", "TenjinStartup", "TenjinReflection", "SourceAppStoreGetter"}

// FindADB locates the Android debug bridge: on PATH, or in the Android SDK's
// usual places on macOS, Linux and Windows.
func FindADB(lookPath func(string) (string, error), getenv func(string) string) (string, bool) {
	return findADB(lookPath, getenv, runtime.GOOS)
}

func findADB(lookPath func(string) (string, error), getenv func(string) string, goos string) (string, bool) {
	if path, err := lookPath("adb"); err == nil {
		return path, true
	}
	var roots []string
	for _, name := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if root := getenv(name); root != "" {
			roots = append(roots, root)
		}
	}
	if home := getenv("HOME"); home != "" {
		roots = append(roots, filepath.Join(home, "Library", "Android", "sdk"), filepath.Join(home, "Android", "Sdk"))
	}
	// Android Studio's default on Windows.
	if local := getenv("LOCALAPPDATA"); local != "" {
		roots = append(roots, filepath.Join(local, "Android", "Sdk"))
	}
	binary := "adb"
	if goos == "windows" {
		binary = "adb.exe"
	}
	for _, root := range roots {
		candidate := filepath.Join(root, "platform-tools", binary)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// ReadAndroid returns the SDK's lines from a connected Android device or
// emulator. serial picks one when several are connected. The error is an
// *Unavailable when there is nothing to read from.
func ReadAndroid(ctx context.Context, run Runner, adb, serial string) ([]byte, error) {
	return readAndroid(ctx, run, adb, serial)
}

// AndroidMark is the newest of the SDK's lines in a device's log now, or ""
// when there is none or the device cannot be read. Passed to After with a
// later read, it leaves only what was logged since.
func AndroidMark(ctx context.Context, run Runner, adb, serial string) string {
	raw, err := readAndroid(ctx, run, adb, serial, "-t", "1")
	if err != nil {
		return ""
	}
	return LastLine(raw)
}

func readAndroid(ctx context.Context, run Runner, adb, serial string, extra ...string) ([]byte, error) {
	out, err := run(ctx, adb, "devices")
	if err != nil {
		return nil, &Unavailable{
			Reason: fmt.Sprintf("adb could not list devices: %v", err),
			Fix:    "Check that adb works: run 'adb devices'.",
		}
	}
	devices := connected(string(out))
	switch {
	case len(devices) == 0:
		return nil, &Unavailable{
			Reason: "No Android device or emulator is connected",
			Fix:    "Start an emulator or plug in a device with USB debugging on, launch the app, then run this again.",
		}
	case serial == "" && len(devices) > 1:
		return nil, &Unavailable{
			Reason: "More than one Android device is connected: " + strings.Join(devices, ", "),
			Fix:    "Pass --serial with the one to read.",
		}
	case serial == "":
		serial = devices[0]
	case !contains(devices, serial):
		return nil, &Unavailable{
			Reason: "No connected Android device has the serial " + serial,
			Fix:    "Connected: " + strings.Join(devices, ", ") + ".",
		}
	}

	// -d dumps what is in the buffer and exits; -s silences every tag but
	// the ones listed.
	args := append(append([]string{"-s", serial, "logcat", "-d"}, extra...), "-v", "time", "-s")
	args = append(args, logcatTags...)
	log, err := run(ctx, adb, args...)
	if err != nil {
		return nil, &Unavailable{Reason: fmt.Sprintf("adb could not read the log of %s: %v", serial, err)}
	}
	return log, nil
}

// connected returns the serials of the devices `adb devices` reports as
// ready. Unauthorized and offline devices are left out.
func connected(output string) []string {
	var serials []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "device" {
			serials = append(serials, fields[0])
		}
	}
	return serials
}

func contains(values []string, v string) bool {
	for _, candidate := range values {
		if candidate == v {
			return true
		}
	}
	return false
}
