package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/buildinfo"
	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/guides"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

func newSDKCmd(opts *options, env *environment) *cobra.Command {
	return group("sdk", "Print the SDK integration guide, or plan an SDK upgrade", newSDKGuideCmd(opts, env), newSDKUpgradeCmd(opts, env))
}

// sdkGuide is what `sdk guide --json` reports.
type sdkGuide struct {
	Name string `json:"name"`
	// Package and SDKVersion are the SDK the guide integrates, at the
	// release this CLI's checks expect.
	Package    string `json:"package"`
	SDKVersion string `json:"sdk_version,omitempty"`
	// Revision is the sdk-llm-guides commit the guide was taken from.
	Revision string `json:"revision"`
	Source   string `json:"source"`
	Content  string `json:"content"`
}

func newSDKGuideCmd(opts *options, env *environment) *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "guide [" + strings.Join(guides.Names(), "|") + "]",
		Short: "Print the SDK integration guide for a platform",
		Long: `Print the Tenjin SDK integration guide for a platform, from
github.com/tenjin/sdk-llm-guides at the revision this version of the Tenjin
CLI was tested with. It is part of the binary, so it works offline and
matches what 'tenjin doctor' checks.

With no platform named, the guide for the project in --path (the current
folder by default) is printed. A folder given in place of a platform is
taken as that project.`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				if _, known := guides.For(args[0]); !known {
					if info, err := os.Stat(args[0]); err == nil && info.IsDir() {
						path, args = args[0], nil
					}
				}
			}
			if len(args) == 1 {
				name = args[0]
			} else {
				p, err := loadProject([]string{path})
				if err != nil {
					return err
				}
				if name, err = guideFor(p); err != nil {
					return err
				}
			}
			g, ok := guides.For(name)
			if !ok {
				return clierr.New(clierr.Usage, "there is no guide for %q", name).
					WithHint("Guides: %s.", strings.Join(guides.Names(), ", "))
			}
			result := sdkGuide{
				Name: g.Name, Package: g.Package, SDKVersion: verify.LatestVersion(g.Package, env.latestSDKs(cmd.Context())),
				Revision: guides.Revision(), Source: "https://github.com/tenjin/sdk-llm-guides/blob/" + guides.Revision() + "/guides",
				Content: g.Content,
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), result)
			}
			_, err = io.WriteString(cmd.OutOrStdout(), guideHeader(result)+result.Content)
			return err
		},
	}
	cmd.Flags().StringVar(&path, "path", ".", "the project whose guide to print, when no platform is named")
	return cmd
}

// guideFor picks the guide for a project.
func guideFor(p *project.Project) (string, error) {
	if name := guides.ForProject(p); name != "" {
		return name, nil
	}
	if p.Framework == project.Native {
		return "", clierr.New(clierr.Usage, "this project builds for both Android and iOS, each with its own SDK").
			WithHint("Name one: tenjin sdk guide android, or tenjin sdk guide ios.")
	}
	return "", clierr.New(clierr.NotFound, "there is no integration guide for %s projects", frameworkNames[p.Framework]).
		WithHint("Guides: %s.", strings.Join(guides.Names(), ", "))
}

// guideHeader says where the guide comes from, and corrects the two things
// it would otherwise have an agent do that do not apply here: look up the
// SDK version over the network, and ask for the SDK key.
func guideHeader(g sdkGuide) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- tenjin sdk guide %s: tenjin/sdk-llm-guides at %s, as tested with Tenjin CLI %s. -->\n\n", g.Name, g.Revision, buildinfo.Get().Version)
	b.WriteString("> **From the Tenjin CLI:**\n")
	if g.SDKVersion != "" {
		fmt.Fprintf(&b, "> - Where this guide says `<TENJIN_SDK_VERSION>`, use **%s**, the release `tenjin doctor` checks against. There is no need to look it up.\n", g.SDKVersion)
	}
	b.WriteString("> - Do not ask for the SDK key or write one. Where the key belongs, write the build's placeholder (`tenjin project detect` lists each build's `sdk_key_placeholder`), and leave it there: the developer has the Tenjin CLI write the key afterwards, with `tenjin setup`. A coding agent does not run that, or `tenjin project set-keys`, itself.\n")
	b.WriteString("> - Check the result with `tenjin doctor`.\n\n")
	return b.String()
}
