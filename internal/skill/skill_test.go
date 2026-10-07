package skill

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func target(t *testing.T, id string) Target {
	t.Helper()
	found, ok := FindTarget(id)
	if !ok {
		t.Fatalf("no target %q", id)
	}
	return found
}

func install(t *testing.T, tg Target, loc Location) Change {
	t.Helper()
	change, err := PlanInstall(tg, loc)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(change); err != nil {
		t.Fatal(err)
	}
	return change
}

func remove(t *testing.T, tg Target, loc Location) Change {
	t.Helper()
	change, err := PlanRemove(tg, loc)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(change); err != nil {
		t.Fatal(err)
	}
	return change
}

func state(t *testing.T, tg Target, loc Location) State {
	t.Helper()
	s, _, err := Check(tg, loc)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestClaudeSkillInstallAndRemove(t *testing.T) {
	dir := t.TempDir()
	claude, loc := target(t, "claude"), Location{ProjectDir: dir}
	file := filepath.Join(dir, ".claude", "skills", "tenjin-cli", "SKILL.md")

	if got := state(t, claude, loc); got != NotInstalled {
		t.Fatalf("state = %q", got)
	}
	if c := install(t, claude, loc); c.Action != Create || c.Path != file {
		t.Fatalf("change = %+v", c)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "---\nname: tenjin-cli\ndescription: ") || !strings.Contains(string(raw), "tenjin doctor --json") {
		t.Errorf("skill file:\n%.300s", raw)
	}
	// A second install has nothing to do.
	if c := install(t, claude, loc); c.Action != Unchanged || state(t, claude, loc) != Current {
		t.Errorf("second install = %+v", c)
	}

	// An older copy is replaced.
	if err := os.WriteFile(file, []byte("---\nname: tenjin-cli\n---\nold\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := state(t, claude, loc); got != Outdated {
		t.Errorf("state of an old copy = %q", got)
	}
	if c := install(t, claude, loc); c.Action != Update {
		t.Errorf("change = %+v", c)
	}

	// Removal takes the skill's folder and leaves the ones above it, which
	// may hold the user's own skills.
	other := filepath.Join(dir, ".claude", "skills", "mine", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := remove(t, claude, loc); c.Action != Remove {
		t.Errorf("change = %+v", c)
	}
	if _, err := os.Stat(filepath.Dir(file)); !errors.Is(err, os.ErrNotExist) {
		t.Error("the skill's folder is still there")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("another skill was removed")
	}
	if c := remove(t, claude, loc); c.Action != Absent {
		t.Errorf("second removal = %+v", c)
	}
}

func TestClaudeSkillGlobal(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	c := install(t, target(t, "claude"), Location{ProjectDir: project, Home: home, Global: true})
	if c.Path != filepath.Join(home, ".claude", "skills", "tenjin-cli", "SKILL.md") {
		t.Errorf("path = %q", c.Path)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude")); err == nil {
		t.Error("a global install wrote into the project")
	}

	if _, err := PlanInstall(target(t, "agents-md"), Location{ProjectDir: project, Home: home, Global: true}); err == nil {
		t.Error("AGENTS.md has no global location, but one was planned")
	}
	if _, err := PlanInstall(target(t, "claude"), Location{ProjectDir: project, Global: true}); err == nil {
		t.Error("a global install without a home directory was planned")
	}
}

// The developer's own AGENTS.md content must come through an install and a
// removal untouched.
func TestAgentsFileKeepsEverythingElse(t *testing.T) {
	cases := []struct{ name, before, after string }{
		{"no file", "", ""},
		{"ends with a newline", "# Notes\n\nUse tabs.\n", "# Notes\n\nUse tabs.\n"},
		{"no trailing newline", "# Notes", "# Notes\n"},
		{"windows line endings", "# Notes\r\n\r\nUse tabs.\r\n", "# Notes\r\n\r\nUse tabs.\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "AGENTS.md")
			agents, loc := target(t, "agents-md"), Location{ProjectDir: dir}
			if tc.before != "" {
				if err := os.WriteFile(file, []byte(tc.before), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			c := install(t, agents, loc)
			if want := map[bool]Action{true: Create, false: Update}[tc.before == ""]; c.Action != want {
				t.Errorf("install action = %q, want %q", c.Action, want)
			}
			raw, _ := os.ReadFile(file)
			if !strings.HasPrefix(string(raw), tc.before) || !strings.Contains(string(raw), "tenjin doctor --json") {
				t.Errorf("after install:\n%.200s", raw)
			}
			if strings.Count(string(raw), "tenjin-cli:begin") != 1 {
				t.Error("the section is not there exactly once")
			}
			// Installing again replaces the section; it does not add another.
			if c := install(t, agents, loc); c.Action != Unchanged {
				t.Errorf("second install = %q", c.Action)
			}

			remove(t, agents, loc)
			raw, err := os.ReadFile(file)
			switch {
			case tc.after == "" && !errors.Is(err, os.ErrNotExist):
				t.Errorf("a file that held only the section was kept: %q", raw)
			case tc.after != "" && string(raw) != tc.after:
				t.Errorf("after removal = %q, want %q", raw, tc.after)
			}
		})
	}
}

func TestAgentsFileSectionInTheMiddleIsReplacedInPlace(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "AGENTS.md")
	old := "# Top\n\n<!-- tenjin-cli:begin (an older wording) -->\nold instructions\n<!-- tenjin-cli:end -->\n\n# Bottom\n"
	if err := os.WriteFile(file, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	agents, loc := target(t, "agents-md"), Location{ProjectDir: dir}
	if got := state(t, agents, loc); got != Outdated {
		t.Errorf("state = %q", got)
	}
	install(t, agents, loc)
	raw, _ := os.ReadFile(file)
	text := string(raw)
	if strings.Contains(text, "old instructions") || !strings.HasPrefix(text, "# Top\n\n<!-- tenjin-cli:begin") || !strings.HasSuffix(text, "<!-- tenjin-cli:end -->\n\n# Bottom\n") {
		t.Errorf("after install:\n%.200s\n...\n%s", text, text[max(0, len(text)-80):])
	}
	remove(t, agents, loc)
	raw, _ = os.ReadFile(file)
	if string(raw) != "# Top\n\n# Bottom\n" {
		t.Errorf("after removal = %q", raw)
	}
}

func TestAgentsFileWithoutTheSectionHasNothingToRemove(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := PlanRemove(target(t, "agents-md"), Location{ProjectDir: dir})
	if err != nil || c.Action != Absent {
		t.Errorf("change = %+v, err = %v", c, err)
	}
}

func TestDetect(t *testing.T) {
	none := func(string) (string, error) { return "", os.ErrNotExist }
	ids := func(targets []Target) string {
		var out []string
		for _, tg := range targets {
			out = append(out, tg.ID)
		}
		return strings.Join(out, ",")
	}

	dir := t.TempDir()
	if got := ids(Detect(dir, none)); got != "" {
		t.Errorf("nothing installed: %q", got)
	}
	onPath := func(name string) (string, error) {
		if name == "claude" || name == "codex" {
			return "/usr/local/bin/" + name, nil
		}
		return "", os.ErrNotExist
	}
	if got := ids(Detect(dir, onPath)); got != "claude,agents-md" {
		t.Errorf("both on PATH: %q", got)
	}
	// A project already set up for an agent counts, even on a machine
	// without the agent.
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ids(Detect(dir, none)); got != "agents-md" {
		t.Errorf("AGENTS.md in the project: %q", got)
	}
}

// The description is written into YAML front matter without quotes.
func TestDescriptionIsAPlainYAMLScalar(t *testing.T) {
	if strings.Contains(description, ": ") || strings.ContainsAny(description, "\n#") || len(description) > 1024 {
		t.Errorf("description would not survive as an unquoted YAML value: %q", description)
	}
}

// A project's AGENTS.md that links outside the project is not written to.
func TestInstallRefusesALinkOutOfTheProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	project, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "dotfile")
	if err := os.WriteFile(target, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(project, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	codex, _ := FindTarget("codex")
	if _, err := PlanInstall(codex, Location{ProjectDir: project}); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "mine\n" {
		t.Errorf("the outside file was changed: %q", got)
	}
}
