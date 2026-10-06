package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/render"
)

func newProjectCmd(opts *options, env *environment) *cobra.Command {
	return group("project", "Inspect the mobile project in a directory, and write its SDK keys", newProjectDetectCmd(opts), newProjectSetKeysCmd(opts, env))
}

func newProjectDetectCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "detect [path]",
		Short: "Report the framework, platforms, bundle IDs and any existing Tenjin SDK",
		Long: `Report what kind of mobile project a directory holds: its framework, the
platforms it builds for, each platform's bundle ID, and whether the Tenjin SDK
is already a dependency.

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
		return nil, clierr.Wrap(clierr.NotFound, err, "cannot read %s: %v", dir, err)
	}
	if p.Framework == project.Unknown {
		e := clierr.New(clierr.NotFound, "no mobile project found in %s", p.Root)
		if len(p.Notes) > 0 {
			return nil, e.WithHint("%s", strings.Join(p.Notes, "\n"))
		}
		return nil, e.WithHint("Run this in the root of an Android, iOS, Flutter, React Native, Capacitor, Unity or Unreal project, or pass its path.")
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
	project.Cordova:             "Cordova (not supported by the Tenjin SDK)",
	project.UnityExport:         "Unity (exported build)",
	project.KotlinMultiplatform: "Kotlin Multiplatform (no Tenjin SDK)",
	project.DotNet:              ".NET MAUI or Xamarin (no Tenjin SDK)",
}

func writeProject(w io.Writer, p *project.Project) error {
	style := render.NewStyler(w)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", style.Bold("Project   "), p.Root)
	fmt.Fprintf(&b, "%s %s\n", style.Bold("Framework "), style.Accent(frameworkNames[p.Framework]))

	if len(p.Tenjin) == 0 {
		fmt.Fprintf(&b, "%s %s\n", style.Bold("Tenjin SDK"), style.Yellow("not found"))
	}
	for _, i := range p.Tenjin {
		fmt.Fprintf(&b, "%s %s %s\n", style.Bold("Tenjin SDK"), style.Green(strings.TrimSpace(i.Package+" "+i.Version)), style.Dim("("+i.Source+")"))
	}

	if len(p.Targets) > 0 {
		b.WriteString("\n")
		rows := make([][]string, 0, len(p.Targets))
		for _, t := range p.Targets {
			id := t.BundleID
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
