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

const gradleFile = "app/build.gradle.kts"

// oldAndroid is a correct Android integration on the SDK's previous major
// version.
func oldAndroid(t *testing.T) string {
	base := testproject.AndroidNative()
	return testproject.Write(t, base.With(testproject.Files{gradleFile: strings.Replace(base[gradleFile], "android-sdk:2.0.0", "android-sdk:1.24.0", 1)}))
}

func TestSDKUpgradeShowsThePlan(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("sdk", "upgrade", oldAndroid(t))
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"com.tenjin:android-sdk 1.24.0 → 2.0.0  " + gradleFile,
		"2.0.0 is a new major version. Version 2 ships minified.",
		"Release notes: https://github.com/tenjin/tenjin-android-sdk/releases",
		// No terminal and no agent: the changes to make by hand.
		"To upgrade it yourself:",
		"1. In " + gradleFile + ", change the declared version from 1.24.0 to 2.0.0. Then change the app's code where it uses what 2.0.0 changes, as said above.",
		"Then, to install it:\n  Sync the project with its Gradle files",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	code, stdout, _ = h.run("sdk", "upgrade", oldAndroid(t), "--json")
	var plan verify.UpgradePlan
	if code != 0 || json.Unmarshal([]byte(stdout), &plan) != nil || len(plan.Upgrades) != 1 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if u := plan.Upgrades[0]; u.Target != "2.0.0" || !u.Major || !u.Edit || !u.Code || u.File != gradleFile {
		t.Errorf("upgrade = %+v", u)
	}
}

func TestSDKUpgradeWithNothingToDo(t *testing.T) {
	h := newHarness(t)
	h.installAgent()
	code, stdout, _ := h.typing().run("sdk", "upgrade", testproject.Write(t, testproject.AndroidNative()))
	if code != 0 || !strings.Contains(stdout, "✓ com.tenjin:android-sdk 2.0.0 is the newest release") || strings.Contains(stdout, "Claude Code") || strings.Contains(stdout, "Then") {
		t.Errorf("exit = %d\n%s", code, stdout)
	}

	// A Podfile that allows the new release needs no agent: pod update
	// installs it.
	base := testproject.IOSNative()
	dir := testproject.Write(t, base.With(testproject.Files{"Podfile.lock": strings.ReplaceAll(base["Podfile.lock"], "1.20.0", "1.19.1")}))
	code, stdout, _ = h.typing().run("sdk", "upgrade", dir)
	if code != 0 || !strings.Contains(stdout, "Podfile already allows 1.20.0, so it needs no change.") || !strings.Contains(stdout, "Then, to install it:\n  Run pod update TenjinSDK.") ||
		strings.Contains(stdout, "Claude Code") {
		t.Errorf("exit = %d\n%s", code, stdout)
	}

	code, _, stderr := h.run("sdk", "upgrade", testproject.Write(t, testproject.AndroidNative().Without(gradleFile).With(testproject.Files{gradleFile: "plugins {\n    id(\"com.android.application\")\n}\nandroid {\n    namespace = \"com.example.sample\"\n    defaultConfig {\n        applicationId = \"com.example.sample\"\n    }\n}\n"})))
	if code != 5 || !strings.Contains(stderr, "no Tenjin SDK is declared in this project") {
		t.Errorf("no SDK: exit = %d, stderr = %q", code, stderr)
	}

	if code, _, stderr := h.run("sdk", "upgrade", oldAndroid(t), "--agent", "claude", "--json"); code != 2 || !strings.Contains(stderr, "has no JSON output") {
		t.Errorf("--agent with --json: exit = %d, stderr = %q", code, stderr)
	}
}

// The agent is told exactly which declaration to change and what the new
// major version changes, and the CLI checks the project afterwards.
func TestSDKUpgradeWithTheAgent(t *testing.T) {
	h := newHarness(t)
	agent := h.installAgent()
	dir := oldAndroid(t)
	t.Setenv("FAKE_AGENT_DO", `sed -i.bak 's/android-sdk:1.24.0/android-sdk:2.0.0/' `+gradleFile+` && rm -f `+gradleFile+`.bak`)

	code, stdout, stderr := h.typing("").run("sdk", "upgrade", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"Claude Code is installed on this machine and can do the upgrade for you",
		"it uses tokens from your Claude Code account and edits files in this project",
		"Upgrade the Tenjin SDK using Claude Code? [Y/n]",
		"Claude Code is upgrading the Tenjin SDK.",
		"1 file changed:\n  " + gradleFile,
		"Checking again",
		"✓ " + gradleFile + " allows com.tenjin:android-sdk 2.0.0",
		"✓ tenjin doctor finds nothing that would stop events reaching Tenjin",
		"Then, to install it:\n  Sync the project with its Gradle files",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	prompt := agent.read("prompt")
	for _, want := range []string{
		"You are upgrading the Tenjin SDK in the Native project in the current directory.",
		`1. com.tenjin:android-sdk, declared in ` + gradleFile + ` as "1.24.0": from 1.24.0 to 2.0.0.`,
		"Change that declaration so that it asks for 2.0.0",
		"2.0.0 is a new major version. What it changes, from its release notes: Version 2 ships minified.",
		"Look for code in the app that uses what changed",
		"do not run a package manager or a build",
		"run `tenjin doctor --json .`",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, testproject.Key) {
		t.Error("the task holds the SDK key")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, gradleFile))
	if !strings.Contains(string(raw), "android-sdk:2.0.0") {
		t.Errorf("%s:\n%s", gradleFile, raw)
	}
}

// An agent that leaves the declaration as it was has not finished the job.
func TestSDKUpgradeThatTheAgentDidNotMake(t *testing.T) {
	h := newHarness(t)
	h.installAgent()
	t.Setenv("FAKE_AGENT_DO", "true")
	code, stdout, stderr := h.run("sdk", "upgrade", oldAndroid(t), "--agent", "claude")
	if code != 9 || !strings.Contains(stdout, "✗ "+gradleFile+" does not allow com.tenjin:android-sdk 2.0.0 yet") ||
		!strings.Contains(stderr, "the upgrade is not finished: 1 declaration still asks for the old version") {
		t.Errorf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
}

// Inside a coding agent nothing is started: the agent at work is told what
// to change.
func TestSDKUpgradeInsideAnAgent(t *testing.T) {
	h := newHarness(t)
	agent := h.installAgent()
	h.vars["CLAUDECODE"] = "1"
	code, stdout, _ := h.typing("").run("sdk", "upgrade", oldAndroid(t))
	if code != 0 || agent.runs() != "" || !strings.Contains(stdout, "To upgrade it yourself:") {
		t.Errorf("exit = %d, runs = %q\n%s", code, agent.runs(), stdout)
	}
}

// doctor points at the upgrade when the SDK is behind.
func TestDoctorPointsAtTheUpgrade(t *testing.T) {
	h := newHarness(t)
	_, stdout, _ := h.run("doctor", oldAndroid(t), "--offline")
	if !strings.Contains(stdout, "To upgrade the Tenjin SDK, with your coding agent or by hand: tenjin sdk upgrade") {
		t.Errorf("stdout:\n%s", stdout)
	}
	_, stdout, _ = h.run("doctor", testproject.Write(t, testproject.AndroidNative()), "--offline")
	if strings.Contains(stdout, "tenjin sdk upgrade") {
		t.Errorf("pointed at the upgrade of a current SDK:\n%s", stdout)
	}
}
