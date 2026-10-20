package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseFormat(t *testing.T) {
	for _, name := range []string{"table", "json"} {
		if got, err := ParseFormat(name); err != nil || string(got) != name {
			t.Errorf("ParseFormat(%q) = %q, %v", name, got, err)
		}
	}
	for _, name := range []string{"", "yaml", "JSON"} {
		if _, err := ParseFormat(name); err == nil {
			t.Errorf("ParseFormat(%q) succeeded, want an error", name)
		}
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, map[string]string{"url": "https://example.com/?a=1&b=2"}); err != nil {
		t.Fatal(err)
	}
	// Indented, newline-terminated, and with & left readable.
	want := "{\n  \"url\": \"https://example.com/?a=1&b=2\"\n}\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestIndent(t *testing.T) {
	cases := []struct {
		name, text string
		indent     int
		width      int
		want       string
	}{
		{"no width leaves the line whole", "one two three four", 2, 0, "  one two three four"},
		{"breaks at spaces", "one two three four", 2, 10, "  one two\n  three\n  four"},
		{"a word longer than the width is not cut", "see https://example.com/a/long/path now", 2, 12, "  see\n  https://example.com/a/long/path\n  now"},
		{"keeps line breaks and code indentation", "Add:\n-keep class x {\n    void y();\n}", 4, 0, "    Add:\n    -keep class x {\n        void y();\n    }"},
		{"counts characters, not bytes", "…a …b …c", 0, 5, "…a …b\n…c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Indent(tc.text, tc.indent, tc.width); got != tc.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

func TestWidthOfSomethingThatIsNotATerminal(t *testing.T) {
	if got := Width(&bytes.Buffer{}); got != 0 {
		t.Errorf("width = %d", got)
	}
	// So output that is piped or captured is never wrapped.
	if got := TextWidth(&bytes.Buffer{}); got != 0 {
		t.Errorf("text width = %d", got)
	}
}

func TestStylerEnvironment(t *testing.T) {
	var buf bytes.Buffer
	// A buffer is not a terminal.
	if NewStyler(&buf).Enabled() {
		t.Error("colour is on for something that is not a terminal")
	}
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("COLORTERM", "")
	forced := NewStyler(&buf)
	if !forced.Enabled() || forced.Red("x") != "\x1b[38;5;167mx\x1b[0m" || forced.Heading("x") != "\x1b[1;38;5;37mx\x1b[0m" {
		t.Errorf("forced styler: enabled = %v, red = %q", forced.Enabled(), forced.Red("x"))
	}
	// NO_COLOR wins over everything.
	t.Setenv("NO_COLOR", "1")
	if s := NewStyler(&buf); s.Enabled() || s.Accent("x") != "x" {
		t.Error("NO_COLOR did not turn colour off")
	}
}

func TestBannerOnlyWhereItCanBeSeen(t *testing.T) {
	var buf bytes.Buffer
	if got := Banner(&buf, "tagline"); got != "" {
		t.Errorf("a banner was written to something that is not a terminal: %q", got)
	}
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("COLORTERM", "")
	got := Banner(&buf, "tagline")
	lines := strings.Split(strings.Trim(got, "\n"), "\n")
	if len(lines) != 7 || !strings.Contains(lines[6], "tagline") {
		t.Fatalf("banner = %q", got)
	}
	// Every row of the wordmark is the same width, or it would look torn.
	for i, line := range logo {
		if n := len([]rune(line.row)); n != len([]rune(logo[0].row)) || n+4 != logoWidth {
			t.Errorf("row %d is %d columns wide", i, n)
		}
	}
}

// Colour codes in a header cell would count towards the column's width and
// push the header out of line with its rows.
func TestTableHeaderStaysAlignedWhenStyled(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("COLORTERM", "")
	var buf bytes.Buffer
	if err := WriteTable(&buf, NewStyler(&buf), []string{"name", "id"}, [][]string{{"a-long-name", "1"}}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(buf.String(), "\n")
	header := strings.NewReplacer("\x1b[38;5;245m", "", "\x1b[0m", "").Replace(lines[0])
	if strings.Index(header, "ID") != strings.Index(lines[1], "1") || header == lines[0] {
		t.Errorf("table:\n%s", buf.String())
	}
}

// A terminal that shows 24-bit colour gets the palette's exact colours.
func TestTrueColourTerminalsGetTheExactPalette(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("COLORTERM", "truecolor")
	style := NewStyler(&bytes.Buffer{})
	if got := style.Red("x"); got != "\x1b[38;2;204;92;92mx\x1b[0m" {
		t.Errorf("Red = %q", got)
	}
	if got := style.Dim("x"); got != "\x1b[38;2;138;138;138mx\x1b[0m" {
		t.Errorf("Dim = %q", got)
	}
}

// Text from a project cannot move the cursor or set the clipboard.
func TestCleanRemovesControlCharacters(t *testing.T) {
	type inner struct{ Name string }
	v := struct {
		Title  string
		Items  []inner
		Params map[string]string
		Next   *inner
	}{
		Title:  "com.evil\x1b]52;c;cHduZWQ=\x07\x1b[2K\x1b[1A spoofed",
		Items:  []inner{{"a\x1b[32m✓"}},
		Params: map[string]string{"k": "v\x9b"},
		Next:   &inner{"tab\tand\nline"},
	}
	CleanAll(&v)
	if left := v.Title + v.Items[0].Name + v.Params["k"]; strings.ContainsAny(left, "\x1b\x07") || strings.IndexByte(left, 0x9b) >= 0 {
		t.Errorf("left: %q %q %q", v.Title, v.Items[0].Name, v.Params["k"])
	}
	if v.Next.Name != "tab\tand\nline" {
		t.Errorf("Next = %q", v.Next.Name)
	}
}

// A cell cannot make a row or a column of its own.
func TestTableCellsStayInTheirCell(t *testing.T) {
	var b bytes.Buffer
	if err := WriteTable(&b, Styler{}, []string{"na\tme", "id"}, [][]string{{"Evil\nFAKE\tROW\r", "1"}}); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[1], "Evil FAKE ROW") || !strings.HasPrefix(lines[0], "NA ME") {
		t.Errorf("table:\n%s", b.String())
	}
	if got := CleanCell("a\tb\nc\x1b[2Kd"); got != "a b c[2Kd" {
		t.Errorf("CleanCell = %q", got)
	}
}

// A cell a spreadsheet would run as a formula is written as text; numbers
// stay numbers.
func TestCSVCellsDoNotRun(t *testing.T) {
	var b bytes.Buffer
	cells := []string{`=HYPERLINK("x")`, "+cmd", "-2+3", "@SUM(A1)", "\tTab", "\rReturn", "-5.2", "+7", "1e5", "plain", ""}
	if err := WriteCSV(&b, []string{"=header"}, [][]string{cells}); err != nil {
		t.Fatal(err)
	}
	want := "'=header\n" + `"'=HYPERLINK(""x"")",'+cmd,'-2+3,'@SUM(A1),` + "'\tTab,\"'\rReturn\",-5.2,+7,1e5,plain,\n"
	if b.String() != want {
		t.Errorf("CSV = %q,\nwant  %q", b.String(), want)
	}
}
