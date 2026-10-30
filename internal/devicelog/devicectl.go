package devicelog

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Streamer runs a program and returns its output, stopping it early as soon
// as stop reports true for a line of it. Stopping the program is not an
// error.
type Streamer func(ctx context.Context, name string, args []string, stop func(line string) bool) ([]byte, error)

// bundleIDPattern is what an iOS bundle ID can be. It also keeps one that
// starts with a dash from being taken for an option of devicectl.
var bundleIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)

// IPhoneLaunchWindow is how long a launch is read for, at most. Tenjin
// usually answers within a second of the app starting, but an app that
// connects once the tracking prompt is answered waits for the person, and
// the read stops as soon as Tenjin has answered anyway.
const IPhoneLaunchWindow = 90 * time.Second

// LaunchStage is how far a launch on an iPhone has come, for a person
// waiting on it.
type LaunchStage int

const (
	// Launching: the phone and the app can be launched, and the launch is
	// asked for.
	Launching LaunchStage = iota + 1
	// AppStarted: the app is running, and its console is followed.
	AppStarted
	// SDKLogging: the Tenjin SDK has written its first line.
	SDKLogging
)

// IPhone is a physical iOS device connected to this Mac.
type IPhone struct {
	UDID, Name    string
	DeveloperMode bool
}

// ConnectedIPhones lists the physical iOS devices connected to this Mac.
func ConnectedIPhones(ctx context.Context, run Runner, xcrun string) ([]IPhone, error) {
	out, err := run(ctx, xcrun, "devicectl", "list", "devices", "--json-output", "-")
	if err != nil {
		return nil, err
	}
	// devicectl may print a line of its own before the JSON.
	if i := strings.Index(string(out), "{"); i > 0 {
		out = out[i:]
	}
	var list struct {
		Result struct {
			Devices []struct {
				ConnectionProperties struct {
					TunnelState   string `json:"tunnelState"`
					PairingState  string `json:"pairingState"`
					TransportType string `json:"transportType"`
				} `json:"connectionProperties"`
				DeviceProperties struct {
					Name                string `json:"name"`
					DeveloperModeStatus string `json:"developerModeStatus"`
				} `json:"deviceProperties"`
				HardwareProperties struct {
					UDID     string `json:"udid"`
					Reality  string `json:"reality"`
					Platform string `json:"platform"`
				} `json:"hardwareProperties"`
			} `json:"devices"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, err
	}
	var phones []IPhone
	for _, d := range list.Result.Devices {
		hw, c := d.HardwareProperties, d.ConnectionProperties
		// A paired phone plugged in but idle has its tunnel "disconnected":
		// devicectl brings it up when it is used. One that cannot be reached
		// at all has no transport.
		reachable := c.TunnelState == "connected" || c.TransportType == "wired" || c.TransportType == "localNetwork"
		if hw.Reality != "physical" || hw.Platform != "iOS" || !reachable || (c.PairingState != "" && c.PairingState != "paired") {
			continue
		}
		phones = append(phones, IPhone{UDID: hw.UDID, Name: d.DeviceProperties.Name, DeveloperMode: d.DeviceProperties.DeveloperModeStatus == "enabled"})
	}
	return phones, nil
}

// ReadIPhone launches the app on a connected iPhone and returns what it
// writes to its console until Tenjin has answered its connect. The app must
// already be installed, as a debug build that turns the SDK's logs on. It
// is left running afterwards: only the console is closed. progress, which
// may be nil, is told each stage once, as the launch reaches it.
func ReadIPhone(ctx context.Context, stream Streamer, xcrun string, phone IPhone, bundleID string, progress func(LaunchStage)) ([]byte, error) {
	parent := ctx
	if !phone.DeveloperMode {
		return nil, &Unavailable{
			Reason: "Developer Mode is off on " + phone.Name,
			Fix:    "Turn it on in Settings → Privacy & Security → Developer Mode, then run this again.",
		}
	}
	if !bundleIDPattern.MatchString(bundleID) {
		return nil, &Unavailable{Reason: "The app's bundle ID could not be read, so it cannot be launched on " + phone.Name}
	}
	reached := LaunchStage(0)
	reach := func(stage LaunchStage) {
		if stage > reached {
			reached = stage
			if progress != nil {
				progress(stage)
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, IPhoneLaunchWindow)
	defer cancel()
	reach(Launching)
	out, err := stream(ctx, xcrun, []string{"devicectl", "device", "process", "launch",
		"--device", phone.UDID, "--terminate-existing", "--console", bundleID}, func(line string) bool {
		if strings.HasPrefix(line, "Launched application") {
			reach(AppStarted)
		}
		if !strings.Contains(line, iosTag) {
			return false
		}
		reach(SDKLogging)
		// The response body comes with debug logs, and "Connect succeeded"
		// after it; either way Tenjin has answered.
		return (strings.Contains(line, iosMarkResponse) ||
			strings.Contains(line, "Connect succeeded") || strings.Contains(line, iosMarkDeduped))
	})
	if parent.Err() != nil {
		return nil, parent.Err()
	}
	if err == nil && !strings.Contains(string(out), iosTag) {
		return nil, &Unavailable{
			Reason: fmt.Sprintf("%s ran on %s for %d seconds and the Tenjin SDK logged nothing", bundleID, phone.Name, int(IPhoneLaunchWindow/time.Second)),
			Fix: "Check that the build on the phone has the SDK in it (after adding it, update the native project, with npx cap sync ios or pod install where the app uses them, then rebuild from Xcode), that debug builds call TenjinSDK.debugLogs(), " +
				"and that connect() runs within " + fmt.Sprint(int(IPhoneLaunchWindow/time.Second)) + " seconds: if it waits for the tracking prompt or a later screen, get the app there. Then run this again.",
		}
	}
	if err != nil && !strings.Contains(string(out), iosTag) {
		detail := lastLine(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return nil, &Unavailable{
			Reason: fmt.Sprintf("Could not launch %s on %s: %s", bundleID, phone.Name, detail),
			Fix:    "Install a debug build on the phone first (run it once from Xcode), keep the phone unlocked, then run this again.",
		}
	}
	return out, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
