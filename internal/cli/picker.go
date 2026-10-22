package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
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
// stops. It is pure state, so tests drive it with keys and read its lines.
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
// note, or final, as it was chosen. width truncates each line (0 does not);
// without details, the second lines are left out, for a short terminal.
type frame struct {
	final     bool
	width     int
	noDetails bool
}

func (p *picker) lines(style render.Styler, f frame) []string {
	labels := make([]string, len(p.items))
	width := 0
	for i, it := range p.items {
		labels[i] = fmt.Sprintf("%d) %s", i+1, it.label)
		width = max(width, utf8.RuneCountInString(labels[i]))
	}
	var out []string
	for i, it := range p.items {
		lead, label := "  ", fmt.Sprintf("%-*s", width, labels[i])
		if !f.final && i == p.cursor {
			lead, label = style.Accent(">")+" ", style.Bold(label)
		}
		mark := " "
		if it.ticked || it.fixed != "" {
			mark = style.Green("✓")
		}
		out = append(out, lead+mark+" "+label+"  "+style.Dim(it.about))
		if it.detail != "" && !f.noDetails {
			out = append(out, "    "+strings.Repeat(" ", width)+"  "+style.Dim(it.detail))
		}
	}
	if !f.final {
		out = append(out, "  "+style.Dim(pickerHint))
		if p.note != "" {
			out = append(out, "  "+style.Yellow(p.note))
		}
	}
	for i := range out {
		out[i] = render.Truncate(out[i], f.width)
	}
	return out
}

// canPick reports whether a choice can be made with the arrow keys.
func (e *environment) canPick() bool { return e.interactive && e.rawInput != nil }

// pick runs a picker at the terminal until Enter accepts the choice, and
// leaves the list on screen as chosen. Ctrl-C stops it, as everywhere.
// ok is false when the terminal could not be put in raw mode, for the
// question to be asked a line at a time instead.
func (e *environment) pick(w io.Writer, style render.Styler, p *picker) (ok bool, err error) {
	if !e.canPick() {
		return false, nil
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
		width, height := screenSize(w)
		f := frame{final: p.done, width: width - 1}
		lines := p.lines(style, f)
		if height > 0 && len(lines) >= height {
			f.noDetails = true
			lines = p.lines(style, f)
		}
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

// screenSize is the size of the terminal w writes to, or zeros.
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
	case '\r', '\n':
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
