package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tenjin/tenjin-cli/internal/agent"
	"github.com/tenjin/tenjin-cli/internal/buildinfo"
	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/skill"
	"github.com/tenjin/tenjin-cli/internal/update"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// The instructions an agent follows describe the version of the CLI that
// installed them. After an update, an agent following an older copy uses
// commands and flags this version may not have, so the copy is named where
// people and agents look: doctor's report, the first run after an update,
// and the error of a command or flag that does not exist.

// errUnknownName is the cause of a usage error for a command that does not
// exist. One for a flag that does not exist carries pflag's NotExistError.
var errUnknownName = errors.New("no such command")

// home is the user's home folder, or "" when it is not known.
func (e *environment) home() string {
	if home := e.getenv("HOME"); home != "" {
		return home
	}
	return e.getenv("USERPROFILE")
}

// otherSkills returns the installed copies of the instructions, in dir and
// in the home folder, that a version of the CLI other than current wrote.
func (e *environment) otherSkills(dir, current string) []skill.Copy {
	return skill.OtherVersions(dir, e.home(), current)
}

// writtenBy says which version wrote a copy, as the end of a sentence.
func writtenBy(c skill.Copy, current string) string {
	switch {
	case c.Version == "":
		return "an earlier version of the CLI"
	case update.Newer(current, c.Version):
		return "an earlier version of the CLI, " + c.Version
	}
	return "another version of the CLI, " + c.Version
}

// reinstall is the command that brings a copy up to date.
func reinstall(c skill.Copy) string {
	if c.Global {
		return "tenjin agent install --global"
	}
	return "tenjin agent install"
}

// skillFindings are doctor's findings for the copies another version wrote:
// for information, since the project itself is not at fault.
func skillFindings(copies []skill.Copy, root, current string) []verify.Finding {
	findings := make([]verify.Finding, 0, len(copies))
	for _, c := range copies {
		f := verify.Finding{
			ID: "agent.skill.version", Severity: verify.Info,
			Title:  "The tenjin-cli skill your coding agent reads was written by " + writtenBy(c, current),
			Detail: "It names that version's commands and flags; this one is " + current + ".",
			File:   c.Path,
			Fix:    "Run '" + reinstall(c) + "'.",
		}
		if rel, err := filepath.Rel(root, c.Path); err == nil && !c.Global {
			f.File = filepath.ToSlash(rel)
		}
		if filepath.Base(c.Path) == "AGENTS.md" {
			f.Title = "AGENTS.md holds the whole tenjin-cli instructions, as an earlier version of the CLI wrote them"
			f.Detail = "An agent that reads AGENTS.md carries them on every turn, and they name that version's commands and flags. This version installs them as a skill, read when a task calls for it, with two lines in AGENTS.md that point to it."
		}
		findings = append(findings, f)
	}
	return findings
}

// skillDir is the folder whose copies of the instructions a notice names:
// the project the command ran on or was given, else the working folder.
func (e *environment) skillDir(ran *cobra.Command) string {
	if e.projectRoot != "" {
		return e.projectRoot
	}
	// A command stopped by a flag it does not have still read the
	// arguments before that flag.
	if ran != nil {
		if args := ran.Flags().Args(); len(args) > 0 {
			if info, err := os.Stat(args[0]); err == nil && info.IsDir() {
				if dir, err := filepath.Abs(args[0]); err == nil {
					return dir
				}
			}
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// hintOtherSkill points an agent that used a command or flag this version
// does not have at the instructions it most likely took it from.
func (e *environment) hintOtherSkill(err *clierr.Error, ran *cobra.Command) {
	var flag *pflag.NotExistError
	if err.Kind != clierr.Usage || !agent.InsideAgent(e.getenv) || !errors.Is(err, errUnknownName) && !errors.As(err, &flag) {
		return
	}
	current := buildinfo.Get().Version
	copies := e.otherSkills(e.skillDir(ran), current)
	if len(copies) == 0 {
		return
	}
	c := copies[0]
	hint := fmt.Sprintf("The installed tenjin-cli skill, %s, was written by %s, and this is %s: run '%s' to update it, then read it again.", c.Path, writtenBy(c, current), current, reinstall(c))
	if err.Hint != "" {
		hint += " " + err.Hint
	}
	err.Hint = hint
}

// reportOtherSkills tells a person, on the first run after an update, about
// the copies of the instructions the update left behind: in the project
// the command ran on, or the folder it ran in, and in the home folder.
func (e *environment) reportOtherSkills(w io.Writer, ran *cobra.Command) {
	if !e.updates.Upgraded() {
		return
	}
	style := render.NewStyler(w)
	for _, c := range e.otherSkills(e.skillDir(ran), e.updates.Current) {
		_, _ = fmt.Fprintf(w, "\nThe tenjin-cli skill in %s was written by %s. %s %s\n",
			render.Clean(c.Path), writtenBy(c, e.updates.Current), style.Dim("Update it with:"), reinstall(c))
	}
}
