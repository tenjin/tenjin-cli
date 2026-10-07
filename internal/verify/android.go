package verify

import (
	"encoding/xml"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

const (
	permInternet     = "android.permission.INTERNET"
	permNetworkState = "android.permission.ACCESS_NETWORK_STATE"
	permAdID         = "com.google.android.gms.permission.AD_ID"
	permPhoneState   = "android.permission.READ_PHONE_STATE"
)

// androidManifest is the part of AndroidManifest.xml the checks read.
type androidManifest struct {
	Permissions []struct {
		Name string `xml:"http://schemas.android.com/apk/res/android name,attr"`
		Node string `xml:"http://schemas.android.com/tools node,attr"`
	} `xml:"uses-permission"`
}

// proguardRules are the keep rules the Android SDK's README asks for.
const proguardRules = `-keep class com.tenjin.** { *; }
-keep public class com.google.android.gms.ads.identifier.** { *; }
-keep public class com.google.android.gms.common.** { *; }
-keep public class com.android.installreferrer.** { *; }
-keep class * extends java.util.ListResourceBundle {
    protected java.lang.Object[][] getContents();
}
-keepattributes Signature
-keepattributes *Annotation*
-keep class com.google.gson.reflect.TypeToken { *; }
-keep class * extends com.google.gson.reflect.TypeToken`

var applicationSubclass = regexp.MustCompile(`:\s*(?:MultiDex)?Application\s*\(|extends\s+(?:MultiDex)?Application\b`)

func (r *runner) checkAndroid(spec frameworkSpec, t *project.Target, src source) {
	if t.Android == nil {
		r.add(Finding{
			ID: "android.project", Severity: Info, Platform: project.Android,
			Title: "No Android project folder yet, so its manifest and build file were not checked",
		})
		return
	}
	r.checkAndroidManifest(t)
	r.checkStores(t)
	if !spec.bundlesPlayServices {
		r.checkPlayServices(t)
	}
	r.checkProguard(t)
	if r.p.Framework == project.Native {
		r.checkConnectLocation(src)
	}
}

func (r *runner) checkAndroidManifest(t *project.Target) {
	file := t.Android.Manifest
	if file == "" {
		r.add(Finding{
			ID: "android.manifest", Severity: Warning, Platform: project.Android,
			Title:  "AndroidManifest.xml was not found",
			Detail: "Looked in " + t.Android.ModuleDir + "/src/main.",
		})
		return
	}
	raw, err := os.ReadFile(filepath.Join(r.p.Root, filepath.FromSlash(file)))
	var manifest androidManifest
	if err != nil || xml.Unmarshal(raw, &manifest) != nil {
		r.add(Finding{
			ID: "android.manifest", Severity: Warning, Platform: project.Android,
			Title: "AndroidManifest.xml could not be read", File: file,
		})
		return
	}
	declared, removed := map[string]bool{}, map[string]bool{}
	for _, p := range manifest.Permissions {
		if p.Node == "remove" {
			removed[p.Name] = true
		} else {
			declared[p.Name] = true
		}
	}

	if declared[permInternet] {
		r.pass("android.permission.internet", project.Android, "INTERNET permission is declared", file, 0)
	} else {
		r.add(Finding{
			ID: "android.permission.internet", Severity: Error, Platform: project.Android,
			Title:  "INTERNET permission is missing",
			Detail: "Without it the SDK cannot send anything in release builds.",
			File:   file,
			Fix:    `Add <uses-permission android:name="` + permInternet + `"/> to the manifest.`,
		})
	}

	if declared[permNetworkState] {
		r.pass("android.permission.network_state", project.Android, "ACCESS_NETWORK_STATE permission is declared", file, 0)
	} else {
		r.add(Finding{
			ID: "android.permission.network_state", Severity: Warning, Platform: project.Android,
			Title: "ACCESS_NETWORK_STATE permission is missing",
			File:  file,
			Fix:   `Add <uses-permission android:name="` + permNetworkState + `"/> to the manifest.`,
		})
	}

	switch {
	case removed[permAdID]:
		r.add(Finding{
			ID: "android.permission.ad_id", Severity: Error, Platform: project.Android,
			Title:  "The AD_ID permission is explicitly removed",
			Detail: "The advertising ID will be all zeros, so installs cannot be attributed by device ID.",
			File:   file,
			Fix:    `Delete the tools:node="remove" entry for ` + permAdID + `.`,
		})
	case declared[permAdID]:
		r.pass("android.permission.ad_id", project.Android, "AD_ID permission is declared", file, 0)
	default:
		r.add(Finding{
			ID: "android.permission.ad_id", Severity: Warning, Platform: project.Android,
			Title:  "The AD_ID permission is not declared",
			Detail: "Tenjin's guide asks for it explicitly. Without it the advertising ID is all zeros on Android 13 and later.",
			File:   file,
			Fix:    `Add <uses-permission android:name="` + permAdID + `"/> to the manifest.`,
		})
	}

	if declared[permPhoneState] {
		r.add(Finding{
			ID: "android.permission.read_phone_state", Severity: Warning, Platform: project.Android,
			Title:  "READ_PHONE_STATE is declared",
			Detail: "Tenjin does not need it, and Google Play flags apps that collect device identifiers with it.",
			File:   file,
			Fix:    "Remove the permission unless another part of the app needs it.",
		})
	}
}

func (r *runner) checkPlayServices(t *project.Target) {
	build := r.readRel(t.Android.BuildFile) + "\n" + r.readRel(t.Android.VersionCatalog)
	file := t.Android.BuildFile

	if strings.Contains(build, "play-services-ads-identifier") {
		r.pass("android.play_services.ads_identifier", project.Android, "play-services-ads-identifier is a dependency", file, 0)
	} else {
		r.add(Finding{
			ID: "android.play_services.ads_identifier", Severity: Error, Platform: project.Android,
			Title:  "play-services-ads-identifier is not a dependency",
			Detail: "The SDK reads the advertising ID through this library. Without it the build still succeeds, and Tenjin rejects the events for having no device identifier.",
			File:   file,
			Fix:    `Add implementation("com.google.android.gms:play-services-ads-identifier:` + adsIdentifierVersion + `") to the app module.`,
		})
	}

	if strings.Contains(build, "play-services-appset") {
		r.pass("android.play_services.appset", project.Android, "play-services-appset is a dependency", file, 0)
	} else {
		r.add(Finding{
			ID: "android.play_services.appset", Severity: Warning, Platform: project.Android,
			Title: "play-services-appset is not a dependency",
			File:  file,
			Fix:   `Add implementation("com.google.android.gms:play-services-appset:` + appSetVersion + `") to the app module.`,
		})
	}
}

func (r *runner) checkProguard(t *project.Target) {
	if !t.Android.Minify {
		return
	}
	matches, _ := filepath.Glob(filepath.Join(r.p.Root, filepath.FromSlash(t.Android.ModuleDir), "*.pro"))
	for _, m := range matches {
		if raw, err := os.ReadFile(m); err == nil && strings.Contains(string(raw), "com.tenjin") {
			rel, _ := filepath.Rel(r.p.Root, m)
			r.pass("android.proguard", project.Android, "ProGuard keep rules for Tenjin are present", filepath.ToSlash(rel), 0)
			return
		}
	}
	r.add(Finding{
		ID: "android.proguard", Severity: Warning, Platform: project.Android,
		Title:  "Code shrinking is on and there are no keep rules for Tenjin",
		Detail: "The SDK reaches Play Services by reflection. Without keep rules, release builds can lose the advertising ID or the install referrer, or crash.",
		File:   t.Android.BuildFile,
		Fix:    "Add these lines to " + t.Android.ModuleDir + "/proguard-rules.pro:\n" + proguardRules,
	})
}

// checkConnectLocation warns when connect() is only ever called from the
// Application class.
func (r *runner) checkConnectLocation(src source) {
	if len(src.connects) == 0 {
		return
	}
	for _, c := range src.connects {
		if !applicationSubclass.MatchString(c.Text) {
			return
		}
	}
	c := src.connects[0]
	r.add(Finding{
		ID: "android.connect.location", Severity: Warning, Platform: project.Android,
		Title:  "connect() is called from the Application class",
		Detail: "The SDK captures the deeplink of a cold launch only when it is created from the launch Activity.",
		File:   c.File, Line: c.Line,
		Fix: "Move the call to the launcher Activity's onResume().",
	})
}

// readRel reads a file by its path relative to the project root, returning ""
// when it cannot be read.
func (r *runner) readRel(rel string) string {
	raw, err := os.ReadFile(filepath.Join(r.p.Root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return string(raw)
}

// storeFlavors are flavor names that say a build is for a store other than
// Google Play, with the TENJIN_APP_STORE value that store needs.
var storeFlavors = []struct{ word, store string }{
	{"amazon", "amazon"}, {"kindle", "amazon"}, {"fire", "amazon"},
	{"galaxy", "other"}, {"samsung", "other"}, {"huawei", "other"}, {"appgallery", "other"},
	{"xiaomi", "other"}, {"onestore", "other"}, {"aptoide", "other"},
}

// checkStores reports what would put a build's events under the wrong app
// in Tenjin: a TENJIN_APP_STORE value Tenjin does not know, and a flavor
// named for another store that does not declare it.
func (r *runner) checkStores(t *project.Target) {
	// A store set in code cannot be read here, so neither can which build
	// is for which store. Say so, instead of guessing from names.
	if file := r.storeSetInCode(); file != "" {
		r.add(Finding{
			ID: "android.store.in_code", Severity: Info, Platform: project.Android, File: file,
			Title:  "The app store is set in code, which these checks cannot read",
			Detail: "A build for another store is its own app in Tenjin, with its own SDK key. The Tenjin CLI can only tell the builds apart when each flavor declares its store in its manifest.",
			Fix:    "To have the Tenjin CLI set up an app and key for each store, declare TENJIN_APP_STORE in each flavor's AndroidManifest.xml instead.",
		})
		return
	}
	for _, b := range t.Builds {
		for _, d := range b.Declared {
			f := Finding{Platform: project.Android}
			if t.SeveralBuilds() {
				f.Build = b.ID
			}
			switch strings.ToLower(d.Store) {
			case "googleplay", "amazon", "other", "android_other":
				continue
			case "":
			default:
				f.ID, f.Severity = "android.store", Warning
				f.Title = "TENJIN_APP_STORE is " + quote(d.Store) + ", which Tenjin does not know"
				f.Detail = "Tenjin files the build's events under its Google Play app."
				f.File = d.Source
				f.Fix = "Set it to googleplay, amazon or other."
				r.add(f)
				continue
			}
			for _, sf := range storeFlavors {
				if d.Variant == "" || !strings.Contains(strings.ToLower(d.Variant), sf.word) {
					continue
				}
				f.ID, f.Severity = "android.store", Warning
				f.Title = "The " + d.Variant + " build declares no app store, so Tenjin files it under the Google Play app"
				f.Detail = "Its name says it is for another store. A build for another store is its own app in Tenjin, with its own SDK key; without TENJIN_APP_STORE its events go to the Google Play app."
				manifest := path.Join(t.Android.ModuleDir, "src", d.Variant, "AndroidManifest.xml")
				f.Fix = "Add <meta-data android:name=\"TENJIN_APP_STORE\" android:value=\"" + sf.store + "\" /> inside <application> in " + manifest + ", then give the build its own app and key (tenjin setup)."
				r.add(f)
				break
			}
		}
	}
}

var setAppStore = regexp.MustCompile(`\bsetAppStore\s*\(`)

// storeSetInCode returns the first source file that sets the app store in
// code, or "".
func (r *runner) storeSetInCode() string {
	for _, rel := range project.SourceFiles(r.p.Root, ".", ".kt", ".java", ".dart", ".js", ".jsx", ".ts", ".tsx") {
		raw, err := os.ReadFile(filepath.Join(r.p.Root, filepath.FromSlash(rel)))
		if err == nil && setAppStore.Match([]byte(blankComments(string(raw)))) {
			return rel
		}
	}
	return ""
}
