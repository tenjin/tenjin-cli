package project

import "strings"

// BlankComments returns source code with its // and /* */ comments replaced
// by spaces, every line break kept, so offsets and line numbers in the result
// are those of the file. It knows string literals, so "image/*" or
// "https://…" in a string is not taken for the start of a comment: double
// and single quotes, backquotes, and triple-quoted strings, which covers
// Kotlin, Java, Groovy, Swift, Objective-C, Dart, JavaScript, TypeScript and
// C#. A string that is not closed by the end of its line ends there, so one
// stray quote cannot hide the rest of a file.
func BlankComments(s string) string {
	b := []byte(s)
	blank := func(from, to int) {
		for i := from; i < to && i < len(b); i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	for i := 0; i < len(b); {
		switch {
		case strings.HasPrefix(s[i:], "//"):
			end := strings.IndexByte(s[i:], '\n')
			if end < 0 {
				end = len(s) - i
			}
			blank(i, i+end)
			i += end
		case strings.HasPrefix(s[i:], "/*"):
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				blank(i, len(s))
				return string(b)
			}
			blank(i, i+2+end+2)
			i += 2 + end + 2
		case strings.HasPrefix(s[i:], `"""`):
			end := strings.Index(s[i+3:], `"""`)
			if end < 0 {
				return string(b)
			}
			i += 3 + end + 3
		case b[i] == '"' || b[i] == '\'' || b[i] == '`':
			i = endOfString(s, i)
		default:
			i++
		}
	}
	return string(b)
}

// endOfString returns the offset just past the string literal that starts
// at i: its closing quote, or the end of the line for a backslash-escaped
// quote or a string left open. A backquoted string may span lines.
func endOfString(s string, i int) int {
	quote := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '\n':
			if quote != '`' {
				return j
			}
		case quote:
			return j + 1
		}
	}
	return len(s)
}
