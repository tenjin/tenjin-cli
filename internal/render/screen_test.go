package render

import "testing"

func TestTruncate(t *testing.T) {
	cases := []struct {
		text  string
		width int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"a line that is too long", 10, "a line th…"},
		{"no width", 0, "no width"},
		// Colour takes no column, and a cut ends it.
		{"\x1b[1mbold text here\x1b[0m", 6, "\x1b[1mbold …\x1b[0m"},
		{"ab\x1b[38;5;245mcd\x1b[0m", 4, "ab\x1b[38;5;245mcd\x1b[0m"},
		// A wide character takes two columns.
		{"项目/build.gradle", 6, "项目/…"},
	}
	for _, tc := range cases {
		if got := Truncate(tc.text, tc.width); got != tc.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tc.text, tc.width, got, tc.want)
		}
	}
}

func TestLinesUp(t *testing.T) {
	if LinesUp(3) != "\r\x1b[3A" || LinesUp(0) != "\r" {
		t.Errorf("LinesUp(3) = %q, LinesUp(0) = %q", LinesUp(3), LinesUp(0))
	}
}
