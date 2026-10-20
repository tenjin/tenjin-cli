package cli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// Keys the checks cannot tie to a build still settle setup's key step, so
// it does not ask for keys the project holds; the doctor says it could not
// tell which key each build sends, and does not pass it.
func TestSetupTakesKeysItCannotTieToABuild(t *testing.T) {
	base := testproject.AndroidMultiStore()
	h, dir := multiStore(t, base.With(testproject.Files{
		flavorBuild:         strings.NewReplacer(testproject.PlayKey, "${tenjinPlayKey}", testproject.AmazonKey, "${tenjinAmazonKey}").Replace(base[flavorBuild]),
		"gradle.properties": "tenjinPlayKey=" + testproject.PlayKey + "\ntenjinAmazonKey=" + testproject.AmazonKey + "\n",
	}))
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.fake.addApp("Sample", "com.example.sample", "amazon")
	h.signIn()
	code, stdout, stderr := h.run("setup", dir, "--yes", "--agent", "none")
	if code != 0 || strings.Count(stdout, "the project already holds a key Tenjin accepts (gradle.properties)") != 2 || strings.Contains(stdout, "--sdk-key") {
		t.Errorf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	_, stdout, _ = h.run("doctor", dir)
	if strings.Count(stdout, "but whether the app sends it could not be read") != 2 || strings.Contains(stdout, "✓ Tenjin accepts") {
		t.Errorf("doctor:\n%s", stdout)
	}
}

// The project passes the SDK a key it holds in BuildConfig, which the checks
// cannot read: the key pasted at setup is checked, and said not to be known
// to be the one the app sends.
func TestSetupDoesNotPassAPastedKeyTheAppMayNotSend(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	dir := testproject.Write(t, base.With(testproject.Files{activity: strings.Replace(base[activity], `"`+testproject.Key+`"`, "BuildConfig.TENJIN_SDK_KEY", 1)}))
	_, stdout, _ := h.typing("", goodKey, "q").run("setup", dir, "--agent", "none")
	lacks(t, stdout, "Tenjin accepts the SDK key …GOOD (from the key you pasted) for com.example.sample, but whether the app sends it could not be read")
	if strings.Contains(stdout, "✓ Tenjin accepts the SDK key …GOOD") {
		t.Errorf("the pasted key passed:\n%s", stdout)
	}
}

// set-keys writes a key over a placeholder in a Unity scene, and says why it
// leaves one in a symbolic link, which it does not write through.
func TestSetKeysInScenesAndLinks(t *testing.T) {
	scene := "%YAML 1.1\n--- !u!114 &1\nMonoBehaviour:\n  m_Script: {fileID: 11500000, guid: e3e00e20639f4f469350ac04120ef3b0, type: 3}\n  sdkKey: __TENJIN_ANDROID_SDK_KEY__\n"
	dir := testproject.Write(t, testproject.Unity().With(testproject.Files{"Assets/Scenes/Main.unity": scene}))
	h := newHarness(t)
	code, stdout, _ := h.run("project", "set-keys", dir, "--offline", "--sdk-key", "android="+goodKey)
	raw, _ := os.ReadFile(filepath.Join(dir, "Assets/Scenes/Main.unity"))
	if code != 0 || !strings.Contains(stdout, "Wrote the Android SDK key at Assets/Scenes/Main.unity:5") || !strings.Contains(string(raw), "sdkKey: "+goodKey) {
		t.Errorf("exit = %d\n%s\nscene:\n%s", code, stdout, raw)
	}

	if runtime.GOOS == "windows" {
		return
	}
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	dir = testproject.Write(t, base.Without(activity))
	outside := filepath.Join(t.TempDir(), "MainActivity.kt")
	if err := os.WriteFile(outside, []byte(strings.Replace(base[activity], testproject.Key, "__TENJIN_ANDROID_SDK_KEY__", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, filepath.FromSlash(activity))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := h.run("project", "set-keys", dir, "--offline", "--sdk-key", "android="+goodKey)
	if code != 9 || !strings.Contains(stdout, "__TENJIN_ANDROID_SDK_KEY__ at "+activity+":9 is left: it is a symbolic link") || strings.Contains(stdout, "No SDK key placeholders") {
		t.Errorf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	if raw, _ := os.ReadFile(outside); strings.Contains(string(raw), goodKey) {
		t.Error("the key was written through the link")
	}
}

// A real key with a stray space inside its quotes is not a placeholder, and
// is never printed whole: not at the terminal, not in the JSON, not in the
// task an agent is given.
func TestAKeyWithSpacesIsNeverPrintedWhole(t *testing.T) {
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	for name, literal := range map[string]string{
		"trailing space": testproject.Key + " ",
		"tab inside":     testproject.Key[:10] + "\t" + testproject.Key[10:],
	} {
		t.Run(name, func(t *testing.T) {
			dir := testproject.Write(t, base.With(testproject.Files{activity: strings.Replace(base[activity], testproject.Key, literal, 1)}))
			h := newHarness(t)
			code, stdout, stderr := h.run("doctor", dir, "--offline")
			if code != 9 || !strings.Contains(stdout, "The SDK key …MNBV has spaces in it") || strings.Contains(stdout, "still a placeholder") {
				t.Errorf("exit = %d\n%s", code, stdout)
			}
			_, jsonOut, jsonErr := h.run("doctor", dir, "--offline", "--json")
			if !strings.Contains(jsonOut, `"sdk.key.whitespace"`) {
				t.Errorf("--json:\n%s", jsonOut)
			}

			p, err := project.Detect(dir)
			if err != nil {
				t.Fatal(err)
			}
			report := verify.Run(context.Background(), p, verify.Options{})
			prompt := integrationPrompt(p, report, map[string]bool{}, nil, "tenjin doctor --json")
			if !strings.Contains(prompt, "has spaces in it") {
				t.Errorf("the task lacks the finding:\n%s", prompt)
			}
			for where, text := range map[string]string{"stdout": stdout, "stderr": stderr, "--json": jsonOut, "--json stderr": jsonErr, "task": prompt} {
				if strings.Contains(strings.Join(strings.Fields(text), ""), testproject.Key) {
					t.Errorf("the whole key is in %s:\n%s", where, text)
				}
			}
		})
	}
}
