package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi"
)

// The write commands: one API write each, its body's fields as flags. Only
// the flags given are sent, so an update changes those and leaves the rest.
// Each asks before it writes, at a terminal; elsewhere it needs --yes.
// --dry-run prints the body it would send and sends nothing.

type fieldKind int

const (
	textField fieldKind = iota
	numberField
	boolField
	numbersField
	// pairsField is an object, given as repeated key=value flags.
	pairsField
)

// field is one of a body's fields, set by one flag.
type field struct {
	flag, name, usage string
	kind              fieldKind
	required          bool
}

// fieldSet binds a body's fields to flags, and builds the body from the
// flags that were given.
type fieldSet struct {
	fields []field
	values map[string]any
}

func newFieldSet(fields ...field) *fieldSet {
	return &fieldSet{fields: fields, values: map[string]any{}}
}

func (s *fieldSet) bind(f *pflag.FlagSet) {
	for _, fd := range s.fields {
		usage := fd.usage
		if fd.required {
			usage += " (required)"
		}
		switch fd.kind {
		case textField:
			s.values[fd.flag] = f.String(fd.flag, "", usage)
		case numberField:
			s.values[fd.flag] = f.Int(fd.flag, 0, usage)
		case boolField:
			s.values[fd.flag] = f.Bool(fd.flag, false, usage)
		case numbersField:
			s.values[fd.flag] = f.IntSlice(fd.flag, nil, usage+", comma-separated")
		case pairsField:
			s.values[fd.flag] = f.StringArray(fd.flag, nil, usage+", as key=value; repeat for each")
		}
	}
}

func (s *fieldSet) body(cmd *cobra.Command) (tenjinapi.Fields, error) {
	body := tenjinapi.Fields{}
	for _, fd := range s.fields {
		if !cmd.Flags().Changed(fd.flag) {
			if fd.required {
				return nil, clierr.New(clierr.Usage, "--%s is required", fd.flag)
			}
			continue
		}
		switch v := s.values[fd.flag].(type) {
		case *string:
			body[fd.name] = *v
		case *int:
			body[fd.name] = *v
		case *bool:
			body[fd.name] = *v
		case *[]int:
			body[fd.name] = *v
		case *[]string:
			pairs := map[string]string{}
			for _, p := range *v {
				key, value, ok := strings.Cut(p, "=")
				if !ok || key == "" {
					return nil, clierr.New(clierr.Usage, "--%s takes key=value, not %q", fd.flag, p)
				}
				pairs[key] = value
			}
			body[fd.name] = pairs
		}
	}
	return body, nil
}

func (s *fieldSet) flagNames() string {
	names := make([]string, 0, len(s.fields))
	for _, fd := range s.fields {
		names = append(names, "--"+fd.flag)
	}
	return strings.Join(names, ", ")
}

// writeSpec is one write command.
type writeSpec struct {
	use, short, long string
	args             cobra.PositionalArgs
	fields           *fieldSet
	// flags adds what the path needs, such as --app and --channel.
	flags func(*pflag.FlagSet)
	// prepare checks what the body and fields cannot, may add to the body,
	// and says what will be done, for the question: "Create the campaign ...".
	prepare func(args []string, body tenjinapi.Fields) (string, error)
	// update refuses an empty body: there would be nothing to change.
	update bool
	// destructive asks with no as the default.
	destructive bool
	send        func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error)
	// done is what a person is told after an answer with no resource in it.
	done string
}

func writeCmd(opts *options, env *environment, s writeSpec) *cobra.Command {
	var yes, dryRun bool
	args := s.args
	if args == nil {
		args = noArgs
	}
	fields := s.fields
	if fields == nil {
		fields = newFieldSet()
	}
	cmd := &cobra.Command{
		Use:   s.use,
		Short: s.short,
		Long:  s.long,
		Args:  args,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			body, err := fields.body(cmd)
			if err != nil {
				return err
			}
			if s.update && len(body) == 0 {
				return clierr.New(clierr.Usage, "nothing to change: give at least one of %s", fields.flagNames())
			}
			what, err := s.prepare(args, body)
			if err != nil {
				return err
			}
			if dryRun {
				return render.WriteJSON(cmd.OutOrStdout(), body)
			}
			if !yes {
				if !env.interactive {
					return clierr.New(clierr.Usage, "this changes your Tenjin account and needs confirmation").WithHint("Pass --yes to %s.", lowerFirst(what))
				}
				ok, err := env.confirm(cmd.ErrOrStderr(), render.Clean(what)+"?", !s.destructive)
				if err != nil {
					return err
				}
				if !ok {
					return clierr.New(clierr.Internal, "nothing was changed")
				}
			}
			doc, err := s.send(cmd.Context(), env.api(), args, body)
			if err != nil {
				return err
			}
			if format == render.JSON {
				return writeRawJSON(cmd.OutOrStdout(), doc)
			}
			if !hasData(doc) {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), s.done)
				return err
			}
			_, err = writeDocument(cmd.OutOrStdout(), doc, view{})
			return err
		},
	}
	fields.bind(cmd.Flags())
	if s.flags != nil {
		s.flags(cmd.Flags())
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do it without asking")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the request body and send nothing")
	return cmd
}

// hasData reports whether an answer holds a resource: a delete's does not.
func hasData(doc tenjinapi.Document) bool {
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(doc, &body)
	data := strings.TrimSpace(string(body.Data))
	return data != "" && data != "null"
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// describe lists a body's fields for the question, in a stable order.
func describe(body tenjinapi.Fields) string {
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %v", k, body[k]))
	}
	return strings.Join(parts, ", ")
}

func newAppsUpdateCmd(opts *options, env *environment) *cobra.Command {
	return writeCmd(opts, env, writeSpec{
		use: "update <app-id>", short: "Change an app's name or settings", update: true,
		long: `Change an app's name or settings. Only the flags given are changed. An app's
bundle ID and platform cannot be changed.`,
		args: exactArgs(1, "the app's ID, from 'tenjin apps list'"),
		fields: newFieldSet(
			field{flag: "name", name: "name", usage: "the app's name"},
			field{flag: "public-key", name: "public_key", usage: "the Google Play public key, for Android receipt validation"},
			field{flag: "ios-shared-secret", name: "ios_shared_secret", usage: "the App Store shared secret, for iOS subscription revenue"},
			field{flag: "protocol", name: "protocol", usage: "the app's URL scheme, for Facebook deep links"},
			field{flag: "match-strings", name: "match_strings", usage: "strings that match campaigns to the app"},
			field{flag: "destination-url", name: "destination_url", usage: "where an android_other app is downloaded from"},
		),
		prepare: func(args []string, body tenjinapi.Fields) (string, error) {
			return fmt.Sprintf("Change the app %s: %s", args[0], describe(body)), nil
		},
		send: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
			return api.UpdateApp(ctx, args[0], body)
		},
	})
}

func newAppsDeleteCmd(opts *options, env *environment) *cobra.Command {
	return writeCmd(opts, env, writeSpec{
		use: "delete <app-id>", short: "Delete an app", destructive: true, done: "Deleted.",
		long: `Delete an app. Tenjin keeps its data, but the app no longer appears in the
dashboard or the API, and its SDK keys stop being accepted.`,
		args: exactArgs(1, "the app's ID, from 'tenjin apps list'"),
		prepare: func(args []string, _ tenjinapi.Fields) (string, error) {
			return "Delete the app " + args[0], nil
		},
		send: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Fields) (tenjinapi.Document, error) {
			return api.DeleteApp(ctx, args[0])
		},
	})
}

func newCampaignWriteCmds(opts *options, env *environment) []*cobra.Command {
	var app string
	return []*cobra.Command{
		writeCmd(opts, env, writeSpec{
			use: "create --app <app-id> --name <name> (--channel <channel-id> | --ad-account <id>)", short: "Create a campaign, with its tracking links",
			fields: newFieldSet(
				field{flag: "name", name: "name", usage: "the campaign's name", required: true},
				field{flag: "channel", name: "channel_id", usage: "the marketing channel's ID; 0 is Organic", kind: numberField},
				field{flag: "ad-account", name: "ad_account_id", usage: "the ad account's ID, instead of --channel"},
			),
			flags: appFlag(&app),
			prepare: func(_ []string, body tenjinapi.Fields) (string, error) {
				if err := requireApp(app); err != nil {
					return "", err
				}
				if _, err := tenjinapi.ParseID(app, "app"); err != nil {
					return "", err
				}
				body["app_id"] = app
				channel, byChannel := body["channel_id"]
				account, byAccount := body["ad_account_id"]
				switch {
				case byChannel && byAccount:
					return "", clierr.New(clierr.Usage, "give --channel or --ad-account, not both")
				case byAccount:
					if _, err := tenjinapi.ParseID(fmt.Sprint(account), "ad account"); err != nil {
						return "", err
					}
					return fmt.Sprintf("Create the campaign %q for app %s with ad account %s", body["name"], app, account), nil
				case byChannel:
					return fmt.Sprintf("Create the campaign %q for app %s on channel %v", body["name"], app, channel), nil
				}
				return "", clierr.New(clierr.Usage, "--channel or --ad-account is required").WithHint("Find one with 'tenjin channels list' or 'tenjin ad-accounts list'.")
			},
			send: func(ctx context.Context, api *tenjinapi.Client, _ []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
				return api.CreateCampaign(ctx, body)
			},
		}),
		writeCmd(opts, env, writeSpec{
			use: "update <campaign-id>", short: "Change a campaign's attribution window", update: true,
			long: `Change a campaign's click attribution window, in seconds. It is the only
part of a campaign the API lets change.`,
			args:   exactArgs(1, "the campaign's ID, from 'tenjin campaigns list'"),
			fields: newFieldSet(field{flag: "attribution-window", name: "attribution_window", usage: "the click attribution window in seconds; 604800 is 7 days", kind: numberField}),
			prepare: func(args []string, body tenjinapi.Fields) (string, error) {
				return fmt.Sprintf("Change the campaign %s: %s", args[0], describe(body)), nil
			},
			send: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
				return api.UpdateCampaign(ctx, args[0], body)
			},
		}),
		writeCmd(opts, env, writeSpec{
			use: "delete <campaign-id>", short: "Delete a campaign that has not tracked installs", destructive: true, done: "Deleted.",
			long: `Delete a campaign. Tenjin refuses to delete one that has started tracking
installs.`,
			args: exactArgs(1, "the campaign's ID, from 'tenjin campaigns list'"),
			prepare: func(args []string, _ tenjinapi.Fields) (string, error) {
				return "Delete the campaign " + args[0], nil
			},
			send: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Fields) (tenjinapi.Document, error) {
				return api.DeleteCampaign(ctx, args[0])
			},
		}),
	}
}

// appWriteCmds are the writes on an app's own resources, under their
// groups in 'tenjin apps'.
func appWriteCmds(opts *options, env *environment) map[string][]*cobra.Command {
	var app string
	var channel int
	needApp := func(f *pflag.FlagSet) { appFlag(&app)(f) }
	needAppChannel := func(f *pflag.FlagSet) {
		appFlag(&app)(f)
		f.IntVar(&channel, "channel", 0, "the marketing channel's ID, from 'tenjin channels list' (required)")
	}
	checkApp := func() error {
		if err := requireApp(app); err != nil {
			return err
		}
		_, err := tenjinapi.ParseID(app, "app")
		return err
	}
	checkAppChannel := func() error {
		if err := checkApp(); err != nil {
			return err
		}
		return requireChannel(channel)
	}
	callbackFields := func(create bool) *fieldSet {
		fields := []field{
			{flag: "name", name: "name", usage: "the callback's name"},
			{flag: "event", name: "event", usage: "the event that triggers it, such as open or purchase"},
			{flag: "active", name: "active", usage: "whether it is sent", kind: boolField},
			{flag: "user-filter", name: "user_filter", usage: "whose events it sends: all, or channel for the channel's own users"},
			{flag: "param", name: "customer_parameters", usage: "a parameter the channel needs", kind: pairsField},
		}
		if create {
			fields = append([]field{{flag: "template", name: "callback_template_id", usage: "the callback template's ID, from 'tenjin apps callback-templates list'", required: true}}, fields...)
		}
		return newFieldSet(fields...)
	}
	s2sFields := func(create bool) *fieldSet {
		return newFieldSet(
			field{flag: "url", name: "url", usage: "where the callback is sent", required: create},
			field{flag: "event", name: "event", usage: "the event that triggers it, such as open or purchase", required: create},
			field{flag: "name", name: "name", usage: "the callback's name"},
			field{flag: "http-method", name: "http_method", usage: "GET or POST"},
			field{flag: "action", name: "action", usage: "ping, or ping_on_first for the first event only"},
			field{flag: "active", name: "active", usage: "whether it is sent", kind: boolField},
			field{flag: "advertising-id-filter", name: "advertising_id_filter", usage: "only for users with a known advertising ID", kind: boolField},
			field{flag: "user-filter", name: "user_filter", usage: "only for users attributed to these channels, by ID", kind: numbersField},
		)
	}
	groupFields := func(create bool) *fieldSet {
		fields := []field{
			{flag: "event", name: "event", usage: "the event that triggers it, such as open or purchase"},
			{flag: "event-definition", name: "event_definition_id", usage: "the event definition's ID, instead of --event"},
			{flag: "name", name: "name", usage: "the group's name"},
			{flag: "active", name: "active", usage: "whether it is sent", kind: boolField},
			{flag: "user-filter", name: "user_filter", usage: "whose events it sends: all, channel, or channel_and_organic"},
			{flag: "setting", name: "callback_settings", usage: "a callback setting", kind: pairsField},
		}
		if create {
			fields = append([]field{{flag: "template", name: "callback_group_template_id", usage: "the template's ID, from 'tenjin channels callback-group-templates'", required: true}}, fields...)
		}
		return newFieldSet(fields...)
	}
	return map[string][]*cobra.Command{
		"callbacks": {
			writeCmd(opts, env, writeSpec{
				use: "create --app <app-id> --template <template-id>", short: "Create a callback from a template",
				fields: callbackFields(true), flags: needApp,
				prepare: func(_ []string, body tenjinapi.Fields) (string, error) {
					return "Create a callback on app " + app + ": " + describe(body), checkApp()
				},
				send: func(ctx context.Context, api *tenjinapi.Client, _ []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					return api.CreateAppCallback(ctx, app, body)
				},
			}),
			writeCmd(opts, env, writeSpec{
				use: "update <callback-id> --app <app-id>", short: "Change a callback", update: true,
				args:   exactArgs(1, "the callback's ID, from 'tenjin apps callbacks list'"),
				fields: callbackFields(false), flags: needApp,
				prepare: func(args []string, body tenjinapi.Fields) (string, error) {
					return "Change the callback " + args[0] + ": " + describe(body), checkApp()
				},
				send: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					return api.UpdateAppCallback(ctx, app, args[0], body)
				},
			}),
		},
		"s2s-callbacks": {
			writeCmd(opts, env, writeSpec{
				use: "create --app <app-id> --url <url> --event <event>", short: "Create a server-to-server callback",
				fields: s2sFields(true), flags: needApp,
				prepare: func(_ []string, body tenjinapi.Fields) (string, error) {
					return "Create a server-to-server callback on app " + app + ": " + describe(body), checkApp()
				},
				send: func(ctx context.Context, api *tenjinapi.Client, _ []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					return api.CreateAppS2SCallback(ctx, app, body)
				},
			}),
			writeCmd(opts, env, writeSpec{
				use: "update <callback-id> --app <app-id>", short: "Change a server-to-server callback", update: true,
				args:   exactArgs(1, "the callback's ID, from 'tenjin apps s2s-callbacks list'"),
				fields: s2sFields(false), flags: needApp,
				prepare: func(args []string, body tenjinapi.Fields) (string, error) {
					return "Change the server-to-server callback " + args[0] + ": " + describe(body), checkApp()
				},
				send: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					return api.UpdateAppS2SCallback(ctx, app, args[0], body)
				},
			}),
		},
		"callback-groups": {
			writeCmd(opts, env, writeSpec{
				use: "create --app <app-id> --channel <channel-id> --template <template-id>", short: "Create a callback group from a template",
				fields: groupFields(true), flags: needAppChannel,
				prepare: func(_ []string, body tenjinapi.Fields) (string, error) {
					if err := checkAppChannel(); err != nil {
						return "", err
					}
					if body["event"] == nil && body["event_definition_id"] == nil {
						return "", clierr.New(clierr.Usage, "--event or --event-definition is required")
					}
					return fmt.Sprintf("Create a callback group on app %s for channel %d: %s", app, channel, describe(body)), nil
				},
				send: func(ctx context.Context, api *tenjinapi.Client, _ []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					return api.CreateAppCallbackGroup(ctx, app, channel, body)
				},
			}),
			writeCmd(opts, env, writeSpec{
				use: "update <group-id> --app <app-id> --channel <channel-id>", short: "Change a callback group", update: true,
				args:   exactArgs(1, "the group's ID, from 'tenjin apps callback-groups list'"),
				fields: groupFields(false), flags: needAppChannel,
				prepare: func(args []string, body tenjinapi.Fields) (string, error) {
					return "Change the callback group " + args[0] + ": " + describe(body), checkAppChannel()
				},
				send: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					return api.UpdateAppCallbackGroup(ctx, app, channel, args[0], body)
				},
			}),
		},
		"callback-settings": {
			writeCmd(opts, env, writeSpec{
				use: "update --app <app-id> --channel <channel-id> --setting <key=value>", short: "Change an app's callback settings for one channel",
				long: `Change the callback settings of an app for one channel: they apply to all of
its callback groups for that channel. Check the current ones first with
'tenjin apps callback-settings get'.`,
				fields: newFieldSet(field{flag: "setting", name: "callback_settings", usage: "a callback setting", kind: pairsField, required: true}),
				flags:  needAppChannel,
				prepare: func(_ []string, body tenjinapi.Fields) (string, error) {
					return fmt.Sprintf("Change the callback settings of app %s for channel %d: %s", app, channel, describe(body)), checkAppChannel()
				},
				send: func(ctx context.Context, api *tenjinapi.Client, _ []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					return api.UpdateAppCallbackSettings(ctx, app, channel, body)
				},
			}),
		},
	}
}

func newSavedReportWriteCmds(opts *options, env *environment) []*cobra.Command {
	reportFields := func(create bool) *fieldSet {
		fields := []field{
			{flag: "name", name: "name", usage: "the report's name", required: create},
			{flag: "granularity", name: "granularity", usage: "daily, weekly, monthly or totals-daily", required: create},
			{flag: "group-by", name: "group_by", usage: "what each row is, as for 'tenjin reports'", required: create},
			{flag: "past-days", name: "past_number_days", usage: "how many days back from today it covers (default 30)", kind: numberField},
			{flag: "apps", name: "app_ids", usage: "only these apps, by comma-separated IDs"},
			{flag: "channels", name: "channel_ids", usage: "only these marketing channels, by comma-separated IDs"},
			{flag: "metrics", name: "metrics", usage: "the metrics, comma-separated"},
		}
		if create {
			fields = append([]field{{flag: "type", name: "report_type", usage: "user_acquisition or ad_revenue", required: true}}, fields...)
		}
		return newFieldSet(fields...)
	}
	return []*cobra.Command{
		writeCmd(opts, env, writeSpec{
			use: "create --type <type> --name <name> --granularity <granularity> --group-by <grouping>", short: "Save a report",
			fields: reportFields(true),
			prepare: func(_ []string, body tenjinapi.Fields) (string, error) {
				return "Save the report " + describe(body), nil
			},
			send: func(ctx context.Context, api *tenjinapi.Client, _ []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
				return api.CreateSavedReport(ctx, body)
			},
		}),
		writeCmd(opts, env, writeSpec{
			use: "update <saved-report-id>", short: "Change a saved report", update: true,
			args:   exactArgs(1, "the saved report's ID, from 'tenjin saved-reports list'"),
			fields: reportFields(false),
			prepare: func(args []string, body tenjinapi.Fields) (string, error) {
				return "Change the saved report " + args[0] + ": " + describe(body), nil
			},
			send: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
				return api.UpdateSavedReport(ctx, args[0], body)
			},
		}),
		writeCmd(opts, env, writeSpec{
			use: "delete <saved-report-id>", short: "Delete a saved report", destructive: true, done: "Deleted.",
			args: exactArgs(1, "the saved report's ID, from 'tenjin saved-reports list'"),
			prepare: func(args []string, _ tenjinapi.Fields) (string, error) {
				return "Delete the saved report " + args[0], nil
			},
			send: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Fields) (tenjinapi.Document, error) {
				return api.DeleteSavedReport(ctx, args[0])
			},
		}),
	}
}
