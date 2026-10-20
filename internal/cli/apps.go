package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi"
)

// platforms are the Tenjin platform IDs the CLI creates apps for. Amazon and
// other Android stores are separate platforms in Tenjin.
var platforms = []string{"ios", "android", "amazon", "android_other"}

func newAppsCmd(opts *options, env *environment) *cobra.Command {
	sub := appDataCmds(opts, env)
	writes := appWriteCmds(opts, env)
	for _, c := range sub {
		c.AddCommand(writes[c.Name()]...)
		switch c.Name() {
		case "s2s-callbacks":
			c.AddCommand(newMacrosCmd(opts))
		case "callback-groups":
			c.AddCommand(newInspectIntegrationCmd(opts, env))
		}
	}
	return group("apps", "Your apps, and their keys, events and callbacks",
		append([]*cobra.Command{
			newAppsListCmd(opts, env),
			newAppsGetCmd(opts, env),
			newAppsCreateCmd(opts, env),
			newAppsEnsureCmd(opts, env),
			newAppsUpdateCmd(opts, env),
			newAppsDeleteCmd(opts, env),
			group("keys", "An app's SDK keys", newAppsKeysListCmd(opts, env)),
		}, sub...)...,
	)
}

func newAppsListCmd(opts *options, env *environment) *cobra.Command {
	var query, platform, bundleID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the apps in your organization",
		Long: `List the apps in your organization: all of them, not the first page.

--query is matched against app names by Tenjin. --platform and --bundle-id
narrow the result further; a bundle ID is matched exactly.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			all, err := env.api().ListApps(cmd.Context(), query)
			if err != nil {
				return err
			}
			apps := []tenjinapi.App{}
			for _, a := range all {
				if (platform == "" || a.Platform == platform) && (bundleID == "" || a.BundleID == bundleID) {
					apps = append(apps, a)
				}
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), apps)
			}
			return writeApps(cmd.OutOrStdout(), apps)
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "only apps whose name matches")
	cmd.Flags().StringVar(&platform, "platform", "", "only apps on this platform (ios, android, amazon, android_other)")
	cmd.Flags().StringVar(&bundleID, "bundle-id", "", "only apps with exactly this bundle ID")
	return cmd
}

// appDetail is what `apps get` and `apps create` report: the app, and where
// its SDK keys are, since the API does not serve them.
type appDetail struct {
	tenjinapi.App
	DashboardURL string `json:"dashboard_url"`
}

func writeAppDetail(w io.Writer, a appDetail) error {
	events := "none received yet"
	if a.SendingEvents {
		events = "being received"
	}
	_, err := fmt.Fprintf(w, "Name       %s\nPlatform   %s\nBundle ID  %s\nApp ID     %s\nEvents     %s\nSDK keys   %s\n",
		oneLine(a.Name), oneLine(a.Platform), oneLine(a.BundleID), oneLine(a.ID), events, oneLine(a.DashboardURL))
	return err
}

// oneLine is a value from the API as it reads on one line of a terminal.
// Anyone in the organization can name an app, and a name could otherwise
// carry escape sequences, or a line break that passes for another field.
func oneLine(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ").Replace(render.Clean(s))
}

func newAppsGetCmd(opts *options, env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "get <app-id>",
		Short: "Show one app, including whether Tenjin is receiving its events",
		Args:  exactArgs(1, "the app's ID, from 'tenjin apps list'"),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			app, err := env.api().GetApp(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			detail := appDetail{App: *app, DashboardURL: env.endpoints.SDKKeysURL(app.ID)}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), detail)
			}
			return writeAppDetail(cmd.OutOrStdout(), detail)
		},
	}
}

func newAppsCreateCmd(opts *options, env *environment) *cobra.Command {
	var (
		in  tenjinapi.NewApp
		yes bool
	)
	cmd := &cobra.Command{
		Use:   "create (--name <name> --bundle-id <id> --platform <platform> | --store-url <url>)",
		Short: "Create an app",
		Long: `Create a Tenjin app, from its name, bundle ID and platform, or from its App
Store or Google Play URL, in which case Tenjin reads the name and bundle ID
from the store.

A Tenjin app is one bundle ID on one platform: a project that ships on iOS and
Android needs two apps. The bundle ID and platform cannot be changed after
creation, so this asks for confirmation unless --yes is given.

It fails with exit status 10 if the app already exists. Use 'tenjin apps
ensure' to get the app whether or not it exists.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			var what string
			switch {
			case in.StoreURL != "" && (in.Name != "" || in.BundleID != ""):
				return clierr.New(clierr.Usage, "--store-url replaces --name and --bundle-id; give one or the other")
			case in.StoreURL != "":
				what = "an app from " + in.StoreURL
			case in.Name == "" || in.BundleID == "" || in.Platform == "":
				return clierr.New(clierr.Usage, "give --name, --bundle-id and --platform, or --store-url")
			default:
				what = fmt.Sprintf("%q with the bundle ID %s on %s", in.Name, in.BundleID, in.Platform)
			}
			if in.Platform != "" && !contains(platforms, in.Platform) {
				return clierr.New(clierr.Usage, "unknown platform %q (use %s)", in.Platform, strings.Join(platforms, ", "))
			}

			if !yes {
				if !env.interactive {
					return clierr.New(clierr.Usage, "creating an app needs confirmation").WithHint("Pass --yes to create %s.", what)
				}
				ok, err := env.confirm(cmd.ErrOrStderr(), "Create "+what+"? The bundle ID and platform cannot be changed later.", true)
				if err != nil {
					return err
				}
				if !ok {
					return declined("Nothing was created.")
				}
			}
			app, created, err := env.api().CreateApp(cmd.Context(), in)
			if err != nil {
				return err
			}
			if !created {
				return clierr.New(clierr.AlreadyExists, "an app with the bundle ID %s on %s already exists: %s (%s)", app.BundleID, app.Platform, oneLine(app.Name), app.ID).
					WithHint("Use it as it is, or run 'tenjin apps ensure' to get the app whether or not it exists.")
			}
			detail := appDetail{App: *app, DashboardURL: env.endpoints.SDKKeysURL(app.ID)}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), detail)
			}
			return writeAppDetail(cmd.OutOrStdout(), detail)
		},
	}
	cmd.Flags().StringVar(&in.Name, "name", "", "the app's name in Tenjin")
	cmd.Flags().StringVar(&in.BundleID, "bundle-id", "", "the app's bundle ID or Android application ID")
	cmd.Flags().StringVar(&in.Platform, "platform", "", "one of: "+strings.Join(platforms, ", "))
	cmd.Flags().StringVar(&in.StoreURL, "store-url", "", "the app's App Store or Google Play URL")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "create the app without asking")
	return cmd
}

// appKeys is what `apps keys list` reports. Keys is empty where Tenjin's API
// does not serve SDK keys.
type appKeys struct {
	AppID string      `json:"app_id"`
	Keys  []keyListed `json:"keys"`
	// Available is false while the keys can only be read in the dashboard.
	Available    bool   `json:"available"`
	DashboardURL string `json:"dashboard_url"`
	// Instructions are the steps to copy a key from the dashboard, for an
	// agent to pass on to the developer.
	Instructions []string `json:"instructions"`
}

// keyListed is one SDK key, shown by its last four characters only.
type keyListed struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Masked    string `json:"masked"`
	CreatedAt string `json:"created_at,omitempty"`
}

func newAppsKeysListCmd(opts *options, env *environment) *cobra.Command {
	var appID string
	cmd := &cobra.Command{
		Use:   "list --app <app-id>",
		Short: "List an app's SDK keys, and where to copy them from",
		Long: `Lists the app's SDK keys by name, showing only the last four characters of
each, and says how to copy one from the dashboard page that shows them for the
app. Where Tenjin's API does not serve SDK keys, only the dashboard steps are
shown.

To write a key into a project, leave a placeholder where it belongs and run
'tenjin setup' or 'tenjin project set-keys'.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if appID == "" {
				return clierr.New(clierr.Usage, "--app is required").WithHint("Find the app's ID with 'tenjin apps list'.")
			}
			// Asking for the app first turns a mistyped ID into an error
			// here, not a dashboard page that fails to load.
			app, err := env.api().GetApp(cmd.Context(), appID)
			if err != nil {
				return err
			}
			url := env.endpoints.SDKKeysURL(app.ID)
			result := appKeys{AppID: app.ID, Keys: []keyListed{}, DashboardURL: url, Instructions: keyInstructions(url)}
			keys, err := env.api().ListSDKKeys(cmd.Context(), app.ID)
			switch {
			case errors.Is(err, tenjinapi.ErrKeysNotServed):
			case err != nil:
				return err
			default:
				result.Available = true
				for _, k := range keys {
					result.Keys = append(result.Keys, keyListed{ID: k.ID, Name: k.Name, Masked: maskKey(k.Value), CreatedAt: k.CreatedAt})
				}
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), result)
			}
			var b strings.Builder
			if result.Available {
				fmt.Fprintf(&b, "SDK keys of %s (%s, %s):\n", oneLine(app.Name), oneLine(app.Platform), oneLine(app.BundleID))
				for _, k := range result.Keys {
					fmt.Fprintf(&b, "  %s  %s\n", oneLine(k.Masked), oneLine(k.Name))
				}
				if len(result.Keys) == 0 {
					b.WriteString("  none yet\n")
				}
				b.WriteString("To write one into a project, leave a placeholder where it belongs and run tenjin setup. ")
				b.WriteString("To copy one yourself:\n")
			} else {
				fmt.Fprintf(&b, "To get the SDK key of %s (%s, %s):\n", oneLine(app.Name), oneLine(app.Platform), oneLine(app.BundleID))
			}
			for i, step := range result.Instructions {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, step)
			}
			switch app.Platform {
			case "android", "ios":
				fmt.Fprintf(&b, "Then check it: tenjin doctor --sdk-key %s=<key>\n", app.Platform)
			default:
				// A store other than Google Play is a build variant of the
				// Android project.
				b.WriteString("Then check it: tenjin doctor --sdk-key android:<variant>=<key>, where <variant> is the build for this store (tenjin project detect lists the builds)\n")
			}
			_, err = io.WriteString(cmd.OutOrStdout(), b.String())
			return err
		},
	}
	cmd.Flags().StringVar(&appID, "app", "", "the app's ID")
	return cmd
}

func writeApps(w io.Writer, apps []tenjinapi.App) error {
	if len(apps) == 0 {
		_, err := fmt.Fprintln(w, "No apps.")
		return err
	}
	rows := make([][]string, 0, len(apps))
	for _, a := range apps {
		events := "no"
		if a.SendingEvents {
			events = "yes"
		}
		rows = append(rows, []string{oneLine(a.Name), oneLine(a.Platform), oneLine(a.BundleID), events, oneLine(a.ID)})
	}
	return render.WriteTable(w, render.NewStyler(w), []string{"name", "platform", "bundle id", "events", "id"}, rows)
}

// ensureResult is what `apps ensure` reports.
type ensureResult struct {
	tenjinapi.App
	Created bool `json:"created"`
}

func newAppsEnsureCmd(opts *options, env *environment) *cobra.Command {
	var (
		bundleID, platform, name string
		yes                      bool
	)
	cmd := &cobra.Command{
		Use:   "ensure --bundle-id <id> --platform <platform>",
		Short: "Find the app for a bundle ID and platform, creating it if needed",
		Long: `Find the Tenjin app with this bundle ID and platform, and create it if there
is none. Running it again returns the same app.

A Tenjin app is one bundle ID on one platform: a project that ships on iOS and
Android needs two apps. The bundle ID and platform cannot be changed after
creation, so creating asks for confirmation unless --yes is given.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if bundleID == "" || platform == "" {
				return clierr.New(clierr.Usage, "--bundle-id and --platform are required")
			}
			if !contains(platforms, platform) {
				return clierr.New(clierr.Usage, "unknown platform %q (use %s)", platform, strings.Join(platforms, ", "))
			}

			result, err := ensureApp(cmd.Context(), env, cmd.ErrOrStderr(), bundleID, platform, name, yes)
			if err != nil {
				return err
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), result)
			}
			verb := "Found"
			if result.Created {
				verb = "Created"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s, %s)\nApp ID  %s\nSDK key %s\n",
				verb, oneLine(result.Name), oneLine(result.Platform), oneLine(result.BundleID), oneLine(result.ID), oneLine(env.endpoints.SDKKeysURL(result.ID)))
			return err
		},
	}
	cmd.Flags().StringVar(&bundleID, "bundle-id", "", "the app's bundle ID or Android application ID")
	cmd.Flags().StringVar(&platform, "platform", "", "one of: "+strings.Join(platforms, ", "))
	cmd.Flags().StringVar(&name, "name", "", "name for a new app (default: the bundle ID)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "create the app without asking")
	return cmd
}

// ensureApp finds the app or creates it. Creation needs --yes or a yes at
// the prompt; without a terminal and without --yes it is refused, never
// assumed.
func ensureApp(ctx context.Context, env *environment, prompt io.Writer, bundleID, platform, name string, yes bool) (*ensureResult, error) {
	api := env.api()
	app, err := api.FindApp(ctx, bundleID, platform)
	if err != nil {
		return nil, err
	}
	if app != nil {
		return &ensureResult{App: *app}, nil
	}

	if name == "" {
		name = bundleID
	}
	if !yes {
		if !env.interactive {
			return nil, clierr.New(clierr.NotFound, "no Tenjin app has the bundle ID %s on %s", bundleID, platform).
				WithHint("Pass --yes to create it.")
		}
		question := fmt.Sprintf("No Tenjin app has the bundle ID %s on %s.\nCreate %q? The bundle ID and platform cannot be changed later.", bundleID, platform, name)
		ok, err := env.confirm(prompt, question, true)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, clierr.New(clierr.NotFound, "no Tenjin app has the bundle ID %s on %s, and none was created", bundleID, platform)
		}
	}
	created, isNew, err := api.CreateApp(ctx, tenjinapi.NewApp{Name: name, BundleID: bundleID, Platform: platform})
	if err != nil {
		return nil, err
	}
	return &ensureResult{App: *created, Created: isNew}, nil
}

func contains(values []string, v string) bool {
	for _, candidate := range values {
		if candidate == v {
			return true
		}
	}
	return false
}
