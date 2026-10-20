package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// A real key with a stray space inside its quotes is not a placeholder, and
// is never printed whole: not at the terminal, not in the JSON, not in the
// task an agent is given.
func TestAKeyWithSpacesIsNeverPrintedWhole(t *testing.T) {
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	for name, literal := range map[string]string{
		"trailing space": testproject.Key + " ",
		"tab inside":     testproject.Key[:10] + "\t" + testproject.Key[10:],
	} {
		t.Run(name, func(t *testing.T) {
			dir := testproject.Write(t, base.With(testproject.Files{activity: strings.Replace(base[activity], testproject.Key, literal, 1)}))
			h := newHarness(t)
			code, stdout, stderr := h.run("doctor", dir, "--offline")
			if code != 9 || !strings.Contains(stdout, "The SDK key …MNBV has spaces in it") || strings.Contains(stdout, "still a placeholder") {
				t.Errorf("exit = %d\n%s", code, stdout)
			}
			_, jsonOut, jsonErr := h.run("doctor", dir, "--offline", "--json")
			if !strings.Contains(jsonOut, `"sdk.key.whitespace"`) {
				t.Errorf("--json:\n%s", jsonOut)
			}

			p, err := project.Detect(dir)
			if err != nil {
				t.Fatal(err)
			}
			report := verify.Run(context.Background(), p, verify.Options{})
			prompt := integrationPrompt(p, report, map[string]bool{}, nil, "tenjin doctor --json")
			if !strings.Contains(prompt, "has spaces in it") {
				t.Errorf("the task lacks the finding:\n%s", prompt)
			}
			for where, text := range map[string]string{"stdout": stdout, "stderr": stderr, "--json": jsonOut, "--json stderr": jsonErr, "task": prompt} {
				if strings.Contains(strings.Join(strings.Fields(text), ""), testproject.Key) {
					t.Errorf("the whole key is in %s:\n%s", where, text)
				}
			}
		})
	}
}
