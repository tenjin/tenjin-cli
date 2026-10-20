package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/agent"
	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/track"
	"github.com/tenjin/tenjin-cli/internal/devicelog"
	"github.com/tenjin/tenjin-cli/internal/keyfill"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

func newDoctorCmd(opts *options, env *environment) *cobra.Command {
	var (
		sdkKeys  []string
		offline  bool
		strict   bool
		device   bool
		serial   string
		logs     string
		builds   []string
		features []string
		fixWith  string
	)
	cmd := &cobra.Command{
		Use:   "doctor [path]",
		Short: "Check a Tenjin SDK integration and explain what is wrong",
		Long: `Check the Tenjin SDK integration in a project: that the SDK is a dependency,
that it is initialised and connected, that the manifest and Info.plist carry
what the SDK needs, and that Tenjin accepts the SDK key for each platform's
bundle ID.

The key check sends one request to Tenjin and no event. Once a
day doctor also asks the SDKs' public registries (Maven Central, pub.dev,
npm, GitHub) for their newest releases, so its fixes name current versions.
--offline skips both.

doctor itself does not edit the project. At a terminal, when your coding
agent (Claude Code or Codex) is installed and there is something to fix in
code, it offers to have the agent fix it, then checks again; --agent picks
one without asking, and --agent none never offers. The agent works under the
same limits as in 'tenjin setup', is never given an SDK key, and leaves any
key already in the project as it is. Where it writes a placeholder for a key
instead, doctor writes the key given with --sdk-key (or the environment) in
its place, once Tenjin accepts that key for that build; with --offline it
writes none.

With --device, doctor also reads what the app's last launch sent and what
Tenjin answered to each request, then asks Tenjin whether the device's
install was processed. It reads a connected Android device or emulator, an
iPhone connected by cable, or a running iOS simulator. On an iPhone, doctor
launches the installed app itself to read its console, and leaves it
running. A project that builds for both platforms is read from an Android
device when one is connected, and from iOS otherwise. On iOS the SDK logs its
requests only when the build calls TenjinSDK.debugLogs(). Tenjin leaves
installs from a simulator out of attribution by design (the SDK marks them),
so an iPhone is the check that confirms one.

--logs reads a saved log instead, from logcat or from an iOS simulator, or
standard input with "-".

Exit status is 0 when nothing would stop events reaching Tenjin and 9
otherwise. With --strict, warnings count too.`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if fixWith != "" && fixWith != "none" {
				if !contains(agent.IDs(), fixWith) {
					return clierr.New(clierr.Usage, "unknown agent %q (use %s, or none)", fixWith, strings.Join(agent.IDs(), ", "))
				}
				// An agent's run is followed by a person; JSON is for scripts
				// and agents, which make the changes themselves.
				if format == render.JSON {
					return clierr.New(clierr.Usage, "--agent %s starts a coding agent, which is for a person at a terminal, and has no JSON output", fixWith).
						WithHint("Run 'tenjin doctor --agent %s' without --json, or read 'tenjin doctor --json' and make the fixes it names.", fixWith)
				}
			}
			p, err := loadProject(args)
			if err != nil {
				return err
			}
			if !p.Supported {
				return clierr.New(clierr.NotFound, "%s", strings.Join(p.Notes, " "))
			}

			if serial != "" && !device {
				return clierr.New(clierr.Usage, "--serial only applies with --device")
			}
			if device && logs != "" {
				return clierr.New(clierr.Usage, "use either --device or --logs, not both")
			}

			given, err := givenKeys(sdkKeys, env.getenv)
			if err != nil {
				return err
			}
			if err := resolveGiven(&given, p, env.getenv); err != nil {
				return err
			}
			if err := narrowBuilds(p, builds); err != nil {
				return err
			}
			if cmd.Flags().Changed("features") && features == nil {
				features = []string{}
			}
			chosen, err := chosenFeatures(p, features)
			if err != nil {
				return err
			}
			vopts := verify.Options{Given: given, Features: chosen}
			var tenjin *track.Client
			if !offline {
				vopts.Latest = env.latestSDKs(cmd.Context())
				tenjin = env.trackClient()
				vopts.Keys = tenjin
			}
			if device || logs != "" {
				input, err := readDevice(cmd, env, p, serial, logs)
				if err != nil {
					return err
				}
				if tenjin != nil {
					input.Lookup = tenjin
				}
				vopts.Device = input
			}
			report := verify.Run(cmd.Context(), p, vopts)

			if format == render.JSON {
				if err := render.WriteJSON(cmd.OutOrStdout(), report); err != nil {
					return err
				}
				return reportStatus(report.Summary, strict)
			}
			if err := writeReport(cmd.OutOrStdout(), report, offline); err != nil {
				return err
			}
			// The fixer writes no key of its own accord: keys are settled
			// after the agent, by writeGivenKeys.
			fixer := &setup{cmd: cmd, env: env, args: args, agentFlag: fixWith, offline: offline, out: cmd.OutOrStdout(), style: render.NewStyler(cmd.OutOrStdout()),
				given: given, features: chosen, inProject: map[string]bool{}, keys: map[string]verify.GivenKey{}}
			if fixer.features == nil {
				fixer.features = suggestedFeatures(p)
			}
			fixed, offered, err := fixer.offerFix(p, report)
			if err != nil {
				return err
			}
			if fixed {
				// The agent changed the project: read it again, write the
				// keys over the placeholders it left, and say what stands now.
				if p, err = loadProject(args); err != nil {
					return err
				}
				if err := narrowBuilds(p, builds); err != nil {
					return err
				}
				if err := fixer.writeGivenKeys(p); err != nil {
					fixer.say("%s %v", fixer.style.Yellow("!"), err)
				}
				vopts.Device = nil
				report = verify.Run(cmd.Context(), p, vopts)
				fixer.say("\n%s", fixer.style.Heading("Checking again"))
				if err := writeReport(cmd.OutOrStdout(), report, offline); err != nil {
					return err
				}
			}
			status := reportStatus(report.Summary, strict)
			if status != nil && !offered && len(doctorWork(report)) > 0 {
				return clierr.From(status).WithHint("Run 'tenjin setup' to fix these step by step, or 'tenjin doctor --agent claude' (or codex) to have your coding agent fix them.")
			}
			return status
		},
	}
	cmd.Flags().StringVar(&fixWith, "agent", "", "fix what can be fixed in code with this coding agent: claude or codex, or none never to offer (default: ask at a terminal)")
	addSDKKeyFlag(cmd, &sdkKeys)
	cmd.Flags().BoolVar(&offline, "offline", false, "skip the SDK key check against Tenjin, and the look for newer SDK releases")
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero on warnings as well as errors")
	cmd.Flags().BoolVar(&device, "device", false, "also read what the app's last launch sent: from a connected Android device or emulator, an iPhone connected by cable, or a running iOS simulator")
	cmd.Flags().StringVar(&serial, "serial", "", serialUsage)
	cmd.Flags().StringVar(&logs, "logs", "", "also read a saved device log (logcat or iOS simulator), or - for standard input")
	addBuildFlag(cmd, &builds)
	addFeaturesFlag(cmd, &features, "check")
	return cmd
}

// doctorWork is what a coding agent can fix of a doctor's report: what setup
// gives its agent, less what a device reported, which no change to the code
// answers by itself.
func doctorWork(report *verify.Report) []verify.Finding {
	var work []verify.Finding
	for _, f := range agentWork(report) {
		if !strings.HasPrefix(f.ID, "device.") {
			work = append(work, f)
		}
	}
	return work
}

// offerFix offers, at a terminal, to have the developer's coding agent fix
// what doctor found, and runs it on a yes or with --agent. The default is no:
// doctor is first a check. fixed says the agent ran; offered, that the
// developer was asked or chose with --agent.
func (s *setup) offerFix(p *project.Project, report *verify.Report) (fixed, offered bool, err error) {
	work := doctorWork(report)
	if len(work) == 0 || s.agentFlag == "none" || agent.InsideAgent(s.env.getenv) {
		return false, s.agentFlag == "none", nil
	}
	installed := agent.Find(s.env.lookPath)
	var helper *agent.Agent
	switch {
	case s.agentFlag != "":
		for i := range installed {
			if installed[i].ID == s.agentFlag {
				helper = &installed[i]
			}
		}
		if helper == nil {
			return false, true, clierr.New(clierr.NotFound, "--agent %s was given, but it is not installed on this machine", s.agentFlag)
		}
	case !s.env.interactive || len(installed) == 0:
		return false, false, nil
	default:
		s.say("\n%s above can be fixed in the project's code.", plural(len(work), "finding"))
		helper, err = s.askToFix(installed)
		if err != nil || helper == nil {
			return false, true, err
		}
	}
	s.helper = helper
	if s.refuseAgent() {
		return false, true, nil
	}
	s.inProject = keysInProject(s.cmd.Context(), p, s.given)
	if err := s.runAgent(p, report); err != nil {
		return false, true, err
	}
	return true, true, nil
}

// keysInProject are the builds whose SDK key is written in the project,
// whether Tenjin accepts it or not. The agent is told to leave those alone:
// doctor may hold no key to write back over a placeholder. It reads the
// files only.
func keysInProject(ctx context.Context, p *project.Project, given verify.GivenKeys) map[string]bool {
	given.PreferSource = true
	report := verify.Run(ctx, p, verify.Options{Given: given})
	in := map[string]bool{}
	for i := range p.Targets {
		t := &p.Targets[i]
		for _, b := range t.Builds {
			if f := findingFor(report, "key.unchecked", t, b); f != nil && f.File != "" {
				in[b.ID] = true
			}
		}
	}
	return in
}

// writeGivenKeys writes, over the placeholders an agent left, the keys given
// with --sdk-key or the environment, each only once Tenjin accepts it for
// that build's own app. A key given without a build serves a platform with
// one build only. Offline nothing can be checked, so nothing is written.
func (s *setup) writeGivenKeys(p *project.Project) error {
	builds, _ := placeholderBuilds(p, keyfill.Find(p.Root))
	keys := map[string]string{}
	for _, pb := range builds {
		b := pb.build
		given, ok := s.given.ByBuild[b.ID]
		if !ok || given.Value == "" {
			given = s.given.All
			if given.Value == "" {
				continue
			}
			if pb.target != nil && len(pb.target.Builds) > 1 {
				s.say("  %s %s: the key from %s was not written: it is not given for this build, and %s has %d. Run tenjin setup, or tenjin project set-keys, to write this build's own key.",
					s.style.Yellow("!"), pb.title, given.Source, platformNames[pb.target.Platform], len(pb.target.Builds))
				continue
			}
		}
		label := pb.title + ", key " + maskKey(given.Value) + " from " + given.Source
		if s.offline {
			s.say("  %s %s: not written: with --offline it cannot be checked with Tenjin first. Run tenjin project set-keys to check and write it.", s.style.Yellow("!"), label)
			continue
		}
		verdict := checkGivenKey(s.cmd.Context(), s.keyCheck(), given.Value, b, false)
		if verdict.ok && !verdict.checked {
			// Kept by setup, which checks again later; doctor writes only
			// what Tenjin has accepted.
			verdict = keyVerdict{message: strings.TrimSuffix(verdict.message, checksAgain) + ", so it was not written"}
		}
		s.asker().verdict(label, verdict)
		if verdict.checked {
			keys[b.SDKKeyPlaceholder] = given.Value
		}
	}
	filled, err := keyfill.Fill(p.Root, keys)
	for _, spot := range filled {
		s.say("  %s Wrote %s at %s:%d", s.style.Green("✓"), keyNoun(p, spot), render.Clean(spot.File), spot.Line)
	}
	if err != nil {
		return fmt.Errorf("could not write the SDK keys: %w", err)
	}
	return nil
}

// askToFix asks whether an installed agent should fix the findings, and
// which one when there are several. No is the default.
func (s *setup) askToFix(installed []agent.Agent) (*agent.Agent, error) {
	if len(installed) == 1 {
		a := &installed[0]
		s.say("%s is installed and can make the changes:", a.Name)
		s.say("  - it works in this project only, under your own %s account;", a.Name)
		s.say("  - %s;", a.Limits())
		s.say("  - it is never given an SDK key, and doctor checks its work afterwards.")
		ok, err := s.env.confirm(s.out, fmt.Sprintf("Fix them with %s?", a.Name), false)
		if err != nil || !ok {
			return nil, err
		}
		return a, nil
	}
	s.say("These coding agents are installed and can make the changes, in this project only and under your own account:")
	options := make([]string, len(installed))
	for i, a := range installed {
		s.say("  %d) %s: %s", i+1, s.style.Bold(a.Name), a.Limits())
		options[i] = fmt.Sprintf("%s (%d)", a.Name, i+1)
	}
	answer, err := s.env.ask(s.out, fmt.Sprintf("Fix them with %s, or neither? [n]", strings.Join(options, ", ")))
	if err != nil {
		return nil, err
	}
	answer = strings.ToLower(answer)
	for i := range installed {
		if answer == fmt.Sprint(i+1) || answer == installed[i].ID || answer == strings.ToLower(installed[i].Name) {
			return &installed[i], nil
		}
	}
	return nil, nil
}

// readDevice gets the device log from a device, a file or standard input. A
// device that cannot be read is reported in the findings, not as a failure
// of the command; a file that cannot be read is the user's mistake.
func readDevice(cmd *cobra.Command, env *environment, p *project.Project, serial, logs string) (*verify.DeviceInput, error) {
	var raw []byte
	var platform project.Platform
	switch {
	case logs == "-":
		b, err := io.ReadAll(env.stdin)
		if err != nil {
			return nil, err
		}
		raw = b
	case logs != "":
		b, err := os.ReadFile(logs)
		if err != nil {
			return nil, clierr.Wrap(clierr.NotFound, err, "cannot read %s: %v", logs, plainOSError(err))
		}
		raw = b
	default:
		var problems []*devicelog.Unavailable
		var tried []project.Platform
		for _, candidate := range devicePlatforms(p, serial) {
			b, _, err := readDeviceLog(cmd.Context(), env, p, candidate, serial)
			var unavailable *devicelog.Unavailable
			if errors.As(err, &unavailable) {
				problems, tried = append(problems, unavailable), append(tried, candidate)
				continue
			}
			if err != nil {
				return nil, err
			}
			raw, platform = b, candidate
			break
		}
		if raw == nil {
			return &verify.DeviceInput{Problem: combineProblems(tried, problems), Platform: firstPlatform(tried, p)}, nil
		}
	}
	log, err := devicelog.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if platform == "" {
		platform = project.Platform(log.Platform)
		if platform == "" {
			platform = firstPlatform(nil, p)
		}
	}
	return &verify.DeviceInput{Log: log, Platform: platform}, nil
}

// devicePlatforms are the platforms whose devices to try, in order: the
// project's own, Android first. A serial in the form of a UDID can only be a
// simulator's, and any other only an Android device's.
func devicePlatforms(p *project.Project, serial string) []project.Platform {
	var out []project.Platform
	for _, platform := range []project.Platform{project.Android, project.IOS} {
		if p.Target(platform) == nil {
			continue
		}
		if serial != "" && (platform == project.IOS) != looksLikeUDID(serial) {
			continue
		}
		out = append(out, platform)
	}
	return out
}

// A simulator's UDID is a UUID; an iPhone's is 8 and 16 hex digits, or 40
// on older models.
var udidPattern = regexp.MustCompile(`^(?:[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}|[0-9A-Fa-f]{8}-[0-9A-Fa-f]{16}|[0-9A-Fa-f]{40})$`)

func looksLikeUDID(s string) bool { return udidPattern.MatchString(s) }

// Where a device log came from.
const (
	fromAndroid   = "android"
	fromIPhone    = "iphone"
	fromSimulator = "simulator"
)

// serialUsage describes --serial, which doctor and setup share.
const serialUsage = "which device to read, when several are connected: an Android serial (see 'adb devices'), or an iPhone's or a simulator's UDID"

// readDeviceLog reads the SDK's lines from a device of one platform, and
// says what kind of device it was. On iOS a connected iPhone is preferred to
// a simulator: tenjin launches the app on it to read its console. The error
// is a *devicelog.Unavailable when there is nothing to read from.
func readDeviceLog(ctx context.Context, env *environment, p *project.Project, platform project.Platform, serial string) ([]byte, string, error) {
	return readLaunch(ctx, env, p, platform, serial, nil)
}

// launchMark is where a device's log stood before a launch: the time of the
// newest line in an Android device's, on the device's clock, and this Mac's
// time for a simulator's, which logs on this Mac's clock.
type launchMark struct {
	android string
	since   time.Time
}

// markLaunch notes where the log stands now, so that a read after a launch
// holds that launch only. An iPhone needs no mark: each read launches the
// app afresh.
func markLaunch(ctx context.Context, env *environment, platform project.Platform, serial string) *launchMark {
	m := &launchMark{since: time.Now()}
	if platform == project.Android {
		if adb, ok := devicelog.FindADB(env.lookPath, env.getenv); ok {
			m.android = devicelog.AndroidMark(ctx, env.runner(), adb, serial)
		}
	}
	return m
}

// readLaunch is readDeviceLog for what came after mark; nil reads it all.
func readLaunch(ctx context.Context, env *environment, p *project.Project, platform project.Platform, serial string, mark *launchMark) ([]byte, string, error) {
	since := time.Time{}
	if mark != nil {
		// A second's margin for the log's rounding.
		since = mark.since.Add(-time.Second)
	}
	if platform == project.IOS {
		xcrun, err := env.lookPath("xcrun")
		if err != nil {
			return nil, "", &devicelog.Unavailable{
				Reason: "Xcode was not found, so no iPhone or iOS simulator could be read",
				Fix:    "Install Xcode, or save the log yourself and pass it with --logs.",
			}
		}
		phones, listErr := devicelog.ConnectedIPhones(ctx, env.runner(), xcrun)
		var matching []devicelog.IPhone
		for _, phone := range phones {
			if serial == "" || strings.EqualFold(phone.UDID, serial) {
				matching = append(matching, phone)
			}
		}
		switch {
		case len(matching) > 1:
			var names []string
			for _, phone := range matching {
				names = append(names, phone.Name+" ("+phone.UDID+")")
			}
			return nil, "", &devicelog.Unavailable{
				Reason: "More than one iPhone is connected: " + strings.Join(names, ", "),
				Fix:    "Pass --serial with the UDID of the one to read.",
			}
		case len(matching) == 1:
			bundle := ""
			if t := p.Target(project.IOS); t != nil {
				bundle = t.BundleID
			}
			raw, err := devicelog.ReadIPhone(ctx, env.streamer(), xcrun, matching[0], bundle)
			return raw, fromIPhone, err
		}
		raw, err := devicelog.ReadIOSSimulatorSince(ctx, env.runner(), xcrun, serial, since)
		// With no simulator either, a devicectl that failed is the more
		// likely reason nothing was found.
		var unavailable *devicelog.Unavailable
		if listErr != nil && errors.As(err, &unavailable) {
			unavailable.Reason += "; and Xcode's devicectl could not list iPhones (" + listErr.Error() + ")"
		}
		return raw, fromSimulator, err
	}
	adb, ok := devicelog.FindADB(env.lookPath, env.getenv)
	if !ok {
		return nil, "", &devicelog.Unavailable{
			Reason: "adb was not found, so the device log could not be read",
			Fix:    "Install the Android platform tools, or save the log yourself and pass it with --logs.",
		}
	}
	raw, err := devicelog.ReadAndroid(ctx, env.runner(), adb, serial)
	if err == nil && mark != nil {
		raw = devicelog.Since(raw, mark.android)
	}
	return raw, fromAndroid, err
}

// combineProblems says why no device could be read, for every platform tried.
func combineProblems(tried []project.Platform, problems []*devicelog.Unavailable) *devicelog.Unavailable {
	switch len(problems) {
	case 0:
		return &devicelog.Unavailable{Reason: "This project builds for no platform whose device can be read"}
	case 1:
		return problems[0]
	}
	var reasons, fixes []string
	for i, u := range problems {
		reasons = append(reasons, u.Reason)
		if u.Fix != "" {
			fixes = append(fixes, platformNames[tried[i]]+": "+u.Fix)
		}
	}
	return &devicelog.Unavailable{Reason: strings.Join(reasons, "; "), Fix: strings.Join(fixes, " ")}
}

// firstPlatform is the platform device findings are reported on when no
// device was read: the first one tried, or the project's first.
func firstPlatform(tried []project.Platform, p *project.Project) project.Platform {
	if len(tried) > 0 {
		return tried[0]
	}
	if len(p.Targets) > 0 {
		return p.Targets[0].Platform
	}
	return project.Android
}

// reportStatus turns a summary into the command's exit status.
func reportStatus(s verify.Summary, strict bool) error {
	switch {
	case s.Errors > 0:
		return clierr.New(clierr.ChecksFailed, "%s will stop events reaching Tenjin", plural(s.Errors, "problem"))
	case strict && s.Warnings > 0:
		return clierr.New(clierr.ChecksFailed, "%s (--strict)", plural(s.Warnings, "warning"))
	}
	return nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func writeReport(w io.Writer, report *verify.Report, offline bool) error {
	report = forPeople(report)
	style, width := render.NewStyler(w), render.TextWidth(w)
	var b strings.Builder
	p := report.Project
	kind := frameworkNames[p.Framework]
	if p.Library != nil {
		kind = project.LibraryKindName(p.Library.Kind)
	}
	fmt.Fprintf(&b, "%s %s (%s)\n", style.Bold("Checking"), p.Root, kind)

	// What the device log says is its own section: it is about the build
	// that ran, which is not always the project on disk.
	sections := []struct {
		platform project.Platform
		device   bool
		title    string
	}{
		{"", false, "Shared code"},
		{project.Android, false, "Android"},
		{project.IOS, false, "iOS"},
		{project.Android, true, "On the device"},
		{project.IOS, true, "On the device"},
		{"", true, "On the device"},
	}
	for _, section := range sections {
		var findings []verify.Finding
		for _, f := range report.Findings {
			if f.Platform == section.platform && strings.HasPrefix(f.ID, "device.") == section.device {
				findings = append(findings, f)
			}
		}
		if len(findings) == 0 {
			continue
		}
		title := section.title
		if p.Library != nil && section.platform == "" && !section.device {
			title = "Library"
		}
		if t := p.Target(section.platform); t != nil && t.BundleID != "" && !section.device {
			title += " · " + t.BundleID
		}
		fmt.Fprintf(&b, "\n%s\n", style.Heading(title))
		// What needs doing comes first, the worst of it at the top, and what
		// passed after it. A problem's explanation and fix make a block of
		// their own, set apart by a blank line.
		sort.SliceStable(findings, func(i, j int) bool {
			return severityOrder[findings[i].Severity] < severityOrder[findings[j].Severity]
		})
		for i, f := range findings {
			if i > 0 && (isBlock(findings[i-1]) || isBlock(f)) {
				b.WriteString("\n")
			}
			writeFinding(&b, style, f, width)
		}
	}

	s := report.Summary
	// Each count takes its severity's colour once there is something to count.
	tint := func(n int, text string, colour func(string) string) string {
		if n == 0 {
			return text
		}
		return colour(text)
	}
	fmt.Fprintf(&b, "\n%s, %s, %s\n",
		tint(s.Errors, plural(s.Errors, "error"), style.Red),
		tint(s.Warnings, plural(s.Warnings, "warning"), style.Yellow),
		tint(s.Passed, fmt.Sprintf("%d passed", s.Passed), style.Green))
	if offline {
		b.WriteString("The SDK key was not checked against Tenjin (--offline).\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// severityOrder is the order findings are listed in.
var severityOrder = map[verify.Severity]int{verify.Error: 0, verify.Warning: 1, verify.Info: 2, verify.Pass: 3}

// isBlock reports whether a finding prints more than its one line.
func isBlock(f verify.Finding) bool {
	return f.Severity != verify.Pass && (f.Detail != "" || f.Fix != "")
}

// writeFinding prints one finding. width is the terminal's, or 0 to leave
// long lines whole, as for a pipe.
func writeFinding(b *strings.Builder, style render.Styler, f verify.Finding, width int) {
	mark := map[verify.Severity]string{
		verify.Pass:    style.Green("✓"),
		verify.Error:   style.Red("✗"),
		verify.Warning: style.Yellow("!"),
		verify.Info:    style.Dim("i"),
	}[f.Severity]

	line := "  " + mark + " " + f.Title
	if f.File != "" {
		where := f.File
		if f.Line > 0 {
			where = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		line += "  " + style.Dim(where)
	}
	b.WriteString(line + "\n")
	if f.Severity == verify.Pass {
		return
	}
	if f.Detail != "" {
		b.WriteString(render.Indent(f.Detail, 6, width) + "\n")
	}
	if f.Fix != "" {
		fix := render.Indent("Fix: "+f.Fix, 6, width)
		b.WriteString(strings.Replace(fix, "Fix:", style.Bold("Fix:"), 1) + "\n")
	}
}
