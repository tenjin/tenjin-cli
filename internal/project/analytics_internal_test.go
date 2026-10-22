package project

import (
	"strings"
	"testing"
)

// A file is matched against an SDK's imports only when it holds one of
// the SDK's hints, so each alternative of the imports must hold one.
func TestEveryImportHasAHint(t *testing.T) {
	for _, sdk := range analyticsSDKs {
		pattern := sdk.uses.String()
		depth, start := 0, 0
		var alternatives []string
		for i := 0; i <= len(pattern); i++ {
			switch {
			case i == len(pattern):
				alternatives = append(alternatives, pattern[start:i])
			case pattern[i] == '\\':
				i++
			case pattern[i] == '(' || pattern[i] == '[':
				depth++
			case pattern[i] == ')' || pattern[i] == ']':
				depth--
			case pattern[i] == '|' && depth == 0:
				alternatives = append(alternatives, pattern[start:i])
				start = i + 1
			}
		}
		for _, alternative := range alternatives {
			if !sdk.hinted(strings.ReplaceAll(alternative, `\`, "")) {
				t.Errorf("%s: %q holds none of the hints %q", sdk.name, alternative, sdk.hints)
			}
		}
	}
}
