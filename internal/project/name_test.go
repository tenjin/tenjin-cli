package project_test

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// The app's name comes from where each framework's tools keep it, not from
// the folder the project happens to be in. Only a literal counts.
func TestAppName(t *testing.T) {
	android := testproject.AndroidNative()
	flutter := testproject.Flutter()
	const (
		manifest = "android/app/src/main/AndroidManifest.xml"
		plist    = "ios/Runner/Info.plist"
	)
	label := func(label string, files testproject.Files) testproject.Files {
		return flutter.With(testproject.Files{manifest: strings.Replace(flutter[manifest], `android:label="Sample"`, `android:label="`+label+`"`, 1)}).With(files)
	}
	displayName := func(name string) testproject.Files {
		return flutter.With(testproject.Files{plist: strings.Replace(flutter[plist], "<dict>", "<dict>\n\t<key>CFBundleDisplayName</key>\n\t<string>"+name+"</string>", 1)})
	}
	unity := func(name string) testproject.Files {
		return testproject.Unity().With(testproject.Files{"ProjectSettings/ProjectSettings.asset": "PlayerSettings:\r\n  productName: " + name + "\r\n  companyName: Example\r\n"})
	}
	gradle := func(settings string) testproject.Files {
		return android.With(testproject.Files{"settings.gradle.kts": settings + "\ninclude(\":app\")\n"})
	}
	capacitor := func(file, config string) testproject.Files {
		return testproject.Files{
			"package.json": `{"dependencies": {"@capacitor/core": "6.0.0", "ionic-capacitor-tenjin": "2.2.0"}}`,
			file:           config,
		}
	}
	for _, c := range []struct {
		name     string
		files    testproject.Files
		platform project.Platform
		want     string
	}{
		{"gradle, Kotlin", gradle(`rootProject.name = "Timer"`), project.Android, "Timer"},
		{"gradle, Groovy", android.Without("settings.gradle.kts").With(testproject.Files{"settings.gradle": "rootProject.name = 'My Timer'\ninclude ':app'\n"}), project.Android, "My Timer"},
		{"gradle, setName", android.Without("settings.gradle.kts").With(testproject.Files{"settings.gradle": "rootProject.setName('Timer')\ninclude ':app'\n"}), project.Android, "Timer"},
		{"gradle, commented out", gradle(`// rootProject.name = "Old"`), project.Android, ""},
		{"gradle, an apostrophe", gradle(`rootProject.name = "Tom's Timer"`), project.Android, "Tom's Timer"},
		{"gradle, escaped quotes", gradle(`rootProject.name = "The \"Best\" App"`), project.Android, `The "Best" App`},
		{"gradle, interpolated", gradle(`rootProject.name = "${base}-android"`), project.Android, ""},
		{"gradle, an expression", gradle(`rootProject.name = "Timer" + suffix`), project.Android, ""},
		{"gradle, a format character", gradle("rootProject.name = \"Timer\u202e\""), project.Android, ""},
		{"gradle, CRLF", gradle("rootProject.name = \"Timer\"\r"), project.Android, "Timer"},
		{"gradle, none", android, project.Android, ""},
		{"xcode", testproject.IOSNative(), project.IOS, "Sample"},
		{"flutter, the Android label", flutter, project.Android, "Sample"},
		{"flutter, the Android label from a resource", label("@string/app_name", testproject.Files{
			"android/app/src/main/res/values/strings.xml": "<resources>\n    <string name=\"app_name\">Tom\\'s Timer</string>\n</resources>\n",
		}), project.Android, "Tom's Timer"},
		{"flutter, the iOS display name", displayName("Tenjin Reports"), project.IOS, "Tenjin Reports"},
		{"flutter, the iOS display name from a build setting", displayName("$(PRODUCT_NAME)"), project.IOS, "Sample"},
		{"flutter, neither is a literal", label("${appName}", nil), project.Android, "sample"},
		{"unity", unity("Space Timer"), project.IOS, "Space Timer"},
		{"unity, single-quoted", unity("'Space: Timer'"), project.IOS, "Space: Timer"},
		{"unity, a quote in single quotes", unity("'Tom''s Timer'"), project.IOS, "Tom's Timer"},
		{"unity, double-quoted", unity(`"宇宙"`), project.IOS, "宇宙"},
		{"unity, empty", unity(""), project.IOS, ""},
		{"react native", testproject.Files{
			"package.json": `{"name": "timer-app", "dependencies": {"react-native": "0.79.2", "react-native-tenjin": "1.7.0"}}`,
			"app.json":     `{"name": "TimerApp", "displayName": "Timer"}`,
		}, project.Android, "Timer"},
		{"expo, app.config", testproject.Files{
			"package.json":  `{"name": "timer-app", "dependencies": {"expo": "53.0.0", "react-native": "0.79.2", "react-native-tenjin": "1.7.0"}}`,
			"app.json":      `{"expo": {"name": "Old"}}`,
			"app.config.ts": "export default {\n  name: 'Tenjin Reports',\n  slug: 'tenjin-reports',\n};\n",
		}, project.Android, "Tenjin Reports"},
		{"expo, app.config not a literal", testproject.Files{
			"package.json":  `{"name": "timer-app", "dependencies": {"expo": "53.0.0", "react-native": "0.79.2", "react-native-tenjin": "1.7.0"}}`,
			"app.json":      `{"expo": {"name": "Timer"}}`,
			"app.config.js": "module.exports = ({ config }) => ({\n  ...config,\n  name: process.env.APP_NAME,\n});\n",
		}, project.Android, "Timer"},
		{"capacitor", capacitor("capacitor.config.json", `{"appId": "com.example.sample", "appName": "Timer", "webDir": "www"}`), project.Android, "Timer"},
		{"capacitor, TypeScript", capacitor("capacitor.config.ts", "const config: CapacitorConfig = {\n  appId: 'com.example.sample',\n  appName: 'Timer',\n};\n"), project.Android, "Timer"},
		{"capacitor, an apostrophe", capacitor("capacitor.config.ts", "const config: CapacitorConfig = {\n  appName: \"Tom's Timer\",\n};\n"), project.Android, "Tom's Timer"},
		{"capacitor, JavaScript", capacitor("capacitor.config.js", "module.exports = {\n  appId: 'com.example.sample',\n  appName: 'Timer',\n};\n"), project.Android, "Timer"},
		{"capacitor, not a literal", capacitor("capacitor.config.ts", "const config = {\n  appName: `${brand} Timer`,\n};\n"), project.Android, ""},
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
