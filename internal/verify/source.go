package verify

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/keyfill"
	"github.com/tenjin/tenjin-cli/internal/project"
)

// frameworkSpec says how the SDK is used in one kind of project: where the
// source lives and what the calls look like.
type frameworkSpec struct {
	exts       []string
	sourceDirs func(t *project.Target) []string
	packages   []string // prefixes of project.Integration.Package this spec covers
	// installFix says how to add the SDK; sdkVersionToken in it stands for
	// the newest release (installFix).
	installFix  string
	initName    string
	connectName string
	// initCall matches any initialisation call. keyLiteral matches one whose
	// key is a string literal, capturing the key.
	initCall   *regexp.Regexp
	keyLiteral *regexp.Regexp
	// deprecatedInit, when set, matches an initialiser the SDK has replaced.
	deprecatedInit *regexp.Regexp
	deprecatedFix  string
	connectCall    *regexp.Regexp
	initFix        string
	connectFix     string
	// normalize rewrites the project's own names for the SDK's receiver to
	// the canonical one (receivers.go). Nil leaves the text as it is.
	normalize func(text string) string
	// bundlesPlayServices is true when the wrapper already ships the Play
	// Services libraries the native Android SDK needs.
	bundlesPlayServices bool
	// attCall matches a request for App Tracking Transparency permission.
	// attPackages are npm packages that provide one, for wrappers whose SDK
	// has no ATT API of its own.
	attCall *regexp.Regexp
	// debugLogsCall matches the call that turns the SDK's request logs on,
	// which a launch check reads; debugLogsFix says how to add it.
	debugLogsCall *regexp.Regexp
	debugLogsFix  string
	attPackages   []string
	// skip lists folders inside the source folders that are the SDK's own
	// code, and so say nothing about the app's use of it.
	skip []string
}

// skips reports whether a file is in one of the spec's skipped folders.
func (s frameworkSpec) skips(rel string) bool {
	for _, prefix := range s.skip {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

func (s frameworkSpec) ownsPackage(pkg string) bool {
	for _, prefix := range s.packages {
		if strings.HasPrefix(pkg, prefix) {
			return true
		}
	}
	return false
}

var nativeSpecs = map[project.Platform]frameworkSpec{
	project.Android: {
		exts: []string{".kt", ".java"},
		sourceDirs: func(t *project.Target) []string {
			return []string{t.Android.ModuleDir + "/src"}
		},
		packages:    []string{"com.tenjin:android-sdk", "tenjin.aar", "tenjin.jar"},
		installFix:  `Add implementation("com.tenjin:android-sdk:` + sdkVersionToken + `") to the app module's dependencies, and make sure mavenCentral() is a repository.`,
		initName:    "TenjinSDK.getInstance(context, key)",
		connectName: "connect()",
		// getInstance has variants that take an app subversion or a
		// shared secret as well; the key is the second argument of each.
		initCall:   regexp.MustCompile(`TenjinSDK\.getInstance(?:WithAppSubversion|WithSharedSecret|WithSharedSecretAppSubversion)?\(`),
		keyLiteral: regexp.MustCompile(`TenjinSDK\.getInstance(?:WithAppSubversion|WithSharedSecret|WithSharedSecretAppSubversion)?\(\s*[^,()]+,\s*"([^"]*)"`),
		// On a Tenjin receiver only, after normalizeAndroid: a media
		// browser's or a socket's connect() is not Tenjin's.
		connectCall: regexp.MustCompile(`TenjinSDK\.connect\(`),
		normalize:   normalizeAndroid,
		initFix:     `In the launcher Activity's onResume(), call TenjinSDK.getInstance(this, "<SDK_KEY>").connect().`,
		connectFix:  "Call connect() on the TenjinSDK instance in the launcher Activity's onResume(), on every launch.",
	},
	project.IOS: {
		exts: []string{".swift", ".m", ".mm"},
		sourceDirs: func(t *project.Target) []string {
			return []string{filepath.ToSlash(filepath.Dir(t.IOS.Xcodeproj))}
		},
		packages:      []string{"TenjinSDK"},
		installFix:    `Add pod 'TenjinSDK' to the app target in the Podfile and run pod install, or add the Swift package https://github.com/tenjin/tenjin-ios-spm.`,
		initName:      "TenjinSDK.getInstance(key)",
		connectName:   "TenjinSDK.connect()",
		initCall:      regexp.MustCompile(`TenjinSDK\.(?:getInstance|initialize)\(|\[TenjinSDK (?:getInstance|initialize|init):`),
		keyLiteral:    regexp.MustCompile(`TenjinSDK\.(?:getInstance|initialize)\(\s*"([^"]*)"|\[TenjinSDK (?:getInstance|initialize|init):\s*@"([^"]*)"`),
		connectCall:   regexp.MustCompile(`TenjinSDK\.connect\(|\[TenjinSDK connect|connectWithDeferredDeeplink`),
		initFix:       `At launch, call TenjinSDK.getInstance("<SDK_KEY>") (Swift) or [TenjinSDK initialize:@"<SDK_KEY>"] (Objective-C).`,
		connectFix:    "Call TenjinSDK.connect() on every launch, after the user has answered the tracking prompt.",
		attCall:       regexp.MustCompile(`requestTrackingAuthorization`),
		debugLogsCall: regexp.MustCompile(`TenjinSDK\.debugLogs\(|\[TenjinSDK debugLogs\]`),
		debugLogsFix:  "Before TenjinSDK.getInstance, add TenjinSDK.debugLogs() inside #if DEBUG … #endif (Swift), or [TenjinSDK debugLogs] inside #ifdef DEBUG … #endif (Objective-C), so that debug builds log what Tenjin answered and release builds stay quiet.",
	},
}

var wrapperSpecs = map[project.Framework]frameworkSpec{
	project.Flutter: {
		exts:                []string{".dart"},
		sourceDirs:          func(*project.Target) []string { return []string{"lib"} },
		packages:            []string{"tenjin_plugin"},
		installFix:          "Run flutter pub add tenjin_plugin.",
		initName:            "TenjinSDK.instance.initialize(sdkKey: ...)",
		connectName:         "TenjinSDK.instance.connect()",
		initCall:            regexp.MustCompile(`TenjinSDK\.instance\.(?:initialize|init)\(`),
		keyLiteral:          regexp.MustCompile(`TenjinSDK\.instance\.(?:initialize\(\s*sdkKey|init\(\s*apiKey)\s*:\s*['"]([^'"]*)['"]`),
		deprecatedInit:      regexp.MustCompile(`TenjinSDK\.instance\.init\(`),
		deprecatedFix:       "Replace TenjinSDK.instance.init(apiKey: ...) with TenjinSDK.instance.initialize(sdkKey: ...).",
		connectCall:         regexp.MustCompile(`TenjinSDK\.instance\.connect\(`),
		normalize:           normalizeFlutter,
		initFix:             `At startup, call TenjinSDK.instance.initialize(sdkKey: "<SDK_KEY>"), choosing the key by platform.`,
		connectFix:          "Call TenjinSDK.instance.connect() on every launch, after the tracking prompt on iOS.",
		bundlesPlayServices: true,
		attCall:             regexp.MustCompile(`requestTrackingAuthorization`),
	},
	project.ReactNative: {
		exts:        []string{".js", ".jsx", ".ts", ".tsx"},
		sourceDirs:  func(*project.Target) []string { return []string{"."} },
		packages:    []string{"react-native-tenjin"},
		installFix:  "Install react-native-tenjin with your package manager, then run pod install in ios/.",
		initName:    "Tenjin.initialize(key)",
		connectName: "Tenjin.connect()",
		initCall:    regexp.MustCompile(`Tenjin\.initialize\(`),
		keyLiteral:  regexp.MustCompile(`Tenjin\.initialize\(\s*['"]([^'"]*)['"]`),
		connectCall: regexp.MustCompile(`Tenjin\.connect\(`),
		normalize:   normalizeReactNative,
		initFix:     `At startup, call Tenjin.initialize("<SDK_KEY>"), choosing the key with Platform.OS.`,
		connectFix:  "Call Tenjin.connect() on every launch, after the tracking prompt on iOS.",
		attPackages: []string{"react-native-tracking-transparency", "expo-tracking-transparency"},
	},
	project.Capacitor: {
		exts:        []string{".ts", ".tsx", ".js", ".jsx", ".vue"},
		sourceDirs:  func(*project.Target) []string { return []string{"src"} },
		packages:    []string{"ionic-capacitor-tenjin"},
		installFix:  "Run npm install ionic-capacitor-tenjin, then npx cap sync.",
		initName:    "Tenjin.initialize({ sdkKey })",
		connectName: "Tenjin.connect()",
		initCall:    regexp.MustCompile(`Tenjin\.initialize\(`),
		keyLiteral:  regexp.MustCompile(`Tenjin\.initialize\(\s*\{\s*sdkKey\s*:\s*['"]([^'"]*)['"]`),
		connectCall: regexp.MustCompile(`Tenjin\.connect\(`),
		normalize:   normalizeCapacitor,
		initFix:     `After the platform is ready, call await Tenjin.initialize({ sdkKey: "<SDK_KEY>" }), choosing the key by platform.`,
		connectFix:  "Call await Tenjin.connect() on every launch, after the tracking prompt on iOS.",
		attPackages: []string{"capacitor-plugin-app-tracking-transparency", "@capacitor-community/app-tracking-transparency"},
	},
}

// call is one place in the source where the SDK is used.
type call struct {
	File string
	Line int
	// Text is the source file's whole content, for checks that look at what
	// surrounds the call.
	Text string
}

// source is what scanning the project's code found.
type source struct {
	inits      []call
	deprecated []call
	connects   []call
	att        []call
	debugLogs  []call
	// keys are the SDK keys written as string literals, in order of first
	// appearance, each with where it was found.
	keys []keyLiteral
}

type keyLiteral struct {
	Value string
	File  string
	Line  int
}

const maxSourceFileSize = 2 << 20

// scanSource reads the project's source files and records every SDK call.
func (r *runner) scanSource(spec frameworkSpec, dirs []string) source {
	var src source
	seen := map[string]bool{}
	for _, dir := range dirs {
		for _, rel := range project.SourceFiles(r.p.Root, dir, spec.exts...) {
			if seen[rel] || spec.skips(rel) || testSource(rel) {
				continue
			}
			seen[rel] = true
			path := filepath.Join(r.p.Root, filepath.FromSlash(rel))
			if info, err := os.Stat(path); err != nil || info.Size() > maxSourceFileSize {
				continue
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			text := blankComments(string(raw))
			// Cheap filter: most files never mention the SDK.
			if !strings.Contains(text, "Tenjin") {
				continue
			}
			if spec.normalize != nil {
				text = spec.normalize(text)
			}
			for _, loc := range spec.initCall.FindAllStringIndex(text, -1) {
				src.inits = append(src.inits, call{File: rel, Line: lineAt(text, loc[0]), Text: text})
			}
			if spec.deprecatedInit != nil {
				for _, loc := range spec.deprecatedInit.FindAllStringIndex(text, -1) {
					src.deprecated = append(src.deprecated, call{File: rel, Line: lineAt(text, loc[0]), Text: text})
				}
			}
			for _, loc := range spec.connectCall.FindAllStringIndex(text, -1) {
				src.connects = append(src.connects, call{File: rel, Line: lineAt(text, loc[0]), Text: text})
			}
			if spec.attCall != nil {
				for _, loc := range spec.attCall.FindAllStringIndex(text, -1) {
					src.att = append(src.att, call{File: rel, Line: lineAt(text, loc[0]), Text: text})
				}
			}
			if spec.debugLogsCall != nil {
				for _, loc := range spec.debugLogsCall.FindAllStringIndex(text, -1) {
					src.debugLogs = append(src.debugLogs, call{File: rel, Line: lineAt(text, loc[0]), Text: text})
				}
			}
			for _, m := range spec.keyLiteral.FindAllStringSubmatchIndex(text, -1) {
				// Some patterns have one capture group per syntax; take
				// whichever matched.
				for g := 2; g+1 < len(m); g += 2 {
					if m[g] >= 0 {
						src.keys = append(src.keys, keyLiteral{Value: text[m[g]:m[g+1]], File: rel, Line: lineAt(text, m[0])})
						break
					}
				}
			}
		}
	}
	return src
}

// checkCalls reports whether the SDK is initialised and connected.
func (r *runner) checkCalls(spec frameworkSpec, src source, platform project.Platform) {
	if len(src.inits) == 0 {
		r.add(Finding{
			ID: "sdk.init", Severity: Error, Platform: platform,
			Title:  "The SDK is never initialised",
			Detail: "No call to " + spec.initName + " was found.",
			Fix:    spec.initFix,
		})
	} else {
		r.pass("sdk.init", platform, "The SDK is initialised", src.inits[0].File, src.inits[0].Line)
	}
	for _, c := range src.deprecated {
		r.add(Finding{
			ID: "sdk.init.deprecated", Severity: Warning, Platform: platform,
			Title: "A deprecated initialiser is used",
			File:  c.File, Line: c.Line, Fix: spec.deprecatedFix,
		})
	}

	if len(src.connects) == 0 {
		r.add(Finding{
			ID: "sdk.connect", Severity: Error, Platform: platform,
			Title:  spec.connectName + " is never called",
			Detail: "Without it no install or session reaches Tenjin.",
			Fix:    spec.connectFix,
		})
	} else if allDebugOnly(src.connects) {
		c := src.connects[0]
		r.add(Finding{
			ID: "sdk.connect", Severity: Error, Platform: platform,
			Title:  spec.connectName + " is called only in debug builds",
			Detail: "Every call is inside #if DEBUG, so release builds, the ones people install, never connect.",
			File:   c.File, Line: c.Line,
			Fix: "Move " + spec.connectName + " out of the #if DEBUG block, so that every build calls it.",
		})
	} else {
		r.pass("sdk.connect", platform, spec.connectName+" is called", src.connects[0].File, src.connects[0].Line)
	}

	for _, k := range src.keys {
		if isPlaceholder(k.Value) && !keyfill.IsPlaceholder(k.Value) {
			r.add(Finding{
				ID: "sdk.key.placeholder", Severity: Error, Platform: platform,
				Title:  "The SDK key is still a placeholder",
				Detail: "Found " + quote(k.Value) + " where the key should be.",
				File:   k.File, Line: k.Line,
				Fix: "Replace it with the app's SDK key from the Tenjin dashboard.",
			})
		}
	}
}

var placeholderKey = regexp.MustCompile(`(?i)^$|[<>{}\s]|your|sdk[_-]?key|api[_-]?key|placeholder|xxxx|changeme|todo`)

// wholeKey is what a real SDK key looks like: a long run of capitals and
// digits.
var wholeKey = regexp.MustCompile(`^[A-Z0-9]{24,64}$`)

// isPlaceholder reports whether a key literal is obviously not a real key.
// Real keys are long runs of letters and digits; one of those is a key even
// when it happens to contain "TODO" or "XXXX", unless it is one character
// over and over.
func isPlaceholder(key string) bool {
	if wholeKey.MatchString(key) {
		return strings.Count(key, key[:1]) == len(key)
	}
	return placeholderKey.MatchString(key)
}

func quote(s string) string { return `"` + s + `"` }

// blankComments removes comments while keeping every newline, so line
// numbers still match the file.
func blankComments(s string) string { return project.BlankComments(s) }

// lineAt returns the 1-based line number of a byte offset.
func lineAt(text string, offset int) int {
	return strings.Count(text[:offset], "\n") + 1
}

// allDebugOnly reports whether every one of the calls is compiled into debug
// builds only.
func allDebugOnly(calls []call) bool {
	for _, c := range calls {
		if !debugOnly(c) {
			return false
		}
	}
	return len(calls) > 0
}
