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

// Dimensions and placeholders written over several lines are read whole.
func TestMultiLineGradleForms(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	p := detect(t, base.With(testproject.Files{
		build: strings.Replace(base[build], "    buildTypes {", `    flavorDimensions += listOf(
        "store", "env"
    )
    productFlavors {
        create("play") { dimension = "store" }
        create("amazon") { dimension = "store" }
        create("prod") { dimension = "env" }
        create("staging") {
            dimension = "env"
            applicationIdSuffix = ".staging"
        }
    }
    buildTypes {`, 1),
		"app/src/play/AndroidManifest.xml":   strings.Replace(manifestStore, "%s", "googleplay", 1),
		"app/src/amazon/AndroidManifest.xml": strings.Replace(manifestStore, "%s", "amazon", 1),
	}))
	got := buildList(t, p, project.Android)
	for _, want := range []string{"com.example.sample.staging amazon", "com.example.sample amazon", "com.example.sample.staging android", "com.example.sample android"} {
		if !strings.Contains(got, want) {
			t.Errorf("no build %q in:\n%s", want, got)
		}
	}

	groovy := `apply plugin: 'com.android.application'
android {
    defaultConfig { applicationId "com.example.sample" }
    flavorDimensions "store"
    productFlavors {
        play { dimension "store" }
        amazon {
            dimension "store"
            manifestPlaceholders = [
                tenjinStore: "amazon"
            ]
        }
    }
}
dependencies { implementation 'com.tenjin:android-sdk:2.0.0' }
`
	p = detect(t, base.With(testproject.Files{
		build:                              "",
		"app/build.gradle":                 groovy,
		"app/src/main/AndroidManifest.xml": strings.Replace(manifestStore, "%s", "${tenjinStore}", 1),
	}))
	if got := buildList(t, p, project.Android); !strings.Contains(got, "android:amazon com.example.sample amazon") {
		t.Errorf("the Amazon flavor's placeholder was not read:\n%s", got)
	}
}

// TENJIN_APP_STORE is read as the SDK reads it.
func TestStoresAsTheSDKReadsThem(t *testing.T) {
	for declared, want := range map[string]string{
		"amazon": "amazon", "Amazon": "amazon", "other": "android_other",
		"Google Play": "android", "googleplay": "android",
		"android_other": "android", "samsung": "android", "": "android",
	} {
		if got := project.TenjinPlatform(project.Android, declared); got != want {
			t.Errorf("%q: %q, want %q", declared, got, want)
		}
	}
}

// The app's Info.plist is the one its own build settings name, with
// $(SRCROOT) resolved, even when an extension's settings come first.
func TestTheAppsOwnInfoPlist(t *testing.T) {
	base := testproject.IOSNative()
	const pbx = "Sample.xcodeproj/project.pbxproj"
	widget := `		CCCC /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				INFOPLIST_FILE = Widget/Info.plist;
				PRODUCT_BUNDLE_IDENTIFIER = com.example.sample.widget;
			};
			name = Debug;
		};
`
	pbxText := strings.Replace(base[pbx], "\tobjects = {\n", "\tobjects = {\n"+widget, 1)
	pbxText = strings.Replace(pbxText, "INFOPLIST_FILE = Sample/Info.plist;", `INFOPLIST_FILE = "$(SRCROOT)/Sample/Info.plist";`, 1)
	p := detect(t, base.With(testproject.Files{pbx: pbxText, "Widget/Info.plist": testproject.InfoPlist}))
	if got := p.Target(project.IOS).IOS.InfoPlist; got != "Sample/Info.plist" {
		t.Errorf("Info.plist = %q", got)
	}
}

// With several application modules, the one with Tenjin is the one checked.
func TestTheModuleWithTenjinIsChosen(t *testing.T) {
	base := testproject.AndroidNative()
	files := testproject.Files{}
	for name, content := range base {
		files[strings.Replace(name, "app/", "mobile/", 1)] = content
	}
	files["settings.gradle.kts"] = "include(\":mobile\", \":automotive\")\n"
	files["automotive/build.gradle.kts"] = "plugins { id(\"com.android.application\") }\nandroid { defaultConfig { applicationId = \"com.example.sample.car\" } }\n"
	p := detect(t, files)
	if got := p.Target(project.Android).Android.ModuleDir; got != "mobile" {
		t.Errorf("module = %q", got)
	}
}

// A Tenjin Swift package declared by a local package the app links is the
// app's Tenjin SDK.
func TestTenjinInALocalSwiftPackage(t *testing.T) {
	base := testproject.IOSNative()
	files := base.With(testproject.Files{
		"Podfile":                        "platform :ios, '13.0'\ntarget 'Sample' do\nend\n",
		"Podfile.lock":                   "",
		"Packages/AppCore/Package.swift": "// swift-tools-version:5.9\nimport PackageDescription\nlet package = Package(name: \"AppCore\", dependencies: [.package(url: \"https://github.com/tenjin/tenjin-ios-spm\", from: \"1.20.0\")])\n",
	})
	p := detect(t, files)
	if len(p.Tenjin) != 1 || p.Tenjin[0].Source != "Packages/AppCore/Package.swift" {
		t.Errorf("Tenjin = %+v", p.Tenjin)
	}
}

// A bundle ID left to a build setting names no app: there is no build.
func TestAnUnresolvedBundleIDMakesNoBuild(t *testing.T) {
	base := testproject.IOSNative()
	const pbx = "Sample.xcodeproj/project.pbxproj"
	p := detect(t, base.With(testproject.Files{pbx: strings.ReplaceAll(base[pbx], "com.example.sample", "$(APP_BUNDLE_ID)")}))
	if b := p.Target(project.IOS).Builds; len(b) != 0 {
		t.Errorf("builds = %+v", b)
	}
}

// The SDK version is read through a version catalog's version.ref, and
// through a variable a build file interpolates.
func TestTheSDKVersionThroughCatalogsAndVariables(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	withCatalog := base.With(testproject.Files{
		build:                       strings.Replace(base[build], `implementation("com.tenjin:android-sdk:2.0.0")`, "implementation(libs.tenjin)", 1),
		"gradle/libs.versions.toml": "[versions]\ntenjin = \"1.16.0\"\n\n[libraries]\ntenjin = { module = \"com.tenjin:android-sdk\", version.ref = \"tenjin\" }\n",
	})
	withVariable := base.With(testproject.Files{
		build:               strings.Replace(base[build], `implementation("com.tenjin:android-sdk:2.0.0")`, `implementation("com.tenjin:android-sdk:$tenjinVersion")`, 1),
		"gradle.properties": "tenjinVersion=1.23.0\n",
	})
	for name, c := range map[string]struct {
		files testproject.Files
		want  string
	}{"catalog": {withCatalog, "1.16.0"}, "variable": {withVariable, "1.23.0"}} {
		p := detect(t, c.files)
		if len(p.Tenjin) != 1 || p.Tenjin[0].Version != c.want {
			t.Errorf("%s: Tenjin = %+v", name, p.Tenjin)
		}
	}
}
