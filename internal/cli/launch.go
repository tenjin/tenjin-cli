package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/devicelog"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// Tenjin processes an install within seconds of accepting it. setup waits
// this long, at most, before saying it has not happened yet.
const (
	processingPoll  = 5 * time.Second
	processingPolls = 12
)

// launch is setup's last step: the app running on a device, and Tenjin's
// answer to it, read from the device's log. It ends when Tenjin has
// processed the device's install, which is the proof that events get
// through. A project that builds for both platforms is checked on each.
func (s *setup) launch(p *project.Project) error {
	platforms, err := serialPlatforms(p, s.serial)
	if err != nil {
		return err
	}
	for _, platform := range platforms {
		if err := s.launchOn(p, platform); err != nil {
			return err
		}
	}
	return nil
}

// noteLaunch notes how the launch on a platform ended.
func (s *setup) noteLaunch(platform project.Platform, outcome string) {
	s.env.note("launch_"+string(platform), outcome)
}

// deviceNoun is what the developer launches the app on, for a platform.
func deviceNoun(platform project.Platform) string {
	if platform == project.IOS {
		return "an iPhone or an iOS simulator"
	}
	return "an Android device or emulator"
}

func (s *setup) launchOn(p *project.Project, platform project.Platform) error {
	if len(p.Targets) > 1 {
		s.say("\n%s", s.style.Bold(platformNames[platform]))
	}
	var available bool
	if platform == project.IOS {
		_, err := s.env.lookPath("xcrun")
		available = err == nil
	} else {
		_, available = devicelog.FindADB(s.env.lookPath, s.env.getenv)
	}
	if !available || !s.env.interactive {
		s.noteLaunch(platform, "not_read")
		switch {
		case !available && platform == project.IOS:
			s.say("Xcode was not found, so the simulator cannot be read from here.")
		case !available:
			s.say("adb was not found, so the device cannot be read from here. Install the Android platform tools.")
		}
		s.say("Launch a debug build on %s, then run %s to see Tenjin's answer.", deviceNoun(platform), s.style.Accent("tenjin doctor --device"))
		return nil
	}
	if platform == project.IOS {
		s.say("Tenjin's answer is read from a launch of a debug build, on one of these:")
		s.say("  %s An iPhone connected to this Mac by cable, unlocked, with the debug build installed (run it once from Xcode). The Tenjin CLI launches the app and reads its console.", s.style.Dim("·"))
		s.say("  %s An iOS simulator, with the debug build launched on it from Xcode.", s.style.Dim("·"))
		s.say("Debug builds must call TenjinSDK.debugLogs() in the iOS launch code, or the SDK logs nothing to read. If the app asks for tracking permission before it connects, answer on the device.")
	}

	// refused is set once a read shows Tenjin refusing the app's connect, so
	// that skipping after it does not end setup as a success.
	refused := false
	// checks are the project's checks, run once for every read of the
	// device: the project does not change while its launch is read, and the
	// wait for Tenjin reads the device every few seconds.
	var checks *verify.Prepared
	for {
		mark := markLaunch(s.cmd.Context(), s.env, platform, s.serial)
		prompt := fmt.Sprintf("\nLaunch a debug build of the app on %s, then press Enter (s to skip):", deviceNoun(platform))
		if platform == project.IOS {
			prompt = "\nConnect the iPhone, or launch the app on a simulator, then press Enter (s to skip):"
		}
		answer, err := s.env.ask(s.out, prompt)
		if err != nil {
			return err
		}
		if strings.EqualFold(answer, "s") {
			s.noteLaunch(platform, "skipped")
			s.say("Skipped. After launching the app, run %s to see Tenjin's answer.", s.style.Accent("tenjin doctor --device"))
			if refused {
				return clierr.New(clierr.ChecksFailed, "Tenjin refused the app's last connect request")
			}
			return nil
		}

		raw, from, err := readLaunch(s.cmd.Context(), s.env, p, platform, s.serial, mark, s.say)
		var unavailable *devicelog.Unavailable
		if errors.As(err, &unavailable) {
			// The device named with --serial cannot be read: a mistake to
			// say, not a launch to wait for.
			if s.serial != "" {
				return deviceNotThere(unavailable)
			}
			// Device names come from the device.
			s.say("%s %s", s.style.Yellow("!"), render.Clean(unavailable.Reason))
			if unavailable.Fix != "" {
				s.say("  %s", render.Clean(unavailable.Fix))
			}
			continue
		}
		if err != nil {
			return err
		}
		if mark.unmarked != nil {
			// Device problems come from the device.
			s.say("%s The device could not be read when the launch was asked for (%s), so what it logged since then is read by its own clock.", s.style.Yellow("!"), render.Clean(mark.unmarked.Reason))
		}
		log, err := devicelog.Parse(bytes.NewReader(raw))
		if err != nil {
			return err
		}

		if checks == nil {
			checks = verify.Prepare(s.cmd.Context(), p, verify.Options{Given: s.givenForChecks()})
		}
		report := s.deviceReport(checks, log, platform)
		// Wait only when Tenjin answered that it has not processed the
		// install yet: a lookup that failed, or could not be made, will not
		// come right by waiting.
		if connectAccepted(report) && notProcessedYet(report) {
			s.say("Tenjin accepted the app's request. Waiting for it to process the install…")
			for i := 0; i < processingPolls && notProcessedYet(report); i++ {
				if err := s.env.pause(processingPoll); err != nil {
					return err
				}
				report = s.deviceReport(checks, log, platform)
			}
		}
		s.writeDeviceFindings(report)

		switch {
		case installProcessed(report):
			s.noteLaunch(platform, "processed")
			s.say("\n%s Tenjin received this device's install and processed it. The integration works end to end.", s.style.Green("✓"))
			s.testDevicePages(report)
			return nil
		case connectAccepted(report) && (from == fromSimulator || deviceFinding(report, "device.simulator") != nil):
			s.noteLaunch(platform, "simulator")
			physical := "an iPhone connected by cable"
			if platform == project.Android {
				physical = "a physical Android device"
			}
			s.say("\nTenjin accepted the app's request. That is as far as a simulator or emulator goes: the SDK marks its installs, and Tenjin leaves them out of attribution by design. To see an install processed, run a debug build on %s, then %s.", physical, s.style.Accent("tenjin doctor --device"))
			return nil
		case connectAccepted(report):
			s.noteLaunch(platform, "accepted")
			s.say("\nTenjin accepted the app's request, and has not processed the install yet. Run %s again in a minute.", s.style.Accent("tenjin doctor --device"))
			s.testDevicePages(report)
			return nil
		}
		if f := deviceFinding(report, "device.connect"); f != nil && f.Severity == verify.Error {
			refused = true
		}
		s.say("\nFix what is marked above, rebuild and relaunch the app; the next read is of the new launch.")
	}
}

// deviceReport checks what the device's log says, and asks Tenjin about the
// install it shows. The project checks have already run, so only the device
// findings are kept.
func (s *setup) deviceReport(checks *verify.Prepared, log *devicelog.Log, platform project.Platform) *verify.Report {
	input := &verify.DeviceInput{Log: log, Platform: platform}
	if !s.offline {
		input.Lookup = s.env.trackClient()
	}
	report := checks.Device(input)
	var device []verify.Finding
	for _, f := range report.Findings {
		if strings.HasPrefix(f.ID, "device.") {
			device = append(device, f)
		}
	}
	report.Findings = device
	return report
}

func connectAccepted(r *verify.Report) bool {
	f := deviceFinding(r, "device.connect")
	return f != nil && f.Severity == verify.Pass
}

// notProcessedYet is Tenjin's answer that it has not processed the install,
// as opposed to having done so or not having been asked.
func notProcessedYet(r *verify.Report) bool {
	f := deviceFinding(r, "device.processed")
	return f != nil && f.Severity == verify.Info
}

func installProcessed(r *verify.Report) bool {
	f := deviceFinding(r, "device.processed")
	return f != nil && f.Severity == verify.Pass
}

func deviceFinding(r *verify.Report, id string) *verify.Finding {
	for i := range r.Findings {
		if r.Findings[i].ID == id {
			return &r.Findings[i]
		}
	}
	return nil
}

// writeDeviceFindings prints what the launch showed. The device's
// identifiers are left to testDevicePages, which says what they are for.
func (s *setup) writeDeviceFindings(r *verify.Report) {
	r = forPeople(r)
	var b strings.Builder
	for _, f := range r.Findings {
		if f.ID != "device.identifiers" {
			writeFinding(&b, s.style, f, render.TextWidth(s.out))
		}
	}
	_, _ = io.WriteString(s.out, b.String())
}

// testDevicePages says where to watch the events that come after the
// install, which only the dashboard shows, with the identifiers that
// register this device. It is optional, and said so: the launch check does
// not need it. The pages are named once per run; each device's identifiers
// are said. A simulator or emulator cannot be a test device.
func (s *setup) testDevicePages(r *verify.Report) {
	device := "the device"
	if f := deviceFinding(forPeople(r), "device.identifiers"); f != nil && f.Detail != "" {
		device = "this device (" + strings.TrimSuffix(f.Detail, ".") + ")"
	} else if s.pagesShown {
		return
	}
	if s.pagesShown {
		s.say("Optional, to watch its events too: register %s as a test device on the same page.", device)
		return
	}
	s.pagesShown = true
	s.say("\nOptional, and not needed for the integration: to watch purchases and custom events arrive one by one, register %s as a test device, then open the Live Event Tool:", device)
	s.say("  %s", s.env.endpoints.TestDevicesURL())
	s.say("  %s", s.env.endpoints.LiveEventsURL())
	s.say("A new test device takes a while to become active, and its events are left out of reports.")
}
