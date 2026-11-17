package verify_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// The keep rules are found in a project whose folder holds a glob
// metacharacter: the folder's name is a name, not a pattern.
func TestProguardRulesInAFolderNamedWithBrackets(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	files := base.With(testproject.Files{
		build:                    strings.NewReplacer("isMinifyEnabled = false", "isMinifyEnabled = true", "android-sdk:2.0.0", "android-sdk:1.16.0").Replace(base[build]),
		"app/proguard-rules.pro": "-keep class com.tenjin.** { *; }\n",
	})
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
	p, err := project.Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	f := expect(t, verify.Run(context.Background(), p, verify.Options{}), "android.proguard", project.Android, verify.Pass)
	if f.File != "app/proguard-rules.pro" {
		t.Errorf("file = %q", f.File)
	}
}
