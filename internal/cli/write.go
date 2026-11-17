package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tenjin/tenjin-cli/internal/agent"
	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi"
)

// The write commands: one API write each, its body's fields as flags. Only
// the flags given are sent, so an update changes those and leaves the rest.
// Each asks before it writes, at a terminal; elsewhere it needs --yes.
// --dry-run says the request it would send, prints its body, and sends
// nothing.

type fieldKind int

const (
	textField fieldKind = iota
	numberField
	boolField
	numbersField
	// pairsField is an object, given as repeated key=value flags.
	pairsField
	// listField is a comma-separated text, given as repeated or
	// comma-separated flags.
	listField
)

// field is one of a body's fields, set by one flag.
type field struct {
	flag, name, usage string
	kind              fieldKind
	required          bool
	// secret is said by its last four characters, in the question and the
	// dry run; an empty one, which clears it, as it is.
	secret bool
	// check refuses a text value the API would not take.
	check func(flag, value string) error
}

// isID checks that a field is an ID; what names it in the error.
func isID(what string) func(flag, value string) error {
	return func(_, value string) error {
		_, err := tenjinapi.ParseID(value, what)
		return err
	}
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
		case listField:
			s.values[fd.flag] = f.StringSlice(fd.flag, nil, usage+"; repeat or separate with commas")
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
			if fd.check != nil {
				if err := fd.check(fd.flag, *v); err != nil {
					return nil, err
				}
			}
			body[fd.name] = *v
		case *int:
			body[fd.name] = *v
		case *bool:
			body[fd.name] = *v
		case *[]int:
			body[fd.name] = *v
		case *[]string:
			if fd.kind == listField {
				body[fd.name] = strings.Join(*v, ",")
				continue
			}
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

// masked is the body with its secrets by their last four characters, and
// whether it had any. An empty one is shown as it is: it clears the secret,
// and hides nothing.
func (s *fieldSet) masked(body tenjinapi.Fields) (tenjinapi.Fields, bool) {
	out := maps.Clone(body)
	hid := false
	for _, fd := range s.fields {
		if v, ok := body[fd.name].(string); ok && fd.secret && v != "" {
			out[fd.name], hid = maskKey(v), true
		}
	}
	return out, hid
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
	// said is the body as the flags that set it, for the question.
	prepare func(args []string, body tenjinapi.Fields, said string) (string, error)
	// look reads what the question must say when what the write does
	// depends on what the account holds: a new what, or "" to keep it, and
	// whether to ask with no as the default. It runs before the question and
	// the dry run, not with --yes alone, where nothing is said.
	look func(ctx context.Context, api *tenjinapi.Client, body tenjinapi.Fields, said string) (what string, cautious bool)
	// keep adds to the body what the API would otherwise change unasked,
	// read from the account, and says it for the question. It runs before
	// the question and the dry run, and with --yes too, since it changes what
	// is sent.
	keep func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (string, error)
	// update refuses an empty body: there would be nothing to change.
	update bool
	// destructive asks with no as the default.
	destructive bool
	send        func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error)
	// show, instead of send, makes the write and writes what it did in the
	// CLI's own form: an app's answer holds its shared secrets.
	show func(cmd *cobra.Command, format render.Format, api *tenjinapi.Client, args []string, body tenjinapi.Fields) error
}

// done is what a person is told after an answer with no resource in it.
func (s writeSpec) done() string {
	switch {
	case s.destructive:
		return "Deleted."
	case s.update:
		return "Changed."
	}
	return "Created."
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
			said := fields.describe(body)
			what, err := s.prepare(args, body, said)
			if err != nil {
				return err
			}
			write := func(api *tenjinapi.Client) error {
				if s.show != nil {
					return s.show(cmd, format, api, args, body)
				}
				doc, err := s.send(cmd.Context(), api, args, body)
				if err != nil {
					return err
				}
				if format == render.JSON {
					return writeRawJSON(cmd.OutOrStdout(), doc)
				}
				if !hasData(doc) {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), s.done())
					return err
				}
				return writeDocument(cmd.OutOrStdout(), cmd.ErrOrStderr(), render.Table, doc, view{})
			}
			// Planned first, so that what the request needs, such as its
			// IDs, is checked before any read, the question and the dry run.
			api := env.api()
			planned, err := api.Plan(write)
			if err != nil {
				return err
			}
			if s.keep != nil {
				kept, err := s.keep(cmd.Context(), api, args, body)
				if err != nil {
					return err
				}
				what += kept
			}
			cautious := false
			if s.look != nil && (dryRun || !yes) {
				var looked string
				if looked, cautious = s.look(cmd.Context(), api, body, said); looked != "" {
					what = looked
				}
			}
			if dryRun {
				shown, masked := fields.masked(body)
				return writeDryRun(cmd, what, planned, shown, masked)
			}
			if err := confirmChange(cmd, env, yes, what, s.destructive || cautious); err != nil {
				return err
			}
			return write(api)
		},
	}
	fields.bind(cmd.Flags())
	if s.flags != nil {
		s.flags(cmd.Flags())
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do it without asking")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the request it would send, with its body, and send nothing")
	return cmd
}

// writeDryRun says, on stderr, what a write would do and the request it
// would send, and writes the request's body, if it has one, on stdout.
func writeDryRun(cmd *cobra.Command, what string, planned *tenjinapi.Request, body any, masked bool) error {
	request := planned.Method + " " + planned.URL
	if !planned.HasBody {
		_, err := fmt.Fprintf(cmd.ErrOrStderr(), "Would %s. Nothing was sent: it would send %s, with no body.\n", lowerFirst(render.Clean(what)), render.Clean(request))
		return err
	}
	note := ""
	if masked {
		note = ", its secrets by their last four characters"
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Would %s. Nothing was sent: it would send %s with this body%s:\n", lowerFirst(render.Clean(what)), render.Clean(request), note)
	return render.WriteJSON(cmd.OutOrStdout(), body)
}

// confirmChange asks before a change to the account: at a terminal, with
// no as the default for a destructive one; elsewhere it needs --yes.
func confirmChange(cmd *cobra.Command, env *environment, yes bool, what string, destructive bool) error {
	if yes {
		return nil
	}
	if !env.interactive {
		return clierr.New(clierr.Usage, "this changes your Tenjin account and needs confirmation").WithHint("Pass --yes to %s.", lowerFirst(what))
	}
	ok, err := env.confirm(cmd.ErrOrStderr(), render.Clean(what)+"?", !destructive)
	if err != nil {
		return err
	}
	if !ok {
		return declined("Nothing was changed.")
	}
	return nil
}

// hasData reports whether an answer holds a resource: a delete's does not.
func hasData(doc tenjinapi.Document) bool {
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(doc, &body)
	data := strings.TrimSpace(string(body.Data))
	return data != "" && data != "null" && data != "[]"
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// describe says a body as the flags that set it, in the order they are
// listed: --name "Summer sale", --active=false, --param token=abc.
func (s *fieldSet) describe(body tenjinapi.Fields) string {
	var parts []string
	for _, fd := range s.fields {
		v, ok := body[fd.name]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case bool:
			parts = append(parts, fmt.Sprintf("--%s=%t", fd.flag, t))
		case map[string]string:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				parts = append(parts, fmt.Sprintf("--%s %s=%s", fd.flag, k, t[k]))
			}
		case []int:
			numbers := make([]string, len(t))
			for i, n := range t {
				numbers[i] = fmt.Sprint(n)
			}
			parts = append(parts, "--"+fd.flag+" "+strings.Join(numbers, ","))
		case string:
			switch {
			case fd.secret && t == "":
				parts = append(parts, fmt.Sprintf("--%s \"\" (which clears it)", fd.flag))
				continue
			case fd.secret:
				t = maskKey(t)
			}
			if strings.ContainsAny(t, " \t") || t == "" {
				parts = append(parts, fmt.Sprintf("--%s %q", fd.flag, t))
			} else {
				parts = append(parts, "--"+fd.flag+" "+t)
			}
		default:
			parts = append(parts, fmt.Sprintf("--%s %v", fd.flag, v))
		}
	}
	return strings.Join(parts, " ")
}

func newAppsUpdateCmd(opts *options, env *environment) *cobra.Command {
	return writeCmd(opts, env, writeSpec{
		use: "update <app-id>", short: "Change an app's name or settings", update: true,
		long: `Change an app's name or settings. Only the flags given are changed. An app's
bundle ID and platform cannot be changed.`,
		args: exactArgs(1, "the app's ID, from 'tenjin apps list'"),
		fields: newFieldSet(
			field{flag: "name", name: "name", usage: "the app's name"},
			field{flag: "public-key", name: "public_key", usage: "the Google Play public key, for Android receipt validation", secret: true},
			field{flag: "ios-shared-secret", name: "ios_shared_secret", usage: "the App Store shared secret, for iOS subscription revenue", secret: true},
			field{flag: "protocol", name: "protocol", usage: "the app's URL scheme, for Facebook deep links"},
			field{flag: "match-strings", name: "match_strings", usage: "strings that match campaigns to the app"},
			field{flag: "destination-url", name: "destination_url", usage: "where an android_other app is downloaded from"},
		),
		prepare: func(args []string, body tenjinapi.Fields, said string) (string, error) {
			return fmt.Sprintf("Change the app %s: %s", args[0], said), nil
		},
		show: func(cmd *cobra.Command, format render.Format, api *tenjinapi.Client, args []string, body tenjinapi.Fields) error {
			app, err := api.UpdateApp(cmd.Context(), args[0], body)
			if err != nil {
				return err
			}
			detail := appDetail{App: *app, DashboardURL: env.endpoints.SDKKeysURL(app.ID)}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), detail)
			}
			for _, s := range []*string{&detail.Name, &detail.Platform, &detail.BundleID, &detail.ID} {
				*s = render.CleanCell(*s)
			}
			return writeAppDetail(cmd.OutOrStdout(), detail)
		},
	})
}

func newAppsDeleteCmd(opts *options, env *environment) *cobra.Command {
	return writeCmd(opts, env, writeSpec{
		use: "delete <app-id>", short: "Delete an app that is not sending events", destructive: true,
		long: `Delete an app. It no longer appears in the dashboard or the API, and its
SDK keys stop being accepted. The app is read first and named, with its
bundle ID and platform, in the question and the dry run. An app that is
sending events is not deleted here: the dashboard shows what would go with
it. A coding agent cannot run this command.`,
		args: exactArgs(1, "the app's ID, from 'tenjin apps list'"),
		prepare: func(args []string, _ tenjinapi.Fields, said string) (string, error) {
			if agent.InsideAgent(env.getenv) {
				return "", clierr.New(clierr.Usage, "'tenjin apps delete' does not run inside a coding agent: deleting an app cannot be undone, and is the developer's to do").
					WithHint("Have the developer run it in their own terminal, or delete the app in the dashboard.")
			}
			return "Delete the app", nil
		},
		// The app is read first, with --yes too: an ID that is nobody's app
		// fails here, before anything is sent, and an app that is sending
		// events is refused.
		keep: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Fields) (string, error) {
			app, err := api.GetApp(ctx, args[0])
			if err != nil {
				return "", err
			}
			named := fmt.Sprintf(" %q (%s on %s, %s)", app.Name, app.BundleID, app.Platform, app.ID)
			if app.SendingEvents {
				return "", clierr.New(clierr.Refused, "the app%s is sending events, so the CLI does not delete it", named).
					WithHint("Delete it in the dashboard, which shows what would go with it.")
			}
			return named, nil
		},
		show: func(cmd *cobra.Command, format render.Format, api *tenjinapi.Client, args []string, _ tenjinapi.Fields) error {
			if err := api.DeleteApp(cmd.Context(), args[0]); err != nil {
				return err
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), map[string]any{"app_id": args[0], "deleted": true})
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Deleted.")
			return err
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
			prepare: func(_ []string, body tenjinapi.Fields, said string) (string, error) {
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
			prepare: func(args []string, body tenjinapi.Fields, said string) (string, error) {
				return fmt.Sprintf("Change the campaign %s: %s", args[0], said), nil
			},
			send: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
				return api.UpdateCampaign(ctx, args[0], body)
			},
		}),
		writeCmd(opts, env, writeSpec{
			use: "delete <campaign-id>", short: "Delete a campaign that has not tracked installs", destructive: true,
			long: `Delete a campaign. Tenjin refuses to delete one that has started tracking
installs.`,
			args: exactArgs(1, "the campaign's ID, from 'tenjin campaigns list'"),
			prepare: func(args []string, _ tenjinapi.Fields, said string) (string, error) {
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
			{flag: "user-filter", name: "user_filter", usage: "whose events it sends: all, or channel for the channel's own users", check: tenjinapi.CallbackUserFilters.Check},
			{flag: "param", name: "customer_parameters", usage: "a parameter the channel needs", kind: pairsField},
		}
		if create {
			fields = append([]field{{flag: "template", name: "callback_template_id", usage: "the callback template's ID, from 'tenjin apps callback-templates list'", required: true, check: isID("callback template")}}, fields...)
		}
		return newFieldSet(fields...)
	}
	s2sFields := func(create bool) *fieldSet {
		return newFieldSet(
			field{flag: "url", name: "url", usage: "where the callback is sent", required: create},
			field{flag: "event", name: "event", usage: "the event that triggers it, such as open or purchase", required: create},
			field{flag: "name", name: "name", usage: "the callback's name", required: create},
			field{flag: "http-method", name: "http_method", usage: "GET or POST", check: tenjinapi.S2SHTTPMethods.Check},
			field{flag: "action", name: "action", usage: "ping, or ping_on_first for the first event only", required: create, check: tenjinapi.S2SActions.Check},
			field{flag: "active", name: "active", usage: "whether it is sent", kind: boolField},
			field{flag: "advertising-id-filter", name: "advertising_id_filter", usage: "only for users with a known advertising ID", kind: boolField},
			field{flag: "user-filter", name: "user_filter", usage: "only for users attributed to these channels, by ID", kind: numbersField},
		)
	}
	groupFields := func(create bool) *fieldSet {
		fields := []field{
			{flag: "event", name: "event", usage: "the event that triggers it, such as open or purchase"},
			{flag: "event-definition", name: "event_definition_id", usage: "the event definition's ID, instead of --event", check: isID("event definition")},
			{flag: "name", name: "name", usage: "the group's name"},
			{flag: "active", name: "active", usage: "whether it is sent", kind: boolField},
			{flag: "user-filter", name: "user_filter", usage: "whose events it sends: all, channel, or channel_and_organic", check: tenjinapi.CallbackGroupUserFilters.Check},
			{flag: "setting", name: "callback_settings", usage: "a callback setting", kind: pairsField},
		}
		if create {
			fields = append([]field{{flag: "template", name: "callback_group_template_id", usage: "the template's ID, from 'tenjin channels callback-group-templates'", required: true, check: isID("callback group template")}}, fields...)
		}
		return newFieldSet(fields...)
	}
	return map[string][]*cobra.Command{
		"callbacks": {
			writeCmd(opts, env, writeSpec{
				use: "create --app <app-id> --template <template-id>", short: "Create a callback from a template",
				fields: callbackFields(true), flags: needApp,
				prepare: func(_ []string, body tenjinapi.Fields, said string) (string, error) {
					what := "Create a callback on app " + app + ": " + said
					if body["active"] == false {
						what += ", then switch it off with a second request (Tenjin creates every callback active)"
					}
					return what, checkApp()
				},
				send: func(ctx context.Context, api *tenjinapi.Client, _ []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					doc, err := api.CreateAppCallback(ctx, app, body)
					if err != nil || body["active"] != false {
						return doc, withParametersHint(err, fmt.Sprintf("tenjin apps callback-templates get %v --app %s", body["callback_template_id"], app))
					}
					// The API creates a callback active whatever it is sent; one
					// asked for inactive is switched off at once, before it can
					// send anything to the channel.
					var created struct {
						Data struct {
							ID         string `json:"id"`
							Attributes struct {
								Active bool `json:"active"`
							} `json:"attributes"`
						} `json:"data"`
					}
					if json.Unmarshal(doc, &created) != nil || created.Data.ID == "" {
						return nil, clierr.New(clierr.Upstream, "a callback was created on app %s, but Tenjin's answer does not say which, so it was not switched off: it may be active", app).
							WithHint("Find it with 'tenjin apps callbacks list --app %s', and switch it off with 'tenjin apps callbacks update <callback-id> --app %s --active=false'.", app, app)
					}
					if !created.Data.Attributes.Active {
						return doc, nil
					}
					id := render.Clean(created.Data.ID)
					switched, err := api.UpdateAppCallback(ctx, app, created.Data.ID, tenjinapi.Fields{"active": false})
					if err != nil {
						e := clierr.From(err)
						return nil, clierr.Wrap(afterChange(e.Kind), err, "the callback %s was created on app %s and is active: switching it off failed: %s", id, app, e.Message).
							WithHint("Switch it off with 'tenjin apps callbacks update %s --app %s --active=false'. Creating it again would make a second callback.", id, app)
					}
					return switched, nil
				},
			}),
			writeCmd(opts, env, writeSpec{
				use: "update <callback-id> --app <app-id>", short: "Change a callback", update: true,
				long: `Change a callback: only the flags given. Tenjin switches a callback on when
an update does not say whether it is active, and replaces the callback's own
parameters with the ones an update sends. So without --active the callback is
read first and its current state sent with the change, and with --param its
other parameters are read and sent with the ones given. --param key= (an
empty value) removes that parameter: it is left out of what is sent.`,
				args:   exactArgs(1, "the callback's ID, from 'tenjin apps callbacks list'"),
				fields: callbackFields(false), flags: needApp,
				prepare: func(args []string, body tenjinapi.Fields, said string) (string, error) {
					return "Change the callback " + args[0] + ": " + said, checkApp()
				},
				keep: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (string, error) {
					return keepCallback(ctx, api, app, args[0], body)
				},
				send: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					doc, err := api.UpdateAppCallback(ctx, app, args[0], body)
					return doc, withParametersHint(err, fmt.Sprintf("tenjin apps callbacks get %s --app %s", args[0], app))
				},
			}),
		},
		"s2s-callbacks": {
			writeCmd(opts, env, writeSpec{
				use: "create --app <app-id> --url <url> --event <event> --name <name> --action <action>", short: "Create a server-to-server callback",
				fields: s2sFields(true), flags: needApp,
				prepare: func(_ []string, body tenjinapi.Fields, said string) (string, error) {
					return "Create a server-to-server callback on app " + app + ": " + said, checkApp()
				},
				send: func(ctx context.Context, api *tenjinapi.Client, _ []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					return api.CreateAppS2SCallback(ctx, app, body)
				},
			}),
			writeCmd(opts, env, writeSpec{
				use: "update <callback-id> --app <app-id>", short: "Change a server-to-server callback", update: true,
				args:   exactArgs(1, "the callback's ID, from 'tenjin apps s2s-callbacks list'"),
				fields: s2sFields(false), flags: needApp,
				prepare: func(args []string, body tenjinapi.Fields, said string) (string, error) {
					return "Change the server-to-server callback " + args[0] + ": " + said, checkApp()
				},
				send: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					return api.UpdateAppS2SCallback(ctx, app, args[0], body)
				},
			}),
		},
		"callback-groups": {
			writeCmd(opts, env, writeSpec{
				use: "create --app <app-id> --channel <channel-id> --template <template-id>", short: "Create a callback group from a template",
				long: `Create a callback group from one of the channel's group templates. Where the
template allows one group per app and the app has one already, Tenjin changes
that group with what is sent instead of making another: --active=false
switches it off. The confirmation says whether it will create or change.`,
				fields: groupFields(true), flags: needAppChannel,
				prepare: func(_ []string, body tenjinapi.Fields, said string) (string, error) {
					if err := checkAppChannel(); err != nil {
						return "", err
					}
					if body["event"] == nil && body["event_definition_id"] == nil {
						return "", clierr.New(clierr.Usage, "--event or --event-definition is required")
					}
					return fmt.Sprintf("Create a callback group on app %s for channel %d: %s", app, channel, said), nil
				},
				look: func(ctx context.Context, api *tenjinapi.Client, body tenjinapi.Fields, said string) (string, bool) {
					template, _ := body["callback_group_template_id"].(string)
					group, err := api.GroupCreateWouldChange(ctx, app, channel, template)
					if err != nil {
						return fmt.Sprintf("Create a callback group on app %s for channel %d, or change the app's group from this template if it has one (that could not be checked): %s", app, channel, said), true
					}
					if group == nil {
						return "", false
					}
					state := "switched off"
					if group.Active {
						state = "active"
					}
					name := group.ID
					if group.Name != "" {
						name = fmt.Sprintf("%q (%s)", group.Name, group.ID)
					}
					what := fmt.Sprintf("Change the app's existing callback group %s for channel %d, which is %s: %s", name, channel, state, said)
					stops := group.Active && body["active"] == false
					if stops {
						what += ". This switches it off, and its callbacks stop"
					}
					return what, stops
				},
				send: func(ctx context.Context, api *tenjinapi.Client, _ []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					return api.CreateAppCallbackGroup(ctx, app, channel, body)
				},
			}),
			writeCmd(opts, env, writeSpec{
				use: "update <group-id> --app <app-id> --channel <channel-id>", short: "Change a callback group", update: true,
				args:   exactArgs(1, "the group's ID, from 'tenjin apps callback-groups list'"),
				fields: groupFields(false), flags: needAppChannel,
				prepare: func(args []string, body tenjinapi.Fields, said string) (string, error) {
					return "Change the callback group " + args[0] + ": " + said, checkAppChannel()
				},
				send: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
					return api.UpdateAppCallbackGroup(ctx, app, channel, args[0], body)
				},
			}),
		},
		"callback-settings": {
			writeCmd(opts, env, writeSpec{
				use: "update --app <app-id> --channel <channel-id> --setting <key=value>", short: "Change an app's callback settings for one channel", update: true,
				long: `Change the callback settings of an app for one channel: they apply to all of
its callback groups for that channel. Check the current ones first with
'tenjin apps callback-settings get'.`,
				fields: newFieldSet(field{flag: "setting", name: "callback_settings", usage: "a callback setting", kind: pairsField, required: true}),
				flags:  needAppChannel,
				prepare: func(_ []string, body tenjinapi.Fields, said string) (string, error) {
					return fmt.Sprintf("Change the callback settings of app %s for channel %d: %s", app, channel, said), checkAppChannel()
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
			{flag: "app", name: "app_ids", usage: "only these apps, by ID", kind: listField},
			{flag: "channel", name: "channel_ids", usage: "only these marketing channels, by ID", kind: listField},
			{flag: "metrics", name: "metrics", usage: "the metrics, from 'tenjin reports metrics'", kind: listField},
		}
		if create {
			fields = append([]field{{flag: "type", name: "report_type", usage: "user_acquisition or ad_revenue", required: true, check: tenjinapi.SavedReportTypes.Check}}, fields...)
		}
		return newFieldSet(fields...)
	}
	return []*cobra.Command{
		writeCmd(opts, env, writeSpec{
			use: "create --type <type> --name <name> --granularity <granularity> --group-by <grouping>", short: "Save a report",
			fields: reportFields(true),
			prepare: func(_ []string, body tenjinapi.Fields, said string) (string, error) {
				return "Save the report " + said, nil
			},
			send: func(ctx context.Context, api *tenjinapi.Client, _ []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
				return api.CreateSavedReport(ctx, body)
			},
		}),
		writeCmd(opts, env, writeSpec{
			use: "update <saved-report-id>", short: "Change a saved report", update: true,
			args:   exactArgs(1, "the saved report's ID, from 'tenjin saved-reports list'"),
			fields: reportFields(false),
			prepare: func(args []string, body tenjinapi.Fields, said string) (string, error) {
				return "Change the saved report " + args[0] + ": " + said, nil
			},
			send: func(ctx context.Context, api *tenjinapi.Client, args []string, body tenjinapi.Fields) (tenjinapi.Document, error) {
				return api.UpdateSavedReport(ctx, args[0], body)
			},
		}),
		writeCmd(opts, env, writeSpec{
			use: "delete <saved-report-id>", short: "Delete a saved report", destructive: true,
			args: exactArgs(1, "the saved report's ID, from 'tenjin saved-reports list'"),
			prepare: func(args []string, _ tenjinapi.Fields, said string) (string, error) {
				return "Delete the saved report " + args[0], nil
			},
			send: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Fields) (tenjinapi.Document, error) {
				return api.DeleteSavedReport(ctx, args[0])
			},
		}),
	}
}

// keepCallback adds to a callback update what the API would otherwise change
// unasked, read from the callback, and says it for the question: whether it
// is active, when the update does not say, and its other parameters, when the
// update sends some; one given an empty value is left out, which removes it.
// A callback that cannot be read is not guessed at.
func keepCallback(ctx context.Context, api *tenjinapi.Client, app, callback string, body tenjinapi.Fields) (string, error) {
	_, activeGiven := body["active"]
	given, paramsGiven := body["customer_parameters"].(map[string]string)
	if activeGiven && !paramsGiven {
		return "", nil
	}
	id := render.Clean(callback)
	var needs []string
	if !activeGiven {
		needs = append(needs, "whether it is active")
	}
	if paramsGiven {
		needs = append(needs, "its other parameters")
	}
	need := strings.Join(needs, " and ")
	state, err := api.ReadCallbackState(ctx, app, callback)
	if err != nil {
		e := clierr.From(err)
		failed := clierr.Wrap(e.Kind, err, "could not read the callback %s, so the change cannot send %s, as it must to leave the callback as it is: %s", id, need, e.Message)
		switch {
		case e.Kind == clierr.NotSignedIn || e.Kind == clierr.Permission || e.Kind == clierr.NotFound || e.Kind == clierr.Busy || paramsGiven:
			// The change would fail the same way, or needs the read
			// whatever else is said: only what fixes the read helps.
			failed.Hint = e.Hint
		case e.Hint != "":
			failed.Hint = e.Hint + " Or say it with --active or --active=false, and nothing is read."
		default:
			failed.Hint = "Say it with --active or --active=false, and nothing is read."
		}
		return "", failed
	}
	if !activeGiven && state.Active == nil || paramsGiven && state.Parameters == nil {
		return "", clierr.New(clierr.Upstream, "the Tenjin API's answer for the callback %s does not say %s, which the change must send to leave the callback as it is", id, need)
	}
	var said string
	if !activeGiven {
		body["active"] = *state.Active
		if *state.Active {
			said = "; it stays active"
		} else {
			said = "; it stays switched off (--active=false is sent, since Tenjin would otherwise switch it on)"
		}
	}
	if paramsGiven {
		merged := make(map[string]any, len(state.Parameters)+len(given))
		var kept, removed []string
		for k, v := range state.Parameters {
			merged[k] = v
			if _, replaced := given[k]; !replaced {
				kept = append(kept, k)
			}
		}
		for k, v := range given {
			// An empty value removes the parameter: what is sent replaces
			// them all, so it is left out.
			if v == "" {
				delete(merged, k)
				removed = append(removed, k)
				continue
			}
			merged[k] = v
		}
		body["customer_parameters"] = merged
		if len(kept) > 0 {
			sort.Strings(kept)
			pairs := make([]string, len(kept))
			for i, k := range kept {
				pairs[i] = "--param " + k + "=" + parameterValue(merged[k])
			}
			sent, them := "are", "them"
			if len(kept) == 1 {
				sent, them = "is", "it"
			}
			said += fmt.Sprintf("; its other parameters stay (%s %s sent too, since Tenjin would otherwise drop %s)", strings.Join(pairs, " "), sent, them)
		}
		if len(removed) > 0 {
			sort.Strings(removed)
			verb := "are"
			if len(removed) == 1 {
				verb = "is"
			}
			said += fmt.Sprintf("; %s %s removed, left out of the parameters sent", strings.Join(removed, ", "), verb)
		}
	}
	return said, nil
}

// parameterValue is a callback parameter's value as a flag gives it.
func parameterValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// afterChange is the kind of a failure that came after an earlier request of
// the same command changed something. Every other kind reads as a request to
// fix and send again, which no longer holds: it could do the change twice.
// Upstream says only that it did not finish, and the message says what was
// changed. A run stopped from outside stays Interrupted.
func afterChange(kind clierr.Kind) clierr.Kind {
	if kind == clierr.Interrupted {
		return kind
	}
	return clierr.Upstream
}

// withParametersHint points a callback Tenjin refused at where the values it
// takes are listed: most refusals are a parameter missing or not accepted.
func withParametersHint(err error, command string) error {
	if err == nil {
		return nil
	}
	e := clierr.From(err)
	if e.Kind != clierr.Refused || e.Hint != "" {
		return err
	}
	return e.WithHint("The parameters it takes: '%s'. Step by step: 'tenjin help callbacks'.", render.Clean(command))
}
