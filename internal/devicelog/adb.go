package devicelog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
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
	serial, err := androidDevice(ctx, run, adb, serial)
	if err != nil {
		return nil, err
	}
	return dumpAndroid(ctx, run, adb, serial, true)
}

// ReadAndroidSince is ReadAndroid for what was logged since a moment on this
// computer's clock, or everything when since is zero. A device stamps its
// log on its own clock, which need not agree with this computer's, so its
// clock is read with the log, and the moment is found on it by how long ago
// it was. The error is an *Unavailable when there is nothing to read from,
// or the device's clock could not be read: the log is then not read at all,
// since what came after the moment could not be told.
func ReadAndroidSince(ctx context.Context, run Runner, adb, serial string, since time.Time) ([]byte, error) {
	if since.IsZero() {
		return ReadAndroid(ctx, run, adb, serial)
	}
	serial, err := androidDevice(ctx, run, adb, serial)
	if err != nil {
		return nil, err
	}
	mark, err := androidClockAt(ctx, run, adb, serial, since)
	if err != nil {
		return nil, err
	}
	raw, err := dumpAndroid(ctx, run, adb, serial, true)
	if err != nil {
		return nil, err
	}
	return Since(raw, mark), nil
}

// AndroidMark is the time of the newest line in a device's whole log now, as
// logcat writes it, or "" when the log is empty. Passed to Since with a later
// read, it leaves only what was logged after. The error is an *Unavailable
// when the device cannot be read: where its log stands is then unknown, and
// a later read must not take the whole log as new (see ReadAndroidSince).
//
// The mark is read without the SDK's tag filter: logcat takes the last lines
// of the buffer (-t) before it filters them by tag, so asking for the SDK's
// newest line finds nothing once any other app has logged since.
func AndroidMark(ctx context.Context, run Runner, adb, serial string) (string, error) {
	serial, err := androidDevice(ctx, run, adb, serial)
	if err != nil {
		return "", err
	}
	raw, err := dumpAndroid(ctx, run, adb, serial, false, "-t", "1")
	if err != nil {
		return "", err
	}
	return newestTime(raw), nil
}

// deviceClockFormat asks the device's date for its time in logcat's form.
// There is no space in it, so that the shell on the device takes it as one
// word; the underscore is read back as logcat's space.
const deviceClockFormat = "+%m-%d_%H:%M:%S"

// androidClockAt is the moment since, on this computer's clock, as the
// device's own clock would have written it, in logcat's time format: the
// device is asked for its time, and since is as long ago on its clock as on
// this one. The device's date has whole seconds, and the question takes a
// moment, so the mark lands up to a second early, never late.
func androidClockAt(ctx context.Context, run Runner, adb, serial string, since time.Time) (string, error) {
	out, err := run(ctx, adb, "-s", serial, "shell", "date", deviceClockFormat)
	elapsed := time.Since(since)
	unreadable := func(detail string) error {
		return &Unavailable{
			Reason: fmt.Sprintf("The clock of %s could not be read, so what it logged since the launch was asked for cannot be told from what it held before: %s", serial, detail),
			Fix:    "Launch the app again.",
		}
	}
	if err != nil {
		return "", unreadable(describe(err))
	}
	// Year 0 serves: logcat writes none, and later knows a January line
	// comes after a December mark.
	clock, err := time.Parse("01-02_15:04:05", strings.TrimSpace(string(out)))
	if err != nil {
		return "", unreadable("its date answered " + strconv.Quote(lastLine(string(out))))
	}
	return clock.Add(-elapsed).Format("01-02 15:04:05.000"), nil
}

// androidDevice is the serial of the device to read: the one given, if it
// is connected, or the only one connected.
func androidDevice(ctx context.Context, run Runner, adb, serial string) (string, error) {
	out, err := run(ctx, adb, "devices")
	if err != nil {
		return "", &Unavailable{
			Reason: "adb could not list devices: " + describe(err),
			Fix:    "Check that adb works: run 'adb devices'.",
		}
	}
	devices := connected(string(out))
	switch {
	case len(devices) == 0 && hasState(string(out), "unauthorized"):
		return "", &Unavailable{
			Reason: "An Android device is connected, but it has not allowed USB debugging from this computer",
			Fix:    "Unlock the device and accept the \"Allow USB debugging?\" prompt, then run this again.",
		}
	case len(devices) == 0 && hasState(string(out), "offline"):
		return "", &Unavailable{
			Reason: "An Android device is connected but offline",
			Fix:    "Reconnect it, or run 'adb kill-server' and then 'adb devices', then run this again.",
		}
	case len(devices) == 0:
		return "", &Unavailable{
			Reason: "No Android device or emulator is connected",
			Fix:    "Start an emulator or plug in a device with USB debugging on, launch the app, then run this again.",
		}
	case serial == "" && len(devices) > 1:
		return "", &Unavailable{
			Reason: "More than one Android device is connected: " + strings.Join(devices, ", "),
			Fix:    "Pass --serial with the one to read.",
		}
	case serial == "":
		return devices[0], nil
	case !contains(devices, serial):
		return "", &Unavailable{
			Reason: "No connected Android device has the serial " + serial,
			Fix:    "Connected: " + strings.Join(devices, ", ") + ".",
		}
	}
	return serial, nil
}

// dumpAndroid dumps a device's log, in logcat's "time" format; filtered, it
// holds the SDK's tags only.
func dumpAndroid(ctx context.Context, run Runner, adb, serial string, filtered bool, extra ...string) ([]byte, error) {
	// -d dumps what is in the buffer and exits; -s silences every tag but
	// the ones listed.
	args := append(append([]string{"-s", serial, "logcat", "-d"}, extra...), "-v", "time")
	if filtered {
		args = append(append(args, "-s"), logcatTags...)
	}
	log, err := run(ctx, adb, args...)
	if err != nil {
		return nil, &Unavailable{Reason: fmt.Sprintf("adb could not read the log of %s: %s", serial, describe(err))}
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

// hasState reports whether `adb devices` lists a device in this state.
func hasState(output, state string) bool {
	for _, line := range strings.Split(output, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[1] == state {
			return true
		}
	}
	return false
}

// describe is a program's failure in its own words: the last line it wrote
// to stderr, where there is one, rather than only its exit status.
func describe(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if line := lastLine(string(exit.Stderr)); line != "" {
			return line
		}
	}
	return err.Error()
}

func contains(values []string, v string) bool {
	for _, candidate := range values {
		if candidate == v {
			return true
		}
	}
	return false
}
