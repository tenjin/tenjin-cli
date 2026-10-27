package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/skill"
)

func TestAgentInstall(t *testing.T) {
	skillFile := func(dir string) string { return filepath.Join(dir, ".claude", "skills", "tenjin-cli", "SKILL.md") }
	exists := func(path string) bool { _, err := os.Stat(path); return err == nil }

	t.Run("asks, lists the files, then writes", func(t *testing.T) {
		h, dir := newHarness(t), t.TempDir()
		h.typing("n")
		code, _, stderr := h.run("agent", "install", dir, "--agent", "claude")
		if code != 0 || exists(skillFile(dir)) || !strings.Contains(stderr, skillFile(dir)) || !strings.Contains(stderr, "Nothing was changed.") {
			t.Fatalf("answered no: exit = %d, written = %v, stderr = %q", code, exists(skillFile(dir)), stderr)
		}
		h.typing("y")
		code, stdout, _ := h.run("agent", "install", dir, "--agent", "claude")
		if code != 0 || !exists(skillFile(dir)) || !strings.Contains(stdout, "create") {
			t.Errorf("answered yes: exit = %d, stdout:\n%s", code, stdout)
		}
		// Nothing to do the second time, so nothing to ask.
		h.env.interactive = false
		code, stdout, _ = h.run("agent", "install", dir, "--agent", "claude")
		if code != 0 || !strings.Contains(stdout, "Already up to date") {
			t.Errorf("second install: exit = %d, stdout:\n%s", code, stdout)
		}
	})

	t.Run("never writes unasked", func(t *testing.T) {
		h, dir := newHarness(t), t.TempDir()
		code, _, stderr := h.run("agent", "install", dir, "--agent", "claude")
		if code != 2 || exists(skillFile(dir)) || !strings.Contains(stderr, "--yes") {
			t.Errorf("no terminal: exit = %d, stderr = %q", code, stderr)
		}
		code, stdout, _ := h.run("agent", "install", dir, "--agent", "claude", "--dry-run", "--json")
		var result struct {
			Changes []struct{ Agent, Path, Action string }
			Applied bool
		}
		if err := json.Unmarshal([]byte(stdout), &result); err != nil || code != 0 {
			t.Fatalf("dry run: exit = %d, err = %v", code, err)
		}
		if result.Applied || exists(skillFile(dir)) || len(result.Changes) != 1 || result.Changes[0].Action != "create" || result.Changes[0].Path != skillFile(dir) {
			t.Errorf("dry run = %+v, written = %v", result, exists(skillFile(dir)))
		}
	})

	t.Run("picks the agents that are present", func(t *testing.T) {
		h, dir := newHarness(t), t.TempDir()
		// No agent on PATH, and nothing in the project.
		code, _, stderr := h.run("agent", "install", dir, "--yes")
		if code != 5 || !strings.Contains(stderr, "--agent") {
			t.Fatalf("none found: exit = %d, stderr = %q", code, stderr)
		}
		// A project that already has an AGENTS.md gets the section there,
		// and only there.
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Notes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, _, _ = h.run("agent", "install", dir, "--yes")
		raw, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if code != 0 || !strings.HasPrefix(string(raw), "# Notes\n") || !strings.Contains(string(raw), "tenjin-cli:begin") || exists(skillFile(dir)) {
			t.Errorf("exit = %d, claude skill written = %v, AGENTS.md:\n%.200s", code, exists(skillFile(dir)), raw)
		}
	})

	t.Run("global goes to the home directory", func(t *testing.T) {
		h, dir, home := newHarness(t), t.TempDir(), t.TempDir()
		h.vars["HOME"] = home
		if code, _, stderr := h.run("agent", "install", dir, "--agent", "claude", "--global", "--yes"); code != 0 {
			t.Fatalf("exit = %d, stderr = %q", code, stderr)
		}
		if !exists(skillFile(home)) || exists(skillFile(dir)) {
			t.Errorf("in home = %v, in project = %v", exists(skillFile(home)), exists(skillFile(dir)))
		}
		if code, _, stderr := h.run("agent", "install", dir, "--agent", "agents-md", "--global", "--yes"); code != 2 || !strings.Contains(stderr, "per project") {
			t.Errorf("AGENTS.md with --global: exit = %d, stderr = %q", code, stderr)
		}
	})

	t.Run("unknown agent", func(t *testing.T) {
		code, _, stderr := newHarness(t).run("agent", "install", "--agent", "copilot", "--yes")
		if code != 2 || !strings.Contains(stderr, "claude for Claude Code") {
			t.Errorf("exit = %d, stderr = %q", code, stderr)
		}
	})
}

func TestAgentStatusAndUninstall(t *testing.T) {
	h, dir := newHarness(t), t.TempDir()
	states := func() string {
		t.Helper()
		code, stdout, stderr := h.run("agent", "status", dir, "--json")
		var rows []struct{ Agent, State string }
		if err := json.Unmarshal([]byte(stdout), &rows); err != nil || code != 0 {
			t.Fatalf("exit = %d, err = %v, stderr = %q", code, err, stderr)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Agent+"="+r.State)
		}
		return strings.Join(out, " ")
	}

	if got := states(); got != "claude=not_installed agents-md=not_installed" {
		t.Errorf("before: %q", got)
	}
	if code, _, stderr := h.run("agent", "install", dir, "--agent", "claude,agents-md", "--yes"); code != 0 {
		t.Fatalf("install: exit = %d, stderr = %q", code, stderr)
	}
	if got := states(); got != "claude=installed agents-md=installed" {
		t.Errorf("after install: %q", got)
	}

	// Uninstall needs no --agent and no agent on the machine: it removes
	// whatever was written.
	if code, _, stderr := h.run("agent", "uninstall", dir); code != 2 || !strings.Contains(stderr, "--yes") {
		t.Errorf("uninstall without a yes: exit = %d, stderr = %q", code, stderr)
	}
	code, stdout, _ := h.run("agent", "uninstall", dir, "--yes")
	if code != 0 || strings.Count(stdout, "remove") != 2 {
		t.Errorf("uninstall: exit = %d, stdout:\n%s", code, stdout)
	}
	if got := states(); got != "claude=not_installed agents-md=not_installed" {
		t.Errorf("after uninstall: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil {
		t.Error("an AGENTS.md that held only the section was left behind")
	}
	if code, stdout, _ := h.run("agent", "uninstall", dir, "--yes"); code != 0 || !strings.Contains(stdout, "Nothing to remove") {
		t.Errorf("second uninstall: exit = %d, stdout = %q", code, stdout)
	}
}

func TestAgentSkillPrintsTheInstructions(t *testing.T) {
	code, stdout, _ := newHarness(t).run("agent", "skill")
	if code != 0 || stdout != skill.Body() {
		t.Errorf("exit = %d, %d bytes", code, len(stdout))
	}
}

var (
	skillCommand = regexp.MustCompile("(?m)^tenjin .+$")
	inlineCode   = regexp.MustCompile("`(tenjin [^`]+)`")
	longFlag     = regexp.MustCompile(`--[a-z][a-z-]*`)
)

// The instructions are only worth installing if every command and flag in
// them exists in this binary.
func TestTheSkillOnlyNamesCommandsThatExist(t *testing.T) {
	var lines []string
	lines = append(lines, skillCommand.FindAllString(skill.Body(), -1)...)
	for _, m := range inlineCode.FindAllStringSubmatch(skill.Body(), -1) {
		lines = append(lines, m[1])
	}
	if len(lines) < 8 {
		t.Fatalf("found only %d commands in the skill; the patterns no longer match it", len(lines))
	}
	// Agents run a release, which has no data commands yet.
	checkCommandLines(t, newHarness(t).withoutData(), lines)
}

// codex names the AGENTS.md target, which Codex reads.
func TestAgentInstallTakesCodex(t *testing.T) {
	h, dir := newHarness(t), t.TempDir()
	if code, _, stderr := h.run("agent", "install", dir, "--agent", "codex", "--yes"); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md not written: %v", err)
	}
}

// An AGENTS.md whose markers do not fence one section is refused as it is:
// guessing could remove the developer's own text.
func TestAgentInstallLeavesBrokenMarkersAlone(t *testing.T) {
	h, dir := newHarness(t), t.TempDir()
	file := filepath.Join(dir, "AGENTS.md")
	content := "# Notes\n\n<!-- tenjin-cli:begin (written by `tenjin agent install`; edits are overwritten) -->\nmy own text\n"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"install", "uninstall"} {
		code, stdout, stderr := h.run("agent", command, dir, "--agent", "codex", "--yes")
		if code != 1 || stdout != "" || !strings.Contains(stderr, "do not fence one section") {
			t.Errorf("%s: exit = %d, stdout = %q, stderr = %q", command, code, stdout, stderr)
		}
	}
	if raw, _ := os.ReadFile(file); string(raw) != content {
		t.Errorf("AGENTS.md was changed: %q", raw)
	}
}
