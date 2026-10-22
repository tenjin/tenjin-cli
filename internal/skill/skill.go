// Package skill carries the instructions that teach a coding agent to use
// this CLI, and puts them where an agent looks for them. The instructions
// are embedded in the binary, so they always describe the commands this
// version has.
package skill

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Name is the skill's name, and the name of the folder it is installed in.
const Name = "tenjin-cli"

// description is what an agent reads to decide whether the skill applies.
// It goes into YAML front matter unquoted, so it must not contain ": ".
const description = "Use when adding the Tenjin SDK to a mobile app, or when a Tenjin integration shows no installs or events. " +
	"Drives the Tenjin CLI (the tenjin command), which reports the project's platforms and bundle IDs, finds or creates the Tenjin app for each, " +
	"checks the integration and says what to fix, and reads what Tenjin answered to a device."

//go:embed tenjin-cli.md
var body string

// Body returns the instructions themselves, as Markdown.
func Body() string { return body }

// Markers fence the instructions inside a file that also holds other
// content, so they can be replaced and removed without touching the rest.
const (
	beginMarker = "<!-- tenjin-cli:begin (written by `tenjin agent install`; edits are overwritten) -->"
	endMarker   = "<!-- tenjin-cli:end -->"
)

// Target is a place the instructions can be installed.
type Target struct {
	ID   string
	Name string
	// Global reports whether the target can be installed for every project
	// of the user, and not only into one project.
	Global bool
	// binaries and markers are how the target's agents are detected: a
	// program on PATH, or a file or folder in the project.
	binaries []string
	markers  []string
}

// Targets lists the supported places, in the order they are offered.
var Targets = []Target{
	{ID: "claude", Name: "Claude Code", Global: true, binaries: []string{"claude"}, markers: []string{".claude"}},
	{ID: "agents-md", Name: "AGENTS.md (Codex, Cursor and others)", binaries: []string{"codex", "cursor-agent", "cursor"}, markers: []string{"AGENTS.md"}},
}

// FindTarget returns the target with the given ID. An agent that reads
// AGENTS.md, such as codex or cursor, names that target too.
func FindTarget(id string) (Target, bool) {
	for _, t := range Targets {
		if t.ID == id || contains(t.binaries, id) {
			return t, true
		}
	}
	return Target{}, false
}

// Detect returns the targets whose agent is installed or already set up in
// the project.
func Detect(projectDir string, lookPath func(string) (string, error)) []Target {
	var found []Target
	for _, t := range Targets {
		if t.present(projectDir, lookPath) {
			found = append(found, t)
		}
	}
	return found
}

func (t Target) present(projectDir string, lookPath func(string) (string, error)) bool {
	for _, binary := range t.binaries {
		if _, err := lookPath(binary); err == nil {
			return true
		}
	}
	for _, marker := range t.markers {
		if _, err := os.Stat(filepath.Join(projectDir, marker)); err == nil {
			return true
		}
	}
	return false
}

// Location says where to install: into a project, or for the user.
type Location struct {
	ProjectDir string
	// Home is the user's home directory. Global selects it over ProjectDir.
	Home   string
	Global bool
}

// Action is what installing or removing does to one file.
type Action string

const (
	Create    Action = "create"
	Update    Action = "update"
	Unchanged Action = "unchanged"
	Remove    Action = "remove"
	// Absent means there is nothing to remove.
	Absent Action = "absent"
)

// Change is one file a plan touches. Apply writes exactly these and nothing
// else.
type Change struct {
	Target string `json:"agent"`
	Path   string `json:"path"`
	Action Action `json:"action"`
	// Detail says what part of the file is concerned, when it is not the
	// whole file.
	Detail string `json:"detail,omitempty"`

	content []byte
	remove  bool
}

// path returns the file a target's instructions live in.
func (t Target) path(loc Location) (string, error) {
	switch {
	case loc.Global && !t.Global:
		return "", fmt.Errorf("%s is installed per project; it has no user-wide location", t.Name)
	case loc.Global && loc.Home == "":
		return "", errors.New("the home directory is not known")
	}
	base := loc.ProjectDir
	if loc.Global {
		base = loc.Home
	}
	path := filepath.Join(base, "AGENTS.md")
	if t.ID == "claude" {
		path = filepath.Join(base, ".claude", "skills", Name, "SKILL.md")
	}
	if !loc.Global {
		if err := noLinks(base, path); err != nil {
			return "", err
		}
	}
	return path, nil
}

// noLinks refuses a path inside a project that goes through a symbolic
// link: a repository could point one at a file outside it, such as a shell
// startup file, which would then be overwritten. In the home folder links
// are left to be followed, since dotfile managers make them on purpose.
func noLinks(base, path string) error {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return err
	}
	current := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link; the Tenjin CLI writes only files that are in the project itself. Replace the link with a file, or add the instructions by hand (tenjin agent skill prints them)", current)
		}
	}
	return nil
}

// skillFile is the whole file a skill-aware agent reads.
func skillFile() string {
	return "---\nname: " + Name + "\ndescription: " + description + "\n---\n\n" +
		"<!-- Written by `tenjin agent install`. Edits are overwritten; run it again after updating the CLI. -->\n\n" + body
}

// block is the fenced section added to a shared instructions file.
func block() string {
	return beginMarker + "\n" + strings.TrimRight(body, "\n") + "\n" + endMarker + "\n"
}

// PlanInstall works out what installing for a target would write, without
// writing it.
func PlanInstall(t Target, loc Location) (Change, error) {
	path, err := t.path(loc)
	if err != nil {
		return Change{}, err
	}
	existing, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Change{}, err
	}
	change := Change{Target: t.ID, Path: path}

	if t.ID == "claude" {
		change.content = []byte(skillFile())
	} else {
		change.Detail = "the section between the tenjin-cli markers; the rest of the file is kept"
		content, err := withBlock(string(existing))
		if err != nil {
			return Change{}, fmt.Errorf("%s: %w", path, err)
		}
		change.content = []byte(content)
	}
	switch {
	case !exists:
		change.Action = Create
	case string(existing) == string(change.content):
		change.Action = Unchanged
	default:
		change.Action = Update
	}
	return change, nil
}

// PlanRemove works out what removing a target's instructions would do.
func PlanRemove(t Target, loc Location) (Change, error) {
	path, err := t.path(loc)
	if err != nil {
		return Change{}, err
	}
	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Change{Target: t.ID, Path: path, Action: Absent}, nil
	}
	if err != nil {
		return Change{}, err
	}
	change := Change{Target: t.ID, Path: path, Action: Remove}
	if t.ID == "claude" {
		change.remove = true
		return change, nil
	}
	rest, had, err := withoutBlock(string(existing))
	if err != nil {
		return Change{}, fmt.Errorf("%s: %w", path, err)
	}
	switch {
	case !had:
		change.Action = Absent
	case strings.TrimSpace(rest) == "":
		// The file held nothing but the section.
		change.remove = true
	default:
		change.Action = Update
		change.Detail = "the section between the tenjin-cli markers is removed; the rest of the file is kept"
		change.content = []byte(rest)
	}
	return change, nil
}

// State is whether a target has the instructions, and whether they are the
// ones this version of the CLI carries.
type State string

const (
	Current      State = "installed"
	Outdated     State = "out_of_date"
	NotInstalled State = "not_installed"
)

// Check reports a target's state at a location, and the file concerned.
func Check(t Target, loc Location) (State, string, error) {
	removal, err := PlanRemove(t, loc)
	if err != nil {
		return "", "", err
	}
	if removal.Action == Absent {
		return NotInstalled, removal.Path, nil
	}
	install, err := PlanInstall(t, loc)
	if err != nil {
		return "", "", err
	}
	if install.Action == Unchanged {
		return Current, install.Path, nil
	}
	return Outdated, install.Path, nil
}

// Apply carries out a planned change.
func Apply(c Change) error {
	switch {
	case c.Action == Unchanged || c.Action == Absent:
		return nil
	case c.remove:
		if err := os.Remove(c.Path); err != nil {
			return err
		}
		// The skill's own folder goes with it when nothing else is in it
		// (os.Remove refuses a folder that is not empty), and when it is a
		// folder, not a link someone made to one. The folders above it, such
		// as .claude, may have been there before the install, empty or not,
		// or be a link to a dotfiles folder, so they stay.
		if filepath.Base(c.Path) == "SKILL.md" {
			dir := filepath.Dir(c.Path)
			if info, err := os.Lstat(dir); err == nil && info.IsDir() {
				_ = os.Remove(dir)
			}
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(c.Path, c.content, 0o644)
}

// withBlock returns content with the section added at the end, or replaced
// where it stands if it is already there. The line breaks it adds are the
// ones withoutBlock takes away, so that removing it gives the file back as
// it was.
func withBlock(content string) (string, error) {
	begin, end, ok, err := findBlock(content)
	switch {
	case err != nil:
		return "", err
	case ok:
		return content[:begin] + strings.TrimSuffix(block(), "\n") + content[end:], nil
	case strings.TrimSpace(content) == "":
		return block(), nil
	case strings.HasSuffix(content, "\n"):
		return content + "\n" + block(), nil
	}
	// A file without a line break at its end gets none after the section.
	return content + "\n\n" + strings.TrimSuffix(block(), "\n"), nil
}

// findBlock locates the section, markers included. Only whole lines that
// are the markers count, so that text which merely mentions one is left
// alone; markers that do not fence exactly one section are an error, and
// the file is not touched.
func findBlock(content string) (begin, end int, ok bool, err error) {
	var begins, ends []int
	for pos := 0; pos < len(content); {
		lineEnd := len(content)
		if i := strings.IndexByte(content[pos:], '\n'); i >= 0 {
			lineEnd = pos + i
		}
		switch strings.TrimSuffix(content[pos:lineEnd], "\r") {
		case beginMarker:
			begins = append(begins, pos)
		case endMarker:
			ends = append(ends, pos)
		}
		pos = lineEnd + 1
	}
	switch {
	case len(begins) == 0 && len(ends) == 0:
		return 0, 0, false, nil
	case len(begins) == 1 && len(ends) == 1 && begins[0] < ends[0]:
		return begins[0], ends[0] + len(endMarker), true, nil
	}
	return 0, 0, false, fmt.Errorf("the tenjin-cli marker lines in it do not fence one section (%d begin and %d end lines), so it was left as it is. "+
		"Remove the section by hand, markers included, then run this again", len(begins), len(ends))
}

// withoutBlock returns content with the section cut out, and whether it was
// there. The line breaks withBlock added go with it.
func withoutBlock(content string) (string, bool, error) {
	begin, end, ok, err := findBlock(content)
	if err != nil || !ok {
		return content, false, err
	}
	before, after := content[:begin], content[end:]
	switch after {
	case "":
		// The file had no line break at its end.
		before = strings.TrimSuffix(before, "\n\n")
	case "\n":
		before, after = strings.TrimSuffix(before, "\n"), ""
	default:
		// Between other text: the blank line that set it apart goes too.
		after = strings.TrimPrefix(after, "\n")
		if strings.HasSuffix(before, "\n\n") && strings.HasPrefix(after, "\n") {
			after = after[1:]
		}
	}
	return before + after, true, nil
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
