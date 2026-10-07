package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/skill"
)

// pluginsURL is where Tenjin's agent plugins, including the one that
// connects the MCP server, are published.
const pluginsURL = "https://github.com/tenjin/agent-plugins"

func newAgentCmd(opts *options, env *environment) *cobra.Command {
	return group("agent", "Teach your coding agent to use this CLI",
		newAgentInstallCmd(opts, env),
		newAgentUninstallCmd(opts, env),
		newAgentStatusCmd(opts, env),
		newAgentSkillCmd(opts),
	)
}

// agentFlags are shared by the commands that act on installed instructions.
type agentFlags struct {
	agents []string
	global bool
}

func (f *agentFlags) register(cmd *cobra.Command) {
	ids := make([]string, 0, len(skill.Targets))
	for _, t := range skill.Targets {
		ids = append(ids, t.ID)
	}
	cmd.Flags().StringSliceVar(&f.agents, "agent", nil, "which to act on: "+strings.Join(ids, ", ")+" (default: the ones found)")
	cmd.Flags().BoolVar(&f.global, "global", false, "for every project of yours, in your home directory (Claude Code only)")
}

// location resolves where the instructions go from the path argument.
func (f *agentFlags) location(env *environment, args []string) (skill.Location, error) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return skill.Location{}, err
	}
	home := env.getenv("HOME")
	if home == "" {
		home = env.getenv("USERPROFILE")
	}
	return skill.Location{ProjectDir: abs, Home: home, Global: f.global}, nil
}

// targets resolves --agent, or detects the agents present when it is absent.
func (f *agentFlags) targets(env *environment, loc skill.Location) ([]skill.Target, error) {
	var targets []skill.Target
	for _, id := range f.agents {
		t, ok := skill.FindTarget(id)
		if !ok {
			return nil, clierr.New(clierr.Usage, "unknown agent %q", id).WithHint("Use one of: %s.", targetList())
		}
		if f.global && !t.Global {
			return nil, clierr.New(clierr.Usage, "%s is set up per project; it cannot be used with --global", t.Name)
		}
		targets = append(targets, t)
	}
	if len(f.agents) > 0 {
		return targets, nil
	}
	for _, t := range skill.Detect(loc.ProjectDir, env.lookPath) {
		if !f.global || t.Global {
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return nil, clierr.New(clierr.NotFound, "no coding agent was found on this machine or in this project").
			WithHint("Name one with --agent: %s.", targetList())
	}
	return targets, nil
}

func targetList() string {
	parts := make([]string, 0, len(skill.Targets))
	for _, t := range skill.Targets {
		parts = append(parts, fmt.Sprintf("%s (%s)", t.ID, t.Name))
	}
	return strings.Join(parts, ", ")
}

// agentResult is what install and uninstall report.
type agentResult struct {
	Changes []skill.Change `json:"changes"`
	// Applied is false for a dry run.
	Applied bool `json:"applied"`
}

func newAgentInstallCmd(opts *options, env *environment) *cobra.Command {
	var (
		flags  agentFlags
		yes    bool
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "install [path]",
		Short: "Give your coding agent the instructions for this CLI",
		Long: `Write the instructions that teach a coding agent to integrate and check the
Tenjin SDK with this CLI: which commands to run, in what order, and what
their output means.

For Claude Code they are installed as a skill, in the project's
.claude/skills folder, or in your home directory with --global. For Codex,
Cursor and other agents that read AGENTS.md they are a marked section of
that file; the rest of the file is left as it is.

The command lists every file it would write and asks before writing. Run it
again after updating the CLI. 'tenjin agent uninstall' removes what it wrote.`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentChange(cmd, opts, env, &flags, args, yes, dryRun, skill.PlanInstall, "write")
		},
	}
	flags.register(cmd)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "write without asking")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be written and stop")
	return cmd
}

func newAgentUninstallCmd(opts *options, env *environment) *cobra.Command {
	var (
		flags  agentFlags
		yes    bool
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "uninstall [path]",
		Short: "Remove the instructions 'tenjin agent install' wrote",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// With no --agent, look everywhere: the agent may have been
			// uninstalled since, and its instructions are still there.
			if len(flags.agents) == 0 {
				for _, t := range skill.Targets {
					if !flags.global || t.Global {
						flags.agents = append(flags.agents, t.ID)
					}
				}
			}
			return runAgentChange(cmd, opts, env, &flags, args, yes, dryRun, skill.PlanRemove, "remove")
		},
	}
	flags.register(cmd)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "remove without asking")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be removed and stop")
	return cmd
}

// runAgentChange plans, shows, confirms and applies an install or a removal.
func runAgentChange(cmd *cobra.Command, opts *options, env *environment, flags *agentFlags, args []string, yes, dryRun bool,
	plan func(skill.Target, skill.Location) (skill.Change, error), verb string) error {
	format, err := opts.format(cmd)
	if err != nil {
		return err
	}
	loc, err := flags.location(env, args)
	if err != nil {
		return err
	}
	targets, err := flags.targets(env, loc)
	if err != nil {
		return err
	}
	result := agentResult{Changes: []skill.Change{}}
	pending := 0
	for _, t := range targets {
		change, err := plan(t, loc)
		if err != nil {
			return clierr.Wrap(clierr.Internal, err, "%s: %v", t.Name, err)
		}
		result.Changes = append(result.Changes, change)
		if change.Action != skill.Unchanged && change.Action != skill.Absent {
			pending++
		}
	}

	out, prompt := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if pending > 0 && !dryRun && !yes {
		if !env.interactive {
			return clierr.New(clierr.Usage, "this would %s %s, and needs confirmation", verb, plural(pending, "file")).
				WithHint("Pass --yes, or --dry-run to see the files.")
		}
		writeChanges(prompt, result.Changes, "This will:")
		ok, err := env.confirm(prompt, "Proceed?", true)
		if err != nil {
			return err
		}
		if !ok {
			return clierr.New(clierr.Internal, "nothing was changed")
		}
	}
	if !dryRun {
		for _, change := range result.Changes {
			if err := skill.Apply(change); err != nil {
				return clierr.Wrap(clierr.Internal, err, "could not %s %s: %v", verb, change.Path, err)
			}
		}
		result.Applied = true
	}

	if format == render.JSON {
		return render.WriteJSON(out, result)
	}
	switch {
	case dryRun:
		writeChanges(out, result.Changes, "Dry run. This would:")
	case pending == 0 && verb == "remove":
		_, _ = fmt.Fprintln(out, "Nothing to remove.")
	case pending == 0:
		writeChanges(out, result.Changes, "Already up to date:")
	default:
		writeChanges(out, result.Changes, "Done:")
		if verb == "write" {
			_, _ = fmt.Fprintf(out, "\nYour agent reads this in its next session. Ask it to integrate the Tenjin SDK, or to check the integration.\nTenjin's MCP server, for dashboard data, comes with the tenjin-core plugin: %s\n", pluginsURL)
		}
	}
	return nil
}

func writeChanges(w io.Writer, changes []skill.Change, heading string) {
	_, _ = fmt.Fprintln(w, heading)
	for _, c := range changes {
		line := fmt.Sprintf("  %-9s %s", c.Action, c.Path)
		if c.Detail != "" && c.Action != skill.Unchanged && c.Action != skill.Absent {
			line += "\n            " + c.Detail
		}
		_, _ = fmt.Fprintln(w, line)
	}
}

// agentStatus is one row of `agent status`.
type agentStatus struct {
	Agent string      `json:"agent"`
	Name  string      `json:"name"`
	State skill.State `json:"state"`
	Path  string      `json:"path"`
}

func newAgentStatusCmd(opts *options, env *environment) *cobra.Command {
	var flags agentFlags
	cmd := &cobra.Command{
		Use:   "status [path]",
		Short: "Show where the instructions are installed, and whether they are current",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			loc, err := flags.location(env, args)
			if err != nil {
				return err
			}
			rows := []agentStatus{}
			for _, t := range skill.Targets {
				if (len(flags.agents) > 0 && !contains(flags.agents, t.ID)) || (flags.global && !t.Global) {
					continue
				}
				state, path, err := skill.Check(t, loc)
				if err != nil {
					return clierr.Wrap(clierr.Internal, err, "%s: %v", t.Name, err)
				}
				rows = append(rows, agentStatus{Agent: t.ID, Name: t.Name, State: state, Path: path})
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), rows)
			}
			words := map[skill.State]string{
				skill.Current:      "installed",
				skill.Outdated:     "out of date: run 'tenjin agent install'",
				skill.NotInstalled: "not installed",
			}
			table := make([][]string, 0, len(rows))
			for _, r := range rows {
				table = append(table, []string{r.Name, words[r.State], r.Path})
			}
			return render.WriteTable(cmd.OutOrStdout(), render.NewStyler(cmd.OutOrStdout()), []string{"agent", "instructions", "file"}, table)
		},
	}
	flags.register(cmd)
	return cmd
}

func newAgentSkillCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "skill",
		Short: "Print the instructions, for an agent this command cannot install them for",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), map[string]string{"name": skill.Name, "instructions": skill.Body()})
			}
			_, err = io.WriteString(cmd.OutOrStdout(), skill.Body())
			return err
		},
	}
}
