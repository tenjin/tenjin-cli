package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

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
	for _, platform := range devicePlatforms(p, "") {
		if err := s.launchOn(p, platform); err != nil {
			return err
		}
	}
	return nil
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
		s.say("On an iPhone connected by cable, the Tenjin CLI launches the app itself to read its console: install a debug build on it first (run it once from Xcode). On a simulator, launch the app yourself. Either way, debug builds must call TenjinSDK.debugLogs(), as the checks above say.")
	}

	for {
		mark := markLaunch(s.cmd.Context(), s.env, platform, "")
		prompt := fmt.Sprintf("\nLaunch a debug build of the app on %s, then press Enter (s to skip):", deviceNoun(platform))
		if platform == project.IOS {
			prompt = "\nInstall a debug build on the iPhone, or launch one on a simulator, then press Enter (s to skip):"
		}
		answer, err := s.env.ask(s.out, prompt)
		if errors.Is(err, io.EOF) || strings.EqualFold(answer, "s") {
			s.say("Skipped. After launching the app, run %s to see Tenjin's answer.", s.style.Accent("tenjin doctor --device"))
			return nil
		}
		if err != nil {
			return err
		}

		raw, from, err := readLaunch(s.cmd.Context(), s.env, p, platform, "", mark)
		var unavailable *devicelog.Unavailable
		if errors.As(err, &unavailable) {
			s.say("%s %s", s.style.Yellow("!"), unavailable.Reason)
			if unavailable.Fix != "" {
				s.say("  %s", unavailable.Fix)
			}
			continue
		}
		if err != nil {
			return err
		}
		log, err := devicelog.Parse(bytes.NewReader(raw))
		if err != nil {
			return err
		}

		report := s.deviceReport(p, log, platform)
		if connectAccepted(report) && !installProcessed(report) && !s.offline {
			s.say("Tenjin accepted the app's request. Waiting for it to process the install…")
			for i := 0; i < processingPolls && !installProcessed(report); i++ {
				if err := s.env.pause(processingPoll); err != nil {
					return err
				}
				report = s.deviceReport(p, log, platform)
			}
		}
		s.writeDeviceFindings(report)

		switch {
		case installProcessed(report):
			s.say("\n%s Tenjin received this device's install and processed it. The integration works end to end.", s.style.Green("✓"))
			s.testDevicePages()
			return nil
		case connectAccepted(report) && from == fromSimulator:
			s.say("\nTenjin accepted the app's request, and has not processed the install. Installs from an iOS simulator have not been seen to be processed; a launch on an iPhone connected by cable confirms it. Run %s with one connected.", s.style.Accent("tenjin doctor --device"))
		case connectAccepted(report):
			s.say("\nTenjin accepted the app's request, and has not processed the install yet. Run %s again in a minute.", s.style.Accent("tenjin doctor --device"))
			s.testDevicePages()
			return nil
		}
		s.say("\nFix what is marked above, rebuild and relaunch the app; the next read is of the new launch.")
	}
}

// deviceReport checks what the device's log says, and asks Tenjin about the
// install it shows. The project checks have already run, so only the device
// findings are kept.
func (s *setup) deviceReport(p *project.Project, log *devicelog.Log, platform project.Platform) *verify.Report {
	input := &verify.DeviceInput{Log: log, Platform: platform}
	if !s.offline {
		input.Lookup = s.env.trackClient()
	}
	report := verify.Run(s.cmd.Context(), p, verify.Options{Given: s.givenForChecks(), Device: input})
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

func (s *setup) writeDeviceFindings(r *verify.Report) {
	r = forPeople(r)
	var b strings.Builder
	for _, f := range r.Findings {
		writeFinding(&b, s.style, f, render.TextWidth(s.out))
	}
	_, _ = io.WriteString(s.out, b.String())
}

// testDevicePages says where to watch the events that come after the
// install, which only the dashboard shows. It says so once per run.
func (s *setup) testDevicePages() {
	if s.pagesShown {
		return
	}
	s.pagesShown = true
	s.say("To watch purchases and custom events arrive as well, register the device as a test device, then open the Live Event Tool:")
	s.say("  %s", s.env.endpoints.TestDevicesURL())
	s.say("  %s", s.env.endpoints.LiveEventsURL())
	s.say("A new test device can take a while to become active, and its events are left out of reports.")
}
