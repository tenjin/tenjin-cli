package cli

import (
	"bufio"
	"strings"
	"testing"
)

// Input that ends at any prompt is a usage error that says so, never an
// Enter, a skip or a no that nobody typed.
func TestInputEndingAtEachPrompt(t *testing.T) {
	cases := []struct {
		name   string
		start  func(t *testing.T) (*harness, []string)
		typed  string
		prompt string
	}{
		{"the feature choice", func(t *testing.T) (*harness, []string) {
			h, dir := correctAndroid(t)
			return h, []string{"setup", dir}
		}, "", "Press Enter when the"},
		{"the build choice", func(t *testing.T) (*harness, []string) {
			h, dir := multiStore(t, placeholderStores())
			h.signIn()
			return h, []string{"setup", dir}
		}, "\n", "Which of these do you ship?"},
		{"a key to paste", func(t *testing.T) (*harness, []string) {
			h, dir := agentPlaceholderAndroid(t)
			return h, []string{"project", "set-keys", dir}
		}, "", "Paste the Android SDK key"},
		{"the launch", func(t *testing.T) (*harness, []string) {
			h, dir := correctAndroid(t)
			h.connectDevice(readCapturedLog(t))
			return h, []string{"setup", dir}
		}, "\n", "then press Enter (s to skip)"},
		{"a yes or no", func(t *testing.T) (*harness, []string) {
			return newHarness(t), []string{"agent", "install", t.TempDir(), "--agent", "claude"}
		}, "", "Proceed? [Y/n]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, args := tc.start(t)
			h.env.interactive = true
			h.env.stdin = bufio.NewReader(strings.NewReader(tc.typed))
			code, stdout, stderr := h.run(args...)
			if code != 2 || !strings.Contains(stdout+stderr, tc.prompt) || !strings.Contains(stderr, "the input ended before an answer was given") {
				t.Errorf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
			}
			if strings.Contains(stderr, "Nothing was changed.") || strings.Contains(stdout, "Skipped.") {
				t.Errorf("the end of the input was taken for an answer:\n%s%s", stdout, stderr)
			}
		})
	}
}
