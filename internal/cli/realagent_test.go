//go:build realagent

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/keyfill"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// This test starts the real Claude Code on a broken sample project, so it
// spends the developer's own Claude usage and only runs on request:
//
//	make build && go test -tags realagent -run RealClaudeCode -v ./internal/cli
//
// Tenjin itself is still the fake, so no account is needed.
func TestSetupWithTheRealClaudeCode(t *testing.T) {
	bin, err := filepath.Abs("../../bin/tenjin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("run `make build` first: the agent needs the tenjin binary to run the doctor")
	}
	// Claude Code refuses to start inside another Claude Code session.
	for _, name := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"} {
		if value, ok := os.LookupEnv(name); ok {
			_ = os.Unsetenv(name)
			t.Cleanup(func() { _ = os.Setenv(name, value) })
		}
	}

	h := newHarness(t)
	isolate(t, h)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	h.env.lookPath = exec.LookPath
	h.env.executable = func() (string, error) { return bin, nil }
	h.vars["TENJIN_SDK_KEY"] = testproject.Key

	// TENJIN_REALAGENT_PROJECT runs the same flow on a copy of any project
	// directory instead of the built-in sample.
	if source := os.Getenv("TENJIN_REALAGENT_PROJECT"); source != "" {
		dir := t.TempDir()
		if out, err := exec.Command("cp", "-R", source+"/.", dir).CombinedOutput(); err != nil {
			t.Fatalf("copying %s: %v: %s", source, err, out)
		}
		p, err := project.Detect(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range p.Targets {
			h.fake.addApp("Sample", target.BundleID, string(target.Platform))
		}
		code, stdout, stderr := h.run("setup", dir, "--agent", "claude")
		t.Logf("exit %d\n%s\n%s", code, stdout, stderr)
		if code != 0 {
			t.Fatalf("setup with Claude Code ended with exit %d", code)
		}
		return
	}

	base := testproject.AndroidNative()
	const (
		build    = "app/build.gradle.kts"
		manifest = "app/src/main/AndroidManifest.xml"
		activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	)
	withoutLibrary := strings.Replace(base[build], `    implementation("com.google.android.gms:play-services-ads-identifier:18.1.0")`+"\n", "", 1)
	withoutPermission := strings.Replace(base[manifest], `    <uses-permission android:name="android.permission.INTERNET" />`+"\n", "", 1)
	dir := testproject.Write(t, base.With(testproject.Files{
		build:    withoutLibrary,
		manifest: withoutPermission,
		activity: strings.Replace(base[activity], testproject.Key, "YOUR_SDK_KEY", 1),
	}))

	code, stdout, stderr := h.run("setup", dir, "--agent", "claude")
	t.Logf("exit %d\n%s\n%s", code, stdout, stderr)
	if code != 0 {
		t.Fatalf("setup with Claude Code ended with exit %d", code)
	}
	for _, want := range []string{"Claude Code is working on the integration", "changed:", "Wrote the Android SDK key at " + activity, "Nothing here would stop events reaching Tenjin"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	keyWrittenByTheCLI(t, dir, activity)

	// The command setup prints must really continue the conversation: asked
	// in a new process, Claude Code knows what it changed.
	resume := regexp.MustCompile(`claude --resume (\S+)`).FindStringSubmatch(stdout)
	if resume == nil {
		t.Fatal("setup printed no resume command")
	}
	again := exec.Command("claude", "--print", "--resume", resume[1], "--strict-mcp-config", "--tools", "")
	again.Dir = dir
	again.Stdin = strings.NewReader("Without using any tool, name the files you changed in this conversation, one per line.")
	answer, err := again.CombinedOutput()
	t.Logf("resumed session answered:\n%s", answer)
	if err != nil || !strings.Contains(string(answer), "AndroidManifest.xml") {
		t.Errorf("the resumed session does not remember its work (err = %v)", err)
	}
}

// This test checks the installed skill, not `tenjin setup`: Claude Code is
// started on a broken project with nothing but the skill and the CLI on its
// PATH, and a one-line request. It runs twice: once signed in, with Tenjin
// serving the app's key, so that the agent fills the placeholder itself; and
// once with no key to be had, so that it hands over to the developer.
//
//	make build && go test -tags realagent -run RealSkill -v ./internal/cli
func TestTheSkillWithTheRealClaudeCode(t *testing.T) {
	bin, err := filepath.Abs("../../bin/tenjin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("run `make build` first")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("Claude Code is not installed")
	}
	for _, served := range []bool{true, false} {
		name := "no key to fetch"
		if served {
			name = "key served by Tenjin"
		}
		t.Run(name, func(t *testing.T) { skillRun(t, bin, claude, served) })
	}
}

func skillRun(t *testing.T, bin, claude string, served bool) {
	base := testproject.AndroidNative()
	const (
		build    = "app/build.gradle.kts"
		manifest = "app/src/main/AndroidManifest.xml"
		activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	)
	dir := testproject.Write(t, base.With(testproject.Files{
		build:    strings.Replace(base[build], `    implementation("com.tenjin:android-sdk:2.0.0")`+"\n", "", 1),
		manifest: strings.Replace(base[manifest], `    <uses-permission android:name="com.google.android.gms.permission.AD_ID" />`+"\n", "", 1),
		activity: "package com.example.sample\n\nimport android.app.Activity\n\nclass MainActivity : Activity()\n",
	}))

	h := newHarness(t)
	isolate(t, h)
	if code, _, stderr := h.run("agent", "install", dir, "--agent", "claude", "--yes"); code != 0 {
		t.Fatalf("installing the skill: exit %d: %s", code, stderr)
	}
	if code, _, _ := h.run("doctor", dir, "--offline"); code != 9 {
		t.Fatalf("the project should start broken; doctor exited %d", code)
	}

	cmd := exec.Command(claude,
		"--print", "--output-format", "stream-json", "--verbose",
		"--permission-mode", "dontAsk",
		"--tools", "Read,Edit,Write,Glob,Grep,Bash,Skill",
		"--allowedTools", "Read,Edit,Write,Glob,Grep,Skill,Bash(tenjin *)",
		"--strict-mcp-config", "--no-session-persistence",
	)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("Add the Tenjin SDK to this Android app. I cannot answer questions right now, so do what you can without me and tell me what is left.")
	for _, kv := range os.Environ() {
		// Claude Code refuses to start inside another Claude Code session.
		if !strings.HasPrefix(kv, "CLAUDECODE=") && !strings.HasPrefix(kv, "CLAUDE_CODE_ENTRYPOINT=") && !strings.HasPrefix(kv, "PATH=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if served {
		// Signed in to the fake, which serves the app's key.
		app := h.fake.addApp("Sample", "com.example.sample", "android")
		h.fake.serveKeys(app.ID, testproject.Key)
		cmd.Env = append(cmd.Env, "TENJIN_API_TOKEN=dashboard-token")
	}

	out, err := cmd.CombinedOutput()
	transcript := string(out)
	if err != nil {
		t.Fatalf("Claude Code failed: %v\n%.3000s", err, transcript)
	}

	// What it ran, and what it said at the end.
	ran := map[string]bool{}
	summary := ""
	for _, line := range strings.Split(transcript, "\n") {
		for _, m := range regexp.MustCompile(`"command":"(tenjin [^"]*)"`).FindAllStringSubmatch(line, -1) {
			if !ran[m[1]] {
				ran[m[1]] = true
				t.Logf("ran: %s", m[1])
			}
		}
		var event struct{ Type, Result string }
		if json.Unmarshal([]byte(line), &event) == nil && event.Type == "result" {
			summary = event.Result
		}
	}
	t.Logf("summary:\n%s", summary)
	if !strings.Contains(transcript, `"name":"Skill"`) && !strings.Contains(transcript, "tenjin-cli") {
		t.Error("Claude Code never used the skill")
	}
	if !strings.Contains(transcript, "tenjin doctor") {
		t.Error("Claude Code never ran the doctor")
	}
	// The transcript holds every tool's output: the key is nowhere in it.
	if strings.Contains(transcript, testproject.Key) {
		t.Error("the whole SDK key reached the agent")
	}
	// The doctor names the newest release of the major version it knows.
	if build, _ := os.ReadFile(filepath.Join(dir, "app/build.gradle.kts")); !regexp.MustCompile(`com\.tenjin:android-sdk:2\.\d+\.\d+`).Match(build) {
		t.Errorf("the SDK was not added at the version the doctor named:\n%s", build)
	}

	// Even signed in, with the key served, the agent leaves the placeholder:
	// a key the CLI wrote would be in a file it reads.
	for command := range ran {
		if strings.HasPrefix(command, "tenjin project set-keys") || strings.HasPrefix(command, "tenjin setup") {
			t.Errorf("the agent ran %q", command)
		}
	}
	source, _ := os.ReadFile(filepath.Join(dir, activity))
	if !strings.Contains(string(source), keyfill.Placeholder(project.Android)) {
		t.Errorf("the key placeholder is not in the launcher Activity:\n%s", source)
	}
	if !strings.Contains(summary, "tenjin setup") {
		t.Error("the summary does not send the developer to tenjin setup for the key")
	}
	// What setup does with the key, as a script would.
	if code, stdout, stderr := h.run("project", "set-keys", dir, "--sdk-key", "android="+testproject.Key); code != 0 {
		t.Fatalf("set-keys: exit %d: %s%s", code, stdout, stderr)
	}
	code, stdout, _ := h.run("doctor", dir, "--strict")
	t.Logf("doctor afterwards (exit %d):\n%s", code, stdout)
	if code != 0 {
		t.Errorf("the integration is not clean once the key is written")
	}
}

// The same check for Codex:
//
//	make build && go test -tags realagent -run RealCodex -v ./internal/cli
//
// Set TENJIN_REALAGENT_CODEX_DEFAULTS=1 to run Codex with its built-in
// settings instead of the ones in ~/.codex/config.toml, for a machine whose
// configured model the account cannot use.
func TestSetupWithTheRealCodex(t *testing.T) {
	bin, err := filepath.Abs("../../bin/tenjin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("run `make build` first: the agent needs the tenjin binary to run the doctor")
	}
	codex, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex is not installed")
	}

	h := newHarness(t)
	isolate(t, h)
	// Served by Tenjin, not given to this run: a key the run was given is
	// readable from Codex's sandbox, so setup would refuse to start it.
	app := h.fake.addApp("Sample", "com.example.sample", "android")
	h.fake.serveKeys(app.ID, testproject.Key)
	h.signIn()
	h.env.executable = func() (string, error) { return bin, nil }
	defaults := ""
	if os.Getenv("TENJIN_REALAGENT_CODEX_DEFAULTS") != "" {
		// A stand-in named codex that adds one option and hands over to the
		// real one. Everything else is what setup itself passes. Listing
		// the MCP servers reads an empty configuration, as the run does.
		defaults = "--ignore-user-config"
		wrapper := filepath.Join(t.TempDir(), "codex")
		script := "#!/bin/sh\nif [ \"$1\" != exec ]; then CODEX_HOME=" + t.TempDir() + " exec " + codex + " \"$@\"; fi\nshift\nexec " + codex + " exec " + defaults + " \"$@\"\n"
		if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		codex = wrapper
	}
	h.env.lookPath = func(name string) (string, error) {
		if name == "codex" {
			return codex, nil
		}
		return "", os.ErrNotExist
	}

	base := testproject.AndroidNative()
	const (
		build    = "app/build.gradle.kts"
		manifest = "app/src/main/AndroidManifest.xml"
		activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	)
	dir := testproject.Write(t, base.With(testproject.Files{
		build:    strings.Replace(base[build], `    implementation("com.google.android.gms:play-services-ads-identifier:18.1.0")`+"\n", "", 1),
		manifest: strings.Replace(base[manifest], `    <uses-permission android:name="android.permission.INTERNET" />`+"\n", "", 1),
		activity: strings.Replace(base[activity], testproject.Key, "YOUR_SDK_KEY", 1),
	}))

	code, stdout, stderr := h.run("setup", dir, "--agent", "codex")
	t.Logf("exit %d\n%s\n%s", code, stdout, stderr)
	if code != 0 {
		t.Fatalf("setup with Codex ended with exit %d", code)
	}
	for _, want := range []string{"Codex is working on the integration", "changed:", "Wrote the Android SDK key at " + activity, "Nothing here would stop events reaching Tenjin"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	keyWrittenByTheCLI(t, dir, activity)

	resume := regexp.MustCompile(`codex resume (\S+)`).FindStringSubmatch(stdout)
	if resume == nil {
		t.Fatal("setup printed no resume command")
	}
	args := []string{"exec"}
	if defaults != "" {
		args = append(args, defaults)
	}
	again := exec.Command("codex", append(args, "--skip-git-repo-check", "resume", resume[1], "-")...)
	again.Dir = dir
	again.Stdin = strings.NewReader("Without running anything, name the files you changed in this conversation, one per line.")
	answer, err := again.CombinedOutput()
	t.Logf("resumed session answered:\n%s", answer)
	if err != nil || !strings.Contains(string(answer), "AndroidManifest.xml") {
		t.Errorf("the resumed session does not remember its work (err = %v)", err)
	}
}

// isolate points every tenjin process started from this test at the
// harness's fake Tenjin, through the environment an agent inherits. The
// session such a process finds is then the "custom" one, which is empty, so
// nothing an agent runs can touch production or a real account, where the
// developer of this repository may be signed in.
func isolate(t *testing.T, h *harness) {
	t.Helper()
	e := h.fake.endpoints()
	t.Setenv("TENJIN_DASHBOARD_URL", e.Issuer)
	t.Setenv("TENJIN_API_URL", e.API)
	t.Setenv("TENJIN_TRACK_URL", e.Track)
}

// keyWrittenByTheCLI fails the test unless the project's key is the one
// setup was given, written where the agent left its placeholder.
func keyWrittenByTheCLI(t *testing.T, dir, file string) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), testproject.Key) || strings.Contains(string(source), keyfill.Placeholder(project.Android)) {
		t.Errorf("the key was not written in place of the placeholder:\n%s", source)
	}
}

// A project shipped to Google Play and the Amazon Appstore: the agent has to
// give each flavor its own placeholder, and setup has to fill each with its
// own app's key.
//
//	make build && go test -tags realagent -run RealClaudeCodeMultiStore -v ./internal/cli
func TestSetupMultiStoreWithTheRealClaudeCodeMultiStore(t *testing.T) {
	bin, err := filepath.Abs("../../bin/tenjin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("run `make build` first: the agent needs the tenjin binary to run the doctor")
	}
	for _, name := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"} {
		if value, ok := os.LookupEnv(name); ok {
			_ = os.Unsetenv(name)
			t.Cleanup(func() { _ = os.Setenv(name, value) })
		}
	}

	h := newHarness(t)
	isolate(t, h)
	h.fake.keyApps[testproject.PlayKey] = "com.example.sample|android"
	h.fake.keyApps[testproject.AmazonKey] = "com.example.sample|amazon"
	h.signIn()
	h.env.lookPath = exec.LookPath
	h.env.executable = func() (string, error) { return bin, nil }

	base := testproject.AndroidMultiStore()
	const (
		build    = "app/build.gradle.kts"
		activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	)
	noKeys := strings.Replace(base[build], `            buildConfigField("String", "TENJIN_SDK_KEY", "\"`+testproject.PlayKey+`\"")`+"\n", "", 1)
	noKeys = strings.Replace(noKeys, `            buildConfigField("String", "TENJIN_SDK_KEY", "\"`+testproject.AmazonKey+`\"")`+"\n", "", 1)
	dir := testproject.Write(t, base.With(testproject.Files{
		build:    noKeys,
		activity: "package com.example.sample\n\nimport android.app.Activity\n\nclass MainActivity : Activity()\n",
	}))

	code, stdout, stderr := h.run("setup", dir, "--yes", "--agent", "claude",
		"--sdk-key", "android="+testproject.PlayKey, "--sdk-key", "android:amazon="+testproject.AmazonKey)
	t.Logf("exit %d\n%s\n%s", code, stdout, stderr)
	if code != 0 {
		t.Fatalf("setup ended with exit %d", code)
	}
	for _, want := range []string{
		"Claude Code is working on the integration",
		"Wrote the SDK key of Android, play (Google Play)",
		"Wrote the SDK key of Android, amazon (Amazon Appstore)",
		"Tenjin accepts the SDK key …AMZN for com.example.sample (the amazon build, Amazon Appstore)",
		"Nothing here would stop events reaching Tenjin",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, build))
	t.Logf("build file:\n%s", raw)
	if !strings.Contains(string(raw), testproject.PlayKey) || !strings.Contains(string(raw), testproject.AmazonKey) {
		t.Error("the build file does not hold both keys")
	}
	if strings.Contains(stdout+stderr, testproject.AmazonKey) || strings.Contains(stdout+stderr, testproject.PlayKey) {
		t.Error("a whole key was printed")
	}
}

// Purchases and ad revenue chosen: the agent has to send both to Tenjin
// from the code that already handles them.
//
//	make build && go test -tags realagent -run RealClaudeCodeFeatures -v ./internal/cli
func TestSetupFeaturesWithTheRealClaudeCodeFeatures(t *testing.T) {
	bin, err := filepath.Abs("../../bin/tenjin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("run `make build` first: the agent needs the tenjin binary to run the doctor")
	}
	for _, name := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"} {
		if value, ok := os.LookupEnv(name); ok {
			_ = os.Unsetenv(name)
			t.Cleanup(func() { _ = os.Setenv(name, value) })
		}
	}

	h := newHarness(t)
	isolate(t, h)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	h.env.lookPath = exec.LookPath
	h.env.executable = func() (string, error) { return bin, nil }

	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	dir := testproject.Write(t, base.With(testproject.Files{
		build: strings.Replace(base[build], "dependencies {", `dependencies {
    implementation("com.android.billingclient:billing-ktx:7.1.1")
    implementation("com.applovin:applovin-sdk:13.0.1")`, 1),
		"app/src/main/java/com/example/sample/MainActivity.kt": "package com.example.sample\n\nimport android.app.Activity\n\nclass MainActivity : Activity()\n",
		"app/src/main/java/com/example/sample/Store.kt": `package com.example.sample

import com.android.billingclient.api.BillingClient
import com.android.billingclient.api.BillingResult
import com.android.billingclient.api.Purchase
import com.android.billingclient.api.PurchasesUpdatedListener

class Store : PurchasesUpdatedListener {
    lateinit var client: BillingClient

    override fun onPurchasesUpdated(result: BillingResult, purchases: MutableList<Purchase>?) {
        for (purchase in purchases.orEmpty()) {
            // The app unlocks the product here.
            unlock(purchase.products.first())
        }
    }

    private fun unlock(product: String) {}
}
`,
		"app/src/main/java/com/example/sample/Ads.kt": `package com.example.sample

import com.applovin.mediation.MaxAd
import com.applovin.mediation.MaxAdRevenueListener

class Ads : MaxAdRevenueListener {
    override fun onAdRevenuePaid(ad: MaxAd) {
        // Revenue reporting goes here.
    }
}
`,
	}))

	code, stdout, stderr := h.run("setup", dir, "--yes", "--agent", "claude", "--features", "purchases,ad_revenue", "--sdk-key", "android="+testproject.Key)
	t.Logf("exit %d\n%s\n%s", code, stdout, stderr)
	if code != 0 {
		t.Fatalf("setup ended with exit %d", code)
	}
	for _, want := range []string{
		"In-app purchases are sent to Tenjin",
		"Ad revenue from AppLovin MAX is sent to Tenjin",
		"Nothing here would stop events reaching Tenjin",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	code, out, _ := h.run("doctor", dir, "--strict", "--offline", "--features", "purchases,ad_revenue")
	t.Logf("doctor --strict afterwards (exit %d):\n%s", code, out)
	if code != 0 {
		t.Error("doctor --strict is not clean")
	}
	for _, f := range []string{"Store.kt", "Ads.kt"} {
		raw, _ := os.ReadFile(filepath.Join(dir, "app/src/main/java/com/example/sample", f))
		t.Logf("%s:\n%s", f, raw)
	}
}

// The upgrade across the Android SDK's major version, made by the real
// Claude Code: the declaration moves to 2.0.0, and an app that uses nothing
// the new version changed keeps its code as it was.
//
//	make build && go test -tags realagent -run RealClaudeCodeUpgrade -v ./internal/cli
func TestSDKUpgradeWithTheRealClaudeCodeUpgrade(t *testing.T) {
	bin, err := filepath.Abs("../../bin/tenjin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("run `make build` first: the agent needs the tenjin binary to run the doctor")
	}
	for _, name := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"} {
		if value, ok := os.LookupEnv(name); ok {
			_ = os.Unsetenv(name)
			t.Cleanup(func() { _ = os.Setenv(name, value) })
		}
	}
	h := newHarness(t)
	isolate(t, h)
	h.env.lookPath = exec.LookPath
	h.env.executable = func() (string, error) { return bin, nil }
	dir := oldAndroid(t)
	before := project.Fingerprint(dir)

	code, stdout, stderr := h.run("sdk", "upgrade", dir, "--agent", "claude", "--offline")
	t.Logf("exit %d\n%s\n%s", code, stdout, stderr)
	if code != 0 {
		t.Fatalf("sdk upgrade with Claude Code ended with exit %d", code)
	}
	if !strings.Contains(stdout, "✓ "+gradleFile+" allows com.tenjin:android-sdk 2.0.0") {
		t.Error("the declaration was not moved to 2.0.0")
	}
	if changed := project.Changed(before, project.Fingerprint(dir)); len(changed) != 1 || changed[0] != gradleFile {
		t.Errorf("changed = %q, want only %s", changed, gradleFile)
	}
}
