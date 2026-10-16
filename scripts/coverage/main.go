// Command coverage reports test coverage for a pull request: of all the
// code, and of the lines the pull request changes, as Markdown for a comment.
// It exits 1 when the changed lines are covered less than -min.
//
//	go test -coverpkg=./... -coverprofile=coverage.out ./...
//	go run ./scripts/coverage -profile coverage.out -base origin/main
//
// Generated code and test helpers are left out of both numbers.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const module = "github.com/tenjin/tenjin-cli/"

// ignored are files that are not counted: generated, or helpers for tests.
var ignored = regexp.MustCompile(`\.gen\.go$|^internal/testproject/|^scripts/`)

type block struct {
	file                string
	startLine, endLine  int
	statements, covered int
}

func main() {
	profile := flag.String("profile", "coverage.out", "the coverage profile")
	base := flag.String("base", "origin/main", "what the pull request is compared with")
	minimum := flag.Float64("min", 80, "the least coverage of changed lines, in percent")
	flag.Parse()

	blocks, err := readProfile(*profile)
	if err != nil {
		fail(err)
	}
	changed, err := changedLines(*base)
	if err != nil {
		fail(err)
	}

	var all, allCovered, diff, diffCovered int
	uncovered := map[string][]int{}
	for _, b := range blocks {
		all += b.statements
		allCovered += b.covered
		lines := changed[b.file]
		touched := 0
		for l := b.startLine; l <= b.endLine; l++ {
			if lines[l] {
				touched = l
				break
			}
		}
		if touched == 0 {
			continue
		}
		diff += b.statements
		diffCovered += b.covered
		if b.covered == 0 {
			uncovered[b.file] = append(uncovered[b.file], b.startLine)
		}
	}

	fmt.Println("<!-- coverage:go -->")
	fmt.Println("### Test coverage")
	fmt.Println()
	fmt.Printf("- All code: **%s** of statements\n", percent(allCovered, all))
	if diff == 0 {
		fmt.Println("- Changed lines: no statements changed")
		return
	}
	fmt.Printf("- Changed lines: **%s** of %d statements (at least %.0f%% wanted)\n", percent(diffCovered, diff), diff, *minimum)
	if len(uncovered) > 0 {
		fmt.Println()
		fmt.Println("<details><summary>Changed code that no test runs</summary>")
		fmt.Println()
		files := make([]string, 0, len(uncovered))
		for f := range uncovered {
			files = append(files, f)
		}
		sort.Strings(files)
		for _, f := range files {
			lines := uncovered[f]
			sort.Ints(lines)
			text := make([]string, len(lines))
			for i, l := range lines {
				text[i] = strconv.Itoa(l)
			}
			fmt.Printf("- `%s`: lines %s\n", f, strings.Join(text, ", "))
		}
		fmt.Println()
		fmt.Println("</details>")
	}
	if 100*float64(diffCovered) < *minimum*float64(diff) {
		os.Exit(1)
	}
}

// readProfile reads a coverage profile. With -coverpkg, one block appears
// once per test binary that loaded it; it counts as covered if any ran it.
func readProfile(path string) ([]block, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	type key struct {
		file string
		pos  string
	}
	merged := map[key]*block{}
	var order []key
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		// file.go:12.3,15.4 2 1
		name, rest, ok := strings.Cut(line, ":")
		fields := strings.Fields(rest)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("unreadable profile line %q", line)
		}
		file := strings.TrimPrefix(name, module)
		if ignored.MatchString(file) {
			continue
		}
		start, end, _ := strings.Cut(fields[0], ",")
		startLine, _ := strconv.Atoi(strings.Split(start, ".")[0])
		endLine, _ := strconv.Atoi(strings.Split(end, ".")[0])
		statements, _ := strconv.Atoi(fields[1])
		count, _ := strconv.Atoi(fields[2])
		k := key{file, fields[0]}
		b, seen := merged[k]
		if !seen {
			b = &block{file: file, startLine: startLine, endLine: endLine, statements: statements}
			merged[k] = b
			order = append(order, k)
		}
		if count > 0 {
			b.covered = statements
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	out := make([]block, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	return out, nil
}

var hunk = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// changedLines are the lines the pull request adds or changes in Go files,
// by file.
func changedLines(base string) (map[string]map[int]bool, error) {
	out, err := exec.Command("git", "diff", "--unified=0", "--no-color", base+"...HEAD", "--", "*.go").Output()
	if err != nil {
		return nil, fmt.Errorf("git diff against %s: %w", base, err)
	}
	changed := map[string]map[int]bool{}
	file := ""
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
			if changed[file] == nil {
				changed[file] = map[int]bool{}
			}
		case strings.HasPrefix(line, "@@"):
			m := hunk.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			start, _ := strconv.Atoi(m[1])
			n := 1
			if m[2] != "" {
				n, _ = strconv.Atoi(m[2])
			}
			for l := start; l < start+n; l++ {
				changed[file][l] = true
			}
		}
	}
	return changed, nil
}

func percent(part, whole int) string {
	if whole == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(part)/float64(whole))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "coverage:", err)
	os.Exit(2)
}
