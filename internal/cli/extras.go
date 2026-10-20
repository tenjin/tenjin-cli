package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/catalog"
	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi"
)

// What the Tenjin MCP server offers besides the API's reads and writes: site
// ID blocking, the documentation, the metric and callback macro catalogs,
// and one view of an app's integration with a channel.

func newSiteIDsCmd(opts *options, env *environment) *cobra.Command {
	var app, campaign string
	var channel uint32
	var replace, yes, dryRun bool
	block := &cobra.Command{
		Use:   "block <site-id>...",
		Short: "Stop attributing installs from these site IDs",
		Long: `Block site IDs: Tenjin stops attributing installs from them. A rule blocks a
site ID everywhere unless --app, --campaign or --channel narrows it to one.
--replace makes these rules the only ones, removing every other rule at once.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return clierr.New(clierr.Usage, "%q takes one or more site IDs", cmd.CommandPath())
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if err := tenjinapi.CheckSiteIDs(args); err != nil {
				return err
			}
			rules := make([]tenjinapi.SiteIDRule, 0, len(args))
			for _, id := range args {
				rule := tenjinapi.SiteIDRule{SiteID: id, AppID: app, CampaignID: campaign}
				if cmd.Flags().Changed("channel") {
					rule.Channel = &channel
				}
				rules = append(rules, rule)
			}
			for _, check := range []struct{ value, what string }{{app, "app"}, {campaign, "campaign"}} {
				if check.value != "" {
					if _, err := tenjinapi.ParseID(check.value, check.what); err != nil {
						return err
					}
				}
			}
			what := "Block " + strings.Join(args, ", ") + scope(app, campaign, cmd.Flags().Changed("channel"), channel)
			if replace {
				what += ", removing every other rule"
			}
			if dryRun {
				planned, err := env.api().Plan(func(api *tenjinapi.Client) error {
					_, err := api.BlockSiteIDs(cmd.Context(), rules, replace)
					return err
				})
				if err != nil {
					return err
				}
				return writeDryRun(cmd, what, planned, map[string]any{"rules": rules, "clear": replace}, false)
			}
			if err := confirmChange(cmd, env, yes, what, replace); err != nil {
				return err
			}
			doc, err := env.api().BlockSiteIDs(cmd.Context(), rules, replace)
			return writeSiteIDAnswer(cmd.OutOrStdout(), format, doc, err, "Blocked.")
		},
	}
	block.Flags().StringVar(&app, "app", "", "only for this app, by its ID")
	block.Flags().StringVar(&campaign, "campaign", "", "only for this campaign, by its ID")
	block.Flags().Uint32Var(&channel, "channel", 0, "only on this marketing channel, by its ID")
	block.Flags().BoolVar(&replace, "replace", false, "make these the only rules")
	addChangeFlags(block, &yes, &dryRun)

	var reinstateYes, reinstateDry bool
	reinstate := &cobra.Command{
		Use:   "reinstate <site-id>...",
		Short: "Attribute installs from these site IDs again",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return clierr.New(clierr.Usage, "%q takes one or more site IDs", cmd.CommandPath())
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if err := tenjinapi.CheckSiteIDs(args); err != nil {
				return err
			}
			what := "Remove every rule for " + strings.Join(args, ", ")
			if reinstateDry {
				planned, err := env.api().Plan(func(api *tenjinapi.Client) error {
					_, err := api.ReinstateSiteIDs(cmd.Context(), args)
					return err
				})
				if err != nil {
					return err
				}
				return writeDryRun(cmd, what, planned, map[string]any{"siteIds": args}, false)
			}
			if err := confirmChange(cmd, env, reinstateYes, what, false); err != nil {
				return err
			}
			doc, err := env.api().ReinstateSiteIDs(cmd.Context(), args)
			return writeSiteIDAnswer(cmd.OutOrStdout(), format, doc, err, "Reinstated.")
		},
	}
	addChangeFlags(reinstate, &reinstateYes, &reinstateDry)

	var clearYes bool
	clear := &cobra.Command{
		Use:   "clear",
		Short: "Remove every site ID rule",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if err := confirmChange(cmd, env, clearYes, "Remove every site ID rule of the organization", true); err != nil {
				return err
			}
			doc, err := env.api().ClearSiteIDs(cmd.Context())
			return writeSiteIDAnswer(cmd.OutOrStdout(), format, doc, err, "Cleared.")
		},
	}
	clear.Flags().BoolVarP(&clearYes, "yes", "y", false, "do it without asking")

	list := &cobra.Command{
		Use:   "list",
		Short: "List the site ID rules",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			doc, err := env.api().SiteIDRules(cmd.Context())
			if err != nil {
				return err
			}
			if format == render.JSON {
				return writeRawJSON(cmd.OutOrStdout(), doc)
			}
			return writeSiteIDRules(cmd.OutOrStdout(), doc)
		},
	}
	return group("site-ids", "Site ID Optimization: block the sites installs should not be attributed to", list, block, reinstate, clear)
}

func addChangeFlags(cmd *cobra.Command, yes, dryRun *bool) {
	cmd.Flags().BoolVarP(yes, "yes", "y", false, "do it without asking")
	cmd.Flags().BoolVar(dryRun, "dry-run", false, "say the request it would send, print its body, and send nothing")
}

func scope(app, campaign string, byChannel bool, channel uint32) string {
	var parts []string
	if app != "" {
		parts = append(parts, "app "+app)
	}
	if campaign != "" {
		parts = append(parts, "campaign "+campaign)
	}
	if byChannel {
		parts = append(parts, fmt.Sprintf("channel %d", channel))
	}
	if len(parts) == 0 {
		return " everywhere"
	}
	return " for " + strings.Join(parts, ", ")
}

func writeSiteIDAnswer(w io.Writer, format render.Format, doc tenjinapi.Document, err error, done string) error {
	if err != nil {
		return err
	}
	if format == render.JSON {
		return writeRawJSON(w, doc)
	}
	_, err = fmt.Fprintln(w, done)
	return err
}

func writeSiteIDRules(w io.Writer, doc tenjinapi.Document) error {
	var body struct {
		Rules []tenjinapi.SiteIDRule `json:"rules"`
	}
	if err := json.Unmarshal(doc, &body); err != nil {
		return clierr.Wrap(clierr.Upstream, err, "the Site ID Optimization service answered with something unexpected")
	}
	if len(body.Rules) == 0 {
		_, err := fmt.Fprintln(w, "No site ID rules.")
		return err
	}
	rows := make([][]string, 0, len(body.Rules))
	for _, r := range body.Rules {
		channel := "all"
		if r.Channel != nil {
			channel = fmt.Sprint(*r.Channel)
		}
		rows = append(rows, []string{render.Clean(r.SiteID), render.Clean(r.AppID), render.Clean(r.CampaignID), channel, render.Clean(r.CreateTime)})
	}
	return render.WriteTable(w, render.NewStyler(w), []string{"site id", "app", "campaign", "channel", "created"}, rows)
}

func newDocsCmd(opts *options, env *environment) *cobra.Command {
	var limit int
	search := &cobra.Command{
		Use:   "search <words>...",
		Short: "Search Tenjin's documentation",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return clierr.New(clierr.Usage, "%q takes what to search for", cmd.CommandPath())
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if limit < 1 || limit > 50 {
				return clierr.New(clierr.Usage, "--limit is from 1 to 50")
			}
			api := env.api()
			results, err := tenjinapi.SearchDocs(cmd.Context(), api.HTTP, env.endpoints.Docs, userAgent(), strings.Join(args, " "), limit)
			if err != nil {
				return err
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), results)
			}
			if len(results) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "Nothing in the documentation matches.")
				return err
			}
			rows := make([][]string, 0, len(results))
			for _, r := range results {
				rows = append(rows, []string{render.Clean(r.Title), render.Clean(r.URL)})
			}
			return render.WriteTable(cmd.OutOrStdout(), render.NewStyler(cmd.OutOrStdout()), []string{"title", "address"}, rows)
		},
	}
	search.Flags().IntVar(&limit, "limit", 20, "how many pages, and how many FAQs, at most (up to 50)")
	return group("docs", "Tenjin's documentation", search)
}

func newMetricsCmd(opts *options) *cobra.Command {
	var category, report string
	cmd := &cobra.Command{
		Use:   "metrics [words]...",
		Short: "Find the metrics a report can show, for --metrics",
		Long: `Lists the metrics the reports can show: their ID (what --metrics takes), name
and meaning. Words narrow it, best match first; an N-day name such as roi_7d
finds its family, roi_Nd. --report keeps one report's: user_acquisition
(tenjin reports spend), ad_monetization (ad-revenue) or skan (skadnetwork,
skan).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			metrics := catalog.SearchMetrics(strings.Join(args, " "), category, report)
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), metrics)
			}
			if len(metrics) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No metric matches.")
				return err
			}
			rows := make([][]string, 0, len(metrics))
			for _, m := range metrics {
				rows = append(rows, []string{m.ID, m.Name, m.Report, m.Description})
			}
			return render.WriteTable(cmd.OutOrStdout(), render.NewStyler(cmd.OutOrStdout()), []string{"id", "name", "report", "meaning"}, rows)
		},
	}
	cmd.Flags().StringVar(&category, "category", "", "only this category, such as Cost, Revenue, Retention or SKAdNetwork")
	cmd.Flags().StringVar(&report, "report", "", "only this report's: user_acquisition, ad_monetization or skan")
	return cmd
}

func newMacrosCmd(opts *options) *cobra.Command {
	var category, provider string
	cmd := &cobra.Command{
		Use:   "macros [words]...",
		Short: "Find the macros a callback URL can use",
		Long: `Lists the macros Tenjin fills in a callback URL. Write one in double braces:
country is {{country}}. Words narrow the list, best match first.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			macros := catalog.SearchMacros(strings.Join(args, " "), category, provider)
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), macros)
			}
			if len(macros) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No macro matches.")
				return err
			}
			rows := make([][]string, 0, len(macros))
			for _, m := range macros {
				rows = append(rows, []string{"{{" + m.Macro + "}}", m.Category, m.Description})
			}
			return render.WriteTable(cmd.OutOrStdout(), render.NewStyler(cmd.OutOrStdout()), []string{"macro", "category", "meaning"}, rows)
		},
	}
	cmd.Flags().StringVar(&category, "category", "", "only this category: App, Attribution, Device Identifier, Device Properties, Event, Ad Impressions or Meta Attributions")
	cmd.Flags().StringVar(&provider, "provider", "", "only ad impression macros from this mediation provider: AppLovin, Unity LevelPlay, AdMob or Topon")
	return cmd
}

func newInspectIntegrationCmd(opts *options, env *environment) *cobra.Command {
	var app string
	var channel int
	cmd := &cobra.Command{
		Use:   "inspect --app <app-id> --channel <channel-id>",
		Short: "Show an app's callbacks to one channel, and what each group still needs",
		Long: `Shows, for one app and one marketing channel: the callback group templates
the channel offers, with the settings each requires; the app's callback groups,
with the settings each still lacks and whether it can be activated; the callback
settings the groups share; and the app's events. Run it before creating or
changing callback groups.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if err := requireApp(app); err != nil {
				return err
			}
			if err := requireChannel(channel); err != nil {
				return err
			}
			in, err := env.api().InspectIntegration(cmd.Context(), app, channel)
			if err != nil {
				return err
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), in)
			}
			return writeIntegration(cmd.OutOrStdout(), cmd.ErrOrStderr(), in)
		},
	}
	appFlag(&app)(cmd.Flags())
	cmd.Flags().IntVar(&channel, "channel", 0, "the marketing channel's ID, from 'tenjin channels list' (required)")
	return cmd
}

func writeIntegration(w, errw io.Writer, in *tenjinapi.Integration) error {
	style := render.NewStyler(w)
	text := func(v any) string {
		if v == nil {
			return ""
		}
		if l, ok := v.([]any); ok {
			parts := make([]string, 0, len(l))
			for _, e := range l {
				parts = append(parts, fmt.Sprint(e))
			}
			return render.Clean(strings.Join(parts, ", "))
		}
		if l, ok := v.([]string); ok {
			return render.Clean(strings.Join(l, ", "))
		}
		return render.Clean(fmt.Sprint(v))
	}
	_, _ = fmt.Fprintln(w, style.Heading("Callback group templates"))
	if len(in.Templates) == 0 {
		_, _ = fmt.Fprintln(w, "None.")
	} else {
		rows := [][]string{}
		for _, t := range in.Templates {
			rows = append(rows, []string{text(t["name"]), text(t["event"]), text(t["platform"]), text(t["required_settings"]), text(t["id"])})
		}
		if err := render.WriteTable(w, style, []string{"name", "event", "platform", "required settings", "id"}, rows); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintln(w, "\n"+style.Heading("The app's callback groups"))
	if len(in.Groups) == 0 {
		_, _ = fmt.Fprintln(w, "None.")
	} else {
		rows := [][]string{}
		for _, g := range in.Groups {
			activatable := "no"
			if g["activatable"] == true {
				activatable = "yes"
			}
			rows = append(rows, []string{text(g["name"]), text(g["event"]), fmt.Sprint(g["active"] == true), activatable, text(g["missing_settings"]), text(g["id"])})
		}
		if err := render.WriteTable(w, style, []string{"name", "event", "active", "activatable", "missing settings", "id"}, rows); err != nil {
			return err
		}
	}
	shared, _ := in.Settings["callback_settings"].(map[string]any)
	names := make([]string, 0, len(shared))
	for k := range shared {
		names = append(names, k)
	}
	sort.Strings(names)
	_, _ = fmt.Fprintf(w, "\n%s %s\n", style.Heading("Shared callback settings:"), orNone(render.CleanCell(strings.Join(names, ", "))))
	events := make([]string, 0, len(in.Events))
	for _, e := range in.Events {
		events = append(events, text(e["event"]))
	}
	_, _ = fmt.Fprintf(w, "%s %s\n", style.Heading("Events:"), orNone(render.CleanCell(strings.Join(events, ", "))))
	for _, warning := range in.Warnings {
		_, _ = fmt.Fprintln(errw, "Note: "+render.CleanCell(warning))
	}
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
