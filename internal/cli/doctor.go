package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/track"
	"github.com/tenjin/tenjin-cli/internal/devicelog"
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
	)
	cmd := &cobra.Command{
		Use:   "doctor [path]",
		Short: "Check a Tenjin SDK integration and explain what is wrong",
		Long: `Check the Tenjin SDK integration in a project: that the SDK is a dependency,
that it is initialised and connected, that the manifest and Info.plist carry
what the SDK needs, and that Tenjin accepts the SDK key for each platform's
bundle ID.

The key check sends one request to Tenjin and no event. Use
--offline to skip it. doctor never changes the project.

With --device, doctor also reads what the app's last launch sent and what
Tenjin answered to each request, then asks Tenjin whether the device's
install was processed. It reads a connected Android device or emulator, an
iPhone connected by cable, or a running iOS simulator. On an iPhone, doctor
launches the installed app itself to read its console, and leaves it
running. A project that builds for both platforms is read from an Android
device when one is connected, and from iOS otherwise. On iOS the SDK logs its
requests only when the build calls TenjinSDK.debugLogs(); installs from a
simulator have not been seen to be processed, so an iPhone is the check
that confirms one.

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
				err = render.WriteJSON(cmd.OutOrStdout(), report)
			} else {
				err = writeReport(cmd.OutOrStdout(), report, offline)
			}
			if err != nil {
				return err
			}
			return reportStatus(report.Summary, strict)
		},
	}
	addSDKKeyFlag(cmd, &sdkKeys)
	cmd.Flags().BoolVar(&offline, "offline", false, "skip the SDK key check against Tenjin")
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero on warnings as well as errors")
	cmd.Flags().BoolVar(&device, "device", false, "also read the log of a connected Android device or emulator, or of a running iOS simulator")
	cmd.Flags().StringVar(&serial, "serial", "", "which device to read, when several are connected: an Android serial (see 'adb devices') or a simulator's UDID")
	cmd.Flags().StringVar(&logs, "logs", "", "also read a saved device log (logcat or iOS simulator), or - for standard input")
	addBuildFlag(cmd, &builds)
	addFeaturesFlag(cmd, &features)
	return cmd
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
			return nil, clierr.Wrap(clierr.NotFound, err, "cannot read %s: %v", logs, err)
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

// readDeviceLog reads the SDK's lines from a device of one platform, and
// says what kind of device it was. On iOS a connected iPhone is preferred to
// a simulator: tenjin launches the app on it to read its console. The error
// is a *devicelog.Unavailable when there is nothing to read from.
func readDeviceLog(ctx context.Context, env *environment, p *project.Project, platform project.Platform, serial string) ([]byte, string, error) {
	if platform == project.IOS {
		xcrun, err := env.lookPath("xcrun")
		if err != nil {
			return nil, "", &devicelog.Unavailable{
				Reason: "Xcode was not found, so no iPhone or iOS simulator could be read",
				Fix:    "Install Xcode, or save the log yourself and pass it with --logs.",
			}
		}
		phones, _ := devicelog.ConnectedIPhones(ctx, env.runner(), xcrun)
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
		raw, err := devicelog.ReadIOSSimulator(ctx, env.runner(), xcrun, serial)
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
	style, width := render.NewStyler(w), render.TextWidth(w)
	var b strings.Builder
	p := report.Project
	fmt.Fprintf(&b, "%s %s (%s)\n", style.Bold("Checking"), p.Root, frameworkNames[p.Framework])

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
		if t := p.Target(section.platform); t != nil && t.BundleID != "" && !section.device {
			title += " · " + t.BundleID
		}
		fmt.Fprintf(&b, "\n%s\n", style.Heading(title))
		for _, f := range findings {
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
