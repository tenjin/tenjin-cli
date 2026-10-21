package cli

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/render"
)

// tagline goes under the wordmark.
const tagline = "Integrate the Tenjin SDK, and work with your Tenjin account, from the terminal"

var (
	// A command's row: its name, then its summary after at least one space
	// (cobra pads names to the longest, which gets just one).
	helpEntry = regexp.MustCompile(`^  (\S+)(\s+\S.*)$`)
	helpFlag  = regexp.MustCompile(`^(\s+)((?:-\w, )?--[\w-]+)`)
	// A guide's row: "tenjin help <guide>", then its summary.
	helpGuide = regexp.MustCompile(`^  (tenjin help \S+)(\s+\S.*)$`)
)

// styleHelp makes help for root and every command under it coloured on a
// terminal: section titles, command names and flags. The text is cobra's
// own, untouched, so piped help is exactly what it was.
func styleHelp(root *cobra.Command) {
	plain := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		out := cmd.OutOrStdout()
		style := render.NewStyler(out)
		if !style.Enabled() {
			plain(cmd, args)
			return
		}
		var text bytes.Buffer
		cmd.SetOut(&text)
		plain(cmd, args)
		cmd.SetOut(out)

		_, _ = io.WriteString(out, colourHelp(text.String(), style, cmd))
	})
}

// colourHelp styles cobra's help text line by line.
func colourHelp(text string, style render.Styler, cmd *cobra.Command) string {
	// Titles that list commands; under any other title an indented first
	// word is not a command name.
	commandTitles := map[string]bool{"Available Commands:": true, "Additional Commands:": true}
	for _, g := range cmd.Groups() {
		commandTitles[g.Title] = true
	}
	titles := map[string]bool{"Usage:": true, "Aliases:": true, "Examples:": true, "Flags:": true, "Global Flags:": true}

	lines := strings.Split(text, "\n")
	section := ""
	for i, line := range lines {
		switch {
		case titles[line] || commandTitles[line]:
			section = line
			lines[i] = style.Heading(line)
		case line == guidesTitle:
			section = line
			lines[i] = style.Heading(line)
		case section == guidesTitle:
			if m := helpGuide.FindStringSubmatch(line); m != nil {
				lines[i] = "  " + style.Accent(m[1]) + m[2]
			}
		case commandTitles[section]:
			if m := helpEntry.FindStringSubmatch(line); m != nil {
				lines[i] = "  " + style.Accent(m[1]) + m[2]
			}
		case strings.HasSuffix(section, "Flags:"):
			if m := helpFlag.FindStringSubmatch(line); m != nil {
				lines[i] = m[1] + style.Accent(m[2]) + line[len(m[0]):]
			}
		}
		if strings.HasPrefix(line, `Use "`) {
			lines[i] = style.Dim(line)
		}
	}
	return strings.Join(lines, "\n")
}

// writeLanding is what 'tenjin' alone shows: the wordmark at a terminal, then
// where each kind of user starts, short enough to read without scrolling.
// 'tenjin --help' lists every command. Asked for JSON or CSV, it is a usage
// error instead: the landing is prose for people, and there is no result to
// give a script without a command.
func writeLanding(root *cobra.Command) error {
	asJSON, _ := root.Flags().GetBool("json")
	if output, _ := root.Flags().GetString("output"); asJSON || output != string(render.Table) {
		return clierr.New(clierr.Usage, "a command is needed: tenjin on its own prints no result").
			WithHint("Run 'tenjin --help' for the commands.")
	}
	w := root.OutOrStdout()
	style := render.NewStyler(w)
	var b strings.Builder
	if banner := render.Banner(w, tagline); banner != "" {
		b.WriteString(banner + "\n")
	} else {
		b.WriteString("The Tenjin CLI: " + strings.ToLower(tagline[:1]) + tagline[1:] + ".\n\n")
	}
	sections := []struct {
		title string
		rows  [][2]string
	}{
		{"App developers, in your app's project:", [][2]string{
			{"tenjin setup", "Set up Tenjin, step by step"},
			{"tenjin doctor", "Check an integration and explain what is wrong"},
		}},
		{"Your Tenjin data:", [][2]string{
			{"tenjin login", "Sign in through the browser"},
			{"tenjin campaigns list", "Your campaigns"},
			{"tenjin reports spend --from <date> --to <date>", "Spend, installs and what they returned"},
		}},
	}
	width := 0
	for _, s := range sections {
		for _, r := range s.rows {
			width = max(width, len(r[0]))
		}
	}
	for _, s := range sections {
		b.WriteString(style.Heading(s.title) + "\n")
		for _, r := range s.rows {
			b.WriteString("  " + style.Accent(fmt.Sprintf("%-*s", width, r[0])) + "  " + r[1] + "\n")
		}
		b.WriteString("\n")
	}
	shown := 0
	for _, s := range sections {
		shown += len(s.rows)
	}
	fmt.Fprintf(&b, "Those are the greatest hits. %d more commands live behind %s:\n", countCommands(root)-shown, style.Accent("tenjin --help"))
	b.WriteString("campaigns, callbacks, site IDs, saved reports, your coding agent and friends.\n")
	fmt.Fprintf(&b, "Step by step: %s, for integration, callbacks, reporting or scripting.\n", style.Accent("tenjin help <guide>"))
	fmt.Fprintf(&b, "Stuck? %s has read the documentation for you.\n", style.Accent("tenjin docs search <words>"))
	_, err := io.WriteString(w, b.String())
	return err
}

// countCommands counts the commands that do something, leaving out help
// and shell completion.
func countCommands(cmd *cobra.Command) int {
	n := 0
	for _, c := range cmd.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		if c.Runnable() && !c.HasSubCommands() {
			n++
		}
		n += countCommands(c)
	}
	return n
}
