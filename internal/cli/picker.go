package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/tenjin/tenjin-cli/internal/render"
)

// pickItem is one choice of a picker.
type pickItem struct {
	label string
	// about says what the item is, dim after the label; detail is a second,
	// dim line, such as why it is suggested.
	about  string
	detail string
	ticked bool
	// fixed, when set, keeps the item ticked, and is what is said when
	// someone tries to untick it.
	fixed string
}

// keyKind is a key the picker knows.
type keyKind int

const (
	keyOther keyKind = iota
	keyUp
	keyDown
	keyEnter
	keyInterrupt
	keyRune
)

type key struct {
	kind keyKind
	r    rune
}

// pickerHint is the line of keys shown under the list.
const pickerHint = "↑/↓ move · space ticks · a all · n none · Enter accepts"

// picker is a multiple choice made with the keyboard: a list with a cursor,
// each item ticked or not. ↑/↓ (or k/j) move the cursor, wrapping at the
// ends; space or x ticks or unticks the item under it; a digit from 1 to 9
// does the same to that item (further items are reached with the arrows);
// a ticks all, n unticks all but the fixed ones; Enter accepts and Ctrl-C
// stops. Esc does nothing: it is where the arrow keys' sequences start, and
// stopping and taking the choice as it stands would both be guesses. It is
// pure state, so tests drive it with keys and read its lines.
type picker struct {
	items  []pickItem
	cursor int
	// needOne, when set, refuses Enter with nothing ticked, and is what is
	// said instead.
	needOne string
	// note is what the last key could not do.
	note    string
	done    bool
	stopped bool
}

func (p *picker) handle(k key) {
	p.note = ""
	n := len(p.items)
	switch {
	case k.kind == keyInterrupt:
		p.stopped = true
	case k.kind == keyEnter:
		if p.needOne != "" && len(p.ticked()) == 0 {
			p.note = p.needOne
			return
		}
		p.done = true
	case k.kind == keyUp || k.kind == keyRune && k.r == 'k':
		p.cursor = (p.cursor - 1 + n) % n
	case k.kind == keyDown || k.kind == keyRune && k.r == 'j':
		p.cursor = (p.cursor + 1) % n
	case k.kind != keyRune:
	case k.r == ' ' || k.r == 'x':
		p.toggle(p.cursor)
	case k.r >= '1' && k.r <= '9':
		i := int(k.r - '1')
		if i >= n {
			p.note = fmt.Sprintf("There are %d in the list.", n)
			return
		}
		p.cursor = i
		p.toggle(i)
	case k.r == 'a':
		for i := range p.items {
			p.items[i].ticked = true
		}
	case k.r == 'n':
		for i := range p.items {
			p.items[i].ticked = p.items[i].fixed != ""
		}
	}
}

func (p *picker) toggle(i int) {
	if p.items[i].fixed != "" {
		p.note = p.items[i].fixed
		return
	}
	p.items[i].ticked = !p.items[i].ticked
}

// ticked are the indexes of the ticked items, in order.
func (p *picker) ticked() []int {
	var out []int
	for i, it := range p.items {
		if it.ticked || it.fixed != "" {
			out = append(out, i)
		}
	}
	return out
}

// frame is how the list is drawn: live, with the cursor, the hint and any
// note, or final, as it was chosen. width truncates each line (0 does not).
// height, for a live list, is the most lines it may take (0 is no limit):
// past it the second lines go, and then the items far from the cursor, with
// a line saying how many are above and below.
type frame struct {
	final  bool
	width  int
	height int
}

func (p *picker) lines(style render.Styler, f frame) []string {
	labels := make([]string, len(p.items))
	width := 0
	for i, it := range p.items {
		labels[i] = fmt.Sprintf("%d) %s", i+1, it.label)
		width = max(width, utf8.RuneCountInString(labels[i]))
	}
	item := func(i int, details bool) []string {
		it := p.items[i]
		lead, label := "  ", fmt.Sprintf("%-*s", width, labels[i])
		if !f.final && i == p.cursor {
			lead, label = style.Accent(">")+" ", style.Bold(label)
		}
		mark := " "
		if it.ticked || it.fixed != "" {
			mark = style.Green("✓")
		}
		out := []string{lead + mark + " " + label + "  " + style.Dim(it.about)}
		if it.detail != "" && details {
			out = append(out, "    "+strings.Repeat(" ", width)+"  "+style.Dim(it.detail))
		}
		return out
	}
	var footer []string
	if !f.final {
		footer = append(footer, "  "+style.Dim(pickerHint))
		if p.note != "" {
			footer = append(footer, "  "+style.Yellow(p.note))
		}
	}

	n := len(p.items)
	details, first, last := true, 0, n
	if !f.final && f.height > 0 {
		total := len(footer)
		for i := range p.items {
			total += len(item(i, true))
		}
		if total > f.height {
			details, total = false, len(footer)+n
		}
		if total > f.height {
			// One line each for "more above" and "more below".
			shown := max(1, f.height-len(footer)-2)
			first = min(max(0, p.cursor-shown/2), n-shown)
			last = first + shown
		}
	}
	var out []string
	if first > 0 {
		out = append(out, "  "+style.Dim(fmt.Sprintf("↑ %d more", first)))
	}
	for i := first; i < last; i++ {
		out = append(out, item(i, details)...)
	}
	if last < n {
		out = append(out, "  "+style.Dim(fmt.Sprintf("↓ %d more", n-last)))
	}
	out = append(out, footer...)
	for i := range out {
		out[i] = render.Truncate(out[i], f.width)
	}
	return out
}

// canPick reports whether a choice can be made with the arrow keys.
func (e *environment) canPick() bool { return e.interactive && e.rawInput != nil }

// minPickHeight is the fewest rows a terminal needs for a picker: one item
// between the lines for more above and below, the hint, a note, and the
// line the cursor rests on.
const minPickHeight = 6

// pick runs a picker at the terminal until Enter accepts the choice, and
// leaves the list on screen as chosen. Ctrl-C stops it, as everywhere, and
// Ctrl-D ends the input, as it does a line. ok is false when the terminal
// could not be put in raw mode, or is too short, for the question to be
// asked a line at a time instead.
func (e *environment) pick(w io.Writer, style render.Styler, p *picker) (ok bool, err error) {
	if !e.canPick() {
		return false, nil
	}
	if _, height := e.size(w); height > 0 && height < minPickHeight {
		return false, nil
	}
	// In raw mode a line feed does not return the carriage, so a --verbose
	// line written meanwhile would break the list; it waits until the
	// terminal is back as it was.
	if log, ok := e.verbose.(*heldLog); ok {
		defer log.hold()()
	}
	restore, err := e.rawInput()
	if err != nil {
		return false, nil
	}
	defer restore()
	_, _ = io.WriteString(w, render.HideCursor)
	defer func() { _, _ = io.WriteString(w, render.ShowCursor) }()

	drawn := 0
	draw := func() {
		// The live list fits the screen, so that it can be drawn again in
		// place; the final one is drawn once, whole, and may wrap.
		f := frame{final: p.done}
		if !p.done {
			width, height := e.size(w)
			f.width, f.height = width-1, height-1
		}
		lines := p.lines(style, f)
		var b strings.Builder
		if drawn > 0 {
			b.WriteString(render.LinesUp(drawn))
		}
		b.WriteString(render.ClearDown)
		for _, line := range lines {
			// In raw mode a line feed does not return the carriage.
			b.WriteString(line + "\r\n")
		}
		_, _ = io.WriteString(w, b.String())
		drawn = len(lines)
	}
	draw()
	for !p.done {
		k, err := e.readKey()
		if err != nil {
			return true, err
		}
		p.handle(k)
		if p.stopped {
			return true, errInterrupted()
		}
		draw()
	}
	return true, nil
}

// heldLog is the --verbose log. While held, its lines wait, and are written
// once it is released.
type heldLog struct {
	mu   sync.Mutex
	w    io.Writer
	held bool
	kept bytes.Buffer
}

func (l *heldLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held {
		return l.kept.Write(b)
	}
	return l.w.Write(b)
}

// hold keeps the lines written from now on, and returns what writes them.
func (l *heldLog) hold() (release func()) {
	l.mu.Lock()
	l.held = true
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.held = false
		_, _ = l.w.Write(l.kept.Bytes())
		l.kept.Reset()
	}
}

// size is the size of the terminal w writes to, or zeros.
func (e *environment) size(w io.Writer) (width, height int) {
	if e.screenSize != nil {
		return e.screenSize(w)
	}
	return screenSize(w)
}

func screenSize(w io.Writer) (width, height int) {
	f, ok := w.(*os.File)
	if !ok {
		return 0, 0
	}
	width, height, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0, 0
	}
	return width, height
}

// readKey waits for a key press. Ctrl-C, in raw mode, is a byte like any
// other; a signal from outside still ends the wait.
func (e *environment) readKey() (key, error) {
	type read struct {
		k   key
		err error
	}
	done := make(chan read, 1)
	go func() {
		k, err := readKey(e.stdin)
		done <- read{k, err}
	}()
	select {
	case r := <-done:
		return r.k, r.err
	case <-e.context().Done():
		return key{}, errInterrupted()
	}
}

// readKey reads one key from a terminal in raw mode. An arrow key is an
// escape sequence, ESC [ A or ESC O A for up (B for down), which arrives in
// one piece; an ESC with nothing after it is a key the picker ignores.
func readKey(r *bufio.Reader) (key, error) {
	c, _, err := r.ReadRune()
	if err != nil {
		return key{}, err
	}
	switch c {
	case 3:
		return key{kind: keyInterrupt}, nil
	case 4:
		return key{}, io.EOF
	case '\r':
		// A terminal that sends CR LF for Enter sends both at once; the LF
		// must not answer the next question.
		if r.Buffered() > 0 {
			if next, _ := r.ReadByte(); next != '\n' {
				_ = r.UnreadByte()
			}
		}
		return key{kind: keyEnter}, nil
	case '\n':
		return key{kind: keyEnter}, nil
	case 0x1b:
		if r.Buffered() == 0 {
			return key{kind: keyOther}, nil
		}
		next, _ := r.ReadByte()
		if next != '[' && next != 'O' {
			_ = r.UnreadByte()
			return key{kind: keyOther}, nil
		}
		// Parameters, if any, then the final byte, which names the key.
		for r.Buffered() > 0 {
			b, _ := r.ReadByte()
			if b >= 0x40 && b <= 0x7e {
				switch b {
				case 'A':
					return key{kind: keyUp}, nil
				case 'B':
					return key{kind: keyDown}, nil
				}
				return key{kind: keyOther}, nil
			}
		}
		return key{kind: keyOther}, nil
	}
	return key{kind: keyRune, r: c}, nil
}
