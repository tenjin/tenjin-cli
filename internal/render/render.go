// Package render writes command results. Every command renders through it so
// that output is one contract: a human form by default, JSON on request, and
// nothing but the result on stdout.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

// Format is an output format a user can ask for.
type Format string

const (
	Table Format = "table"
	JSON  Format = "json"
)

// ParseFormat validates a user-supplied format name.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case Table, JSON:
		return Format(s), nil
	}
	return "", fmt.Errorf("unknown output format %q (use %q or %q)", s, Table, JSON)
}

// WriteJSON writes v as indented JSON followed by a newline.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// WriteTable writes rows under an upper-cased header, columns aligned. The
// header is styled after the columns are laid out, so colour codes do not
// count towards their width.
func WriteTable(w io.Writer, style Styler, header []string, rows [][]string) error {
	var laid strings.Builder
	tw := tabwriter.NewWriter(&laid, 0, 0, 2, ' ', 0)
	upper := make([]string, len(header))
	for i, h := range header {
		upper[i] = strings.ToUpper(h)
	}
	if _, err := fmt.Fprintln(tw, strings.Join(upper, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	head, body, _ := strings.Cut(laid.String(), "\n")
	_, err := io.WriteString(w, style.Dim(head)+"\n"+body)
	return err
}

// Width returns the number of columns of the terminal w writes to, or 0
// when w is not a terminal.
func Width(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return width
}

// maxTextWidth keeps prose readable on a very wide terminal.
const maxTextWidth = 100

// TextWidth is the width to wrap prose to when writing to w: the terminal's,
// up to a readable maximum, or 0 when w is not a terminal.
func TextWidth(w io.Writer) int {
	return min(Width(w), maxTextWidth)
}

// Indent lays text out as a block indented by indent spaces. With a width
// it also breaks lines at spaces so that none is longer than width; with 0
// it only indents. Line breaks already in the text are kept, along with the
// leading space of the lines they start, so code stays as written.
func Indent(text string, indent, width int) string {
	pad := strings.Repeat(" ", indent)
	var out []string
	for _, line := range strings.Split(text, "\n") {
		lead := line[:len(line)-len(strings.TrimLeft(line, " "))]
		current := ""
		for _, word := range strings.Fields(line) {
			switch {
			case current == "":
				current = lead + word
			case width > 0 && indent+len([]rune(current))+1+len([]rune(word)) > width:
				out = append(out, pad+current)
				current = lead + word
			default:
				current += " " + word
			}
		}
		out = append(out, strings.TrimRight(pad+current, " "))
	}
	return strings.Join(out, "\n")
}

// Styler colours text for a terminal and leaves it plain everywhere else.
type Styler struct{ enabled bool }

// NewStyler enables colour when w is a terminal. NO_COLOR turns it off
// everywhere; CLICOLOR_FORCE keeps it on when the output is piped, for
// example into `less -R`.
func NewStyler(w io.Writer) Styler {
	if os.Getenv("NO_COLOR") != "" {
		return Styler{}
	}
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return Styler{enabled: true}
	}
	f, ok := w.(*os.File)
	return Styler{enabled: ok && term.IsTerminal(int(f.Fd())) && colourSupported(f)}
}

// Enabled reports whether the styler colours anything.
func (s Styler) Enabled() bool { return s.enabled }

func (s Styler) wrap(code, text string) string {
	if !s.enabled {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// accent is the one colour that is the CLI's own: a teal that reads on dark
// and on light backgrounds. It is from the 256-colour set, which every
// current terminal has.
const accent = "38;5;37"

func (s Styler) Red(text string) string    { return s.wrap("31", text) }
func (s Styler) Green(text string) string  { return s.wrap("32", text) }
func (s Styler) Yellow(text string) string { return s.wrap("33", text) }
func (s Styler) Bold(text string) string   { return s.wrap("1", text) }
func (s Styler) Dim(text string) string    { return s.wrap("2", text) }
func (s Styler) Accent(text string) string { return s.wrap(accent, text) }

// Heading is for the title of a section of output.
func (s Styler) Heading(text string) string { return s.wrap("1;"+accent, text) }

// logo is the wordmark, with the colour of each row: a fade from light to
// deep teal.
var logo = []struct{ colour, row string }{
	{"38;5;80", `████████╗███████╗███╗   ██╗     ██╗██╗███╗   ██╗`},
	{"38;5;44", `╚══██╔══╝██╔════╝████╗  ██║     ██║██║████╗  ██║`},
	{"38;5;43", `   ██║   █████╗  ██╔██╗ ██║     ██║██║██╔██╗ ██║`},
	{"38;5;37", `   ██║   ██╔══╝  ██║╚██╗██║██   ██║██║██║╚██╗██║`},
	{"38;5;36", `   ██║   ███████╗██║ ╚████║╚█████╔╝██║██║ ╚████║`},
	{"38;5;30", `   ╚═╝   ╚══════╝╚═╝  ╚═══╝ ╚════╝ ╚═╝╚═╝  ╚═══╝`},
}

// logoWidth is the number of columns the wordmark needs, with its margin.
const logoWidth = 52

// Banner returns the wordmark with a line of text under it, for the places
// a person first meets the CLI. It is empty unless w is a terminal wide
// enough for it: in a pipe or a log file a logo is noise.
func Banner(w io.Writer, tagline string) string {
	style := NewStyler(w)
	if width := Width(w); !style.Enabled() || (width > 0 && width < logoWidth) {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, line := range logo {
		b.WriteString("  " + style.wrap(line.colour, line.row) + "\n")
	}
	b.WriteString("  " + style.Dim(tagline) + "\n")
	return b.String()
}
