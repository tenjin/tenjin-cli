package project_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

func TestSourceFilesLeaveOutAWebBuild(t *testing.T) {
	deep := "feature/home/src/main/kotlin/com/company/app/feature/home/ui/screen/HomeScreen.kt"
	files := testproject.Files{
		"package.json":        `{"scripts": {"build": "ng build", "build:web": "vite build --outDir public-build"}}`,
		"capacitor.config.ts": `const config = { webDir: 'www/browser' };`,
		"angular.json":        `{"projects": {"app": {"architect": {"build": {"options": {"outputPath": "out/app"}}}}}}`,
		"src/app.ts":          "",
		"www/browser/main.js": "",
		"out/app/main.js":     "",
		"dist/main.js":        "",
		// Builds for another target or for tests, beside dist.
		"dist-staging/assets/index.js": "",
		"public-build/main.js":         "",
		deep:                           "",
	}
	got := strings.Join(project.SourceFiles(testproject.Write(t, files), ".", ".ts", ".js", ".kt"), ",")
	if got != "capacitor.config.ts,"+deep+",src/app.ts" {
		t.Errorf("source files = %s", got)
	}

	// Without a build, the web folder is the app as written.
	files["package.json"] = `{}`
	delete(files, "angular.json")
	got = strings.Join(project.SourceFiles(testproject.Write(t, files), ".", ".js"), ",")
	if got != "dist/main.js,dist-staging/assets/index.js,out/app/main.js,public-build/main.js,www/browser/main.js" {
		t.Errorf("source files without a build = %s", got)
	}
}

// A project in a folder whose name holds a glob metacharacter is read like
// any other: the folder's name is a name, not a pattern.
func TestGlobInAFolderNamedWithBrackets(t *testing.T) {
	write := func(files testproject.Files) string {
		root := filepath.Join(t.TempDir(), "app [v2]")
		for rel, content := range files {
			p := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	root := write(testproject.Files{"Game.uproject": `{"FileVersion": 3}`, "Config/DefaultEngine.ini": "", "Source/Game/Game.cpp": ""})
	if got := strings.Join(project.Glob(root, ".", "*.uproject"), ","); got != "Game.uproject" {
		t.Errorf("Glob = %q", got)
	}
	if got := strings.Join(project.Glob(root, "Source/Game", "*.cpp"), ","); got != "Source/Game/Game.cpp" {
		t.Errorf("Glob in a folder = %q", got)
	}
	if got := project.Glob(root, "missing", "*"); got != nil {
		t.Errorf("Glob in a missing folder = %q", got)
	}
	p, err := project.Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Framework != project.Unreal {
		t.Errorf("framework = %q", p.Framework)
	}

	// A pod library at the root, and an app that takes the SDK from a pod by
	// path: both found through the podspecs their folders hold.
	root = write(testproject.Files{"AdsKit.podspec": "Pod::Spec.new do |s|\n  s.name = 'AdsKit'\n  s.dependency 'TenjinSDK'\nend\n"})
	if p, err = project.Detect(root); err != nil || p.Library == nil || p.Library.Source != "AdsKit.podspec" {
		t.Errorf("library = %+v, %v", p.Library, err)
	}
	root = write(testproject.IOSNative().With(testproject.Files{
		"Podfile":               "platform :ios, '13.0'\ntarget 'Sample' do\n  pod 'AdsKit', :path => 'Vendor'\nend\n",
		"Podfile.lock":          "",
		"Vendor/AdsKit.podspec": "Pod::Spec.new do |s|\n  s.name = 'AdsKit'\n  s.dependency 'TenjinSDK'\nend\n",
	}))
	if p, err = project.Detect(root); err != nil || len(p.Tenjin) != 1 || p.Tenjin[0].Source != "Vendor/AdsKit.podspec" {
		t.Errorf("Tenjin = %+v, %v", p.Tenjin, err)
	}
}

func TestTestSource(t *testing.T) {
	for rel, want := range map[string]bool{
		"__mocks__/analytics.js":   true,
		"e2e/login.test.js":        true,
		"src/flows/login.e2e.ts":   true,
		"src/__tests__/a.test.tsx": true,
		"src/screens/Home.tsx":     false,
		"src/e2eHelpers.ts":        false,
	} {
		if got := project.TestSource(rel); got != want {
			t.Errorf("TestSource(%q) = %v, want %v", rel, got, want)
		}
	}
}
