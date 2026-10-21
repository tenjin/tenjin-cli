package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/clierr"
)

func readme(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var readmeCommand = regexp.MustCompile("(?m)^tenjin [^#\n]+")

// The README is the first thing a developer reads: every command and flag it
// names must exist in this binary.
func TestTheReadmeOnlyNamesCommandsThatExist(t *testing.T) {
	body := readme(t)
	var lines []string
	lines = append(lines, readmeCommand.FindAllString(body, -1)...)
	for _, m := range inlineCode.FindAllStringSubmatch(body, -1) {
		lines = append(lines, m[1])
	}
	if len(lines) < 20 {
		t.Fatalf("found only %d commands in the README; the patterns no longer match it", len(lines))
	}
	checkCommandLines(t, lines)
}

// checkCommandLines fails for each line, "tenjin <command> [flags]", that
// names a command or a flag this binary does not have.
func checkCommandLines(t *testing.T, lines []string) {
	t.Helper()
	root := newRootCmd(newHarness(t).env)
	root.InitDefaultCompletionCmd() // cobra adds it only when the CLI runs
	for _, line := range lines {
		words := strings.Fields(line)[1:]
		if len(words) == 0 || strings.HasPrefix(words[0], "<") {
			continue // tenjin <command> --help
		}
		if words[0] == "help" {
			words = words[1:] // tenjin help <command or guide>
			if len(words) == 0 || strings.HasPrefix(words[0], "<") {
				continue
			}
		}
		cmd, rest, err := root.Find(words)
		if err != nil || cmd == root {
			t.Errorf("%q: no such command", line)
			continue
		}
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") && !strings.HasPrefix(rest[0], "<") && !strings.HasPrefix(rest[0], "[") && cmd.HasSubCommands() {
			t.Errorf("%q: %q is not a subcommand of %q", line, rest[0], cmd.CommandPath())
		}
		for _, flag := range longFlag.FindAllString(line, -1) {
			name := strings.TrimPrefix(flag, "--")
			if name != "help" && cmd.Flags().Lookup(name) == nil && cmd.InheritedFlags().Lookup(name) == nil {
				t.Errorf("%q: %q has no flag %s", line, cmd.CommandPath(), flag)
			}
		}
	}
}

var exitCodeRow = regexp.MustCompile(`(?m)^\| (\d+) \| `)

// The README's exit codes are what scripts branch on: they must be every code
// the CLI can exit with, and no other. The kinds are read from clierr's
// source, so a new kind fails here until the README lists its code.
func TestTheReadmeListsEveryExitCode(t *testing.T) {
	body := readme(t)
	start := strings.Index(body, "| Exit code |")
	if start < 0 {
		t.Fatal("the README has no exit code table")
	}
	table := body[start:]
	if end := strings.Index(table, "\n\n"); end >= 0 {
		table = table[:end]
	}
	var listed []int
	for _, m := range exitCodeRow.FindAllStringSubmatch(table, -1) {
		code, _ := strconv.Atoi(m[1])
		listed = append(listed, code)
	}

	want := map[int]bool{0: true}
	for _, kind := range errorKinds(t) {
		want[kind.ExitCode()] = true
	}
	var wanted []int
	for code := range want {
		wanted = append(wanted, code)
	}
	sort.Ints(wanted)
	sort.Ints(listed)
	if !slices.Equal(listed, wanted) {
		t.Errorf("the README lists exit codes %v; the CLI exits with %v", listed, wanted)
	}
}

// errorKinds returns every clierr.Kind constant, read from the package source.
func errorKinds(t *testing.T) []clierr.Kind {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "../clierr/clierr.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []clierr.Kind
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		if ident, ok := spec.Type.(*ast.Ident); !ok || ident.Name != "Kind" {
			return true
		}
		for _, value := range spec.Values {
			if lit, ok := value.(*ast.BasicLit); ok {
				s, _ := strconv.Unquote(lit.Value)
				kinds = append(kinds, clierr.Kind(s))
			}
		}
		return true
	})
	if len(kinds) < 5 {
		t.Fatalf("found only %d error kinds in clierr.go", len(kinds))
	}
	return kinds
}
