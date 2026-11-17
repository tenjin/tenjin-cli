// Package render writes command results. Every command renders through it so
// that output is one contract: a human form by default, JSON on request, and
// nothing but the result on stdout.
package render

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

// Format is an output format a user can ask for.
type Format string

const (
	Table Format = "table"
	JSON  Format = "json"
	// CSV is for the data commands: rows for a spreadsheet.
	CSV Format = "csv"
)

// ParseFormat validates a user-supplied format name.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case Table, JSON, CSV:
		return Format(s), nil
	}
	return "", fmt.Errorf("unknown output format %q (use %q, %q, or %q for the data commands)", s, Table, JSON, CSV)
}

// WriteCSV writes a header and rows as CSV. A cell a spreadsheet would read
// as a formula is written as text.
func WriteCSV(w io.Writer, header []string, rows [][]string) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(inert(header)); err != nil {
		return err
	}
	for _, row := range rows {
		if err := cw.Write(inert(row)); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

var number = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$`)

// inert keeps the cells of a row from running in a spreadsheet: one that
// starts as a formula does, such as a name from an ad network that starts
// with =, gets a leading ', which spreadsheets read as "this is text". A
// number, such as -5.2, stays a number.
func inert(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		if cell != "" && strings.ContainsRune("=+-@\t\r", rune(cell[0])) && !number.MatchString(cell) {
			cell = "'" + cell
		}
		out[i] = cell
	}
	return out
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
// count towards their width. A tab or line break in a cell is a space: it
// would make a column or a row of its own.
func WriteTable(w io.Writer, style Styler, header []string, rows [][]string) error {
	var laid strings.Builder
	tw := tabwriter.NewWriter(&laid, 0, 0, 2, ' ', 0)
	upper := make([]string, len(header))
	for i, h := range header {
		upper[i] = strings.ToUpper(flatten(h))
	}
	if _, err := fmt.Fprintln(tw, strings.Join(upper, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = flatten(c)
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
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
type Styler struct{ enabled, truecolor bool }

// terminal is isTerminal, replaced in tests, which run without one.
var terminal = isTerminal

// isTerminal reports whether f is a terminal whose escape codes work: on
// Windows, a console whose handling of them could be turned on.
func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) && colourSupported(f) }

// NewStyler enables colour when w is a terminal that handles escape codes.
// NO_COLOR turns it off everywhere; CLICOLOR_FORCE keeps it on when the output is piped, for
// example into `less -R`.
func NewStyler(w io.Writer) Styler {
	if os.Getenv("NO_COLOR") != "" {
		return Styler{}
	}
	// Terminals that show 24-bit colour say so here.
	truecolor := os.Getenv("COLORTERM") == "truecolor" || os.Getenv("COLORTERM") == "24bit"
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return Styler{enabled: true, truecolor: truecolor}
	}
	f, ok := w.(*os.File)
	return Styler{enabled: ok && escapeCodes(f), truecolor: truecolor}
}

// Enabled reports whether the styler colours anything.
func (s Styler) Enabled() bool { return s.enabled }

func (s Styler) wrap(code, text string) string {
	if !s.enabled {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// colour is one colour of the palette: exact where the terminal shows 24-bit
// colour, and the nearest of the 256-colour set, which every current
// terminal has, otherwise.
type colour struct {
	r, g, b uint8
	nearest int
}

func (s Styler) code(c colour) string {
	if s.truecolor {
		return fmt.Sprintf("38;2;%d;%d;%d", c.r, c.g, c.b)
	}
	return fmt.Sprintf("38;5;%d", c.nearest)
}

// The palette is muted on purpose: each colour is a mid tone that keeps a
// contrast of 3 or more on a white background and on a dark one, so it
// reads in a light terminal as well as a dark one, and none of it shouts.
// The terminal's own red, green and yellow are not used: their brightness
// depends on the theme, and in many they are glaring.
var (
	errorColour   = colour{204, 92, 92, 167}   // a soft red
	warningColour = colour{184, 134, 11, 136}  // amber
	successColour = colour{78, 154, 106, 71}   // sage green
	accentColour  = colour{38, 150, 150, 37}   // Tenjin teal
	quietColour   = colour{138, 138, 138, 245} // grey, for what is secondary
)

func (s Styler) Red(text string) string    { return s.wrap(s.code(errorColour), text) }
func (s Styler) Green(text string) string  { return s.wrap(s.code(successColour), text) }
func (s Styler) Yellow(text string) string { return s.wrap(s.code(warningColour), text) }
func (s Styler) Bold(text string) string   { return s.wrap("1", text) }
func (s Styler) Accent(text string) string { return s.wrap(s.code(accentColour), text) }

// Dim is for what is secondary: file names, hints, table headers. It is a
// grey rather than the terminal's faint style, which some consoles, the
// Windows one among them, do not show.
func (s Styler) Dim(text string) string { return s.wrap(s.code(quietColour), text) }

// Heading is for the title of a section of output.
func (s Styler) Heading(text string) string { return s.wrap("1;"+s.code(accentColour), text) }

// logo is the wordmark, with the colour of each row: a gentle fade from a
// light to a deep teal.
var logo = []struct {
	colour colour
	row    string
}{
	{colour{104, 186, 186, 73}, `████████╗███████╗███╗   ██╗     ██╗██╗███╗   ██╗`},
	{colour{82, 174, 174, 73}, `╚══██╔══╝██╔════╝████╗  ██║     ██║██║████╗  ██║`},
	{colour{60, 162, 162, 37}, `   ██║   █████╗  ██╔██╗ ██║     ██║██║██╔██╗ ██║`},
	{colour{38, 150, 150, 37}, `   ██║   ██╔══╝  ██║╚██╗██║██   ██║██║██║╚██╗██║`},
	{colour{30, 134, 134, 30}, `   ██║   ███████╗██║ ╚████║╚█████╔╝██║██║ ╚████║`},
	{colour{24, 118, 118, 30}, `   ╚═╝   ╚══════╝╚═╝  ╚═══╝ ╚════╝ ╚═╝╚═╝  ╚═══╝`},
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
		b.WriteString("  " + style.wrap(style.code(line.colour), line.row) + "\n")
	}
	b.WriteString("  " + style.Dim(tagline) + "\n")
	return b.String()
}
