package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
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

	// The final list is whole, whatever the height.
	if got := p.lines(render.Styler{}, frame{final: true, height: 2}); len(got) != 4 {
		t.Errorf("final, height 2: %q", got)
	}
}

// A live list taller than the terminal leaves out the second lines, then
// shows the items around the cursor, with how many are above and below.
func TestPickerLinesFitTheHeight(t *testing.T) {
	p := samplePicker()
	plain := func(f frame) string {
		lines := p.lines(render.Styler{}, f)
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		return strings.Join(lines, "\n")
	}
	if got := plain(frame{height: 4}); got != strings.Join([]string{
		"> ✓ 1) App opens      installs",
		"  ✓ 2) Purchases      items",
		"    3) Subscriptions  renewals",
		"  " + pickerHint,
	}, "\n") {
		t.Errorf("height 4:\n%s", got)
	}

	p = &picker{}
	for i := range 12 {
		p.items = append(p.items, pickItem{label: fmt.Sprintf("flavor%02d", i+1), detail: "suggested"})
	}
	for i := 0; i < 6; i++ {
		p.handle(down)
	}
	if got := plain(frame{height: 7}); got != strings.Join([]string{
		"  ↑ 4 more",
		"    5) flavor05",
		"    6) flavor06",
		">   7) flavor07",
		"    8) flavor08",
		"  ↓ 4 more",
		"  " + pickerHint,
	}, "\n") {
		t.Errorf("height 7, cursor on 7:\n%s", got)
	}
	// Every cursor position fits and shows the cursor.
	for range 12 {
		p.handle(down)
		lines := p.lines(render.Styler{}, frame{height: 7})
		if len(lines) > 7 || !strings.Contains(strings.Join(lines, "\n"), ">") {
			t.Fatalf("cursor %d:\n%s", p.cursor, strings.Join(lines, "\n"))
		}
	}
	p.cursor = 0
	if got := plain(frame{height: 7}); !strings.HasPrefix(got, ">   1) flavor01") || !strings.Contains(got, "↓ 8 more") || strings.Contains(got, "↑ ") {
		t.Errorf("at the top:\n%s", got)
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
		"\r\n":            {enter},
		"\r\n\n":          {enter, enter},
		"\r\r":            {enter, enter},
		"\n":              {enter},
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

	// So is Ctrl-D, as at a question asked a line at a time.
	restored = h.keyboard("\x1b[B\x04\r")
	code, stdout, stderr = h.run("setup", dir)
	if code != 2 || !strings.Contains(stderr, "the input ended before an answer was given") || *restored != 1 || strings.Contains(stdout, "Integrating:") {
		t.Errorf("Ctrl-D: exit = %d, restored %d, stderr = %q\n%s", code, *restored, stderr, stdout)
	}
}

// A terminal that sends CR LF for Enter answers the picker only: the LF
// does not also answer the question after it.
func TestEnterAsCRLFAnswersOnlyThePicker(t *testing.T) {
	h, dir := analyticsSetup(t, withAnalytics())
	h.keyboard("a\r\nboss_defeated\n\nq\n")
	_, stdout, _ := h.run("setup", dir, "--agent", "none")
	lacks(t, stdout, "Custom events: boss_defeated.")
}

// liveFrames are the lists a picker drew and drew over, and the last one,
// left on screen.
func liveFrames(stdout string) (live []string, final string) {
	start := strings.Index(stdout, render.HideCursor)
	end := strings.Index(stdout, render.ShowCursor)
	frames := strings.Split(stdout[start:end], render.ClearDown)[1:]
	return frames[:len(frames)-1], frames[len(frames)-1]
}

// A list taller than the terminal is drawn as a window around the cursor,
// so that each frame can be drawn over; a terminal too short for that gets
// the question a line at a time.
func TestThePickerFitsAShortTerminal(t *testing.T) {
	h, dir := analyticsSetup(t, withAnalytics())
	h.env.screenSize = func(io.Writer) (int, int) { return 100, 8 }
	h.keyboard(strings.Repeat("\x1b[B", 8) + "\rq\n")
	_, stdout, _ := h.run("setup", dir, "--agent", "none", "--events", "x", "--user-id", "y")
	live, final := liveFrames(stdout)
	for _, frame := range live {
		if n := strings.Count(frame, "\r\n"); n > 7 {
			t.Errorf("a frame of %d lines on 8 rows:\n%s", n, frame)
		}
	}
	all := strings.Join(live, "")
	lacks(t, all, "↑ ", "↓ ")
	if strings.Count(final, "\r\n") != 8 || strings.Contains(final, "more") {
		t.Errorf("the list left on screen is not whole:\n%s", final)
	}

	h.env.screenSize = func(io.Writer) (int, int) { return 100, 5 }
	h.keyboard("\nq\n")
	_, stdout, _ = h.run("setup", dir, "--agent", "none")
	lacks(t, stdout, "Type numbers to add or remove")
	if strings.Contains(stdout, render.HideCursor) {
		t.Errorf("the picker ran on 5 rows:\n%s", stdout)
	}
}

// The live list is cut to the width so it never wraps; the one left on
// screen is not redrawn, so it is whole.
func TestTheChosenListIsWhole(t *testing.T) {
	h, dir := correctAndroid(t)
	h.env.screenSize = func(io.Writer) (int, int) { return 40, 30 }
	h.keyboard("\rq\n")
	_, stdout, _ := h.run("setup", dir)
	live, final := liveFrames(stdout)
	if len(live) != 1 || !strings.Contains(live[0], "…") {
		t.Errorf("the live list is not cut:\n%q", live)
	}
	if strings.Contains(final, "…") || !strings.Contains(final, "installs and opens, by campaign (always included)") {
		t.Errorf("the chosen list is cut:\n%s", final)
	}
}

// With --verbose, a line logged while the terminal is in raw mode waits
// until it is back as it was.
func TestVerboseLinesWaitForThePicker(t *testing.T) {
	h := newHarness(t)
	var log bytes.Buffer
	h.env.verbose = &heldLog{w: &log}
	h.keyboard("\r")
	const line = "tenjin: GET https://releases.example/latest → 200 in 3ms\n"
	h.env.rawInput = func() (func(), error) {
		_, _ = io.WriteString(h.env.verbose, line)
		return func() {
			if log.Len() != 0 {
				t.Errorf("written in raw mode: %q", log.String())
			}
		}, nil
	}
	if ok, err := h.env.pick(io.Discard, render.Styler{}, samplePicker()); !ok || err != nil {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if log.String() != line {
		t.Errorf("log = %q", log.String())
	}
	_, _ = io.WriteString(h.env.verbose, line)
	if log.String() != line+line {
		t.Errorf("a line after the picker waited: %q", log.String())
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
