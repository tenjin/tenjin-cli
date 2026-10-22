package cli

import (
	"bufio"
	"errors"
	"io"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

var (
	up, down, enter, ctrlC = key{kind: keyUp}, key{kind: keyDown}, key{kind: keyEnter}, key{kind: keyInterrupt}
	colourCode             = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

func press(r rune) key { return key{kind: keyRune, r: r} }

func samplePicker() *picker {
	return &picker{items: []pickItem{
		{label: "App opens", about: "installs", ticked: true, fixed: "App opens are always included."},
		{label: "Purchases", about: "items", detail: "suggested: Billing in build.gradle", ticked: true},
		{label: "Subscriptions", about: "renewals"},
	}}
}

func TestPickerKeys(t *testing.T) {
	cases := []struct {
		name   string
		keys   []key
		ticked []int
		cursor int
		note   string
	}{
		{"nothing pressed", nil, []int{0, 1}, 0, ""},
		{"down and space", []key{down, down, press(' ')}, []int{0, 1, 2}, 2, ""},
		{"j and x", []key{press('j'), press('x')}, []int{0}, 1, ""},
		{"down wraps to the top", []key{down, down, down}, []int{0, 1}, 0, ""},
		{"up wraps to the bottom", []key{up, press(' ')}, []int{0, 1, 2}, 2, ""},
		{"k moves up", []key{down, down, press('k')}, []int{0, 1}, 1, ""},
		{"a fixed item stays ticked", []key{press(' ')}, []int{0, 1}, 0, "App opens are always included."},
		{"a ticks all", []key{press('a')}, []int{0, 1, 2}, 0, ""},
		{"n unticks all but the fixed", []key{press('a'), press('n')}, []int{0}, 0, ""},
		{"a digit toggles that item and moves to it", []key{press('3')}, []int{0, 1, 2}, 2, ""},
		{"a digit twice toggles back", []key{press('2'), press('2')}, []int{0, 1}, 1, ""},
		{"a digit past the list", []key{press('9')}, []int{0, 1}, 0, "There are 3 in the list."},
		{"a note lasts one key", []key{press('1'), down}, []int{0, 1}, 1, ""},
		{"other keys do nothing", []key{press('q'), {kind: keyOther}}, []int{0, 1}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := samplePicker()
			for _, k := range tc.keys {
				p.handle(k)
			}
			if got := p.ticked(); !reflect.DeepEqual(got, tc.ticked) || p.cursor != tc.cursor || p.note != tc.note {
				t.Errorf("ticked %v, cursor %d, note %q; want %v, %d, %q", got, p.cursor, p.note, tc.ticked, tc.cursor, tc.note)
			}
			if p.done || p.stopped {
				t.Error("finished without Enter or Ctrl-C")
			}
		})
	}
}

func TestPickerEnterAndCtrlC(t *testing.T) {
	p := samplePicker()
	p.handle(enter)
	if !p.done || p.stopped {
		t.Errorf("Enter: done %v, stopped %v", p.done, p.stopped)
	}
	p = samplePicker()
	p.handle(ctrlC)
	if p.done || !p.stopped {
		t.Errorf("Ctrl-C: done %v, stopped %v", p.done, p.stopped)
	}
	// A choice that needs one refuses Enter with none ticked.
	p = &picker{items: []pickItem{{label: "play"}, {label: "amazon"}}, needOne: "Tick at least one build."}
	p.handle(enter)
	if p.done || p.note != "Tick at least one build." {
		t.Errorf("done %v, note %q", p.done, p.note)
	}
	p.handle(press(' '))
	p.handle(enter)
	if !p.done {
		t.Error("Enter with one ticked did not accept")
	}
}

func TestPickerLines(t *testing.T) {
	p := samplePicker()
	p.handle(down)
	p.handle(press('1'))
	// Without colour the ticks and the cursor still show.
	live := strings.Join(p.lines(render.Styler{}, frame{}), "\n")
	want := strings.Join([]string{
		"> ✓ 1) App opens      installs",
		"  ✓ 2) Purchases      items",
		"                      suggested: Billing in build.gradle",
		"    3) Subscriptions  renewals",
		"  " + pickerHint,
		"  App opens are always included.",
	}, "\n")
	if live != want {
		t.Errorf("live:\n%s\nwant:\n%s", live, want)
	}

	// Once chosen, no cursor, hint or note.
	p.handle(enter)
	final := strings.Join(p.lines(render.Styler{}, frame{final: true}), "\n")
	if final != strings.Join([]string{
		"  ✓ 1) App opens      installs",
		"  ✓ 2) Purchases      items",
		"                      suggested: Billing in build.gradle",
		"    3) Subscriptions  renewals",
	}, "\n") {
		t.Errorf("final:\n%s", final)
	}

	// A short terminal leaves the second lines out.
	if got := p.lines(render.Styler{}, frame{final: true, noDetails: true}); len(got) != 3 {
		t.Errorf("without details: %q", got)
	}
}

func TestPickerLinesFitTheWidth(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")
	style := render.NewStyler(io.Discard)
	for _, f := range []frame{{width: 20}, {width: 20, final: true}} {
		for _, line := range samplePicker().lines(style, f) {
			plain := colourCode.ReplaceAllString(line, "")
			if n := len([]rune(plain)); n > 20 {
				t.Errorf("%q is %d columns", plain, n)
			}
			if strings.Contains(line, "\x1b[") && !strings.HasSuffix(line, "\x1b[0m") {
				t.Errorf("%q leaves a colour on", line)
			}
		}
	}
	if got := samplePicker().lines(style, frame{width: 20})[0]; !strings.Contains(got, "…") {
		t.Errorf("a cut line has no …: %q", got)
	}
}

func TestReadKey(t *testing.T) {
	cases := map[string][]key{
		"\x1b[A\x1b[B":    {up, down},
		"\x1bOA\x1bOB":    {up, down},
		"\x1b[1;5A":       {up},
		"\x1b[C":          {{kind: keyOther}},
		"\x1b":            {{kind: keyOther}},
		"\r\n":            {enter, enter},
		"\x03":            {ctrlC},
		" x3":             {press(' '), press('x'), press('3')},
		"\x1bj":           {{kind: keyOther}, press('j')},
		"\x1b[A \x1b[B\r": {up, press(' '), down, enter},
	}
	for typed, want := range cases {
		r := bufio.NewReader(strings.NewReader(typed))
		var got []key
		for {
			k, err := readKey(r)
			if errors.Is(err, io.EOF) {
				break
			}
			got = append(got, k)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v, want %v", typed, got, want)
		}
	}
}

// keyboard makes the harness a terminal that takes single keys, as a raw
// one does, with keys as the user's presses. It returns how many times the
// terminal was put back.
func (h *harness) keyboard(keys string) *int {
	restored := new(int)
	h.env.interactive = true
	h.env.rawInput = func() (func(), error) {
		return func() { *restored++ }, nil
	}
	h.env.stdin = bufio.NewReader(strings.NewReader(keys))
	return restored
}

func TestSetupChoosesFeaturesWithTheArrowKeys(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	dir := testproject.Write(t, withBilling())

	// Down to purchases, untick it, down to subscriptions, tick it, Enter;
	// then the rest of setup gets lines, and the first, q, stops it.
	restored := h.keyboard("\x1b[B \x1b[B \rq\n")
	_, stdout, _ := h.run("setup", dir)
	lacks(t, stdout,
		render.HideCursor, pickerHint, render.ShowCursor,
		"  ✓ 1) App opens and attribution",
		"    2) In-app purchases",
		"  ✓ 3) Subscriptions",
		"Integrating: App opens and attribution, Subscriptions.",
	)
	if strings.Contains(stdout, "Type numbers") {
		t.Error("the line-at-a-time question was asked as well")
	}
	if *restored != 1 {
		t.Errorf("the terminal was put back %d times, want 1", *restored)
	}
}

func TestCtrlCAtThePickerStops(t *testing.T) {
	h, dir := correctAndroid(t)
	restored := h.keyboard("\x1b[B\x03")
	code, stdout, stderr := h.run("setup", dir)
	if code != 130 || strings.Contains(stdout, "Integrating:") {
		t.Errorf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	if *restored != 1 || !strings.HasSuffix(stdout, render.ShowCursor) {
		t.Errorf("restored %d times; the cursor is shown again: %v", *restored, strings.HasSuffix(stdout, render.ShowCursor))
	}

	// Input that ends at the picker is a usage error, and the terminal is
	// put back too.
	restored = h.keyboard("\x1b[B")
	code, _, stderr = h.run("setup", dir)
	if code != 2 || !strings.Contains(stderr, "the input ended before an answer was given") || *restored != 1 {
		t.Errorf("exit = %d, restored %d, stderr = %q", code, *restored, stderr)
	}
}

func TestSetupChoosesBuildsWithTheArrowKeys(t *testing.T) {
	h, dir := multiStore(t, placeholderStores())
	h.signIn()
	// Enter takes the features; n unticks both builds, and Enter is then
	// refused; space ticks the first; Enter; y creates its app.
	h.keyboard("\rn\r \ry\n\nq\n")
	_, stdout, _ := h.run("setup", dir, "--sdk-key", "android="+testproject.PlayKey)
	lacks(t, stdout, "Which of these do you ship?", "Tick at least one build.", "Setting up 1 build.")
	for _, a := range h.fake.apps {
		if a.Platform == "amazon" {
			t.Errorf("an app was created for a build that does not ship: %+v", a)
		}
	}
}
