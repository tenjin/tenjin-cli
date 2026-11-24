package verify

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

// The Godot checks follow the Tenjin Godot SDK's README and its addon:
// enabling the plugin registers the Tenjin autoload that GDScript calls,
// and its export plugin adds the Android SDK as a Maven dependency (which
// needs a Gradle build) and, on iOS, the framework, the tracking prompt
// text and Tenjin's SKAdNetwork IDs.

var godotSpec = frameworkSpec{
	exts:        []string{".gd"},
	sourceDirs:  func(*project.Target) []string { return []string{"."} },
	packages:    []string{"tenjin-godot-sdk"},
	installFix:  "Download tenjin-godot-sdk-" + sdkVersionToken + ".zip from https://github.com/tenjin/tenjin-godot-sdk/releases, copy its addons/tenjin folder into the project's addons/ folder, and enable Tenjin SDK in Project > Project Settings > Plugins.",
	initName:    "Tenjin.initialize(key)",
	connectName: "Tenjin.connect_sdk()",
	initCall:    regexp.MustCompile(`\bTenjin\.initialize\(`),
	keyLiteral:  regexp.MustCompile(`\bTenjin\.initialize\(\s*"([^"]*)"`),
	connectCall: regexp.MustCompile(`\bTenjin\.connect_sdk(?:_with_deferred_deeplink)?\(`),
	normalize:   project.BlankHashComments,
	initFix:     `In the main scene's _ready(), call Tenjin.initialize() with each platform's own key, declared as constants: Tenjin.initialize(IOS_SDK_KEY if OS.get_name() == "iOS" else ANDROID_SDK_KEY).`,
	connectFix:  "Call Tenjin.connect_sdk() after Tenjin.initialize(), and again in _notification() when what is NOTIFICATION_APPLICATION_RESUMED.",
	attCall:     regexp.MustCompile(`\bTenjin\.request_tracking_authorization\(`),
	// The addon's own scripts call the native SDK themselves.
	skip: []string{"addons/tenjin/"},
}

var (
	godotInitArgument = regexp.MustCompile(`\bTenjin\.initialize\(\s*([^()\n]*(?:\(\)[^()\n]*)*?)\s*\)`)
	godotName         = regexp.MustCompile(`^[A-Za-z_]\w*$`)
	godotString       = regexp.MustCompile(`^"([^"\\]*)"$`)
	// godotChoice is a key chosen by platform, the way the Godot SDK's
	// README has it: A if OS.get_name() == "iOS" else B, with == or !=,
	// and either platform named.
	godotChoice    = regexp.MustCompile(`^(\S+)\s+if\s+OS\.get_name\(\)\s*(==|!=)\s*"(iOS|Android)"\s+else\s+(\S+)$`)
	godotDeclared  = `(?m)^[ \t]*(?:const|var)\s+%s\s*(?::\s*String\s*)?:?=\s*(.+?)[ \t]*$`
	godotStoreCall = regexp.MustCompile(`\bTenjin\.set_app_store\(`)
	godotEnabled   = regexp.MustCompile(`(?m)^enabled=PackedStringArray\(([^)]*)\)`)
)

func (r *runner) checkGodot() {
	spec := godotSpec
	r.checkDependency(spec, "")
	r.checkGodotPlugin()
	src := r.scanSource(spec, spec.sourceDirs(nil))
	shared := resolveGodotKeys(&src)
	r.checkCalls(spec, src, "")
	r.checkGodotResume(src)

	presets := project.GodotPresets(r.p.Root)
	if t := r.p.Target(project.Android); t != nil {
		r.checkGodotGradleBuild(presets)
		r.checkGodotStoreType(src)
		r.checkKey(t, src)
		r.checkDevice(t, src)
	}
	if t := r.p.Target(project.IOS); t != nil {
		r.checkDevice(t, src)
		r.checkGodotSKAN(presets)
		r.checkATTRequest(spec, src)
		r.checkKey(t, src)
	}
	r.checkGodotSharedKey(shared)
}

// godotKey is a key the game passes to Tenjin.initialize(), with the name
// of the constant or variable it was read from ("" for a literal).
type godotKey struct {
	keyLiteral
	Name string
}

// resolveGodotKeys reads the keys passed by name, as in
// Tenjin.initialize(SDK_KEY), from where the constant or variable is
// declared in the same file, and the keys chosen by platform with
// OS.get_name(), each then only that platform's. Only string values are
// taken; anything computed is left alone. It returns the keys that every
// platform's build sends.
func resolveGodotKeys(src *source) []godotKey {
	seen := map[string]bool{}
	for _, k := range src.keys {
		seen[k.File+"|"+strconv.Itoa(k.Line)+"|"+k.Value] = true
	}
	var shared []godotKey
	for _, c := range src.inits {
		m := godotInitArgument.FindStringSubmatch(c.Text[c.At:])
		if m == nil {
			continue
		}
		for _, k := range godotResolve(c, m[1], "", "", c.Line, 0) {
			if k.Platform == "" {
				shared = append(shared, k)
			}
			id := k.File + "|" + strconv.Itoa(k.Line) + "|" + k.Value
			if seen[id] {
				continue
			}
			seen[id] = true
			src.keys = append(src.keys, k.keyLiteral)
		}
	}
	return shared
}

// godotResolve reads the keys an expression in c's file stands for: a
// string, a constant or variable declared with a string or with another
// such expression, or a choice by platform. line is where the expression
// is, for a string; name the constant it was read through.
func godotResolve(c call, expr string, platform project.Platform, name string, line, depth int) []godotKey {
	expr = strings.TrimSpace(expr)
	if depth > 3 {
		return nil
	}
	if m := godotString.FindStringSubmatch(expr); m != nil {
		return []godotKey{{keyLiteral: keyLiteral{Value: m[1], File: c.File, Line: line, Platform: platform}, Name: name}}
	}
	if m := godotChoice.FindStringSubmatch(expr); m != nil {
		named, other := project.IOS, project.Android
		if m[3] == "Android" {
			named, other = other, named
		}
		if m[2] == "!=" {
			named, other = other, named
		}
		// A choice inside a key already chosen by platform keeps the
		// outer platform: that branch only runs there.
		if platform != "" {
			named, other = platform, platform
		}
		keys := godotResolve(c, m[1], named, "", line, depth+1)
		return append(keys, godotResolve(c, m[4], other, "", line, depth+1)...)
	}
	if !godotName.MatchString(expr) {
		return nil
	}
	decl := regexp.MustCompile(fmt.Sprintf(godotDeclared, regexp.QuoteMeta(expr)))
	loc := decl.FindStringSubmatchIndex(c.Text)
	if loc == nil {
		return nil
	}
	return godotResolve(c, c.Text[loc[2]:loc[3]], platform, expr, lineAt(c.Text, loc[0]), depth+1)
}

// checkGodotSharedKey says plainly when one key is sent by both the
// Android and the iOS build. Each is its own app in Tenjin, with its own
// key, so Tenjin refuses that key for one of them; a refusal of it then
// says why.
func (r *runner) checkGodotSharedKey(shared []godotKey) {
	if len(shared) == 0 || r.p.Target(project.Android) == nil || r.p.Target(project.IOS) == nil {
		return
	}
	k := shared[0]
	what := "The same SDK key, " + mask(k.Value) + ","
	if k.Name != "" {
		what = "One constant, " + k.Name + " (" + mask(k.Value) + "),"
	}
	why := what + " is passed to Tenjin.initialize() on both Android and iOS. The Android app and the iOS app are each their own app in Tenjin, with their own SDK key, so Tenjin refuses this key for one of them."
	fix := `Declare one key per platform and choose it by platform: const ANDROID_SDK_KEY := "<Android key>", const IOS_SDK_KEY := "<iOS key>", then Tenjin.initialize(IOS_SDK_KEY if OS.get_name() == "iOS" else ANDROID_SDK_KEY).`
	explained := false
	for i := range r.report.Findings {
		f := &r.report.Findings[i]
		if (f.ID == "key.rejected" || f.ID == "key.no_app") && f.File == k.File && f.Line == k.Line {
			f.Detail = why + " " + f.Detail
			f.Fix = fix
			explained = true
		}
	}
	if explained {
		return
	}
	r.add(Finding{
		ID: "godot.shared_key", Severity: Info,
		Title:  strings.TrimSuffix(what, ",") + " is passed for both Android and iOS",
		Detail: "The Android app and the iOS app are each their own app in Tenjin, with their own SDK key, so Tenjin refuses one key for one of them.",
		File:   k.File, Line: k.Line,
		Fix: fix,
	})
}

// checkGodotPlugin checks that the addon is enabled. Enabling it registers
// the Tenjin autoload and the export plugin that adds the native SDKs to
// the builds; copied in but not enabled, the game's calls to Tenjin fail.
func (r *runner) checkGodotPlugin() {
	if !r.declares(godotSpec) {
		return
	}
	const settings = "project.godot"
	if m := godotEnabled.FindStringSubmatch(r.readRel(settings)); m != nil && strings.Contains(m[1], `"res://`+project.GodotAddon+`"`) {
		r.pass("godot.plugin", "", "The Tenjin SDK plugin is enabled", settings, 0)
		return
	}
	r.add(Finding{
		ID: "godot.plugin", Severity: Error,
		Title:  "The Tenjin SDK plugin is not enabled",
		Detail: "Enabling it registers the Tenjin autoload the game calls, and the export plugin that adds the native SDKs to Android and iOS builds. Without it there is no Tenjin in GDScript and no SDK in the builds.",
		File:   settings,
		Fix:    "Enable Tenjin SDK in Project > Project Settings > Plugins.",
	})
}

// checkGodotResume reports when nothing connects again after the app comes
// back from the background, which the README asks for.
func (r *runner) checkGodotResume(src source) {
	if len(src.connects) == 0 {
		return
	}
	for _, c := range src.connects {
		if strings.Contains(c.Text, "NOTIFICATION_APPLICATION_RESUMED") {
			return
		}
	}
	r.add(Finding{
		ID: "godot.resume", Severity: Info,
		Title:  "Tenjin.connect_sdk() is not called again when the app returns from the background",
		Detail: "Tenjin counts a session each time connect_sdk() runs. Without a call when the app is resumed, sessions after the first are not sent.",
		Fix:    "In the main scene's script, call Tenjin.connect_sdk() in _notification() when what is NOTIFICATION_APPLICATION_RESUMED.",
	})
}

// checkGodotGradleBuild checks that Android exports use a Gradle build:
// the addon adds the Tenjin Android SDK as a Maven dependency, which only a
// Gradle build resolves.
func (r *runner) checkGodotGradleBuild(presets []project.GodotPreset) {
	const file = "export_presets.cfg"
	for _, p := range presets {
		if p.Platform != "Android" {
			continue
		}
		if p.Options["gradle_build/use_gradle_build"] == "true" {
			continue
		}
		f := Finding{
			ID: "godot.gradle_build", Severity: Error, Platform: project.Android,
			Title:  "The Android export preset does not use a Gradle build",
			Detail: "The Tenjin addon adds the Android SDK and the Play Services libraries it needs as Maven dependencies, which only a Gradle build adds to the APK. Without it the Android build has no Tenjin SDK.",
			File:   file, Line: p.Line,
			Fix: "In Project > Export, select the Android preset, turn on Gradle Build > Use Gradle Build, and install the Android build template when Godot asks.",
		}
		if p.Name != "" {
			f.Title = "The Android export preset " + p.Name + " does not use a Gradle build"
			f.Fix = "In Project > Export, select " + p.Name + ", turn on Gradle Build > Use Gradle Build, and install the Android build template when Godot asks."
		}
		r.add(f)
		return
	}
	r.pass("godot.gradle_build", project.Android, "The Android export uses a Gradle build, which adds Tenjin's Android SDK", file, 0)
}

// checkGodotStoreType reports an Android build that does not say which
// store it is for.
func (r *runner) checkGodotStoreType(src source) {
	for _, c := range append(src.inits, src.connects...) {
		if godotStoreCall.MatchString(project.BlankStrings(c.Text, ".gd")) {
			return
		}
	}
	r.add(Finding{
		ID: "godot.store_type", Severity: Info, Platform: project.Android,
		Title:  "The Android app store is not set",
		Detail: "It is unspecified unless set. Tenjin's README asks for Google Play on Google Play builds; Amazon and other stores need their own value.",
		Fix:    "Call Tenjin.set_app_store(Tenjin.AppStore.GOOGLE_PLAY) between Tenjin.initialize() and Tenjin.connect_sdk(), with AMAZON or OTHER for builds for those stores.",
	})
}

// checkGodotSKAN looks for Tenjin's SKAdNetwork postback address in the iOS
// preset's Additional Plist Content. The addon adds Tenjin's SKAdNetwork
// IDs, but not that key.
func (r *runner) checkGodotSKAN(presets []project.GodotPreset) {
	const file = "export_presets.cfg"
	for _, p := range presets {
		if p.Platform != "iOS" {
			continue
		}
		extra := p.Options["application/additional_plist_content"]
		if strings.Contains(extra, plistSKAN) && strings.Contains(extra, "tenjin-skan.com") {
			r.pass("ios.plist.skan_endpoint", project.IOS, "The iOS export preset points SKAdNetwork postbacks at Tenjin", file, p.OptionLines["application/additional_plist_content"])
			return
		}
	}
	r.add(Finding{
		ID: "ios.plist.skan_endpoint", Severity: Warning, Platform: project.IOS,
		Title:  plistSKAN + " is not set to Tenjin",
		Detail: "The Tenjin addon adds Tenjin's SKAdNetwork IDs to Info.plist, but not " + plistSKAN + ". Without it Apple does not send SKAdNetwork postback copies to Tenjin.",
		Fix:    "In Project > Export, select the iOS preset and add this to Application > Additional Plist Content: <key>" + plistSKAN + "</key><string>" + tenjinSKANURL + "</string>",
	})
}
