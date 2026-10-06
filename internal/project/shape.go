package project

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path"
	"regexp"
	"strings"
)

// This file reads the parts of a project that decide how the SDK is
// integrated: where the app starts, how dependencies are declared, and which
// other SDKs are present that interact with Tenjin's.

var (
	gradleNamespace = regexp.MustCompile(`(?m)^\s*namespace\s*=?\s*["']([^"']+)["']`)
	swiftUIApp      = regexp.MustCompile(`@main\s+(?:\w+\s+)*struct\s+\w+\s*:[^{]*\bApp\b`)
	// A class marked as the entry point is the app delegate, whatever it
	// inherits from (FlutterAppDelegate, RCTAppDelegate, UIResponder).
	swiftDelegate   = regexp.MustCompile(`(?:@main|@UIApplicationMain)\s+(?:@\w+\s+)*(?:\w+\s+)*class\s+\w+`)
	objcDelegate    = regexp.MustCompile(`@implementation\s+\w*AppDelegate\b`)
	trackingRequest = regexp.MustCompile(`requestTrackingAuthorization|RequestAuthorizationTracking|RequestTrackingAuthorization|requestTrackingPermission`)
)

// androidShape fills in how the Android module is built and where the app
// starts.
func (d *detector) androidShape(info *AndroidInfo, content string) {
	info.DSL = "groovy"
	if strings.HasSuffix(info.BuildFile, ".kts") {
		info.DSL = "kotlin"
	}
	info.VersionCatalog = d.versionCatalog(info.ModuleDir)
	if info.Manifest == "" {
		return
	}

	var manifest struct {
		Package     string `xml:"package,attr"`
		Application struct {
			Activities []manifestActivity `xml:"activity"`
			Aliases    []manifestActivity `xml:"activity-alias"`
		} `xml:"application"`
	}
	if xml.Unmarshal([]byte(d.read(info.Manifest)), &manifest) != nil {
		return
	}
	namespace := manifest.Package
	if m := gradleNamespace.FindStringSubmatch(content); m != nil {
		namespace = m[1]
	}
	// An alias carries the launcher filter on behalf of the activity it
	// points to.
	name := ""
	for _, a := range manifest.Application.Activities {
		if a.launcher() {
			name = a.Name
			break
		}
	}
	if name == "" {
		for _, a := range manifest.Application.Aliases {
			if a.launcher() {
				name = a.Target
				break
			}
		}
	}
	if name == "" {
		return
	}
	switch {
	case strings.HasPrefix(name, "."):
		name = namespace + name
	case !strings.Contains(name, ".") && namespace != "":
		name = namespace + "." + name
	}
	info.LauncherActivity = name

	// The class's file, when it is in this module and not in a library.
	suffix := "/" + strings.ReplaceAll(name, ".", "/")
	for _, f := range SourceFiles(d.root, path.Join(info.ModuleDir, "src"), ".kt", ".java") {
		if strings.HasSuffix(strings.TrimSuffix(strings.TrimSuffix(f, ".kt"), ".java"), suffix) {
			info.LauncherSource = f
			break
		}
	}
}

type manifestActivity struct {
	Name    string `xml:"http://schemas.android.com/apk/res/android name,attr"`
	Target  string `xml:"http://schemas.android.com/apk/res/android targetActivity,attr"`
	Filters []struct {
		Actions    []manifestName `xml:"action"`
		Categories []manifestName `xml:"category"`
	} `xml:"intent-filter"`
}

type manifestName struct {
	Name string `xml:"http://schemas.android.com/apk/res/android name,attr"`
}

// launcher reports whether the activity is the one the home screen starts.
func (a manifestActivity) launcher() bool {
	for _, f := range a.Filters {
		main, launcher := false, false
		for _, action := range f.Actions {
			main = main || action.Name == "android.intent.action.MAIN"
		}
		for _, category := range f.Categories {
			launcher = launcher || category.Name == "android.intent.category.LAUNCHER"
		}
		if main && launcher {
			return true
		}
	}
	return false
}

// versionCatalog returns the Gradle version catalog of the build that owns
// moduleDir, or "". The catalog sits beside the settings file, which is
// above the module: the project root for a native app, android/ for a
// cross-platform one.
func (d *detector) versionCatalog(moduleDir string) string {
	for dir := moduleDir; ; dir = path.Dir(dir) {
		if d.exists(path.Join(dir, "settings.gradle")) || d.exists(path.Join(dir, "settings.gradle.kts")) || dir == "." {
			if catalog := path.Join(dir, "gradle/libs.versions.toml"); d.exists(catalog) {
				return catalog
			}
			if dir == "." {
				return ""
			}
		}
	}
}

// iosShape finds where the iOS app starts: a SwiftUI App, or an app delegate.
func (d *detector) iosShape(info *IOSInfo) {
	var delegate string
	for _, f := range SourceFiles(d.root, path.Dir(info.Xcodeproj), ".swift", ".m", ".mm") {
		if isTestPath(f) {
			continue
		}
		content := stripComments(d.read(f))
		if strings.HasSuffix(f, ".swift") {
			if swiftUIApp.MatchString(content) {
				info.Lifecycle, info.EntryPoint = "swiftui", f
				return
			}
			if delegate == "" && swiftDelegate.MatchString(content) {
				delegate = f
			}
		} else if delegate == "" && objcDelegate.MatchString(content) {
			delegate = f
		}
	}
	if delegate != "" {
		info.Lifecycle, info.EntryPoint = "app-delegate", delegate
	}
}

// TrackingPrompt is where the app asks for App Tracking Transparency
// permission: a call in the source, or a package that provides the prompt.
type TrackingPrompt struct {
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Package string `json:"package,omitempty"`
}

// trackingPackages are the packages that add a tracking prompt to frameworks
// whose Tenjin SDK has none of its own.
var trackingPackages = []string{
	"react-native-tracking-transparency", "expo-tracking-transparency",
	"capacitor-plugin-app-tracking-transparency", "@capacitor-community/app-tracking-transparency",
}

// maxScannedSources bounds the source scan in very large projects, such as a
// Unity project with thousands of scripts.
const maxScannedSources = 5000

// trackingPrompt looks for an existing tracking prompt. It matters only on
// iOS, where connect() must wait for the user's answer.
func (d *detector) trackingPrompt() {
	ios := d.p.Target(IOS)
	if ios == nil {
		return
	}
	type scan struct {
		dir  string
		exts []string
	}
	var scans []scan
	switch d.p.Framework {
	case Flutter:
		scans = append(scans, scan{"lib", []string{".dart"}})
	case ReactNative:
		scans = append(scans, scan{".", []string{".js", ".jsx", ".ts", ".tsx"}})
	case Capacitor:
		scans = append(scans, scan{"src", []string{".ts", ".tsx", ".js", ".jsx", ".vue"}})
	case Unity:
		scans = append(scans, scan{"Assets", []string{".cs"}})
	}
	if ios.IOS != nil {
		scans = append(scans, scan{path.Dir(ios.IOS.Xcodeproj), []string{".swift", ".m", ".mm"}})
	}
	for _, s := range scans {
		files := SourceFiles(d.root, s.dir, s.exts...)
		if len(files) > maxScannedSources {
			files = files[:maxScannedSources]
		}
		for _, f := range files {
			content := blankComments(d.read(f))
			if loc := trackingRequest.FindStringIndex(content); loc != nil {
				d.p.TrackingPrompt = &TrackingPrompt{File: f, Line: strings.Count(content[:loc[0]], "\n") + 1}
				return
			}
		}
	}
	if pkg, ok := d.packageJSON(); ok {
		for _, name := range trackingPackages {
			if _, ok := pkg.dependency(name); ok {
				d.p.TrackingPrompt = &TrackingPrompt{Package: name}
				return
			}
		}
	}
	if regexp.MustCompile(`(?m)^\s+app_tracking_transparency\s*:`).MatchString(d.read("pubspec.yaml")) {
		d.p.TrackingPrompt = &TrackingPrompt{Package: "app_tracking_transparency"}
	}
}

// AdSDK is an advertising or mediation SDK found among the dependencies.
type AdSDK struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// adSDKs are the SDKs that interact with a Tenjin integration: Tenjin takes
// impression-level ad revenue from each of them through a dedicated call.
var adSDKs = []struct {
	name    string
	pattern *regexp.Regexp
	// unityDirs are where the SDK's Unity plugin unpacks itself.
	unityDirs []string
}{
	{
		"AppLovin MAX",
		regexp.MustCompile(`com\.applovin:applovin-sdk|['"]AppLovinSDK['"]|- AppLovinSDK\b|AppLovin-MAX-Swift-Package|\bapplovin_max\s*:|"react-native-applovin-max"|"com\.applovin\.mediation\.ads"`),
		[]string{"Assets/MaxSdk"},
	},
	{
		"Google AdMob",
		regexp.MustCompile(`play-services-ads["':]|['"]Google-Mobile-Ads-SDK['"]|- Google-Mobile-Ads-SDK\b|swift-package-manager-google-mobile-ads|\bgoogle_mobile_ads\s*:|"react-native-google-mobile-ads"|"@capacitor-community/admob"|"com\.google\.ads\.mobile"`),
		[]string{"Assets/GoogleMobileAds"},
	},
	{
		"ironSource LevelPlay",
		regexp.MustCompile(`com\.ironsource\.sdk:mediationsdk|com\.unity3d\.ads-mediation:mediation-sdk|['"]IronSourceSDK['"]|- IronSourceSDK\b|\bironsource_mediation\s*:|"ironsource-mediation"|"com\.unity\.services\.levelplay"`),
		[]string{"Assets/LevelPlay", "Assets/IronSource"},
	},
	{"TopOn", regexp.MustCompile(`com\.anythink\.sdk:`), nil},
	{"TradPlus", regexp.MustCompile(`com\.tradplusad:`), nil},
	{"CAS", regexp.MustCompile(`com\.cleveradssolutions:|\bclever_ads_solutions\s*:`), nil},
}

// findAdSDKs looks through the files that declare dependencies.
func (d *detector) findAdSDKs() {
	files := []string{"pubspec.yaml", "package.json", "Packages/manifest.json"}
	for _, t := range d.p.Targets {
		if t.Android != nil {
			files = append(files, t.Android.BuildFile, t.Android.VersionCatalog)
		}
		if t.IOS != nil {
			dir := path.Dir(t.IOS.Xcodeproj)
			files = append(files, t.IOS.Podfile, path.Join(dir, "Podfile.lock"), path.Join(t.IOS.Xcodeproj, "project.pbxproj"))
		}
	}
	contents := map[string]string{}
	for _, f := range dedupe(files) {
		raw := d.read(f)
		if strings.HasSuffix(f, "Podfile") {
			raw = stripRubyComments(raw)
		} else if !strings.HasSuffix(f, ".lock") {
			raw = stripComments(raw)
		}
		contents[f] = raw
	}
	for _, sdk := range adSDKs {
		for _, f := range dedupe(files) {
			if sdk.pattern.MatchString(contents[f]) {
				d.p.AdSDKs = append(d.p.AdSDKs, AdSDK{Name: sdk.name, Source: f})
				break
			}
		}
		if d.p.Framework != Unity || d.hasAdSDK(sdk.name) {
			continue
		}
		for _, dir := range sdk.unityDirs {
			if d.isDir(dir) {
				d.p.AdSDKs = append(d.p.AdSDKs, AdSDK{Name: sdk.name, Source: dir})
				break
			}
		}
	}
	if d.p.Framework == Unity && d.hasAdSDK("AppLovin MAX") && d.p.Target(IOS) != nil {
		d.note("AppLovin MAX is present. Its Unity plugin overwrites NSAdvertisingAttributionReportEndpoint during the iOS build, so SKAdNetwork postback copies stop reaching Tenjin unless it is set back afterwards.")
	}
}

func (d *detector) hasAdSDK(name string) bool {
	for _, sdk := range d.p.AdSDKs {
		if sdk.Name == name {
			return true
		}
	}
	return false
}

// packageManager names the JavaScript package manager, from the
// packageManager field or the lockfile present.
func (d *detector) packageManager() string {
	var pkg struct {
		PackageManager string `json:"packageManager"`
	}
	if b, err := os.ReadFile(d.path("package.json")); err == nil && json.Unmarshal(b, &pkg) == nil {
		if name, _, _ := strings.Cut(pkg.PackageManager, "@"); name != "" {
			return name
		}
	}
	for _, lock := range []struct{ file, manager string }{
		{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"},
		{"package-lock.json", "npm"}, {"npm-shrinkwrap.json", "npm"},
	} {
		if d.exists(lock.file) {
			return lock.manager
		}
	}
	return ""
}

// finish adds what is read the same way for every framework, once the
// framework and its targets are known.
func (d *detector) finish() {
	for i := range d.p.Targets {
		// A target whose builds were not read separately ships one build.
		if t := &d.p.Targets[i]; t.Builds == nil {
			t.Builds = []Build{}
			if t.BundleID != "" {
				t.Builds = []Build{mainBuild(t)}
			}
		}
		if ios := d.p.Targets[i].IOS; ios != nil {
			d.iosShape(ios)
		}
	}
	switch d.p.Framework {
	case ReactNative, Capacitor, Cordova:
		d.p.PackageManager = d.packageManager()
	}
	d.trackingPrompt()
	d.findAdSDKs()
	d.findFeatures()
}
