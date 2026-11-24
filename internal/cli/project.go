package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/render"
)

func newProjectCmd(opts *options, env *environment) *cobra.Command {
	return group("project", "Detect the mobile project in a directory, and write its SDK keys", newProjectDetectCmd(opts, env), newProjectSetKeysCmd(opts, env))
}

func newProjectDetectCmd(opts *options, env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "detect [path]",
		Short: "Report the framework, platforms, bundle IDs and any Tenjin SDK",
		Long: `Report what kind of mobile project a directory holds: its framework, the
platforms it builds for, each platform's bundle ID, and whether the Tenjin SDK
is already a dependency. It also lists what the app already sends to other
analytics SDKs (Firebase, Amplitude, Mixpanel, AppsFlyer, Adjust,
GameAnalytics, Facebook, Flurry): the events, by name where the code shows
one, and the value it gives as the user's ID, each with its file and line.

It only reads files. It works offline and without signing in.`,
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
			env.noteProject(p)
			p.ReadAnalytics()
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), p)
			}
			return writeProject(cmd.OutOrStdout(), p)
		},
	}
}

// loadProject detects the project at the optional path argument, defaulting
// to the working directory. Finding nothing is an error: every caller needs a
// project to work on.
func loadProject(args []string) (*project.Project, error) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	p, err := project.Detect(dir)
	if err != nil {
		return nil, clierr.Wrap(clierr.NotFound, err, "cannot read %s: %v", dir, plainOSError(err))
	}
	if p.Framework == project.Unknown && p.Library == nil {
		e := clierr.New(clierr.NotFound, "no mobile project found in %s", p.Root)
		if len(p.Notes) > 0 {
			return nil, e.WithHint("%s", strings.Join(p.Notes, "\n"))
		}
		return nil, e.WithHint("Run this in the root of an Android, iOS, Flutter, React Native, Capacitor, Unity, Unreal or Godot project, or pass its path.")
	}
	return p, nil
}

var frameworkNames = map[project.Framework]string{
	project.Native:              "Native",
	project.Flutter:             "Flutter",
	project.ReactNative:         "React Native",
	project.Capacitor:           "Capacitor",
	project.Unity:               "Unity",
	project.Unreal:              "Unreal Engine",
	project.Godot:               "Godot",
	project.Cordova:             "Cordova (not supported by the Tenjin SDK)",
	project.UnityExport:         "Unity (exported build)",
	project.KotlinMultiplatform: "Kotlin Multiplatform (no Tenjin SDK)",
	project.DotNet:              ".NET MAUI or Xamarin (no Tenjin SDK)",
}

func writeProject(w io.Writer, p *project.Project) error {
	p = forPeople(p)
	style := render.NewStyler(w)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", style.Bold("Project    "), p.Root)
	// A library is not an app: what an app would show here is the example
	// app's, which is a project of its own.
	if lib := p.Library; lib != nil {
		name := project.LibraryKindName(lib.Kind)
		if lib.Name != "" {
			name = lib.Name + ", a " + name
		}
		fmt.Fprintf(&b, "%s %s that wraps %s %s\n", style.Bold("Library    "), name, strings.TrimSpace(lib.Tenjin.Package+" "+lib.Tenjin.Version), style.Dim("("+lib.Source+")"))
		if lib.Example != "" {
			fmt.Fprintf(&b, "%s %s %s\n", style.Bold("Example    "), lib.Example, style.Dim("(an app of its own: pass its path to see it)"))
		}
		_, err := io.WriteString(w, b.String())
		return err
	}
	fmt.Fprintf(&b, "%s %s\n", style.Bold("Framework  "), style.Accent(frameworkNames[p.Framework]))

	if len(p.Tenjin) == 0 {
		fmt.Fprintf(&b, "%s %s\n", style.Bold("Tenjin SDK "), style.Yellow("not found"))
	}
	for _, i := range p.Tenjin {
		fmt.Fprintf(&b, "%s %s %s\n", style.Bold("Tenjin SDK "), style.Green(strings.TrimSpace(i.Package+" "+i.Version)), style.Dim("("+i.Source+")"))
	}

	if len(p.Targets) > 0 {
		b.WriteString("\n")
		rows := make([][]string, 0, len(p.Targets))
		for _, t := range p.Targets {
			id := t.AppBundleID()
			if id == "" {
				id = "(not found)"
			}
			rows = append(rows, []string{string(t.Platform), id, t.Source})
		}
		if err := render.WriteTable(&b, style, []string{"platform", "bundle id", "read from"}, rows); err != nil {
			return err
		}
	}

	// Several builds of one platform are several apps in Tenjin; say which.
	if len(p.Builds()) > len(p.Targets) {
		b.WriteString("\n" + style.Heading("Apps in Tenjin") + "\n")
		var rows [][]string
		for _, build := range p.Builds() {
			rows = append(rows, []string{build.ID, build.BundleID, project.StoreName(build.TenjinPlatform), strings.Join(build.Variants, ", "), build.SDKKeyPlaceholder})
		}
		if err := render.WriteTable(&b, style, []string{"build", "bundle id", "store", "variants", "key placeholder"}, rows); err != nil {
			return err
		}
	}

	// The features beyond app opens the project suggests, and why.
	var suggestions []string
	for _, f := range p.Features {
		if f.Suggested && !f.Required {
			var why []string
			for _, e := range f.Evidence {
				why = append(why, e.What+" in "+e.File)
			}
			suggestions = append(suggestions, fmt.Sprintf("  %s %s", style.Bold(fmt.Sprintf("%-28s", f.Name)), style.Dim(strings.Join(why, "; "))))
		}
	}
	if len(suggestions) > 0 {
		b.WriteString("\n" + style.Heading("Suggested features") + "\n" + strings.Join(suggestions, "\n") + "\n")
	}

	// What custom events and a customer user ID could be taken from.
	if lines := analyticsSummary(p); len(lines) > 0 {
		b.WriteString("\n" + style.Heading("Analytics in the app") + "\n")
		width := 0
		for _, line := range lines {
			width = max(width, len(line[0]))
		}
		for _, line := range lines {
			fmt.Fprintf(&b, "  %s  %s\n", style.Bold(fmt.Sprintf("%-*s", width, line[0])), line[1])
		}
	}

	if lines := buildShape(p); len(lines) > 0 {
		b.WriteString("\n" + style.Heading("Build") + "\n")
		for _, line := range lines {
			fmt.Fprintf(&b, "  %s %s\n", style.Dim(fmt.Sprintf("%-16s", line[0])), line[1])
		}
	}

	if len(p.Notes) > 0 {
		b.WriteString("\n" + style.Heading("Notes") + "\n")
		for _, n := range p.Notes {
			b.WriteString("  " + style.Yellow("-") + " " + strings.TrimLeft(render.Indent(n, 4, render.TextWidth(w)), " ") + "\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// buildShape describes, as label and value, the parts of the project that
// decide where the SDK goes and how.
func buildShape(p *project.Project) [][2]string {
	var lines [][2]string
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, [2]string{label, value})
		}
	}
	for _, t := range p.Targets {
		if a := t.Android; a != nil {
			gradle := map[string]string{"kotlin": "Kotlin DSL", "groovy": "Groovy DSL"}[a.DSL]
			if a.VersionCatalog != "" {
				gradle += ", version catalog"
			}
			if a.Minify {
				gradle += ", code shrinking on"
			}
			add("Android build", fmt.Sprintf("%s (%s)", gradle, a.BuildFile))
			add("Launcher", withFile(a.LauncherActivity, a.LauncherSource))
		}
		if i := t.IOS; i != nil {
			var deps []string
			if i.Podfile != "" {
				deps = append(deps, "CocoaPods")
			}
			if i.SwiftPackages {
				deps = append(deps, "Swift packages")
			}
			add("iOS packages", strings.Join(deps, ", "))
			add("iOS entry point", withFile(map[string]string{"swiftui": "SwiftUI App", "app-delegate": "App delegate"}[i.Lifecycle], i.EntryPoint))
			if i.GeneratedInfoPlist && i.InfoPlist == "" {
				add("Info.plist", "generated by Xcode from build settings")
			}
		}
	}
	add("Package manager", p.PackageManager)
	if prompt := p.TrackingPrompt; prompt != nil {
		where := prompt.Package
		if prompt.File != "" {
			where = fmt.Sprintf("%s:%d", prompt.File, prompt.Line)
		}
		add("Tracking prompt", where)
	} else if p.Target(project.IOS) != nil && p.Supported && p.Framework != project.Unreal {
		// Unreal source is not searched, so nothing can be said about it.
		add("Tracking prompt", "none found")
	}
	var ads []string
	for _, sdk := range p.AdSDKs {
		ads = append(ads, withFile(sdk.Name, sdk.Source))
	}
	add("Ad SDKs", strings.Join(ads, ", "))
	return lines
}

func withFile(what, file string) string {
	if what == "" || file == "" {
		return what
	}
	return what + " (" + file + ")"
}

// forPeople returns a copy of a result with the control characters taken out
// of every string, for writing to a terminal: names, paths and values in it
// come from the project's files, which could carry escape sequences. JSON
// output escapes them itself.
func forPeople[T any](v *T) *T {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var c T
	if err := json.Unmarshal(raw, &c); err != nil {
		return v
	}
	render.CleanAll(&c)
	return &c
}

// plainOSError is a file error without the operation and path Go puts in
// front ("stat demo/x: no such file or directory"), which the message
// already says.
func plainOSError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}
