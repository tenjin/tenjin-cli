package cli

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	// A command on a line of its own, possibly numbered, then perhaps two
	// spaces and what it does.
	guideCommand  = regexp.MustCompile(`(?m)^\s*(?:\d+\.\s+)?(tenjin [^\n]+)`)
	quotedCommand = regexp.MustCompile(`'(tenjin [^']+)'`)
	twoSpaces     = regexp.MustCompile(`\s{2,}`)
)

// commandsIn lists the tenjin commands a help text names.
func commandsIn(text string) []string {
	var lines []string
	for _, m := range guideCommand.FindAllStringSubmatch(text, -1) {
		lines = append(lines, twoSpaces.Split(m[1], 2)[0])
	}
	for _, m := range quotedCommand.FindAllStringSubmatch(text, -1) {
		lines = append(lines, m[1])
	}
	return lines
}

// The guides and the commands' examples are what people copy: every command
// and flag they name must exist.
func TestTheHelpTextsOnlyNameCommandsThatExist(t *testing.T) {
	var lines []string
	for _, g := range helpGuides {
		if g.name == "scripting" {
			g.text = scriptingGuide(true)
		}
		found := commandsIn(g.text)
		if len(found) < 3 {
			t.Errorf("guide %s: found only %d commands; the patterns no longer match it", g.name, len(found))
		}
		lines = append(lines, found...)
	}
	for path, h := range commandHelp {
		lines = append(lines, commandsIn(h.long)...)
		lines = append(lines, commandsIn(h.example)...)
		if h.long == "" && h.example == "" {
			t.Errorf("%s: no text", path)
		}
	}
	checkCommandLines(t, newHarness(t), lines)
	// Without the data commands, a release's guides are all there is.
	var release []string
	for _, c := range newGuideCmds(false) {
		release = append(release, commandsIn(c.Long)...)
	}
	checkCommandLines(t, newHarness(t).withoutData(), release)

	root := newRootCmd(newHarness(t).env)
	for path := range commandHelp {
		if cmd, _, err := root.Find(strings.Fields(path)); err != nil || cmd == root || cmd.CommandPath() != "tenjin "+path {
			t.Errorf("commandHelp names %q, which is not a command", path)
		}
	}
}

// The guides are help topics: 'tenjin help <guide>' prints one, and the
// landing and 'tenjin --help' say how to find them.
func TestTheGuides(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("help", "callbacks")
	if code != 0 || !strings.Contains(stdout, "To an ad network, step by step") || strings.Contains(stdout, "Usage:") {
		t.Errorf("help callbacks: exit = %d\n%s\n%s", code, stdout, stderr)
	}
	if _, stdout, _ := h.run("--help"); !strings.Contains(stdout, "Guides, step by step:") {
		t.Errorf("--help lists no guides:\n%s", stdout)
	} else {
		for _, g := range helpGuides {
			if !strings.Contains(stdout, "tenjin help "+g.name) {
				t.Errorf("--help does not list the %s guide", g.name)
			}
		}
	}
	if _, stdout, _ := h.run(); !strings.Contains(stdout, "tenjin help <guide>") {
		t.Errorf("the landing does not point at the guides:\n%s", stdout)
	}
	if code, stdout, _ := h.run("apps", "callbacks", "--help"); code != 0 || !strings.Contains(stdout, "tenjin help callbacks") {
		t.Errorf("apps callbacks --help does not point at the guide:\n%s", stdout)
	}
	if _, stdout, _ := h.run("apps", "callbacks", "create", "--help"); !strings.Contains(stdout, "Examples:") {
		t.Errorf("apps callbacks create --help has no examples:\n%s", stdout)
	}
}

// Help for a topic that does not exist is a usage error that suggests one,
// and help asked for as JSON is refused like the landing: it is prose.
func TestHelpRefusesWhatItCannotAnswer(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"help", "callback"}, `did you mean "callbacks"?`},
		{[]string{"help", "reprting"}, `did you mean "reporting"?`},
		{[]string{"help", "apps", "callbakcs"}, `did you mean "callbacks"?`},
		{[]string{"help", "nothing-like-it"}, "integration, callbacks, reporting, scripting"},
		{[]string{"help", "callbacks", "--json"}, "no --json or -o form"},
		{[]string{"help", "-o", "csv"}, "no --json or -o form"},
		{[]string{"apps", "--json"}, "tenjin apps on its own prints no result"},
	} {
		code, stdout, stderr := h.run(tc.args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) {
			t.Errorf("%v: exit = %d, stdout = %q, stderr = %q", tc.args, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{{"help"}, {"help", "apps"}, {"help", "apps", "list"}, {"help", "reports", "metrics", "roi"}} {
		if code, stdout, stderr := h.run(args...); code != 0 || !strings.Contains(stdout, "Usage:") {
			t.Errorf("%v: exit = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
	}
}

var exitCodeLine = regexp.MustCompile(`(?m)^  (\d+)\s{2,}`)

// The scripting guide's exit codes are the README's: every code the CLI can
// exit with, and no other.
func TestTheScriptingGuideListsEveryExitCode(t *testing.T) {
	codes := func(re *regexp.Regexp, text string) []string {
		var out []string
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			out = append(out, m[1])
		}
		slices.Sort(out)
		return out
	}
	var scripting string
	for _, g := range helpGuides {
		if g.name == "scripting" {
			scripting = g.text
		}
	}
	readmeCodes, guideCodes := codes(exitCodeRow, readme(t)), codes(exitCodeLine, scripting)
	if len(readmeCodes) == 0 || !slices.Equal(readmeCodes, guideCodes) {
		t.Errorf("the scripting guide lists exit codes %v, the README %v", guideCodes, readmeCodes)
	}
}

// When Tenjin refuses a callback, the hint says where its parameters are.
func TestARefusedCallbackPointsAtItsParameters(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	refuse := func(*http.Request) (int, any) {
		return http.StatusBadRequest, map[string]string{"error": "Missing customer_parameters: event_name"}
	}
	h.fake.writeAnswers = map[string]func(*http.Request) (int, any){
		"POST /v2/apps/" + testApp + "/callbacks":               refuse,
		"PATCH /v2/apps/" + testApp + "/callbacks/" + testOther: refuse,
	}
	for _, args := range [][]string{
		{"apps", "callbacks", "create", "--app", testApp, "--template", testOther, "--yes"},
		{"apps", "callbacks", "update", testOther, "--app", testApp, "--name", "x", "--yes"},
	} {
		code, _, stderr := h.run(args...)
		if code != 11 || !strings.Contains(stderr, "Missing customer_parameters") || !strings.Contains(stderr, "The parameters it takes: 'tenjin apps") {
			t.Errorf("%v: exit = %d, stderr = %q", args[2], code, stderr)
		}
	}
}
