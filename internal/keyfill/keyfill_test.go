package keyfill

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlaceholders(t *testing.T) {
	if Placeholder(project.Android) != "__TENJIN_ANDROID_SDK_KEY__" || Placeholder(project.IOS) != "__TENJIN_IOS_SDK_KEY__" {
		t.Errorf("placeholders = %q, %q", Placeholder(project.Android), Placeholder(project.IOS))
	}
	if !IsPlaceholder("__TENJIN_IOS_SDK_KEY__") || IsPlaceholder("x__TENJIN_IOS_SDK_KEY__") || IsPlaceholder("REALKEY") {
		t.Error("IsPlaceholder")
	}
}

func TestFindAndFill(t *testing.T) {
	root := t.TempDir()
	dart := "final key = Platform.isIOS\n    ? '__TENJIN_IOS_SDK_KEY__'\n    : '__TENJIN_ANDROID_SDK_KEY__';\n"
	write(t, root, map[string]string{
		"lib/main.dart":                dart,
		"android/app/src/main/Main.kt": `val k = "__TENJIN_ANDROID_SDK_KEY__"` + "\n",
		"node_modules/lib/x.js":        `"__TENJIN_ANDROID_SDK_KEY__"`, // dependencies are never touched
		"README.md":                    "__TENJIN_ANDROID_SDK_KEY__\n", // nor files that are not code or config
		"lib/other.dart":               "nothing here\n",
		"lib/look_alike.dart":          "'__TENJIN_WINDOWS_SDK_KEY__'\n",
	})

	spots := Find(root)
	var got []string
	for _, s := range spots {
		got = append(got, string(s.Platform)+"@"+s.File+":"+strconv.Itoa(s.Line))
	}
	want := "android@android/app/src/main/Main.kt:1 ios@lib/main.dart:2 android@lib/main.dart:3"
	if strings.Join(got, " ") != want {
		t.Fatalf("found %q, want %q", strings.Join(got, " "), want)
	}

	// Only Android's key is known: its placeholders are filled, iOS's stays.
	filled, err := Fill(root, map[string]string{Placeholder(project.Android): "ANDROIDKEY"})
	if err != nil || len(filled) != 2 {
		t.Fatalf("filled = %+v, err = %v", filled, err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "lib/main.dart"))
	if string(raw) != "final key = Platform.isIOS\n    ? '__TENJIN_IOS_SDK_KEY__'\n    : 'ANDROIDKEY';\n" {
		t.Errorf("main.dart = %q", raw)
	}
	for _, rel := range []string{"node_modules/lib/x.js", "README.md", "lib/look_alike.dart"} {
		if b, _ := os.ReadFile(filepath.Join(root, rel)); strings.Contains(string(b), "ANDROIDKEY") {
			t.Errorf("%s was changed", rel)
		}
	}
	// The file keeps its permissions.
	if info, _ := os.Stat(filepath.Join(root, "lib/main.dart")); runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	if left := Find(root); len(left) != 1 || left[0].Platform != project.IOS {
		t.Errorf("left = %+v", left)
	}
}

// A build other than the main one has its own placeholder, and only its key
// fills it.
func TestFillABuildsOwnPlaceholder(t *testing.T) {
	root := t.TempDir()
	gradle := `productFlavors {
    create("play") { buildConfigField("String", "TENJIN_SDK_KEY", "\"__TENJIN_ANDROID_SDK_KEY__\"") }
    create("amazon") { buildConfigField("String", "TENJIN_SDK_KEY", "\"__TENJIN_ANDROID_SDK_KEY_AMAZON__\"") }
}
`
	write(t, root, map[string]string{
		"app/build.gradle.kts": gradle,
		// A file that holds only a build's placeholder, none of a platform's.
		"lib/stores.dart": "const amazonKey = '__TENJIN_ANDROID_SDK_KEY_AMAZON__'; // Tenjin\n",
	})
	spots := Find(root)
	if len(spots) != 3 || spots[1].Placeholder != "__TENJIN_ANDROID_SDK_KEY_AMAZON__" || spots[1].Platform != project.Android || spots[1].Line != 3 || spots[2].File != "lib/stores.dart" {
		t.Fatalf("spots = %+v", spots)
	}
	if !IsPlaceholder("__TENJIN_ANDROID_SDK_KEY_AMAZON_PROD__") {
		t.Error("a build's placeholder is not recognised")
	}
	filled, err := Fill(root, map[string]string{"__TENJIN_ANDROID_SDK_KEY_AMAZON__": "AMAZONKEY"})
	if err != nil || len(filled) != 2 {
		t.Fatalf("filled = %+v, err = %v", filled, err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "app/build.gradle.kts"))
	if want := strings.Replace(gradle, "__TENJIN_ANDROID_SDK_KEY_AMAZON__", "AMAZONKEY", 1); string(raw) != want {
		t.Errorf("build file:\n%s", raw)
	}
}
