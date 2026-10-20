package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi"
)

// The data commands: one command per API read, its parameters as flags. At a
// terminal they print a table; with --json, the API's answer as it is, so the
// API's documentation describes their JSON.

// view is how a resource reads in a table.
type view struct {
	// noun names the resources, plural, for "No campaigns."
	noun string
	// columns are the attributes a list shows, before the ID. Empty shows
	// every attribute of the first item, in the API's order: reports carry
	// the metrics that were asked for.
	columns []string
}

// dataSpec is one data command.
type dataSpec struct {
	use, short, long string
	args             cobra.PositionalArgs
	view             view
	// paged adds --page, --per-page and --all; allPages makes --all the
	// default, unless a page is asked for.
	paged    bool
	allPages bool
	flags    func(*pflag.FlagSet)
	fetch    func(ctx context.Context, api *tenjinapi.Client, args []string, p tenjinapi.Paging) (tenjinapi.Document, error)
}

func dataCmd(opts *options, env *environment, s dataSpec) *cobra.Command {
	var paging tenjinapi.Paging
	args := s.args
	if args == nil {
		args = noArgs
	}
	cmd := &cobra.Command{
		Use:         s.use,
		Short:       s.short,
		Long:        s.long,
		Args:        args,
		Annotations: map[string]string{csvAnnotation: "yes"},
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if paging.All && (paging.Page > 0 || paging.PerPage > 0) {
				return clierr.New(clierr.Usage, "--all reads every page: leave out --page and --per-page")
			}
			if paging.PerPage > 1000 {
				return clierr.New(clierr.Usage, "--per-page is at most 1000")
			}
			if s.allPages && !cmd.Flags().Changed("page") && !cmd.Flags().Changed("per-page") {
				paging.All = true
			}
			doc, err := s.fetch(cmd.Context(), env.api(), args, paging)
			if err != nil {
				return err
			}
			if format == render.JSON {
				return writeRawJSON(cmd.OutOrStdout(), doc)
			}
			if err := writeDocument(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, doc, s.view); err != nil {
				return err
			}
			page := max(paging.Page, 1)
			if s.paged && !paging.All && morePages(doc, page) {
				_, err = fmt.Fprintf(cmd.ErrOrStderr(), "There is more: --page %d for the next page, or --all for every page.\n", page+1)
			}
			return err
		},
	}
	if s.paged {
		cmd.Flags().IntVar(&paging.Page, "page", 0, "the page to show (default 1)")
		cmd.Flags().IntVar(&paging.PerPage, "per-page", 0, "how many to a page, up to 1000 (default 25)")
		cmd.Flags().BoolVar(&paging.All, "all", false, "every page, as one list")
	}
	if s.flags != nil {
		s.flags(cmd.Flags())
	}
	return cmd
}

// writeRawJSON writes the API's document, indented like the CLI's own JSON.
func writeRawJSON(w io.Writer, doc tenjinapi.Document) error {
	var out bytes.Buffer
	if err := json.Indent(&out, doc, "", "  "); err != nil {
		return clierr.Wrap(clierr.Upstream, err, "the Tenjin API returned something that is not JSON")
	}
	out.WriteByte('\n')
	_, err := out.WriteTo(w)
	return err
}

type item struct {
	ID         json.RawMessage `json:"id"`
	Attributes json.RawMessage `json:"attributes"`
}

// csvAnnotation marks the commands that can write CSV.
const csvAnnotation = "csv"

// writeDocument writes a document as a table, or as CSV: a list as rows, one
// resource as its fields. A table wider than the terminal is followed, on
// errw, by how to see it whole.
func writeDocument(w, errw io.Writer, format render.Format, doc tenjinapi.Document, v view) error {
	header, rows, err := tableOf(doc, v)
	if err != nil {
		return err
	}
	if format == render.CSV {
		if header == nil {
			header = []string{}
		}
		return render.WriteCSV(w, header, rows)
	}
	if header == nil {
		_, err := fmt.Fprintf(w, "No %s.\n", v.noun)
		return err
	}
	var table bytes.Buffer
	if err := render.WriteTable(&table, render.NewStyler(w), header, rows); err != nil {
		return err
	}
	if width := render.Width(w); width > 0 && widest(table.String()) > width && errw != nil {
		defer func() {
			_, _ = fmt.Fprintln(errw, "Wider than the terminal: -o csv or --json shows every column whole.")
		}()
	}
	_, err = table.WriteTo(w)
	return err
}

// morePages reports whether a list has pages after this one: it has a next
// link, or its meta counts more items than the pages up to this one hold, as
// a report's does.
func morePages(doc tenjinapi.Document, page int) bool {
	var body struct {
		Links struct {
			Next string `json:"next"`
		} `json:"links"`
		Meta struct {
			Count   int `json:"count"`
			PerPage int `json:"per_page"`
		} `json:"meta"`
	}
	if json.Unmarshal(doc, &body) != nil {
		return false
	}
	return body.Links.Next != "" || (body.Meta.PerPage > 0 && page*body.Meta.PerPage < body.Meta.Count)
}

// tableOf lays a document out as a header and rows. A list with no items has
// no header.
func tableOf(doc tenjinapi.Document, v view) (header []string, rows [][]string, err error) {
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(doc, &body); err != nil {
		return nil, nil, clierr.Wrap(clierr.Upstream, err, "the Tenjin API returned something that is not a JSON:API document")
	}
	data := bytes.TrimSpace(body.Data)
	if len(data) > 0 && data[0] == '[' {
		var items []item
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, nil, clierr.Wrap(clierr.Upstream, err, "the Tenjin API returned a list that is not one")
		}
		header, rows = itemRows(items, v)
		return header, rows, nil
	}
	var one item
	if err := json.Unmarshal(data, &one); err != nil || len(data) == 0 {
		return nil, nil, clierr.New(clierr.Upstream, "the Tenjin API's answer holds no data")
	}
	header, rows = itemFields(one)
	return header, rows, nil
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// widest is the width of a text's longest line, colour codes left out.
func widest(text string) int {
	most := 0
	for _, line := range strings.Split(ansi.ReplaceAllString(text, ""), "\n") {
		most = max(most, utf8.RuneCountInString(line))
	}
	return most
}

func itemRows(items []item, v view) ([]string, [][]string) {
	if len(items) == 0 {
		return nil, nil
	}
	columns := v.columns
	if len(columns) == 0 {
		// Every attribute, but the image addresses reports carry for the
		// dashboard: they make a table too wide to read. --json keeps them.
		for _, k := range orderedKeys(items[0].Attributes) {
			if !strings.HasSuffix(k, "icon_url") {
				columns = append(columns, k)
			}
		}
	}
	header := make([]string, 0, len(columns)+1)
	for _, c := range columns {
		header = append(header, strings.ReplaceAll(render.Clean(c), "_", " "))
	}
	hasID := len(bytes.TrimSpace(items[0].ID)) > 0
	if hasID {
		header = append(header, "id")
	}
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		var attrs map[string]json.RawMessage
		_ = json.Unmarshal(it.Attributes, &attrs)
		row := make([]string, 0, len(header))
		for _, c := range columns {
			row = append(row, cell(attrs[c]))
		}
		if hasID {
			row = append(row, cell(it.ID))
		}
		rows = append(rows, row)
	}
	return header, rows
}

// itemFields lays one resource out as its ID, then every attribute in the
// API's order.
func itemFields(it item) ([]string, [][]string) {
	var attrs map[string]json.RawMessage
	_ = json.Unmarshal(it.Attributes, &attrs)
	rows := [][]string{}
	if len(bytes.TrimSpace(it.ID)) > 0 {
		rows = append(rows, []string{"id", cell(it.ID)})
	}
	for _, k := range orderedKeys(it.Attributes) {
		rows = append(rows, []string{strings.ReplaceAll(render.Clean(k), "_", " "), cell(attrs[k])})
	}
	return []string{"field", "value"}, rows
}

// orderedKeys returns an object's keys in the order they were sent.
func orderedKeys(raw json.RawMessage) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var keys []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return keys
		}
		key, _ := t.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}

// cell is a JSON value as it reads in a table, cleaned for a terminal.
func cell(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0, string(raw) == "null":
		return ""
	case raw[0] == '"':
		var s string
		_ = json.Unmarshal(raw, &s)
		return render.Clean(s)
	case string(raw) == "true":
		return "yes"
	case string(raw) == "false":
		return "no"
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return render.Clean(string(raw))
	}
	return render.Clean(compact.String())
}

// appFlag adds the --app flag the app's sub-resources need.
func appFlag(app *string) func(*pflag.FlagSet) {
	return func(f *pflag.FlagSet) {
		f.StringVar(app, "app", "", "the app's ID, from 'tenjin apps list' (required)")
	}
}

// optionalInt is a number flag that is sent only when given, so that 0, the
// Organic channel, can be asked for.
type optionalInt struct{ n *int }

func (o *optionalInt) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("%q is not a number", s)
	}
	o.n = &n
	return nil
}

func (o *optionalInt) String() string {
	if o.n == nil {
		return ""
	}
	return strconv.Itoa(*o.n)
}

func (o *optionalInt) Type() string { return "int" }

// optionalChannel adds a --channel that narrows a list; 0 is Organic.
func optionalChannel(f *pflag.FlagSet, channel *optionalInt, whose string) {
	f.Var(channel, "channel", "only this marketing channel's "+whose+", by its ID; 0 is Organic")
}

func requireApp(app string) error {
	if app == "" {
		return clierr.New(clierr.Usage, "--app is required").WithHint("Find the app's ID with 'tenjin apps list'.")
	}
	return nil
}

func requireChannel(channel int) error {
	if channel <= 0 {
		return clierr.New(clierr.Usage, "--channel is required").WithHint("Find the channel's ID with 'tenjin channels list'.")
	}
	return nil
}

func newCampaignsCmd(opts *options, env *environment) *cobra.Command {
	var q tenjinapi.CampaignQuery
	var channel optionalInt
	cmd := group("campaigns", "Your campaigns",
		dataCmd(opts, env, dataSpec{
			use: "list", short: "List campaigns", paged: true,
			view: view{noun: "campaigns", columns: []string{"name", "platform", "channel_id", "app_id"}},
			flags: func(f *pflag.FlagSet) {
				f.StringVar(&q.App, "app", "", "only this app's campaigns, by its ID")
				optionalChannel(f, &channel, "campaigns")
				f.StringVar(&q.Platform, "platform", "", "only campaigns on this platform (ios, android, amazon, android_other)")
				f.StringVar(&q.Query, "query", "", "only campaigns whose name matches")
			},
			fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
				q.Channel = channel.n
				return api.Campaigns(ctx, q, p)
			},
		}),
		dataCmd(opts, env, dataSpec{
			use: "get <campaign-id>", short: "Show one campaign, with its tracking links",
			args: exactArgs(1, "the campaign's ID, from 'tenjin campaigns list'"),
			fetch: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Paging) (tenjinapi.Document, error) {
				return api.Campaign(ctx, args[0])
			},
		}),
	)
	cmd.AddCommand(newCampaignWriteCmds(opts, env)...)
	return cmd
}

func newChannelsCmd(opts *options, env *environment) *cobra.Command {
	var query string
	return group("channels", "Marketing channels: ad networks and other sources",
		dataCmd(opts, env, dataSpec{
			use: "list", short: "List marketing channels", paged: true,
			view:  view{noun: "channels", columns: []string{"name", "short_id"}},
			flags: func(f *pflag.FlagSet) { f.StringVar(&query, "query", "", "only channels whose name matches") },
			fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
				return api.Channels(ctx, query, p)
			},
		}),
		dataCmd(opts, env, dataSpec{
			use: "get <channel-id>", short: "Show one marketing channel",
			args: exactArgs(1, "the channel's ID, from 'tenjin channels list'"),
			fetch: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Paging) (tenjinapi.Document, error) {
				id, err := tenjinapi.ChannelID(args[0])
				if err != nil {
					return nil, err
				}
				return api.Channel(ctx, id)
			},
		}),
		dataCmd(opts, env, dataSpec{
			use: "callback-group-templates <channel-id>", short: "List a channel's callback group templates", paged: true,
			args: exactArgs(1, "the channel's ID, from 'tenjin channels list'"),
			view: view{noun: "callback group templates", columns: []string{"name", "event", "platform"}},
			fetch: func(ctx context.Context, api *tenjinapi.Client, args []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
				id, err := tenjinapi.ChannelID(args[0])
				if err != nil {
					return nil, err
				}
				return api.CallbackGroupTemplates(ctx, id, p)
			},
		}),
	)
}

func newAdAccountsCmd(opts *options, env *environment) *cobra.Command {
	var channel optionalInt
	var query string
	return group("ad-accounts", "The ad network accounts Tenjin reads spend from",
		dataCmd(opts, env, dataSpec{
			use: "list", short: "List ad accounts", paged: true,
			view: view{noun: "ad accounts", columns: []string{"name", "channel_id"}},
			flags: func(f *pflag.FlagSet) {
				optionalChannel(f, &channel, "accounts")
				f.StringVar(&query, "query", "", "only accounts whose name matches")
			},
			fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
				return api.AdAccounts(ctx, channel.n, query, p)
			},
		}),
		dataCmd(opts, env, dataSpec{
			use: "get <ad-account-id>", short: "Show one ad account",
			args: exactArgs(1, "the ad account's ID, from 'tenjin ad-accounts list'"),
			fetch: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Paging) (tenjinapi.Document, error) {
				return api.AdAccount(ctx, args[0])
			},
		}),
	)
}

func newSKAdNetworksCmd(opts *options, env *environment) *cobra.Command {
	return group("skadnetworks", "SKAdNetwork IDs Tenjin knows",
		dataCmd(opts, env, dataSpec{
			use: "list", short: "List SKAdNetwork IDs", paged: true,
			view: view{noun: "SKAdNetwork IDs"},
			fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
				return api.SKAdNetworks(ctx, p)
			},
		}),
		dataCmd(opts, env, dataSpec{
			use: "get <skadnetwork-id>", short: "Show one SKAdNetwork ID, such as 4fzdc2evr5.skadnetwork",
			args: exactArgs(1, "the SKAdNetwork ID, such as 4fzdc2evr5.skadnetwork"),
			fetch: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Paging) (tenjinapi.Document, error) {
				return api.SKAdNetwork(ctx, args[0])
			},
		}),
	)
}

// appDataCmds are the reads of an app's own resources, under 'tenjin apps'.
func appDataCmds(opts *options, env *environment) []*cobra.Command {
	var app string
	var channel int
	var narrow optionalInt
	channelFlag := func(f *pflag.FlagSet, required bool) {
		if !required {
			optionalChannel(f, &narrow, "ones")
			return
		}
		f.IntVar(&channel, "channel", 0, "the marketing channel's ID, from 'tenjin channels list' (required)")
	}
	return []*cobra.Command{
		group("events", "The events an app has sent",
			dataCmd(opts, env, dataSpec{
				use: "list --app <app-id>", short: "List the events an app has sent", paged: true,
				view: view{noun: "events", columns: []string{"name", "event"}}, flags: appFlag(&app),
				fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
					if err := requireApp(app); err != nil {
						return nil, err
					}
					return api.AppEvents(ctx, app, p)
				},
			}),
		),
		group("callbacks", "An app's callbacks to marketing channels",
			dataCmd(opts, env, dataSpec{
				use: "list --app <app-id>", short: "List an app's callbacks", paged: true,
				view:  view{noun: "callbacks", columns: []string{"name", "event", "channel_id", "active"}},
				flags: func(f *pflag.FlagSet) { appFlag(&app)(f); channelFlag(f, false) },
				fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
					if err := requireApp(app); err != nil {
						return nil, err
					}
					return api.AppCallbacks(ctx, app, narrow.n, p)
				},
			}),
			dataCmd(opts, env, dataSpec{
				use: "get <callback-id> --app <app-id>", short: "Show one of an app's callbacks",
				args: exactArgs(1, "the callback's ID, from 'tenjin apps callbacks list'"), flags: appFlag(&app),
				fetch: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Paging) (tenjinapi.Document, error) {
					if err := requireApp(app); err != nil {
						return nil, err
					}
					return api.AppCallback(ctx, app, args[0])
				},
			}),
		),
		group("s2s-callbacks", "An app's server-to-server callbacks",
			dataCmd(opts, env, dataSpec{
				use: "list --app <app-id>", short: "List an app's server-to-server callbacks", paged: true,
				view: view{noun: "server-to-server callbacks", columns: []string{"name", "event", "http_method", "active"}}, flags: appFlag(&app),
				fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
					if err := requireApp(app); err != nil {
						return nil, err
					}
					return api.AppS2SCallbacks(ctx, app, p)
				},
			}),
			dataCmd(opts, env, dataSpec{
				use: "get <callback-id> --app <app-id>", short: "Show one of an app's server-to-server callbacks",
				args: exactArgs(1, "the callback's ID, from 'tenjin apps s2s-callbacks list'"), flags: appFlag(&app),
				fetch: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Paging) (tenjinapi.Document, error) {
					if err := requireApp(app); err != nil {
						return nil, err
					}
					return api.AppS2SCallback(ctx, app, args[0])
				},
			}),
		),
		group("callback-templates", "The callback templates an app can use",
			dataCmd(opts, env, dataSpec{
				use: "list --app <app-id>", short: "List the callback templates an app can use", paged: true,
				view:  view{noun: "callback templates", columns: []string{"name", "event", "channel_id"}},
				flags: func(f *pflag.FlagSet) { appFlag(&app)(f); channelFlag(f, false) },
				fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
					if err := requireApp(app); err != nil {
						return nil, err
					}
					return api.AppCallbackTemplates(ctx, app, narrow.n, p)
				},
			}),
			dataCmd(opts, env, dataSpec{
				use: "get <template-id> --app <app-id>", short: "Show one callback template",
				args: exactArgs(1, "the template's ID, from 'tenjin apps callback-templates list'"), flags: appFlag(&app),
				fetch: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Paging) (tenjinapi.Document, error) {
					if err := requireApp(app); err != nil {
						return nil, err
					}
					return api.AppCallbackTemplate(ctx, app, args[0])
				},
			}),
		),
		group("callback-groups", "An app's callback groups, per marketing channel",
			dataCmd(opts, env, dataSpec{
				use: "list --app <app-id> --channel <channel-id>", short: "List an app's callback groups for one channel", paged: true,
				view:  view{noun: "callback groups", columns: []string{"name", "event", "active"}},
				flags: func(f *pflag.FlagSet) { appFlag(&app)(f); channelFlag(f, true) },
				fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
					if err := requireApp(app); err != nil {
						return nil, err
					}
					if err := requireChannel(channel); err != nil {
						return nil, err
					}
					return api.AppCallbackGroups(ctx, app, channel, p)
				},
			}),
			dataCmd(opts, env, dataSpec{
				use: "get <group-id> --app <app-id> --channel <channel-id>", short: "Show one of an app's callback groups",
				args:  exactArgs(1, "the group's ID, from 'tenjin apps callback-groups list'"),
				flags: func(f *pflag.FlagSet) { appFlag(&app)(f); channelFlag(f, true) },
				fetch: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Paging) (tenjinapi.Document, error) {
					if err := requireApp(app); err != nil {
						return nil, err
					}
					if err := requireChannel(channel); err != nil {
						return nil, err
					}
					return api.AppCallbackGroup(ctx, app, channel, args[0])
				},
			}),
		),
		group("callback-settings", "An app's callback settings, per marketing channel",
			dataCmd(opts, env, dataSpec{
				use: "get --app <app-id> --channel <channel-id>", short: "Show an app's callback settings for one channel",
				flags: func(f *pflag.FlagSet) { appFlag(&app)(f); channelFlag(f, true) },
				fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, _ tenjinapi.Paging) (tenjinapi.Document, error) {
					if err := requireApp(app); err != nil {
						return nil, err
					}
					if err := requireChannel(channel); err != nil {
						return nil, err
					}
					return api.AppCallbackSettings(ctx, app, channel)
				},
			}),
		),
	}
}

func newReportsCmd(opts *options, env *environment) *cobra.Command {
	report := func(name tenjinapi.Report, short, granularities, groupings string) *cobra.Command {
		var q tenjinapi.ReportQuery
		var apps, channels, metrics []string
		return dataCmd(opts, env, dataSpec{
			use: string(name) + " --from <date> --to <date>", short: short, paged: true, allPages: true,
			long: short + `, for the dates from --from to --to (YYYY-MM-DD), both included.
Every row is shown: --page and --per-page show one page instead.

The other flags take the API's own values and are passed as they are:
--granularity is one of ` + granularities + `, and --group-by one of ` + groupings + `.
--app, --channel and --metrics can be repeated or given comma-separated; find
metric names with 'tenjin reports metrics'. Left out, each is the API's
default.`,
			view: view{noun: "rows"},
			flags: func(f *pflag.FlagSet) {
				f.StringVar(&q.From, "from", "", "the first day, YYYY-MM-DD (required)")
				f.StringVar(&q.To, "to", "", "the last day, YYYY-MM-DD (required)")
				f.StringVar(&q.Granularity, "granularity", "", "how the days are grouped: "+granularities)
				f.StringVar(&q.GroupBy, "group-by", "", "what each row is: "+groupings)
				f.StringSliceVar(&apps, "app", nil, "only these apps, by ID")
				f.StringSliceVar(&channels, "channel", nil, "only these marketing channels, by ID")
				f.StringSliceVar(&metrics, "metrics", nil, "the metrics to report, from 'tenjin reports metrics'")
			},
			fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
				q.Apps, q.Channels, q.Metrics = strings.Join(apps, ","), strings.Join(channels, ","), strings.Join(metrics, ",")
				if q.From == "" || q.To == "" {
					return nil, clierr.New(clierr.Usage, "--from and --to are required").WithHint("For example: --from 2026-10-01 --to 2026-10-07")
				}
				return api.Report(ctx, name, q, p)
			},
		})
	}
	return group("reports", "Spend, ad revenue and SKAdNetwork reports",
		newMetricsCmd(opts),
		report(tenjinapi.ReportSpend, "User acquisition: spend, installs and what they returned", "daily, weekly, monthly, totals-daily", "app, channel, country, site, campaign, campaign,country, channel,app, channel,app,country, creative"),
		report(tenjinapi.ReportAdRevenue, "Ad revenue, by ad network", "daily, weekly, monthly, totals-daily", "app, channel, country, channel,app, channel,app,country, ad_placement"),
		report(tenjinapi.ReportSKAdNetwork, "SKAdNetwork postbacks, as Tenjin attributes them", "daily", "app, channel, campaign, sk_ad_network, sk_campaign, sk_source_app, fidelity_type, conversion_value"),
		report(tenjinapi.ReportSKAN, "SKAdNetwork postbacks, as Apple sends them", "daily", "app, channel, campaign, sk_ad_network, sk_campaign, sk_source_app, fidelity_type, conversion_value"),
	)
}

func newSavedReportsCmd(opts *options, env *environment) *cobra.Command {
	var reportType string
	cmd := group("saved-reports", "Reports saved in the dashboard",
		dataCmd(opts, env, dataSpec{
			use: "list", short: "List saved reports", paged: true,
			view: view{noun: "saved reports", columns: []string{"name", "report_type"}},
			flags: func(f *pflag.FlagSet) {
				f.StringVar(&reportType, "type", "", "only this type: user_acquisition or ad_revenue")
			},
			fetch: func(ctx context.Context, api *tenjinapi.Client, _ []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
				return api.SavedReports(ctx, reportType, p)
			},
		}),
		dataCmd(opts, env, dataSpec{
			use: "get <saved-report-id>", short: "Show a saved report's settings",
			args: exactArgs(1, "the saved report's ID, from 'tenjin saved-reports list'"),
			fetch: func(ctx context.Context, api *tenjinapi.Client, args []string, _ tenjinapi.Paging) (tenjinapi.Document, error) {
				return api.SavedReport(ctx, args[0])
			},
		}),
		dataCmd(opts, env, dataSpec{
			use: "run <saved-report-id>", short: "Pull a saved report's data", paged: true, allPages: true,
			long: `Pull a saved report's data. Every row is shown: --page and --per-page show
one page instead.`,
			args: exactArgs(1, "the saved report's ID, from 'tenjin saved-reports list'"),
			view: view{noun: "rows"},
			fetch: func(ctx context.Context, api *tenjinapi.Client, args []string, p tenjinapi.Paging) (tenjinapi.Document, error) {
				return api.RunSavedReport(ctx, args[0], p)
			},
		}),
	)
	cmd.AddCommand(newSavedReportWriteCmds(opts, env)...)
	return cmd
}
