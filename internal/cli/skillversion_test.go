package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// writeOldSkill puts a copy of the skill in dir as the given version of the
// CLI wrote it; "" for one written before versions were recorded.
func writeOldSkill(t *testing.T, dir, version string) string {
	t.Helper()
	by := "`tenjin agent install`"
	if version != "" {
		by += " (Tenjin CLI " + version + ")"
	}
	path := skillFile(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: tenjin-cli\ndescription: old\n---\n\n<!-- Written by " + by + ". Edits are overwritten; run it again after updating the CLI. -->\n\n# Old instructions\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// doctor names, for information, each copy of the skill another version
// wrote: in the project, and in the home folder.
func TestDoctorNamesASkillAnotherVersionWrote(t *testing.T) {
	h, dir, home := newHarness(t), testproject.Write(t, testproject.AndroidNative()), t.TempDir()
	h.vars["HOME"] = home
	findings := func() (int, []verify.Finding) {
		t.Helper()
		code, stdout, stderr := h.run("doctor", dir, "--offline", "--json")
		var report verify.Report
		if err := json.Unmarshal([]byte(stdout), &report); err != nil {
			t.Fatalf("exit = %d, err = %v, stderr = %q", code, err, stderr)
		}
		var out []verify.Finding
		for _, f := range report.Findings {
			if f.ID == "agent.skill.version" {
				out = append(out, f)
			}
		}
		return code, out
	}

	clean, none := findings()
	if len(none) != 0 {
		t.Fatalf("no skill installed: %+v", none)
	}
	// One this version wrote is not mentioned.
	if code, _, stderr := h.run("agent", "install", dir, "--agent", "claude", "--yes"); code != 0 {
		t.Fatalf("install: exit = %d, %s", code, stderr)
	}
	if _, found := findings(); len(found) != 0 {
		t.Errorf("a skill this version wrote: %+v", found)
	}

	writeOldSkill(t, dir, "0.0.1")
	global := writeOldSkill(t, home, "")
	code, found := findings()
	if code != clean || len(found) != 2 {
		t.Fatalf("exit = %d (was %d), findings = %+v", code, clean, found)
	}
	if f := found[0]; f.Severity != verify.Info || f.File != ".claude/skills/tenjin-cli/SKILL.md" || !strings.Contains(f.Title, "another version of the CLI, 0.0.1") || f.Fix != "Run 'tenjin agent install'." {
		t.Errorf("the project's copy: %+v", f)
	}
	if f := found[1]; f.File != global || !strings.Contains(f.Title, "an earlier version of the CLI") || f.Fix != "Run 'tenjin agent install --global'." {
		t.Errorf("the home folder's copy: %+v", f)
	}

	_, stdout, _ := h.run("doctor", dir, "--offline")
	if !strings.Contains(stdout, "The tenjin-cli skill your coding agent reads was written by") || !strings.Contains(stdout, "tenjin agent install --global") {
		t.Errorf("the report does not name the copies:\n%s", stdout)
	}
}

// An agent that uses a command or flag this version does not have, with
// an older skill installed, is told where it most likely took it from.
func TestAnUnknownNameFromInsideAnAgentPointsAtAnOlderSkill(t *testing.T) {
	h, dir := newHarness(t), t.TempDir()
	path := writeOldSkill(t, dir, "")
	t.Chdir(dir)

	for _, args := range [][]string{{"doctor", "--fix-it"}, {"projekt", "detect"}, {"project", "dettect"}} {
		code, _, stderr := h.run(args...)
		if code != 2 || strings.Contains(stderr, "tenjin-cli skill") {
			t.Errorf("%v from a person: exit = %d, stderr = %q", args, code, stderr)
		}
	}

	h.vars["CODEX_THREAD_ID"] = "thread-1"
	for _, args := range [][]string{{"doctor", "--fix-it"}, {"projekt", "detect"}, {"project", "dettect"}} {
		code, _, stderr := h.run(args...)
		if code != 2 || !strings.Contains(stderr, "The installed tenjin-cli skill, "+path+", was written by an earlier version of the CLI") || !strings.Contains(stderr, "run 'tenjin agent install'") {
			t.Errorf("%v from an agent: exit = %d, stderr = %q", args, code, stderr)
		}
	}
	code, _, stderr := h.run("doctor", "--fix-it", "--json")
	var body struct{ Error struct{ Code, Hint string } }
	if err := json.Unmarshal([]byte(stderr), &body); err != nil || code != 2 || !strings.Contains(body.Error.Hint, "tenjin agent install") {
		t.Errorf("--json: exit = %d, err = %v, stderr = %q", code, err, stderr)
	}

	// A flag given a wrong value is not a name the skill could have
	// taught, and a skill this version wrote is not to blame.
	if code, _, stderr := h.run("doctor", "--strict=maybe"); code != 2 || strings.Contains(stderr, "tenjin-cli skill") {
		t.Errorf("a wrong value: exit = %d, stderr = %q", code, stderr)
	}
	if code, _, stderr := h.run("agent", "install", dir, "--agent", "claude", "--yes"); code != 0 {
		t.Fatalf("install: exit = %d, %s", code, stderr)
	}
	if code, _, stderr := h.run("doctor", "--fix-it"); code != 2 || strings.Contains(stderr, "tenjin-cli skill") {
		t.Errorf("with a current skill: exit = %d, stderr = %q", code, stderr)
	}
}

// The skill named is the one in the project the command was given, not in
// the folder it was run from.
func TestTheSkillNamedIsTheProjectsWhereverTheCommandRuns(t *testing.T) {
	h, dir := newHarness(t), testproject.Write(t, testproject.AndroidNative())
	withRelease(t, h, "v0.1.0")
	path := writeOldSkill(t, dir, "0.0.9")
	t.Chdir(t.TempDir())
	if err := os.WriteFile(h.env.updates.State, []byte(`{"ran":"0.0.9"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := h.run("project", "detect", dir); code != 0 || !strings.Contains(stderr, "The tenjin-cli skill in "+path+" was written by") {
		t.Errorf("after an update: exit = %d, stderr:\n%s", code, stderr)
	}

	// The path is known when it comes before the flag that does not exist:
	// the arguments after it are not read.
	h.vars["CODEX_THREAD_ID"] = "thread-1"
	if code, _, stderr := h.run("doctor", dir, "--fix-it"); code != 2 || !strings.Contains(stderr, "The installed tenjin-cli skill, "+path+",") {
		t.Errorf("from an agent: exit = %d, stderr = %q", code, stderr)
	}
}

// The first run after an update names, once, the copies of the skill the
// update left behind; never with --json.
func TestTheFirstRunAfterAnUpdateNamesAnOlderSkill(t *testing.T) {
	h, dir := newHarness(t), testproject.Write(t, testproject.AndroidNative())
	withRelease(t, h, "v0.1.0")
	path := writeOldSkill(t, dir, "0.0.9")
	t.Chdir(dir)
	if err := os.WriteFile(h.env.updates.State, []byte(`{"ran":"0.0.9"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	line := "The tenjin-cli skill in " + path + " was written by an earlier version of the CLI, 0.0.9. Update it with: tenjin agent install"

	if _, _, stderr := h.run("project", "detect", dir, "--json"); stderr != "" {
		t.Errorf("--json: stderr = %q", stderr)
	}
	code, stdout, stderr := h.run("project", "detect", dir)
	if code != 0 || !strings.Contains(stderr, line) || strings.Contains(stdout, "tenjin-cli skill") {
		t.Errorf("exit = %d, stderr:\n%s", code, stderr)
	}
	if _, _, stderr := h.run("project", "detect", dir); strings.Contains(stderr, "tenjin-cli skill") {
		t.Errorf("told twice:\n%s", stderr)
	}
}
