package verify

import (
	"path"
	"regexp"
	"strings"
)

// Every check reads a file as its own tools do: a commented-out dependency,
// permission or key is not there.

var (
	xmlComment      = regexp.MustCompile(`(?s)<!--.*?-->`)
	propertyComment = regexp.MustCompile(`(?m)^[ \t]*[#!].*$`)
)

// withoutComments blanks a file's comments as its own language writes them,
// keeping every line break: properties files' # and ! lines, # to the end of
// the line in TOML and ProGuard rules, XML's <!-- -->, none in JSON, and //
// and /* */ in the rest.
func withoutComments(rel, text string) string {
	switch path.Ext(rel) {
	case ".properties":
		return propertyComment.ReplaceAllStringFunc(text, blank)
	case ".toml", ".pro", ".txt":
		return hashComments(text)
	case ".plist", ".xml":
		return xmlComment.ReplaceAllStringFunc(text, blank)
	case ".json":
		return text
	}
	return blankComments(text)
}

// readCode reads a file by its path relative to the project root, without
// its comments; "" when it cannot be read.
func (r *runner) readCode(rel string) string {
	if rel == "" {
		return ""
	}
	return withoutComments(rel, r.readRel(rel))
}

// blank turns text into spaces, keeping its line breaks.
func blank(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		return ' '
	}, text)
}

// hashComments blanks from a # outside quotes to the end of its line.
func hashComments(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		var quote rune
		for j, c := range line {
			switch {
			case quote != 0 && c == quote:
				quote = 0
			case quote == 0 && (c == '"' || c == '\''):
				quote = c
			case quote == 0 && c == '#':
				lines[i] = line[:j] + blank(line[j:])
			}
			if lines[i] != line {
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}
