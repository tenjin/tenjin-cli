package skill

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
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

// install applies an install and returns its first change, the skill's.
func install(t *testing.T, tg Target, loc Location) Change {
	t.Helper()
	return installAll(t, tg, loc)[0]
}

func installAll(t *testing.T, tg Target, loc Location) []Change {
	t.Helper()
	changes, err := PlanInstall(tg, loc)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if err := Apply(c); err != nil {
			t.Fatal(err)
		}
	}
	return changes
}

// remove applies a removal and returns its first change, the skill's.
func remove(t *testing.T, tg Target, loc Location) Change {
	t.Helper()
	return removeAll(t, tg, loc)[0]
}

func removeAll(t *testing.T, tg Target, loc Location) []Change {
	t.Helper()
	changes, err := PlanRemove(tg, loc)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if err := Apply(c); err != nil {
			t.Fatal(err)
		}
	}
	return changes
}

func state(t *testing.T, tg Target, loc Location) State {
	t.Helper()
	s, err := Check(tg, loc)
	if err != nil {
		t.Fatal(err)
	}
	return s.State
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

// Removal takes the skill's own folder only. The folders above it may have
// been there, empty, before the install, so they stay; and ~/.claude may be
// a link to a dotfiles folder, holding Claude Code's settings.
func TestRemovalTakesOnlyTheSkillsFolder(t *testing.T) {
	claude := target(t, "claude")
	dir := t.TempDir()
	loc := Location{ProjectDir: dir}
	if err := os.MkdirAll(filepath.Join(dir, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	install(t, claude, loc)
	remove(t, claude, loc)
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", Name)); !errors.Is(err, os.ErrNotExist) {
		t.Error("the skill's folder was left behind")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills")); err != nil {
		t.Error("an empty .claude/skills that was there before was removed")
	}

	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	home, dotfiles := t.TempDir(), t.TempDir()
	if err := os.Symlink(dotfiles, filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	global := Location{ProjectDir: dir, Home: home, Global: true}
	install(t, claude, global)
	remove(t, claude, global)
	if info, err := os.Lstat(filepath.Join(home, ".claude")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the linked ~/.claude was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dotfiles, "skills")); err != nil {
		t.Error("the dotfiles folder lost its skills folder")
	}

	// A skill folder that is itself a link is left as it is.
	linked := t.TempDir()
	skills := filepath.Join(home, ".claude", "skills")
	if err := os.Symlink(linked, filepath.Join(skills, Name)); err != nil {
		t.Fatal(err)
	}
	install(t, claude, global)
	remove(t, claude, global)
	if info, err := os.Lstat(filepath.Join(skills, Name)); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the linked skill folder was removed: %v", err)
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

	if _, err := PlanInstall(target(t, "agents"), Location{ProjectDir: project, Home: home, Global: true}); err == nil {
		t.Error("the agents' target has no global location, but one was planned")
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
		{"no trailing newline", "# Notes", "# Notes"},
		{"ends with a blank line", "# Notes\n\n", "# Notes\n\n"},
		{"windows line endings", "# Notes\r\n\r\nUse tabs.\r\n", "# Notes\r\n\r\nUse tabs.\r\n"},
		// Text that only mentions a marker is the developer's, not a section.
		{"mentions the markers", "The CLI writes between `<!-- tenjin-cli:begin` and\n<!-- tenjin-cli:end --> lines.\n\nKeep this.\n",
			"The CLI writes between `<!-- tenjin-cli:begin` and\n<!-- tenjin-cli:end --> lines.\n\nKeep this.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "AGENTS.md")
			agents, loc := target(t, "agents"), Location{ProjectDir: dir}
			if tc.before != "" {
				if err := os.WriteFile(file, []byte(tc.before), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			c := installAll(t, agents, loc)[1]
			if want := map[bool]Action{true: Create, false: Update}[tc.before == ""]; c.Action != want || c.Path != file {
				t.Errorf("install = %+v, want %q of %s", c, want, file)
			}
			raw, _ := os.ReadFile(file)
			if !strings.HasPrefix(string(raw), tc.before) || !strings.Contains(string(raw), pointer) {
				t.Errorf("after install:\n%.200s", raw)
			}
			if strings.Count(string(raw), beginMarker) != 1 {
				t.Error("the section is not there exactly once")
			}
			// Installing again replaces the section; it does not add another.
			if c := installAll(t, agents, loc)[1]; c.Action != Unchanged {
				t.Errorf("second install = %q", c.Action)
			}

			removeAll(t, agents, loc)
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
	old := "# Top\n\n" + beginMarker + "\nold instructions\n" + endMarker + "\n\n# Bottom\n"
	if err := os.WriteFile(file, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	agents, loc := target(t, "agents"), Location{ProjectDir: dir}
	if got := state(t, agents, loc); got != Outdated {
		t.Errorf("state = %q", got)
	}
	installAll(t, agents, loc)
	raw, _ := os.ReadFile(file)
	text := string(raw)
	if text != "# Top\n\n"+beginMarker+"\n"+pointer+endMarker+"\n\n# Bottom\n" {
		t.Errorf("after install:\n%s", text)
	}
	removeAll(t, agents, loc)
	raw, _ = os.ReadFile(file)
	if string(raw) != "# Top\n\n# Bottom\n" {
		t.Errorf("after removal = %q", raw)
	}
}

// Markers that do not fence one section are not guessed at: install and
// removal refuse, and the file stays as it was.
func TestAgentsFileWithBrokenMarkersIsLeftAlone(t *testing.T) {
	cases := map[string]string{
		"a begin line only": "# Notes\n\n" + beginMarker + "\nmy own text\n",
		"an end line only":  "# Notes\n" + endMarker + "\nmy own text\n",
		"two sections":      beginMarker + "\na\n" + endMarker + "\n\nmine\n\n" + beginMarker + "\nb\n" + endMarker + "\n",
		"end before begin":  endMarker + "\nmine\n" + beginMarker + "\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "AGENTS.md")
			if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			agents, loc := target(t, "agents"), Location{ProjectDir: dir}
			if _, err := PlanInstall(agents, loc); err == nil || !strings.Contains(err.Error(), "do not fence one section") || !strings.Contains(err.Error(), file) {
				t.Errorf("install: err = %v", err)
			}
			if _, err := PlanRemove(agents, loc); err == nil || !strings.Contains(err.Error(), "Remove the section by hand") {
				t.Errorf("remove: err = %v", err)
			}
			if _, err := Check(agents, loc); err == nil {
				t.Error("status: no error")
			}
			if raw, _ := os.ReadFile(file); string(raw) != content {
				t.Errorf("the file was changed: %q", raw)
			}
		})
	}
}

func TestAgentsFileWithoutTheSectionHasNothingToRemove(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, err := PlanRemove(target(t, "agents"), Location{ProjectDir: dir})
	if err != nil || len(changes) != 2 || changes[0].Action != Absent || changes[1].Action != Absent {
		t.Errorf("changes = %+v, err = %v", changes, err)
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
	if got := ids(Detect(dir, onPath)); got != "claude,agents" {
		t.Errorf("both on PATH: %q", got)
	}
	// A project already set up for an agent counts, even on a machine
	// without the agent.
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ids(Detect(dir, none)); got != "agents" {
		t.Errorf("AGENTS.md in the project: %q", got)
	}
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ids(Detect(other, none)); got != "agents" {
		t.Errorf(".agents in the project: %q", got)
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

// SDK keys never go to an agent: no command the skill has it run writes one
// into the project, where the agent would read it.
func TestTheSkillNeverHasTheAgentWriteAKey(t *testing.T) {
	blocks := regexp.MustCompile("(?s)```sh\n(.*?)```").FindAllStringSubmatch(Body(), -1)
	if len(blocks) == 0 {
		t.Fatal("no commands found in the skill")
	}
	for _, b := range blocks {
		if strings.Contains(b[1], "set-keys") || strings.Contains(b[1], "tenjin setup") || strings.Contains(b[1], "--sdk-key") {
			t.Errorf("the skill has the agent run:\n%s", b[1])
		}
	}
	if !strings.Contains(Body(), "Do not run `tenjin setup` or `tenjin project set-keys` yourself") {
		t.Error("the skill does not tell the agent to leave the keys to the developer")
	}
}

// Claude Code and the other agents get the same skill, under the same name,
// so a tool that reads both folders finds nothing to choose between; the
// pointer in AGENTS.md names the file the agents' target wrote.
func TestBothTargetsInstallOneSkill(t *testing.T) {
	dir := t.TempDir()
	loc := Location{ProjectDir: dir}
	claude := install(t, target(t, "claude"), loc)
	agents := installAll(t, target(t, "agents"), loc)
	if len(agents) != 2 || agents[0].Path != filepath.Join(dir, ".agents", "skills", Name, "SKILL.md") || agents[1].Path != filepath.Join(dir, "AGENTS.md") {
		t.Fatalf("agents' changes = %+v", agents)
	}
	first, _ := os.ReadFile(claude.Path)
	second, _ := os.ReadFile(agents[0].Path)
	if len(first) == 0 || string(first) != string(second) {
		t.Error("the two skill files differ")
	}
	named := regexp.MustCompile(`read (\S+/SKILL\.md)`).FindStringSubmatch(pointer)
	if named == nil || filepath.Join(dir, filepath.FromSlash(named[1])) != agents[0].Path {
		t.Errorf("the pointer names %v, not the skill it goes with", named)
	}
	if lines := strings.Count(pointer, "\n"); lines != 2 {
		t.Errorf("the pointer has %d lines", lines)
	}

	// Removal takes each skill's own folder and the pointer, and leaves the
	// folders above.
	removeAll(t, target(t, "agents"), loc)
	if _, err := os.Stat(filepath.Dir(agents[0].Path)); !errors.Is(err, os.ErrNotExist) {
		t.Error("the agents' skill folder is still there")
	}
	if _, err := os.Stat(filepath.Join(dir, ".agents", "skills")); err != nil {
		t.Errorf(".agents/skills was removed: %v", err)
	}
	if _, err := os.Stat(claude.Path); err != nil {
		t.Errorf("removing the agents' target took Claude Code's skill: %v", err)
	}
}

// A project whose .agents folder is a link out of it is not written to.
func TestInstallRefusesALinkedAgentsFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	project, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(project, ".agents")); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanInstall(target(t, "agents"), Location{ProjectDir: project}); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("err = %v", err)
	}
}
