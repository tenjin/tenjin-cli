package project

import (
	"path"
	"strings"
)

// BlankComments returns source code with its // and /* */ comments replaced
// by spaces, every line break kept, so offsets and line numbers in the result
// are those of the file. It knows string literals, so "image/*" or
// "https://…" in a string is not taken for the start of a comment: double
// and single quotes, backquotes, and triple-quoted strings, which covers
// Kotlin, Java, Groovy, Swift, Objective-C, Dart, JavaScript, TypeScript and
// C#. A string that is not closed by the end of its line ends there, so one
// stray quote cannot hide the rest of a file. It knows regular expression
// literals too, as JavaScript, TypeScript, Groovy and Swift write them: the
// body of /\/*$/ is blanked like a comment's text, so the /* in it opens no
// comment and a quote in it opens no string, and its slashes stay, as a
// string's quotes do.
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
		case b[i] == '/' && regexStarts(b, i):
			end := endOfRegex(s, i)
			if end < 0 {
				// No closing slash on the line: a division after all.
				i++
				continue
			}
			blank(i+1, end-1)
			i = end
		case strings.HasPrefix(s[i:], `"""`), strings.HasPrefix(s[i:], `'''`):
			end := strings.Index(s[i+3:], s[i:i+3])
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

// regexKeywords are the words after which a slash opens a regular expression
// literal rather than a division: return /x/ is a return of a regex.
var regexKeywords = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true, "of": true, "new": true,
	"delete": true, "void": true, "throw": true, "case": true, "do": true, "else": true,
}

// regexStarts reports whether the slash at i opens a regular expression
// literal: it does where what comes before it cannot end an expression, so
// that it cannot be a division. A slash after a value, a closing bracket or
// a string divides; one after an operator, an opening bracket, a comma, a
// colon, a semicolon, a brace or one of regexKeywords, or at the start,
// does not. b has the comments before i blanked already, so one between
// the two is looked past. Three pairs that touch are not literals: JSX's
// </a> and />, Dart's ~/ (integer division), and a++ or a-- before a
// division.
func regexStarts(b []byte, i int) bool {
	if i+1 < len(b) && b[i+1] == '>' {
		return false
	}
	j := i - 1
	for j >= 0 && space(b[j]) {
		j--
	}
	if j < 0 {
		return true
	}
	switch c := b[j]; {
	case wordByte(c):
		start := j
		for start > 0 && wordByte(b[start-1]) {
			start--
		}
		return regexKeywords[string(b[start:j+1])]
	case c == '+' || c == '-':
		return j == 0 || b[j-1] != c
	case c == '<' || c == '~':
		return j != i-1
	case strings.IndexByte("(,=:[!&|?{};*%>^", c) >= 0:
		return true
	}
	return false
}

// endOfRegex returns the offset just past the slash that closes the regular
// expression literal opening at i, or -1 when its line ends first: a slash
// with no closing one on its line is a division after all. A backslash
// escapes the next character, and a slash inside a character class ([/])
// closes nothing.
func endOfRegex(s string, i int) int {
	inClass := false
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
			if j < len(s) && s[j] == '\n' {
				return -1
			}
		case '\n':
			return -1
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				return j + 1
			}
		}
	}
	return -1
}

// BlankStrings returns code with the contents of its string literals
// replaced by spaces, their quotes and every line break kept, so that
// offsets and line numbers are those of the file and nothing written inside
// a string reads as code. s has its comments blanked already
// (BlankComments); ext is the file's extension, for C#'s verbatim strings.
func BlankStrings(s, ext string) string {
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
		case strings.HasPrefix(s[i:], `"""`), strings.HasPrefix(s[i:], `'''`):
			end := strings.Index(s[i+3:], s[i:i+3])
			if end < 0 {
				blank(i+3, len(s))
				return string(b)
			}
			blank(i+3, i+3+end)
			i += 3 + end + 3
		case ext == ".cs" && (strings.HasPrefix(s[i:], `@"`) || strings.HasPrefix(s[i:], `$@"`) || strings.HasPrefix(s[i:], `@$"`)):
			start := i + strings.IndexByte(s[i:], '"') + 1
			end := endOfVerbatim(s, start)
			blank(start, end)
			i = end + 1
		case b[i] == '"' || b[i] == '\'' || b[i] == '`':
			end := endOfString(s, i)
			closing := end
			if end-1 > i && s[end-1] == b[i] {
				closing = end - 1
			}
			blank(i+1, closing)
			i = end
		default:
			i++
		}
	}
	return string(b)
}

// endOfVerbatim returns the offset of the quote that closes a C# verbatim
// string whose contents start at i, where "" is a quote and a backslash is
// itself.
func endOfVerbatim(s string, i int) int {
	for j := i; j < len(s); j++ {
		if s[j] == '"' {
			if j+1 < len(s) && s[j+1] == '"' {
				j++
				continue
			}
			return j
		}
	}
	return len(s)
}

// readDependencies reads a file that declares dependencies without its
// comments, as its own tool reads it: Ruby's # in a Podfile, # in YAML and
// TOML, none in JSON or a lock file, // and /* */ in Gradle and the rest.
func (d *detector) readDependencies(rel string) string {
	raw := d.read(rel)
	switch {
	case path.Base(rel) == "Podfile":
		return stripRubyComments(raw)
	case strings.HasSuffix(rel, ".lock"), strings.HasSuffix(rel, ".json"):
		return raw
	case strings.HasSuffix(rel, ".yaml"), strings.HasSuffix(rel, ".yml"), strings.HasSuffix(rel, ".toml"):
		return hashComments(raw)
	}
	return stripComments(raw)
}

// hashComments blanks from a # outside quotes to the end of its line,
// keeping line breaks.
func hashComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		var quote byte
	scan:
		for j := 0; j < len(line); j++ {
			switch c := line[j]; {
			case quote != 0 && c == quote:
				quote = 0
			case quote == 0 && (c == '"' || c == '\''):
				quote = c
			case quote == 0 && c == '#':
				lines[i] = line[:j] + strings.Repeat(" ", len(line)-j)
				break scan
			}
		}
	}
	return strings.Join(lines, "\n")
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
