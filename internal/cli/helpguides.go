package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// guidesTitle heads the list of guides in 'tenjin --help'.
const guidesTitle = "Guides, step by step:"

// A guide walks through one task with the commands it takes, where --help is
// the reference for each command. A command with no action of its own is a
// help topic to cobra: 'tenjin help callbacks' prints it, and 'tenjin --help'
// lists it. TestTheHelpTextsOnlyNameCommandsThatExist checks every command
// and flag a guide names, and TestTheGuides that each is listed.
var helpGuides = []struct {
	name, short, text string
	// data marks a guide to the data commands, there only when they are.
	data bool
}{
	{"integration", "Add the SDK to an app, and check a launch", `Add the Tenjin SDK to an app, check it, and check a real launch.

The guided way, in the app's project, from sign-in to an install Tenjin has
processed:

  tenjin setup

It finds the project's Tenjin app (creating it after asking), fetches its SDK
key, offers to have your coding agent (Claude Code or Codex) make the changes,
checks them, and asks you to launch a debug build.

The same, step by step:

  tenjin project detect
      what the project is: framework, platforms, bundle IDs
  tenjin sdk guide android
      the integration guide (also ios, flutter, react-native, capacitor,
      unity)
  tenjin doctor --offline
      what is missing, each with a file, a line and a fix
  tenjin login
      sign in, for the steps that need your Tenjin account
  tenjin apps ensure --bundle-id <bundle-id> --platform android
      find the build's Tenjin app, or create it (one per bundle ID and store)
  tenjin project set-keys
      write the SDK keys where placeholders were left for them
  tenjin doctor
      the same checks, and each SDK key checked with Tenjin
  tenjin doctor --device
      after launching a debug build: what Tenjin answered

doctor offers to have your coding agent fix what it finds; --agent claude does
it without asking. When the SDK is behind, the upgrade is planned the same way:

  tenjin sdk upgrade
      the newest release, what it changes, and who makes the change

To teach your coding agent these commands:

  tenjin agent install

An agent never gets an SDK key: it writes a placeholder, and setup or set-keys
fills it.`, false},

	{"callbacks", "Send installs and events to ad networks or a server", `Callbacks tell an ad network, or a server of your own, about an app's installs
and events. There are three kinds:

  callbacks          to an ad network, each from one of that network's templates
  callback groups    an ad network's callbacks set up together, from a group
                     template, with settings they share
  S2S callbacks      to an address of your own

To an ad network, step by step:

  1. Find the network's channel ID:
       tenjin channels list --query lifestreet
  2. See the callbacks the app already has for it: a callback cannot be
     deleted, so one made twice stays twice.
       tenjin apps callbacks list --app <app-id> --channel <channel-id>
  3. Pick a template, and read the parameters it needs:
       tenjin apps callback-templates list --app <app-id> --channel <channel-id>
       tenjin apps callback-templates get <template-id> --app <app-id>
  4. Create the callback switched off, and see the request first:
       tenjin apps callbacks create --app <app-id> --template <template-id> --param event_name=purchase --active=false --dry-run
     Then the same without --dry-run.
  5. Switch it on once it is right:
       tenjin apps callbacks update <callback-id> --app <app-id> --active

A callback group instead:

  1. See the network's group templates, the groups the app already has, and
     the settings each still lacks:
       tenjin apps callback-groups inspect --app <app-id> --channel <channel-id>
  2. If the app has no group from the template, create one switched off:
       tenjin apps callback-groups create --app <app-id> --channel <channel-id> --template <group-template-id> --event open --active=false
     If it has one, change that group instead: where a template allows one
     group per app, create changes the app's group with what it sends, so
     --active=false would switch a live group off.
       tenjin apps callback-groups update <group-id> --app <app-id> --channel <channel-id> --setting key=value
  3. Give the settings the network's groups share:
       tenjin apps callback-settings update --app <app-id> --channel <channel-id> --setting key=value
  4. Switch the group on once inspect shows it lacks nothing (unknown is not
     nothing: it means the group's template could not be read):
       tenjin apps callback-groups update <group-id> --app <app-id> --channel <channel-id> --active

To your own server:

  tenjin apps s2s-callbacks macros country      what an address can carry, such as {{country}}
  tenjin apps s2s-callbacks create --app <app-id> --url "https://example.com/postback?c={{country}}" --event open --name Installs --action ping --active=false

A callback cannot be deleted, only switched off (update --active=false), so
create it switched off and switch it on once it is right. Every change asks
first; --yes skips the question, and --dry-run sends nothing.`, true},

	{"reporting", "Read spend, ad revenue and SKAdNetwork reports", `Read Tenjin's reports from the terminal, as a table, CSV or JSON.

  tenjin reports spend --from 2026-10-01 --to 2026-10-07
  tenjin reports spend --from 2026-10-01 --to 2026-10-07 --group-by channel,app --granularity totals-daily
  tenjin reports ad-revenue --from 2026-10-01 --to 2026-10-07
  tenjin reports skan --from 2026-10-01 --to 2026-10-07

--group-by and --granularity take the values each report's --help lists.
--app and --channel narrow it, each repeated or comma-separated. --metrics
picks the columns; the names are here (--report is user_acquisition for
spend, ad_monetization for ad-revenue, skan for skadnetwork and skan):

  tenjin reports metrics roi
  tenjin reports metrics --report user_acquisition

A report reads every page. -o csv writes it for a spreadsheet, and --json
prints Tenjin's own answer:

  tenjin reports spend --from 2026-10-01 --to 2026-10-07 -o csv > spend.csv

Reports saved in the dashboard:

  tenjin saved-reports list
  tenjin saved-reports run <saved-report-id>`, true},

	{"scripting", "Drive the CLI from a script, CI or a coding agent", scriptingGuide(false), false},
}

// scriptingGuide is the scripting guide, with the flags of the data
// commands when they are on.
func scriptingGuide(data bool) string {
	var b strings.Builder
	b.WriteString(`Every command but setup is built to be driven by a program.

  --json             only the result on stdout, and errors on stderr as
                     {"error": {"code", "message", "hint"}}
`)
	if data {
		b.WriteString(`  -o csv             for the data commands, a table for a spreadsheet
`)
	}
	b.WriteString(`  --yes              a change without the question: an app created, the
                     agent's instructions written or removed`)
	if data {
		b.WriteString(`, or a
                     change to your Tenjin data`)
	}
	b.WriteString(`; without it
                     and without a terminal, nothing is changed
  --dry-run          what a change would write or send, and nothing changed
`)
	b.WriteString(`  --verbose          each request on stderr, never a credential

A command never prompts when stdin is not a terminal. Signing out asks
nothing. In CI, set TENJIN_API_TOKEN to an API token from the Tenjin
dashboard instead of 'tenjin login'.

The exit status says what happened:

  0     success
  1     an unexpected error
  2     wrong usage
  3     not signed in
  4     your role does not allow it
  5     not found
  6     rate limited
  7     Tenjin is unavailable
  9     doctor found problems
  10    already exists
  11    Tenjin refused the request, such as a value it does not accept
  12    another tenjin command was using the sign-in, and nothing was
        done: run it again (one that had changed something exits 7)
  130   stopped: Ctrl-C, SIGTERM or SIGHUP

For a coding agent (Claude Code, Codex, Cursor and others):

  tenjin agent install       teach it the CLI, in this project
  tenjin agent skill         the same instructions, for any other agent

An agent that can run tenjin acts as you. Prefer 'tenjin login' to giving it
TENJIN_API_TOKEN, so the token is not in its environment or its logs;
'tenjin logout' ends that access.`)
	return b.String()
}

// newGuideCmds returns the guides as help topics.
func newGuideCmds(data bool) []*cobra.Command {
	cmds := make([]*cobra.Command, 0, len(helpGuides))
	for _, g := range helpGuides {
		switch {
		case g.data && !data:
			continue
		case g.name == "scripting":
			g.text = scriptingGuide(data)
		}
		cmds = append(cmds, &cobra.Command{Use: g.name, Short: g.short, Long: g.text})
	}
	return cmds
}

// rootUsageTemplate is cobra's usage template with the guides listed after
// the commands, as 'tenjin help <guide>', which is how one is opened.
const rootUsageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Other commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasHelpSubCommands}}

` + guidesTitle + `{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad (print "tenjin help " .Name) 25}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsagesWrapped 80 | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsagesWrapped 80 | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`

// commandHelp adds to a command's help: a description where it has none,
// and examples. Keys are command paths below 'tenjin'.
// TestTheHelpTextsOnlyNameCommandsThatExist checks the paths, and the
// commands and flags the texts name.
var commandHelp = map[string]struct{ long, example string }{
	"apps": {long: `Your apps, and their keys, events and callbacks. A command about one app
takes its ID, from 'tenjin apps list'.

'tenjin help callbacks' walks through setting up callbacks.`},
	"apps callbacks": {long: `Callbacks send an app's installs and events to an ad network. Each one comes
from one of the network's callback templates, with the parameters that
template needs: 'tenjin apps callback-templates get' shows them. Several set
up together are a callback group: 'tenjin apps callback-groups'. To an
address of your own, use 'tenjin apps s2s-callbacks'.

A callback cannot be deleted, only switched off.
Step by step: 'tenjin help callbacks'.`},
	"apps callbacks list": {example: `  tenjin apps callbacks list --app <app-id>
  tenjin apps callbacks list --app <app-id> --channel <channel-id>`},
	"apps callbacks create": {example: `  # The network's templates, and the parameters of the one to use
  tenjin apps callback-templates list --app <app-id> --channel <channel-id>
  tenjin apps callback-templates get <template-id> --app <app-id>

  # The callback, switched off; the request is shown and not sent
  tenjin apps callbacks create --app <app-id> --template <template-id> --param event_name=purchase --active=false --dry-run`},
	"apps callbacks update": {example: `  tenjin apps callbacks update <callback-id> --app <app-id> --active
  tenjin apps callbacks update <callback-id> --app <app-id> --active=false`},

	"apps s2s-callbacks": {long: `Server-to-server callbacks send an app's installs and events to an address of
your own. The address can carry macros, such as {{country}}; the list is in
'tenjin apps s2s-callbacks macros'. For an ad network, use
'tenjin apps callbacks'.

A callback cannot be deleted, only switched off.
Step by step: 'tenjin help callbacks'.`},
	"apps s2s-callbacks create": {example: `  tenjin apps s2s-callbacks macros country
  tenjin apps s2s-callbacks create --app <app-id> --url "https://example.com/postback?c={{country}}" --event open --name Installs --action ping --active=false`},
	"apps s2s-callbacks update": {example: `  tenjin apps s2s-callbacks update <callback-id> --app <app-id> --active`},

	"apps callback-templates": {long: `An ad network's callback templates: the callbacks it accepts, each with the
parameters it needs ('get' shows them). 'tenjin apps callbacks create' makes
a callback from one.
Step by step: 'tenjin help callbacks'.`},
	"apps callback-templates list": {example: `  tenjin channels list --query lifestreet
  tenjin apps callback-templates list --app <app-id> --channel <channel-id>`},

	"apps callback-groups": {long: `A callback group is an ad network's callbacks set up together, from one of
the network's group templates, with settings they share. The group templates
are in 'tenjin channels callback-group-templates', the shared settings in
'tenjin apps callback-settings'. 'inspect' shows, for one network, its group
templates, the app's groups with the settings each still lacks, the shared
settings and the app's events. Where a template allows one group per app,
'create' changes the app's group if it has one.
Step by step: 'tenjin help callbacks'.`},
	"apps callback-groups inspect": {example: `  tenjin apps callback-groups inspect --app <app-id> --channel <channel-id>`},
	"apps callback-groups create": {example: `  # The group templates, and the groups the app already has
  tenjin apps callback-groups inspect --app <app-id> --channel <channel-id>

  # A group, switched off; the request is shown and not sent
  tenjin apps callback-groups create --app <app-id> --channel <channel-id> --template <group-template-id> --event open --active=false --dry-run`},
	"apps callback-groups update": {example: `  tenjin apps callback-groups update <group-id> --app <app-id> --channel <channel-id> --active
  tenjin apps callback-groups update <group-id> --app <app-id> --channel <channel-id> --setting key=value`},

	"apps callback-settings": {long: `The settings an app's callback groups to one ad network share.
'tenjin apps callback-groups inspect' shows which ones each group still needs.
Step by step: 'tenjin help callbacks'.`},
	"apps callback-settings update": {example: `  tenjin apps callback-settings update --app <app-id> --channel <channel-id> --setting key=value --dry-run`},

	"campaigns create": {example: `  tenjin channels list --query facebook
  tenjin campaigns create --app <app-id> --name "Summer UA" --channel <channel-id> --dry-run`},
	"campaigns list": {example: `  tenjin campaigns list --app <app-id>
  tenjin campaigns list --all --json`},

	"reports": {long: `Tenjin's reports, as a table, CSV (-o csv) or JSON. Each reads every page.
Step by step: 'tenjin help reporting'.`},
	"reports spend": {example: `  tenjin reports spend --from 2026-10-01 --to 2026-10-07
  tenjin reports spend --from 2026-10-01 --to 2026-10-07 --group-by channel,app --granularity totals-daily -o csv`},
	"reports metrics": {example: `  tenjin reports metrics roi
  tenjin reports metrics --report user_acquisition`},

	"saved-reports run": {example: `  tenjin saved-reports list
  tenjin saved-reports run <saved-report-id> -o csv`},
	"site-ids block": {example: `  tenjin site-ids block <site-id> --app <app-id> --dry-run`},
}

// applyCommandHelp sets commandHelp's texts on the commands under root.
func applyCommandHelp(root *cobra.Command) {
	for path, h := range commandHelp {
		cmd, _, err := root.Find(strings.Fields(path))
		if err != nil || cmd.CommandPath() != "tenjin "+path {
			continue // not there without the data commands, or a mistake TestTheHelpTextsOnlyNameCommandsThatExist reports
		}
		if cmd.Long == "" && h.long != "" {
			cmd.Long = h.long
		}
		cmd.Example = h.example
	}
}
