//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The browser is opened by a program detached from the CLI, in a process
// group of its own, so Ctrl-C at the sign-in does not close the browser.
func TestOpenBrowserDetachesTheOpener(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "opened")
	script := "#!/bin/sh\necho \"$$ $(ps -o pgid= -p $$) $1\" > " + out + ".tmp\nmv " + out + ".tmp " + out + "\n"
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	const url = "https://tenjin.example/authorize?state=1"
	if err := openBrowser(url); err != nil {
		t.Fatal(err)
	}
	var fields []string
	for deadline := time.Now().Add(5 * time.Second); len(fields) < 3; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the opener did not run")
		}
		raw, _ := os.ReadFile(out)
		fields = strings.Fields(string(raw))
	}
	if pid, group, opened := fields[0], fields[1], fields[2]; opened != url || group != pid {
		t.Errorf("opened %q as process %s in group %s", opened, pid, group)
	}
}
