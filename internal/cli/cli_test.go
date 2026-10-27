package cli

import (
	"encoding/json"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

func TestVersion(t *testing.T) {
	code, stdout, stderr := newHarness(t).run("version")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "Tenjin CLI ") || !strings.Contains(stdout, "commit ") {
		t.Errorf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestVersionFlagMatchesVersionCommand(t *testing.T) {
	h := newHarness(t)
	_, fromCommand, _ := h.run("version")
	code, fromFlag, _ := h.run("--version")
	if code != 0 || fromFlag != fromCommand {
		t.Errorf("--version (exit %d) = %q, version = %q", code, fromFlag, fromCommand)
	}
}

// With JSON output, stdout must hold the result and nothing else.
func TestVersionJSON(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"version", "--json"}, {"version", "-o", "json"}, {"--json", "version"}} {
		code, stdout, stderr := h.run(args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d, stderr = %q", args, code, stderr)
		}
		var got map[string]string
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("%v: stdout is not JSON: %v\n%s", args, err, stdout)
		}
		for _, key := range []string{"version", "commit", "date", "go", "os", "arch"} {
			if got[key] == "" {
				t.Errorf("%v: missing %q in %v", args, key, got)
			}
		}
	}
}

// tenjin alone is a short landing that fits a screen; a group alone prints
// its help.
func TestNoArgsPrintsHelp(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run()
	if lines := strings.Count(stdout, "\n"); code != 0 || lines > 25 || !strings.Contains(stdout, "tenjin setup") || !strings.Contains(stdout, "tenjin --help") {
		t.Errorf("landing: exit = %d, %d lines:\n%s", code, lines, stdout)
	}
	// The landing is prose: asked for JSON, there is no result to give, so
	// stdout stays empty and the error is JSON.
	for _, args := range [][]string{{"--json"}, {"-o", "json"}} {
		code, stdout, stderr := h.run(args...)
		var e struct{ Error struct{ Code string } }
		if code != 2 || stdout != "" || json.Unmarshal([]byte(stderr), &e) != nil || e.Error.Code != "usage" {
			t.Errorf("%v: exit = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
	}
	code, stdout, _ = h.run("project")
	if code != 0 || !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "Use \"tenjin") {
		t.Errorf("project: exit = %d, stdout = %q", code, stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown command", []string{"bogus"}, `unknown command "bogus"`},
		{"unknown command suggests", []string{"versoin"}, `did you mean "version"?`},
		{"unknown subcommand", []string{"project", "detcet"}, `did you mean "detect"?`},
		{"unknown flag", []string{"version", "--nope"}, "unknown flag: --nope"},
		{"unknown format", []string{"version", "-o", "yaml"}, `unknown output format "yaml"`},
		{"conflicting formats", []string{"version", "--json", "-o", "table"}, "--json conflicts with --output table"},
		{"unexpected argument", []string{"version", "extra"}, `takes no arguments, got "extra"`},
		{"too many arguments", []string{"doctor", "a", "b"}, "takes at most 1 argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := newHarness(t).run(tc.args...)
			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
		})
	}
}

// errorBody decodes the JSON error an agent would read from stderr.
func errorBody(t *testing.T, stderr string) map[string]string {
	t.Helper()
	var body struct {
		Error map[string]string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stderr), &body); err != nil {
		t.Fatalf("stderr is not a JSON error: %v\n%s", err, stderr)
	}
	return body.Error
}

func TestErrorsAreJSONWhenJSONIsRequested(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		args []string
		code string
		exit int
	}{
		{[]string{"bogus", "--json"}, "usage", 2},
		{[]string{"version", "--nope", "-o", "json"}, "usage", 2},
		{[]string{"project", "detect", t.TempDir(), "--json"}, "not_found", 5},
	}
	for _, tc := range cases {
		code, stdout, stderr := h.run(tc.args...)
		if code != tc.exit || stdout != "" {
			t.Errorf("%v: exit = %d, stdout = %q", tc.args, code, stdout)
		}
		if got := errorBody(t, stderr); got["code"] != tc.code || got["message"] == "" {
			t.Errorf("%v: error = %v", tc.args, got)
		}
	}
}

func TestProjectDetect(t *testing.T) {
	dir := testproject.Write(t, testproject.Flutter())
	h := newHarness(t)

	code, stdout, stderr := h.run("project", "detect", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"Flutter", "tenjin_plugin 1.5.0", "com.example.sample", "android", "ios"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	code, stdout, _ = h.run("project", "detect", dir, "--json")
	var got struct {
		Framework string
		Targets   []struct {
			Platform string
			BundleID string `json:"bundle_id"`
		}
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || code != 0 {
		t.Fatalf("exit = %d, err = %v\n%s", code, err, stdout)
	}
	if got.Framework != "flutter" || len(got.Targets) != 2 || got.Targets[0].BundleID != "com.example.sample" {
		t.Errorf("got %+v", got)
	}
}

func TestProjectDetectOnAnEmptyDirectory(t *testing.T) {
	code, stdout, stderr := newHarness(t).run("project", "detect", t.TempDir())
	if code != 5 || stdout != "" || !strings.Contains(stderr, "no mobile project found") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestDoctorPassesACorrectIntegration(t *testing.T) {
	dir := testproject.Write(t, testproject.AndroidNative())
	code, stdout, stderr := newHarness(t).run("doctor", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "Tenjin accepts the SDK key") || !strings.Contains(stdout, "0 errors, 0 warnings") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if strings.Contains(stdout, testproject.Key) {
		t.Error("the report prints the whole SDK key")
	}
}

func TestDoctorFailsABrokenIntegration(t *testing.T) {
	base := testproject.AndroidNative()
	const manifest = "app/src/main/AndroidManifest.xml"
	dir := testproject.Write(t, base.With(testproject.Files{
		manifest: strings.Replace(base[manifest], `<uses-permission android:name="android.permission.INTERNET" />`, "", 1),
	}))
	h := newHarness(t)

	code, stdout, stderr := h.run("doctor", dir)
	if code != 9 {
		t.Fatalf("exit = %d, want 9", code)
	}
	if !strings.Contains(stdout, "INTERNET permission is missing") || !strings.Contains(stdout, "Fix:") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "1 problem will stop events reaching Tenjin") {
		t.Errorf("stderr = %q", stderr)
	}

	// The JSON report is complete on stdout even though the command fails.
	code, stdout, stderr = h.run("doctor", dir, "--json")
	var report struct {
		Findings []struct{ ID, Severity string }
		Summary  struct{ Errors int }
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if code != 9 || report.Summary.Errors != 1 {
		t.Errorf("exit = %d, summary = %+v", code, report.Summary)
	}
	if got := errorBody(t, stderr); got["code"] != "checks_failed" {
		t.Errorf("error = %v", got)
	}
}

func TestDoctorOfflineDoesNotCallTenjin(t *testing.T) {
	h := newHarness(t)
	dir := testproject.Write(t, testproject.AndroidNative())
	code, stdout, _ := h.run("doctor", dir, "--offline")
	if code != 0 || len(h.fake.keysAsked) != 0 {
		t.Errorf("exit = %d, keys sent to Tenjin = %q", code, h.fake.keysAsked)
	}
	if !strings.Contains(stdout, "was not checked against Tenjin") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestDoctorStrictFailsOnWarnings(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	dir := testproject.Write(t, base.With(testproject.Files{
		build: strings.Replace(base[build], `implementation("com.google.android.gms:play-services-appset:16.1.0")`, "", 1),
	}))
	h := newHarness(t)
	if code, _, _ := h.run("doctor", dir); code != 0 {
		t.Errorf("exit = %d, want 0 for a warning", code)
	}
	if code, _, _ := h.run("doctor", dir, "--strict"); code != 9 {
		t.Errorf("exit = %d, want 9 with --strict", code)
	}
}

func TestDoctorReadsTheKeyFromTheEnvironment(t *testing.T) {
	h := newHarness(t)
	h.vars["TENJIN_SDK_KEY"] = "FROMENVIRONMENT"
	dir := testproject.Write(t, testproject.AndroidNative())

	if code, _, stderr := h.run("doctor", dir); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	// The flag wins over the environment.
	h.run("doctor", dir, "--sdk-key", "FROMFLAG")
	if got := strings.Join(h.fake.keysAsked, ","); got != "FROMENVIRONMENT,FROMFLAG" {
		t.Errorf("keys sent = %q", got)
	}
}

func TestDoctorRefusesAnUnsupportedProject(t *testing.T) {
	dir := testproject.Write(t, testproject.Files{
		"config.xml": `<widget id="com.example.cordova"></widget>`,
	})
	code, _, stderr := newHarness(t).run("doctor", dir)
	if code != 5 || !strings.Contains(stderr, "Cordova") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

// The first screen tells app developers from everyone else: the SDK's
// integration leads with setup, the data commands stand apart, and nothing
// is promised that does not exist yet.
func TestHelpSeparatesTheIntegrationFromTheData(t *testing.T) {
	_, stdout, _ := newHarness(t).run("--help")
	sdk, data, account := strings.Index(stdout, "The Tenjin SDK, in a project:"), strings.Index(stdout, "Your Tenjin data:"), strings.Index(stdout, "Your account:")
	if sdk < 0 || data < sdk || account < data {
		t.Fatalf("help is not grouped as expected:\n%s", stdout)
	}
	// The integration's commands are listed in the order they are taken,
	// setup first, not alphabetically. Rows are matched whole: setup's own
	// description mentions "project".
	steps := stdout[sdk:data]
	setup, project, doctor := strings.Index(steps, "\n  setup "), strings.Index(steps, "\n  project "), strings.Index(steps, "\n  doctor ")
	if setup < 0 || setup > project || project > doctor {
		t.Errorf("the integration's commands are out of order:\n%s", steps)
	}
	if !strings.Contains(stdout[data:account], "\n  apps ") {
		t.Errorf("apps is not under the data commands:\n%s", stdout[data:account])
	}
	for _, notBuilt := range []string{"test devices", "live events"} {
		if strings.Contains(stdout, notBuilt) {
			t.Errorf("help mentions %q, which has no command yet", notBuilt)
		}
	}
}

// Until the data commands are released, the CLI is for the SDK's
// integration: the data commands are not there, and the app commands setup
// is made of work without being listed.
func TestWithoutTheDataCommandsTheCLIIsForTheIntegration(t *testing.T) {
	h := newHarness(t).withoutData()
	_, help, _ := h.run("--help")
	for _, absent := range []string{"Your Tenjin data:", "\n  apps ", "campaigns", "reports", "site-ids", "help callbacks", "help reporting", "csv"} {
		if strings.Contains(help, absent) {
			t.Errorf("--help names %q:\n%s", absent, help)
		}
	}
	if !strings.Contains(help, "The Tenjin SDK, in a project:") || !strings.Contains(help, "tenjin help integration") {
		t.Errorf("--help:\n%s", help)
	}
	if _, landing, _ := h.run(); strings.Contains(landing, "campaigns") || !strings.Contains(landing, "(integration, scripting)") {
		t.Errorf("landing:\n%s", landing)
	}
	for _, args := range [][]string{{"campaigns", "list"}, {"reports", "spend"}, {"apps", "update", "1"}, {"apps", "callbacks", "list"}, {"help", "callbacks"}} {
		if code, _, _ := h.run(args...); code != 2 {
			t.Errorf("%v: exit = %d, want a usage error", args, code)
		}
	}
	if code, _, stderr := h.run("project", "detect", "-o", "csv"); code != 2 || !strings.Contains(stderr, "-o takes table or json") {
		t.Errorf("-o csv: exit = %d, stderr = %q", code, stderr)
	}
	h.signIn()
	h.fake.addApp("Sample", "com.example.sample", "android")
	if code, stdout, stderr := h.run("apps", "ensure", "--bundle-id", "com.example.sample", "--platform", "android", "--json"); code != 0 || !strings.Contains(stdout, "com.example.sample") {
		t.Errorf("apps ensure: exit = %d, stderr = %q", code, stderr)
	}
	if code, stdout, _ := h.run("apps", "--help"); code != 0 || !strings.Contains(stdout, "ensure") || strings.Contains(stdout, "callbacks") {
		t.Errorf("apps --help:\n%s", stdout)
	}
}

// Every command a release has, the hidden apps commands included, names
// only commands and flags a release has, and nothing of the data commands.
func TestAReleasesHelpNamesNothingItLacks(t *testing.T) {
	h := newHarness(t).withoutData()
	root := newRootCmd(h.env)
	var lines []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		text := c.Short + "\n" + c.Long + "\n" + c.Example + "\n" + c.LocalFlags().FlagUsages()
		lines = append(lines, commandsIn(c.Long)...)
		lines = append(lines, commandsIn(c.Example)...)
		for _, data := range []string{"csv", "--all", "data commands", "campaign", "callback", "tenjin reports", "site id"} {
			if strings.Contains(strings.ToLower(text), data) {
				t.Errorf("%s: its help mentions %q:\n%s", c.CommandPath(), data, text)
			}
		}
		for _, sub := range c.Commands() {
			if sub.Name() != "completion" {
				walk(sub)
			}
		}
	}
	walk(root)
	checkCommandLines(t, h, lines)
	if code, _, stderr := h.run("doctor", "-o", "yaml"); code != 2 || strings.Contains(stderr, "csv") {
		t.Errorf("-o yaml: exit = %d, stderr = %q", code, stderr)
	}
}

func TestDoctorSaysWhereAGivenKeyCameFrom(t *testing.T) {
	// The project holds a placeholder; the key that is checked comes from
	// outside, and the report must not read as if the two were the same.
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	dir := testproject.Write(t, base.With(testproject.Files{activity: strings.Replace(base[activity], testproject.Key, "YOUR_SDK_KEY", 1)}))

	h := newHarness(t)
	h.vars["TENJIN_SDK_KEY"] = testproject.Key
	_, stdout, _ := h.run("doctor", dir)
	if !strings.Contains(stdout, "still a placeholder") || !strings.Contains(stdout, "Tenjin accepts the SDK key …MNBV (from TENJIN_SDK_KEY) for com.example.sample") {
		t.Errorf("stdout:\n%s", stdout)
	}
	_, stdout, _ = h.run("doctor", dir, "--sdk-key", testproject.Key)
	if !strings.Contains(stdout, "(from --sdk-key)") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// Rules to paste, each on its own line under the fix, indented with it.
func TestDoctorPrintsAMultiLineFix(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	// An SDK from before 2.0.0, which has no keep rules of its own.
	old := strings.Replace(base[build], "android-sdk:2.0.0", "android-sdk:1.24.0", 1)
	dir := testproject.Write(t, base.With(testproject.Files{build: strings.Replace(old, "isMinifyEnabled = false", "isMinifyEnabled = true", 1)}))
	_, stdout, _ := newHarness(t).run("doctor", dir)
	want := "      Fix: Add these lines to app/proguard-rules.pro:\n      -keep class com.tenjin.** { *; }\n"
	if !strings.Contains(stdout, want) || !strings.Contains(stdout, "\n          protected java.lang.Object[][] getContents();\n") || !strings.Contains(stdout, "TypeToken") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// Colour and the wordmark are for a person at a terminal. Tests reach them
// with CLICOLOR_FORCE, which is also how a user keeps colour through a pipe.
func TestHelpIsStyledOnATerminal(t *testing.T) {
	_, plain, _ := newHarness(t).run("--help")
	_, landing, _ := newHarness(t).run()
	if strings.Contains(plain+landing, "\x1b[") || strings.Contains(plain+landing, "█") {
		t.Fatalf("piped help carries styling:\n%q\n%q", plain, landing)
	}

	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("COLORTERM", "")
	// The wordmark is the landing's, not the full help's.
	if _, landing, _ = newHarness(t).run(); !strings.Contains(landing, "████████╗") || !strings.Contains(landing, "\x1b[38;5;37mtenjin setup") {
		t.Errorf("styled landing:\n%q", landing)
	}
	_, styled, _ := newHarness(t).run("--help")
	if strings.Contains(styled, "█") {
		t.Errorf("the full help carries the wordmark")
	}
	for _, want := range []string{
		"\x1b[1;38;5;37mYour Tenjin data:\x1b[0m", // a section title
		"  \x1b[38;5;37msaved-reports\x1b[0m",     // the longest name, one space from its summary
		"  \x1b[38;5;37msetup\x1b[0m",             // a command name
		"\x1b[38;5;37m--json\x1b[0m",              // a flag
	} {
		if !strings.Contains(styled, want) {
			t.Errorf("styled help lacks %q", want)
		}
	}
	// Styling adds to the text and changes none of it.
	stripped := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(styled, "")
	if !strings.HasSuffix(stripped, plain) {
		t.Errorf("styled help, with the codes removed, does not end with the plain help:\n%s", stripped)
	}

	// A subcommand's help is coloured and has no wordmark.
	_, sub, _ := newHarness(t).run("apps", "--help")
	if strings.Contains(sub, "█") || !strings.Contains(sub, "\x1b[1;38;5;37mUsage:\x1b[0m") {
		t.Errorf("apps help:\n%q", sub)
	}

	t.Setenv("NO_COLOR", "1")
	if _, off, _ := newHarness(t).run("--help"); off != plain {
		t.Error("NO_COLOR did not give the plain help")
	}
}

func TestReportsAreStyledOnATerminal(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("COLORTERM", "")
	base := testproject.AndroidNative()
	const manifest = "app/src/main/AndroidManifest.xml"
	dir := testproject.Write(t, base.With(testproject.Files{
		manifest: strings.Replace(base[manifest], `<uses-permission android:name="android.permission.INTERNET" />`, "", 1),
	}))
	h := newHarness(t)

	_, stdout, stderr := h.run("doctor", dir)
	if !strings.Contains(stdout, "\x1b[38;5;167m1 error\x1b[0m, 0 warnings, \x1b[38;5;71m") || !strings.Contains(stderr, "\x1b[38;5;167mtenjin:\x1b[0m") {
		t.Errorf("stdout tail: %q\nstderr: %q", stdout[max(0, len(stdout)-120):], stderr)
	}
	// JSON is never styled, whatever the environment says.
	_, stdout, stderr = h.run("doctor", dir, "--json")
	if strings.Contains(stdout, "\x1b[") || strings.Contains(stderr, "\x1b[") {
		t.Error("JSON output carries colour codes")
	}
	_, stdout, _ = h.run("project", "detect", dir, "--json")
	if strings.Contains(stdout, "\x1b[") {
		t.Error("project detect --json carries colour codes")
	}
}

// --verbose is what support asks for: every request and what came back, on
// stderr, with no credential in it, and the result on stdout unchanged.
func TestVerboseShowsEachRequest(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	dir := testproject.Write(t, testproject.AndroidNative())

	_, quiet, quietErr := h.run("doctor", dir, "--json")
	code, stdout, stderr := h.run("doctor", dir, "--json", "--verbose")
	if code != 0 || stdout != quiet || quietErr != "" {
		t.Fatalf("exit = %d; stdout changed: %v", code, stdout != quiet)
	}
	if !strings.Contains(stderr, "tenjin: POST "+h.fake.srv.URL+"/v0/event → 202 in ") {
		t.Errorf("stderr:\n%s", stderr)
	}

	_, _, stderr = h.run("apps", "list", "-v")
	if !strings.Contains(stderr, "tenjin: GET "+h.fake.srv.URL+"/v2/apps?page=1&per_page=1000 → 200 in ") {
		t.Errorf("stderr:\n%s", stderr)
	}
	for _, secret := range []string{testproject.Key, "Bearer", "refresh-"} {
		if strings.Contains(stderr, secret) {
			t.Errorf("--verbose wrote %q:\n%s", secret, stderr)
		}
	}

	// Programs run on the developer's behalf are listed too.
	h.connectDevice("")
	_, _, stderr = h.run("doctor", dir, "--device", "--offline", "-v")
	if !strings.Contains(stderr, "tenjin: run /opt/android/platform-tools/adb devices") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

// A project's files cannot write escape sequences to the terminal through
// the CLI's output.
func TestProjectTextIsCleanedForPeople(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	evil := strings.Replace(base[build], `applicationId = "com.example.sample"`, "applicationId = \"com.example.sample\x1b]52;c;cHduZWQ=\x07\x1b[1A\"", 1)
	dir := testproject.Write(t, base.With(testproject.Files{build: evil}))
	for _, args := range [][]string{{"project", "detect", dir}, {"doctor", dir, "--offline"}} {
		_, stdout, stderr := newHarness(t).run(args...)
		if strings.ContainsAny(stdout+stderr, "\x1b\x07") {
			t.Errorf("%v wrote control characters:\n%q", args, stdout)
		}
	}

	// Through setup, whether the app is missing or found, and with the key
	// left as a placeholder so that every step speaks of the build.
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	dir = testproject.Write(t, base.With(testproject.Files{build: evil, activity: strings.Replace(base[activity], testproject.Key, "__TENJIN_ANDROID_SDK_KEY__", 1)}))
	for _, exists := range []bool{false, true} {
		h := newHarness(t)
		if exists {
			h.fake.addApp("Sample", "com.example.sample\x1b]52;c;cHduZWQ=\x07\x1b[1A", "android")
		}
		h.signIn()
		_, stdout, stderr := h.run("setup", dir, "--sdk-key", wrongKey)
		if !strings.Contains(stdout, "Step 7 of 7") || strings.ContainsAny(stdout+stderr, "\x1b\x07") {
			t.Errorf("setup, app exists %v, wrote control characters:\n%q\n%q", exists, stdout, stderr)
		}
	}
	// At a terminal, the question that offers to create the app names it too.
	h := newHarness(t)
	h.signIn()
	_, stdout, stderr := h.typing("", "n").run("setup", dir, "--sdk-key", wrongKey)
	if !strings.Contains(stdout+stderr, "No Tenjin app has the bundle ID com.example.sample") || strings.ContainsAny(stdout+stderr, "\x1b\x07") {
		t.Errorf("setup's question wrote control characters, or was not asked:\n%q\n%q", stdout, stderr)
	}
}

// A repository that keeps its app in a folder of its own is pointed to it.
func TestAProjectInAFolderIsPointedTo(t *testing.T) {
	root := t.TempDir()
	for name, content := range testproject.Flutter() {
		path := filepath.Join(root, "mobile", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, _, stderr := newHarness(t).run("doctor", root, "--offline")
	if code != 5 || !strings.Contains(stderr, "Found: mobile (flutter)") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}
