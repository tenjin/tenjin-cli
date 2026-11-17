package cli

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// Installing the plugin into a native project is the developer's step:
// step 5 gives its command, whether or not there is code left to add, and
// does not say that only the keys are left.
func TestSetupSaysThePluginInstallIsTheDevelopers(t *testing.T) {
	unsynced := testproject.Capacitor().With(testproject.Files{"ios/App/CapApp-SPM/Package.swift": "let package = Package()\n"})
	delegate := "ios/App/App/AppDelegate.swift"
	placeholders := unsynced.With(testproject.Files{
		delegate:     strings.Replace(unsynced[delegate], "        return true\n", "        #if DEBUG\n        TenjinSDK.debugLogs()\n        #endif\n        return true\n", 1),
		"src/app.ts": strings.Replace(unsynced["src/app.ts"], "'"+testproject.Key+"'", "Capacitor.getPlatform() === 'ios' ? '__TENJIN_IOS_SDK_KEY__' : '__TENJIN_ANDROID_SDK_KEY__'", 1),
	})
	for _, c := range []struct {
		name   string
		files  testproject.Files
		byHand bool
	}{
		{"with code to add", unsynced, true},
		{"with only the keys and the install", placeholders, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.fake.addApp("Sample", "com.example.sample", "android")
			h.fake.addApp("Sample", "com.example.sample", "ios")
			h.signIn()
			_, stdout, _ := h.typing("", "", "", "", "", "q").run("setup", testproject.Write(t, c.files), "--agent", "none")
			step5 := between(stdout, "Step 5 of 7", "Step 6 of 7")
			lacks(t, step5,
				"✗ iOS: ionic-capacitor-tenjin is not in the iOS project yet  ios/App/CapApp-SPM/Package.swift",
				"The plugin is yours to install into the native projects, before the launch:\n  · iOS: Run npx cap sync ios, then build the app again.\n",
			)
			if strings.Contains(step5, "What is left is the SDK keys") || strings.Contains(step5, "To integrate it yourself") != c.byHand {
				t.Errorf("step 5:\n%s", step5)
			}
		})
	}
}
