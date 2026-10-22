package project_test

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

func TestSourceFilesLeaveOutAWebBuild(t *testing.T) {
	deep := "feature/home/src/main/kotlin/com/company/app/feature/home/ui/screen/HomeScreen.kt"
	files := testproject.Files{
		"package.json":        `{"scripts": {"build": "ng build"}}`,
		"capacitor.config.ts": `const config = { webDir: 'www/browser' };`,
		"angular.json":        `{"projects": {"app": {"architect": {"build": {"options": {"outputPath": "out/app"}}}}}}`,
		"src/app.ts":          "",
		"www/browser/main.js": "",
		"out/app/main.js":     "",
		"dist/main.js":        "",
		deep:                  "",
	}
	got := strings.Join(project.SourceFiles(testproject.Write(t, files), ".", ".ts", ".js", ".kt"), ",")
	if got != "capacitor.config.ts,"+deep+",src/app.ts" {
		t.Errorf("source files = %s", got)
	}

	// Without a build, the web folder is the app as written.
	files["package.json"] = `{}`
	delete(files, "angular.json")
	got = strings.Join(project.SourceFiles(testproject.Write(t, files), ".", ".js"), ",")
	if got != "dist/main.js,out/app/main.js,www/browser/main.js" {
		t.Errorf("source files without a build = %s", got)
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
