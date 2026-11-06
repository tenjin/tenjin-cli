// Package skill carries the instructions that teach a coding agent to use
// this CLI, and puts them where an agent looks for them. The instructions
// are embedded in the binary, so they always describe the commands this
// version has.
package skill

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/buildinfo"
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

// Markers fence the pointer to the instructions inside AGENTS.md, which
// also holds other content, so it can be replaced and removed without
// touching the rest.
const (
	beginMarker = "<!-- tenjin-cli:begin (written by `tenjin agent install`; edits are overwritten) -->"
	endMarker   = "<!-- tenjin-cli:end -->"
)

// Target is a set of agents the instructions can be installed for.
type Target struct {
	ID   string
	Name string
	// Global reports whether the target can be installed for every project
	// of the user, and not only into one project.
	Global bool
	// folder holds the skills folder the target's agents read: .claude or
	// .agents.
	folder string
	// pointer is true when the target also has a marked section in
	// AGENTS.md that points to its skill, for the tools that read AGENTS.md
	// and no skills folder.
	pointer bool
	// binaries and markers are how the target's agents are detected: a
	// program on PATH, or a file or folder in the project.
	binaries []string
	markers  []string
}

// Targets lists the supported places, in the order they are offered.
//
// Claude Code reads .claude/skills. Codex reads .agents/skills: as of
// 0.166.0 it looks for one in each folder from the project's root down to
// the working folder, and in ~/.agents/skills, and reads no .claude folder.
// Cursor and GitHub Copilot read both folders, and Gemini CLI
// .agents/skills, by their documentation. An agent loads a skill's name and
// description up front and its body only when the task calls for it, so the
// instructions cost little in a session that has nothing to do with Tenjin.
//
// No tool reads two copies in full. The two skill files are one file under
// one name, so a tool that reads both folders lists the same skill twice and
// has nothing to choose between. AGENTS.md, which Codex reads on every turn
// and Claude Code reads where a project has no CLAUDE.md (the default as of
// 2.1.308), holds only a pointer, which sends a tool to the file only when
// it has not loaded the skill.
var Targets = []Target{
	{ID: "claude", Name: "Claude Code", Global: true, folder: ".claude", binaries: []string{"claude"}, markers: []string{".claude"}},
	{ID: "agents", Name: "Codex, Cursor, Copilot, Gemini CLI and others", folder: ".agents", pointer: true,
		binaries: []string{"codex", "cursor-agent", "cursor", "copilot", "gemini"}, markers: []string{".agents", "AGENTS.md"}},
}

// FindTarget returns the target with the given ID. An agent that reads the
// .agents folder, such as codex or cursor, names that target too.
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

// files returns the files a target's instructions live in: its skill, and
// the AGENTS.md that holds its pointer, or "" when it has none.
func (t Target) files(loc Location) (skillPath, pointerPath string, err error) {
	switch {
	case loc.Global && !t.Global:
		return "", "", fmt.Errorf("%s is installed per project; it has no user-wide location", t.Name)
	case loc.Global && loc.Home == "":
		return "", "", errors.New("the home directory is not known")
	}
	base := loc.ProjectDir
	if loc.Global {
		base = loc.Home
	}
	skillPath = filepath.Join(base, t.folder, "skills", Name, "SKILL.md")
	if t.pointer {
		pointerPath = filepath.Join(base, "AGENTS.md")
	}
	if !loc.Global {
		for _, path := range []string{skillPath, pointerPath} {
			if path == "" {
				continue
			}
			if err := noLinks(base, path); err != nil {
				return "", "", err
			}
		}
	}
	return skillPath, pointerPath, nil
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

// skillFile is the whole file a skill-aware agent reads. It says which
// version of the CLI wrote it, since it describes that version's commands.
func skillFile() string {
	return "---\nname: " + Name + "\ndescription: " + description + "\n---\n\n" +
		"<!-- Written by `tenjin agent install` (Tenjin CLI " + buildinfo.Get().Version + "). Edits are overwritten; run it again after updating the CLI. -->\n\n" + body
}

// writtenBy finds the version in the line skillFile writes. Copies written
// before it named one have the line without it.
var writtenBy = regexp.MustCompile("(?m)^<!-- Written by `tenjin agent install`(?: \\(Tenjin CLI ([^\\s)]+)\\))?\\.")

// Copy is a copy of the instructions installed where an agent reads them.
type Copy struct {
	Path string
	// Global is true for a copy in the home folder, which serves every
	// project.
	Global bool
	// Version is the version of the CLI that wrote it, or "" when it does
	// not say: one written before versions were recorded.
	Version string
}

// OtherVersions returns the copies of the instructions, in a project and in
// the home folder (home may be ""), that a version of the CLI other than
// current wrote: their commands and flags may not be this version's. An
// AGENTS.md that holds the whole instructions, as versions before the
// pointer wrote it, is one of them.
func OtherVersions(projectDir, home, current string) []Copy {
	var copies []Copy
	look := func(base string, global bool) {
		for _, t := range Targets {
			if global && !t.Global {
				continue
			}
			skillPath, pointerPath, err := t.files(Location{ProjectDir: base, Home: base, Global: global})
			if err != nil {
				continue
			}
			if content, ok, _ := read(skillPath); ok {
				if version := recorded(content); version != current {
					copies = append(copies, Copy{Path: skillPath, Global: global, Version: version})
				}
			}
			if text, ok := section(pointerPath); pointerPath != "" && ok && strings.HasPrefix(text, "# ") {
				copies = append(copies, Copy{Path: pointerPath, Global: global})
			}
		}
	}
	// Run from the home folder, the copies there are the user-wide ones,
	// read through any link a dotfiles manager made.
	if projectDir != "" && (home == "" || !sameDir(projectDir, home)) {
		look(projectDir, false)
	}
	if home != "" {
		look(home, true)
	}
	return copies
}

// sameDir reports whether two paths name the same folder.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

// recorded returns the version a skill file says wrote it, or "".
func recorded(content []byte) string {
	if m := writtenBy.FindSubmatch(content); m != nil {
		return string(m[1])
	}
	return ""
}

// pointer is what the section in AGENTS.md says: where the instructions
// are, for a tool that has not loaded them as a skill. The path is from the
// project's root, where AGENTS.md is.
const pointer = "For Tenjin SDK work (adding the SDK, or an integration that shows no installs or events), use the tenjin-cli skill.\n" +
	"If it is not among your skills, read .agents/skills/tenjin-cli/SKILL.md and follow it.\n"

// block is the fenced section added to AGENTS.md.
func block() string {
	return beginMarker + "\n" + pointer + endMarker + "\n"
}

// PlanInstall works out what installing for a target would write, without
// writing it: its skill first, then the pointer to it, if it has one.
func PlanInstall(t Target, loc Location) ([]Change, error) {
	skillPath, pointerPath, err := t.files(loc)
	if err != nil {
		return nil, err
	}
	existing, exists, err := read(skillPath)
	if err != nil {
		return nil, err
	}
	content := []byte(skillFile())
	changes := []Change{{Target: t.ID, Path: skillPath, Action: action(exists, existing, content), content: content}}
	if pointerPath == "" {
		return changes, nil
	}
	if existing, exists, err = read(pointerPath); err != nil {
		return nil, err
	}
	withPointer, err := withBlock(string(existing))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", pointerPath, err)
	}
	return append(changes, Change{
		Target: t.ID, Path: pointerPath, Action: action(exists, existing, []byte(withPointer)),
		Detail:  "the section between the tenjin-cli markers, two lines that point to the skill; the rest of the file is kept",
		content: []byte(withPointer),
	}), nil
}

// read returns a file's content, and whether it is there.
func read(path string) ([]byte, bool, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return content, err == nil, err
}

func action(exists bool, existing, content []byte) Action {
	switch {
	case !exists:
		return Create
	case bytes.Equal(existing, content):
		return Unchanged
	}
	return Update
}

// PlanRemove works out what removing a target's instructions would do: its
// skill first, then the pointer to it, if it has one.
func PlanRemove(t Target, loc Location) ([]Change, error) {
	skillPath, pointerPath, err := t.files(loc)
	if err != nil {
		return nil, err
	}
	_, exists, err := read(skillPath)
	if err != nil {
		return nil, err
	}
	changes := []Change{{Target: t.ID, Path: skillPath, Action: Absent}}
	if exists {
		changes[0].Action, changes[0].remove = Remove, true
	}
	if pointerPath == "" {
		return changes, nil
	}
	existing, exists, err := read(pointerPath)
	if err != nil {
		return nil, err
	}
	change := Change{Target: t.ID, Path: pointerPath, Action: Absent}
	rest, had, err := withoutBlock(string(existing))
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: %w", pointerPath, err)
	case !exists || !had:
	case strings.TrimSpace(rest) == "":
		// The file held nothing but the section.
		change.Action, change.remove = Remove, true
	default:
		change.Action = Update
		change.Detail = "the section between the tenjin-cli markers is removed; the rest of the file is kept"
		change.content = []byte(rest)
	}
	return append(changes, change), nil
}

// State is whether a target has the instructions, and whether they are the
// ones this version of the CLI carries.
type State string

const (
	Current      State = "installed"
	Outdated     State = "out_of_date"
	NotInstalled State = "not_installed"
)

// Status is a target's state at a location.
type Status struct {
	State State
	// Path is the target's skill file.
	Path string
	// Detail says what is out of date, where the state alone does not.
	Detail string
}

// Check reports a target's state at a location.
func Check(t Target, loc Location) (Status, error) {
	removal, err := PlanRemove(t, loc)
	if err != nil {
		return Status{}, err
	}
	install, err := PlanInstall(t, loc)
	if err != nil {
		return Status{}, err
	}
	status := Status{State: NotInstalled, Path: install[0].Path}
	if !slices.ContainsFunc(removal, func(c Change) bool { return c.Action != Absent }) {
		return status, nil
	}
	status.State = Current
	var details []string
	for i, c := range install {
		if c.Action == Unchanged {
			continue
		}
		status.State = Outdated
		if i == 0 {
			if c.Action == Create {
				details = append(details, c.Path+" is missing")
			}
			continue
		}
		switch text, ok := section(c.Path); {
		case !ok:
			details = append(details, c.Path+" has no pointer to the skill")
		case strings.HasPrefix(text, "# "):
			// Versions before the pointer put the whole instructions
			// there, which start with their title.
			details = append(details, c.Path+" holds the whole instructions, as earlier versions wrote them; they are in the skill now, and AGENTS.md has two lines that point to it")
		}
	}
	status.Detail = strings.Join(details, "; ")
	return status, nil
}

// section returns the text between the markers in a file, and whether the
// file has them.
func section(path string) (string, bool) {
	content, _, err := read(path)
	if err != nil {
		return "", false
	}
	begin, end, ok, err := findBlock(string(content))
	if err != nil || !ok {
		return "", false
	}
	text := strings.TrimSuffix(string(content[begin:end]), endMarker)
	return strings.TrimLeft(strings.TrimPrefix(text, beginMarker), "\r\n"), true
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
		// as .claude or .agents, may have been there before the install,
		// empty or not, or be a link to a dotfiles folder, so they stay.
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
