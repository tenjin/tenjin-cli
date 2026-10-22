package render

import (
	"fmt"
	"os"
	"strings"
)

// What redraws a list in place, such as setup's choices made with the arrow
// keys. Like colour, these are for a terminal only.
const (
	HideCursor = "\x1b[?25l"
	ShowCursor = "\x1b[?25h"
	// ClearDown clears from the cursor to the end of the screen.
	ClearDown = "\x1b[J"
)

// LinesUp moves the cursor to the start of the line n lines up.
func LinesUp(n int) string {
	if n <= 0 {
		return "\r"
	}
	return fmt.Sprintf("\r\x1b[%dA", n)
}

// CursorControl turns on, where it must be asked for (a Windows console),
// the terminal's handling of escape codes, and reports whether f handles
// them.
func CursorControl(f *os.File) bool { return colourSupported(f) }

// Truncate shortens text to at most width columns, ending it with … where
// it is cut, so that a line never wraps. Escape codes take no column and are
// kept; a cut ends any colour. Width 0 or less leaves the text as it is.
func Truncate(text string, width int) string {
	if width <= 0 || columns(text) <= width {
		return text
	}
	var b strings.Builder
	used, styled, next := 0, false, 0
	for i, r := range text {
		if i < next {
			continue
		}
		if r == 0x1b {
			next = escapeEnd(text, i)
			b.WriteString(text[i:next])
			styled = true
			continue
		}
		if used+runeWidth(r) > width-1 {
			break
		}
		b.WriteRune(r)
		used += runeWidth(r)
	}
	b.WriteString("…")
	if styled {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// columns is the number of columns text takes, escape codes left out.
func columns(text string) int {
	n, next := 0, 0
	for i, r := range text {
		switch {
		case i < next:
		case r == 0x1b:
			next = escapeEnd(text, i)
		default:
			n += runeWidth(r)
		}
	}
	return n
}

// escapeEnd is the index just past the escape sequence starting at i: a
// CSI sequence (ESC [ … final byte), or ESC and the one byte after it.
func escapeEnd(text string, i int) int {
	if i+1 < len(text) && text[i+1] == '[' {
		for j := i + 2; j < len(text); j++ {
			if text[j] >= 0x40 && text[j] <= 0x7e {
				return j + 1
			}
		}
		return len(text)
	}
	return min(i+2, len(text))
}

// runeWidth is 2 for the wide characters of East Asian scripts and for
// emoji, and 1 for the rest: near enough for a line not to wrap.
func runeWidth(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115f,
		r >= 0x2e80 && r <= 0xa4cf,
		r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe30 && r <= 0xfe4f,
		r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1faff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}
