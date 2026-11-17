package cli

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files with the current output")

// golden compares got with testdata/<name>, after replacing the temporary
// project directory with a fixed word. Run `go test ./internal/cli -update`
// to accept a deliberate change.
func golden(t *testing.T, name, got, root string) {
	t.Helper()
	// In JSON a Windows path has its backslashes escaped.
	escaped, _ := json.Marshal(root)
	got = strings.ReplaceAll(got, strings.Trim(string(escaped), `"`), "<root>")
	got = strings.ReplaceAll(got, root, "<root>")
	file := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s.\n--- got ---\n%s\n--- want ---\n%s", file, got, want)
	}
}

// The JSON of these two commands is what scripts and agents parse. A change
// to it has to be a decision, not a side effect.
func TestJSONOutputIsStable(t *testing.T) {
	projects := []struct {
		name  string
		files testproject.Files
	}{
		{"android", testproject.AndroidNative()},
		{"ios", testproject.IOSNative()},
		{"flutter", testproject.Flutter()},
		{"unity", testproject.Unity()},
	}
	for _, p := range projects {
		t.Run(p.name, func(t *testing.T) {
			dir := testproject.Write(t, p.files)
			// The temporary directory may be reached through a symlink.
			root, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			h := newHarness(t)

			code, stdout, stderr := h.run("project", "detect", root, "--json")
			if code != 0 {
				t.Fatalf("project detect: exit %d: %s", code, stderr)
			}
			golden(t, "project-detect-"+p.name+".json", stdout, root)

			code, stdout, stderr = h.run("doctor", root, "--json")
			if code != 0 {
				t.Fatalf("doctor: exit %d: %s", code, stderr)
			}
			golden(t, "doctor-"+p.name+".json", stdout, root)
		})
	}
}

func TestProjectDetectDescribesTheBuild(t *testing.T) {
	dir := testproject.Write(t, testproject.Flutter())
	code, stdout, _ := newHarness(t).run("project", "detect", dir)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{
		"Android build    Kotlin DSL (android/app/build.gradle.kts)",
		"Launcher         com.example.sample.MainActivity",
		"Tracking prompt  lib/main.dart:9",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q:\n%s", want, stdout)
		}
	}
}

func TestProjectsWithoutATenjinSDKAreRefused(t *testing.T) {
	cases := []struct {
		name  string
		files testproject.Files
		want  string
	}{
		{"exported unity build", testproject.Files{
			"launcher/build.gradle":     "apply plugin: 'com.android.application'\nandroid { defaultConfig { applicationId 'com.example.game' } }\n",
			"unityLibrary/build.gradle": "apply plugin: 'com.android.library'\n",
		}, "exported from Unity"},
		{"dotnet", testproject.Files{
			"Shop.csproj": `<Project><PropertyGroup><UseMaui>true</UseMaui></PropertyGroup></Project>`,
		}, "no SDK for .NET MAUI"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := testproject.Write(t, tc.files)
			h := newHarness(t)
			// Detection reports it; the commands that need an SDK decline.
			if code, stdout, _ := h.run("project", "detect", dir); code != 0 || !strings.Contains(stdout, tc.want) {
				t.Errorf("project detect: exit = %d, stdout:\n%s", code, stdout)
			}
			if code, _, stderr := h.run("doctor", dir); code != 5 || !strings.Contains(stderr, tc.want) {
				t.Errorf("doctor: exit = %d, stderr = %q", code, stderr)
			}
		})
	}
}
