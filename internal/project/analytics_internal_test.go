package project

import (
	"strings"
	"testing"
)

// alternatives splits a pattern at its top-level |, each written as the
// text it matches: without the escapes, and without \b, which is a word
// boundary rather than text.
func alternatives(pattern string) []string {
	depth := 0
	var out []string
	var cur strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		case c == '\\' && i+1 < len(pattern):
			i++
			if pattern[i] != 'b' {
				cur.WriteByte(pattern[i])
			}
		case c == '(' || c == '[':
			depth++
			cur.WriteByte(c)
		case c == ')' || c == ']':
			depth--
			cur.WriteByte(c)
		case c == '|' && depth == 0:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(out, cur.String())
}

// A file is matched against an SDK's imports only when it holds one of
// the SDK's hints, so each alternative of the imports must hold one.
func TestEveryImportHasAHint(t *testing.T) {
	for _, sdk := range analyticsSDKs {
		for _, alternative := range alternatives(sdk.uses.String()) {
			if !sdk.hinted(alternative) {
				t.Errorf("%s: %q holds none of the hints %q", sdk.name, alternative, sdk.hints)
			}
		}
	}
}

// A source file is matched against a feature's code signal only when it
// holds one of the signal's hints, so each alternative must hold one.
func TestEveryCodeSignalHasAHint(t *testing.T) {
	for _, s := range codeSignals {
		for _, alternative := range alternatives(s.pattern.String()) {
			if !s.hinted(alternative) {
				t.Errorf("%s: %q holds none of the hints %q", s.what, alternative, s.hints)
			}
		}
	}
}
