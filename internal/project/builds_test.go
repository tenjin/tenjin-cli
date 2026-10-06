package project_test

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

func buildList(t *testing.T, p *project.Project, platform project.Platform) string {
	t.Helper()
	target := p.Target(platform)
	if target == nil {
		t.Fatalf("no %s target", platform)
	}
	var out []string
	for _, b := range target.Builds {
		out = append(out, b.ID+" "+b.BundleID+" "+b.TenjinPlatform+" "+strings.Join(b.Variants, ",")+" "+b.SDKKeyPlaceholder)
	}
	return strings.Join(out, "\n")
}

const manifestStore = `<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <application>
        <meta-data android:name="TENJIN_APP_STORE" android:value="%s" />
    </application>
</manifest>
`

func TestABuildWithoutFlavors(t *testing.T) {
	p := detect(t, testproject.AndroidNative())
	if got := buildList(t, p, project.Android); got != "android com.example.sample android  __TENJIN_ANDROID_SDK_KEY__" {
		t.Errorf("builds:\n%s", got)
	}
	if got := buildList(t, p, project.Android); strings.Contains(got, "\n") {
		t.Errorf("more than one build:\n%s", got)
	}
}

// The usual multi-store setup: a flavor per store, each declaring its store
// in its own source set's manifest.
func TestAFlavorPerStore(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	p := detect(t, base.With(testproject.Files{
		build: strings.Replace(base[build], "    buildTypes {", `    flavorDimensions += "store"
    productFlavors {
        create("play") { dimension = "store" }
        create("amazon") { dimension = "store" }
        create("galaxy") {
            dimension = "store"
            applicationIdSuffix = ".galaxy"
        }
    }
    buildTypes {
        debug { applicationIdSuffix = ".debug" }`, 1),
		"app/src/play/AndroidManifest.xml":   strings.Replace(manifestStore, "%s", "googleplay", 1),
		"app/src/amazon/AndroidManifest.xml": strings.Replace(manifestStore, "%s", "amazon", 1),
		"app/src/galaxy/AndroidManifest.xml": strings.Replace(manifestStore, "%s", "other", 1),
	}))
	want := `android com.example.sample android play __TENJIN_ANDROID_SDK_KEY__
android:amazon com.example.sample amazon amazon __TENJIN_ANDROID_SDK_KEY_AMAZON__
android:galaxy com.example.sample.galaxy android_other galaxy __TENJIN_ANDROID_SDK_KEY_GALAXY__`
	if got := buildList(t, p, project.Android); got != want {
		t.Errorf("builds:\n%s\nwant:\n%s", got, want)
	}
	_, amazon := p.Build("android:amazon")
	if amazon == nil || amazon.Store != "amazon" || amazon.StoreSource != "app/src/amazon/AndroidManifest.xml" {
		t.Errorf("amazon = %+v", amazon)
	}
	// The debug suffix is not a shipped build, but still worth a note.
	if !hasNote(p, "com.example.sample.debug") || !hasNote(p, "ships 3 builds") {
		t.Errorf("notes = %q", p.Notes)
	}
}

// Groovy, with the store set through manifest placeholders, and two flavor
// dimensions whose builds collapse to the apps that differ.
func TestStoresFromManifestPlaceholdersAcrossDimensions(t *testing.T) {
	p := detect(t, testproject.Files{
		"settings.gradle": "include ':app'\n",
		"app/build.gradle": `apply plugin: 'com.android.application'
android {
    defaultConfig {
        applicationId "com.example.game"
        manifestPlaceholders = [tenjinStore: "googleplay"]
    }
    flavorDimensions "store", "env"
    productFlavors {
        play { dimension "store" }
        amazon {
            dimension "store"
            manifestPlaceholders = [tenjinStore: "amazon"]
        }
        dev {
            dimension "env"
            applicationIdSuffix ".dev"
        }
        prod { dimension "env" }
    }
}
`,
		"app/src/main/AndroidManifest.xml": strings.Replace(manifestStore, "%s", "${tenjinStore}", 1),
	})
	want := `android:playDev com.example.game.dev android playDev __TENJIN_ANDROID_SDK_KEY_PLAY_DEV__
android com.example.game android playProd __TENJIN_ANDROID_SDK_KEY__
android:amazonDev com.example.game.dev amazon amazonDev __TENJIN_ANDROID_SDK_KEY_AMAZON_DEV__
android:amazonProd com.example.game amazon amazonProd __TENJIN_ANDROID_SDK_KEY_AMAZON_PROD__`
	if got := buildList(t, p, project.Android); got != want {
		t.Errorf("builds:\n%s\nwant:\n%s", got, want)
	}
}

// Flavors that differ in neither bundle ID nor store are one app.
func TestFlavorsThatShareAnApp(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	p := detect(t, base.With(testproject.Files{
		build: strings.Replace(base[build], "    buildTypes {", `    productFlavors {
        create("free") {}
        create("paid") {}
    }
    buildTypes {`, 1),
	}))
	if got := buildList(t, p, project.Android); got != "android com.example.sample android free,paid __TENJIN_ANDROID_SDK_KEY__" {
		t.Errorf("builds:\n%s", got)
	}
}

// A Flutter project's Android flavors are read the same way; iOS has one
// build.
func TestFlutterBuilds(t *testing.T) {
	base := testproject.Flutter()
	const build = "android/app/build.gradle.kts"
	if _, ok := base[build]; !ok {
		t.Fatalf("the Flutter fixture has no %s", build)
	}
	p := detect(t, base.With(testproject.Files{
		build: strings.Replace(base[build], "    defaultConfig {", `    flavorDimensions += "store"
    productFlavors {
        create("play") { dimension = "store" }
        create("amazon") { dimension = "store" }
    }
    defaultConfig {`, 1),
		"android/app/src/amazon/AndroidManifest.xml": strings.Replace(manifestStore, "%s", "amazon", 1),
	}))
	if got := buildList(t, p, project.Android); got != "android com.example.sample android play __TENJIN_ANDROID_SDK_KEY__\nandroid:amazon com.example.sample amazon amazon __TENJIN_ANDROID_SDK_KEY_AMAZON__" {
		t.Errorf("android builds:\n%s", got)
	}
	if got := buildList(t, p, project.IOS); got != "ios com.example.sample ios  __TENJIN_IOS_SDK_KEY__" {
		t.Errorf("ios builds:\n%s", got)
	}
}

func TestShout(t *testing.T) {
	for in, want := range map[string]string{"amazon": "AMAZON", "amazonProd": "AMAZON_PROD", "play-store": "PLAY_STORE", "v2Beta": "V2_BETA"} {
		if got := project.Shout(in); got != want {
			t.Errorf("Shout(%q) = %q, want %q", in, got, want)
		}
	}
}
