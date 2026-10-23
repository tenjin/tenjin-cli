package project_test

import (
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// The app's name comes from where each framework's tools keep it, not from
// the folder the project happens to be in.
func TestAppName(t *testing.T) {
	android := testproject.AndroidNative()
	for _, c := range []struct {
		name     string
		files    testproject.Files
		platform project.Platform
		want     string
	}{
		{"gradle, Kotlin", android.With(testproject.Files{"settings.gradle.kts": "rootProject.name = \"Timer\"\ninclude(\":app\")\n"}), project.Android, "Timer"},
		{"gradle, Groovy", android.Without("settings.gradle.kts").With(testproject.Files{"settings.gradle": "rootProject.name = 'My Timer'\ninclude ':app'\n"}), project.Android, "My Timer"},
		{"gradle, commented out", android.With(testproject.Files{"settings.gradle.kts": "// rootProject.name = \"Old\"\ninclude(\":app\")\n"}), project.Android, ""},
		{"gradle, none", android, project.Android, ""},
		{"xcode", testproject.IOSNative(), project.IOS, "Sample"},
		{"flutter", testproject.Flutter(), project.Android, "sample"},
		{"unity", testproject.Unity().With(testproject.Files{"ProjectSettings/ProjectSettings.asset": "PlayerSettings:\n  productName: Space Timer\n  applicationIdentifier:\n    Android: com.example.sample\n    iPhone: com.example.sample\n"}), project.IOS, "Space Timer"},
		{"react native", testproject.Files{
			"package.json": `{"name": "timer-app", "dependencies": {"react-native": "0.79.2", "react-native-tenjin": "1.7.0"}}`,
			"app.json":     `{"name": "TimerApp", "displayName": "Timer"}`,
		}, project.Android, "Timer"},
		{"capacitor", testproject.Files{
			"package.json":          `{"dependencies": {"@capacitor/core": "6.0.0", "ionic-capacitor-tenjin": "2.2.0"}}`,
			"capacitor.config.json": `{"appId": "com.example.sample", "appName": "Timer", "webDir": "www"}`,
		}, project.Android, "Timer"},
		{"capacitor, TypeScript", testproject.Files{
			"package.json":        `{"dependencies": {"@capacitor/core": "6.0.0", "ionic-capacitor-tenjin": "2.2.0"}}`,
			"capacitor.config.ts": "const config: CapacitorConfig = {\n  appId: 'com.example.sample',\n  appName: 'Timer',\n};\n",
		}, project.Android, "Timer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := project.Detect(testproject.Write(t, c.files))
			if err != nil {
				t.Fatal(err)
			}
			if got := project.AppName(p, c.platform); got != c.want {
				t.Errorf("AppName = %q, want %q", got, c.want)
			}
		})
	}
}
