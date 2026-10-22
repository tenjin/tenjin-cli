package project

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Analytics is what the app already sends to other analytics SDKs, read from
// its code: the events it logs and the value it gives as the user's ID. It
// is what a Tenjin integration's custom events and customer user ID can be
// taken from, so that nobody has to guess them.
type Analytics struct {
	// Events are sorted with the most often sent first.
	Events  []AnalyticsEvent  `json:"events"`
	UserIDs []AnalyticsUserID `json:"user_ids"`
}

// AnalyticsEvent is an event the app sends to an analytics SDK. Exactly one
// of Name and Token is set, or neither:
//   - Name is the event's name when the code shows it: a string literal, a
//     constant of the SDK's own whose value is documented, or one of the
//     app's constants set to a string literal. Expression then says which
//     constant, if it was one.
//   - Token is an Adjust event token, which identifies the event in Adjust's
//     dashboard and is not a name.
//   - With neither, the name is made at runtime, and Expression is how the
//     code writes it: a variable, or a string built from parts.
type AnalyticsEvent struct {
	Name       string `json:"name,omitempty"`
	Token      string `json:"token,omitempty"`
	Expression string `json:"expression,omitempty"`
	SDK        string `json:"sdk"`
	// File and Line are where it is first sent.
	File string `json:"file"`
	Line int    `json:"line"`
	// Through is the app's own function that hands the name to the SDK,
	// when File and Line are a call to that function.
	Through string `json:"through,omitempty"`
	// Count is how many calls in the code send it.
	Count int `json:"count"`
}

// AnalyticsUserID is a value the app gives an analytics SDK as the user's
// ID, as the code writes it.
type AnalyticsUserID struct {
	Expression string `json:"expression"`
	SDK        string `json:"sdk"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Through    string `json:"through,omitempty"`
	Count      int    `json:"count"`
}

// Names are the event names found, each once, the most often sent first.
func (a Analytics) Names() []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range a.Events {
		if e.Name != "" && !seen[e.Name] {
			seen[e.Name] = true
			out = append(out, e.Name)
		}
	}
	return out
}

// EventsNamed returns where an event of that name is sent.
func (a Analytics) EventsNamed(name string) []AnalyticsEvent {
	var out []AnalyticsEvent
	for _, e := range a.Events {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

const (
	maxAnalyticsEvents  = 50
	maxAnalyticsUserIDs = 10
	maxExpressionLength = 120
)

// receiver is how code reaches an SDK: a pattern, in the files with one of
// exts, or in every file when exts is empty.
type receiver struct {
	pattern string
	exts    []string
}

func anywhere(patterns ...string) []receiver {
	out := make([]receiver, len(patterns))
	for i, p := range patterns {
		out[i] = receiver{pattern: p}
	}
	return out
}

// analyticsSDK says how code sends events and user IDs to one SDK, on every
// framework the CLI supports.
type analyticsSDK struct {
	name string
	// uses matches what shows that a file uses the SDK: its import, or a
	// name only the SDK has. Nothing in a file without it counts.
	uses *regexp.Regexp
	// receivers are the SDK's own expressions for what its calls are made
	// on; objcReceivers the same in an Objective-C message.
	receivers     []receiver
	objcReceivers []string
	// types and factories make the receivers a file names itself: a
	// variable of one of the types, or set from one of the factories.
	types     []string
	factories []string
	// jsModules are the JavaScript packages the SDK is imported from.
	jsModules string
	// events and userIDs are the methods that send an event, by its name,
	// and set the user's ID; objcEvents and objcUserIDs the first part of
	// their selectors. userIDProperties are properties that set the ID.
	events, userIDs         []string
	objcEvents, objcUserIDs []string
	userIDProperties        []string
	// nameSecond lists the file extensions where an event's name is the
	// second argument, after a context.
	nameSecond []string
	// functionArg is which argument holds the name or the ID in the SDK's
	// functions imported in JavaScript.
	functionArg int
	// conveniences are methods that send one event of the SDK's own.
	conveniences map[string]string
	// known maps the SDK's constants to the names they stand for.
	known func(expression, ext string) string
	// constructors build an event from a token (Adjust), not a name.
	constructors     []string
	objcConstructors []string
}

const getInstanceArgs = `\s*\(\s*(?:[^()]|\([^()]*\))*\)`

var analyticsSDKs = []analyticsSDK{
	{
		name: "Firebase Analytics",
		uses: regexp.MustCompile(`com\.google\.firebase\.analytics|com\.google\.firebase\.Firebase\b|import\s+Firebase(?:Analytics|Core)?\b|@import\s+Firebase|#import\s+[<"]Firebase|package:firebase_analytics/|using\s+Firebase\.Analytics|@react-native-firebase/analytics|['"]@?firebase/analytics['"]|@capacitor-firebase/analytics|@capacitor-community/firebase-analytics`),
		receivers: append(anywhere(`FirebaseAnalytics\s*\.\s*getInstance`+getInstanceArgs, `Firebase\s*\.\s*analytics`, `FirebaseAnalytics\s*\.\s*instance`,
			`FirebaseAnalytics`, `analytics\s*\(\s*\)`, `firebase\s*\.\s*analytics\s*\(\s*\)`),
			// In Swift the SDK's class is Analytics; elsewhere that name is
			// as likely the app's own.
			receiver{`Analytics`, []string{".swift"}}),
		objcReceivers: []string{`FIRAnalytics`},
		types:         []string{"FirebaseAnalytics"},
		factories:     []string{`FirebaseAnalytics\s*\.\s*getInstance`, `Firebase\s*\.\s*analytics`, `FirebaseAnalytics\s*\.\s*instance`, `getAnalytics\s*\(`},
		jsModules:     `@react-native-firebase/analytics|@?firebase/analytics|@capacitor-firebase/analytics|@capacitor-community/firebase-analytics`,
		events:        []string{"logEvent", "LogEvent"},
		objcEvents:    []string{"logEventWithName"},
		userIDs:       []string{"setUserId", "setUserID", "SetUserId"},
		objcUserIDs:   []string{"setUserID"},
		functionArg:   1,
		conveniences:  firebaseConveniences(),
		known:         firebaseConstant,
	},
	{
		name: "Amplitude",
		uses: regexp.MustCompile(`com\.amplitude\.|import\s+Amplitude(?:Swift)?\b|@import\s+Amplitude|#import\s+[<"]Amplitude|package:amplitude_flutter/|@amplitude/|amplitude-js|\bAmplitude\s*\.\s*(?:Instance|getInstance|instance)\b`),
		receivers: anywhere(`Amplitude\s*\.\s*getInstance`+getInstanceArgs, `Amplitude\s*\.\s*instance\s*\(\s*\)`, `Amplitude\s*\.\s*Instance`,
			`Amplitude\s*\.\s*instance`),
		objcReceivers: []string{`\[\s*Amplitude\s+instance\s*\]`, `Amplitude`},
		types:         []string{"Amplitude"},
		factories:     []string{`Amplitude\s*\(`, `Amplitude\s*\.\s*getInstance`, `Amplitude\s*\.\s*instance`},
		jsModules:     `@amplitude/[\w-]+|amplitude-js`,
		events:        []string{"logEvent", "track"},
		objcEvents:    []string{"logEvent"},
		userIDs:       []string{"setUserId", "setUserID"},
		objcUserIDs:   []string{"setUserId"},
	},
	{
		name: "Mixpanel",
		uses: regexp.MustCompile(`com\.mixpanel\.|import\s+Mixpanel\b|@import\s+Mixpanel|#import\s+[<"]Mixpanel|package:mixpanel_flutter/|mixpanel-react-native|mixpanel-browser|using\s+mixpanel\b`),
		receivers: anywhere(`MixpanelAPI\s*\.\s*getInstance`+getInstanceArgs, `Mixpanel\s*\.\s*mainInstance\s*\(\s*\)`, `Mixpanel\s*\.\s*sharedInstance\s*\(\s*\)`,
			`Mixpanel`),
		objcReceivers: []string{`\[\s*Mixpanel\s+sharedInstance\s*\]`},
		types:         []string{"MixpanelAPI", "Mixpanel"},
		factories:     []string{`MixpanelAPI\s*\.\s*getInstance`, `Mixpanel\s*\.\s*(?:mainInstance|sharedInstance|initialize|init)\b`, `Mixpanel\s*\(`},
		jsModules:     `mixpanel-react-native|mixpanel-browser`,
		events:        []string{"track", "Track"},
		objcEvents:    []string{"track"},
		userIDs:       []string{"identify", "Identify"},
		objcUserIDs:   []string{"identify"},
	},
	{
		name: "AppsFlyer",
		uses: regexp.MustCompile(`com\.appsflyer|import\s+AppsFlyerLib\b|@import\s+AppsFlyerLib|#import\s+[<"]AppsFlyerLib|package:appsflyer_sdk/|react-native-appsflyer|appsflyer-capacitor-plugin|using\s+AppsFlyerSDK`),
		receivers: anywhere(`AppsFlyerLib\s*\.\s*getInstance\s*\(\s*\)`, `AppsFlyerLib\s*\.\s*shared\s*\(\s*\)`, `AppsFlyerLib\s*\.\s*shared`,
			`\[\s*AppsFlyerLib\s+shared\s*\]`, `AppsFlyer`),
		objcReceivers:    []string{`\[\s*AppsFlyerLib\s+shared\s*\]`},
		types:            []string{"AppsFlyerLib", "AppsflyerSdk"},
		factories:        []string{`AppsFlyerLib\s*\.\s*(?:getInstance|shared)`, `AppsflyerSdk\s*\(`},
		jsModules:        `react-native-appsflyer|appsflyer-capacitor-plugin`,
		events:           []string{"logEvent", "trackEvent", "sendEvent"},
		objcEvents:       []string{"logEvent"},
		userIDs:          []string{"setCustomerUserId", "setCustomerUserID"},
		objcUserIDs:      []string{"setCustomerUserID"},
		userIDProperties: []string{"customerUserID"},
		nameSecond:       []string{".kt", ".java"},
		known:            appsFlyerConstant,
	},
	{
		name:             "Adjust",
		uses:             regexp.MustCompile(`com\.adjust\.sdk|import\s+Adjust(?:Sdk)?\b|@import\s+Adjust|#import\s+[<"]Adjust|package:adjust_sdk/|react-native-adjust|using\s+(?:com\.adjust\.sdk|AdjustSdk)\b`),
		constructors:     []string{`(?:new\s+)?(?:AdjustEvent|ADJEvent)(?:\s*\.\s*init)?`},
		objcConstructors: []string{`\[\s*ADJEvent\s+eventWithEventToken`},
	},
	{
		name:          "GameAnalytics",
		uses:          regexp.MustCompile(`com\.gameanalytics|import\s+GameAnalytics\b|@import\s+GameAnalytics|#import\s+[<"]GameAnalytics|package:gameanalytics_sdk/|react-native-gameanalytics|using\s+GameAnalyticsSDK`),
		receivers:     anywhere(`GameAnalytics`),
		objcReceivers: []string{`GameAnalytics`},
		jsModules:     `react-native-gameanalytics|gameanalytics`,
		events:        []string{"addDesignEvent", "addDesignEventWithEventId", "NewDesignEvent"},
		objcEvents:    []string{"addDesignEventWithEventId"},
		userIDs:       []string{"configureUserId", "setCustomId", "SetCustomId"},
		objcUserIDs:   []string{"configureUserId"},
	},
	{
		name: "Facebook App Events",
		uses: regexp.MustCompile(`com\.facebook\.appevents|import\s+FacebookCore\b|import\s+FBSDKCoreKit\b|@import\s+FBSDKCoreKit|#import\s+[<"]FBSDKCoreKit|package:facebook_app_events/|react-native-fbsdk(?:-next)?\b|using\s+Facebook\.Unity`),
		receivers: append(anywhere(`AppEventsLogger\s*\.\s*newLogger`+getInstanceArgs, `AppEventsLogger`, `FBSDKAppEvents\s*\.\s*shared`, `FBSDKAppEvents`),
			receiver{`AppEvents\s*\.\s*shared`, []string{".swift"}}, receiver{`AppEvents`, []string{".swift"}}, receiver{`FB`, []string{".cs"}}),
		objcReceivers:    []string{`\[\s*FBSDKAppEvents\s+shared\s*\]`, `FBSDKAppEvents\s*\.\s*shared`, `FBSDKAppEvents`},
		types:            []string{"AppEventsLogger", "FacebookAppEvents"},
		factories:        []string{`AppEventsLogger\s*\.\s*newLogger`, `FacebookAppEvents\s*\(`},
		jsModules:        `react-native-fbsdk(?:-next)?`,
		events:           []string{"logEvent", "LogAppEvent"},
		objcEvents:       []string{"logEvent"},
		userIDs:          []string{"setUserID", "setUserId"},
		objcUserIDs:      []string{"setUserID"},
		userIDProperties: []string{"userID"},
	},
	{
		name:          "Flurry",
		uses:          regexp.MustCompile(`com\.flurry\.android|import\s+Flurry_iOS_SDK\b|import\s+FlurryAnalytics\b|#import\s+[<"]Flurry|@import\s+Flurry|package:flutter_flurry_sdk/|react-native-flurry-sdk`),
		receivers:     anywhere(`FlurryAgent`, `Flurry`),
		objcReceivers: []string{`Flurry`},
		jsModules:     `react-native-flurry-sdk`,
		events:        []string{"logEvent"},
		objcEvents:    []string{"logEvent"},
		userIDs:       []string{"setUserId", "setUserID"},
		objcUserIDs:   []string{"setUserID"},
	},
}

// vendoredSDK matches a file that declares one of the analytics SDKs' own
// classes: the SDK's source, copied into the project, whose calls are not
// the app's.
var vendoredSDK = regexp.MustCompile(`(?:@implementation|@interface|\bclass|\bobject|\bstruct)\s+(?:Amplitude|Mixpanel|MixpanelAPI|Flurry|FlurryAgent|AppsFlyerLib|AppsFlyer|GameAnalytics|FirebaseAnalytics|FIRAnalytics|Adjust|ADJEvent|AdjustEvent|FBSDKAppEvents|AppEventsLogger)\b`)

// unityVendorFolders are where SDKs unpack themselves in a Unity project.
var unityVendorFolders = []string{"Plugins", "Firebase", "GameAnalytics", "AppsFlyer", "FacebookSDK", "Adjust", "AdjustSdk", "Mixpanel", "Amplitude",
	"Flurry", "Tenjin", "ExternalDependencyManager", "MaxSdk", "GoogleMobileAds", "LevelPlay", "IronSource", "Samples"}

// sourceFile is one of the app's source files, comments blanked.
type sourceFile struct {
	rel, ext, text string
	// index is the file's place in the scan, which orders what is found.
	index int
}

// analyticsKind is what a call gives an SDK.
type analyticsKind int

const (
	eventCall analyticsKind = iota
	userIDCall
	tokenCall
)

// sighting is one call that sends an event or a user ID.
type sighting struct {
	sdk     string
	kind    analyticsKind
	name    string // the event's name, or Adjust's token, when known
	expr    string // the code that gives it, when it is not a plain literal
	file    string
	index   int
	line    int
	through string
}

// findAnalytics reads what the app sends to other analytics SDKs.
func (d *detector) findAnalytics() {
	d.p.Analytics = Analytics{Events: []AnalyticsEvent{}, UserIDs: []AnalyticsUserID{}}
	switch d.p.Framework {
	case Native, Flutter, ReactNative, Capacitor, Unity:
	default:
		return
	}
	var files []sourceFile
	for _, rel := range SourceFiles(d.root, ".", featureCodeExts...) {
		if len(files) >= maxFeatureScanFiles {
			break
		}
		if TestSource(rel) || (d.p.Framework == Unity && unityVendor(rel)) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(d.root, filepath.FromSlash(rel)))
		if err != nil || len(raw) > 2<<20 {
			continue
		}
		text := BlankComments(string(raw))
		if vendoredSDK.MatchString(text) {
			continue
		}
		files = append(files, sourceFile{rel: rel, ext: path.Ext(rel), text: text, index: len(files)})
	}
	s := &analyticsScan{files: files, constants: appConstants(files), sdkCalls: map[string]bool{}, declared: map[string][]declaration{}}
	for i := range files {
		for j := range analyticsSDKs {
			if analyticsSDKs[j].uses.MatchString(files[i].text) {
				s.scan(&files[i], &analyticsSDKs[j])
			}
		}
	}
	s.followWrappers()
	d.p.Analytics = s.result()
}

func unityVendor(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) < 3 || parts[0] != "Assets" {
		return false
	}
	for _, v := range unityVendorFolders {
		if strings.EqualFold(parts[1], v) {
			return true
		}
	}
	return false
}

type analyticsScan struct {
	files     []sourceFile
	constants map[string]map[string]bool
	sightings []sighting
	// sdkCalls are the places of the SDK calls found, by file and offset,
	// so that a wrapper named like the SDK's method does not count them
	// again.
	sdkCalls map[string]bool
	wrappers []wrapper
	declared map[string][]declaration
}

// wrapper is a function of the app's own that passes one of its
// parameters on to an SDK as an event's name or the user's ID.
type wrapper struct {
	sdk    *analyticsSDK
	kind   analyticsKind
	name   string
	family string
	param  int // position among the positional parameters, or -1
	label  string
	file   string
	line   int
	// container is the class or object the wrapper is declared in, or ""
	// for a function of its own.
	container string
	inside    sighting
	callers   int
}

// scan finds one SDK's calls in a file.
func (s *analyticsScan) scan(f *sourceFile, sdk *analyticsSDK) {
	recv, functions := s.receivers(f, sdk)
	if len(recv) > 0 {
		alternatives := `(?:^|[^\w$])(?:` + strings.Join(recv, "|") + `)\s*[!?]*\s*\.\s*`
		s.calls(f, sdk, regexp.MustCompile(alternatives+`(`+methods(sdk.events, sdk.conveniences)+`)\s*\(`), eventCall, false)
		if len(sdk.userIDs) > 0 {
			s.calls(f, sdk, regexp.MustCompile(alternatives+`(`+strings.Join(sdk.userIDs, "|")+`)\s*\(`), userIDCall, false)
		}
		if len(sdk.userIDProperties) > 0 {
			re := regexp.MustCompile(alternatives + `(?:` + strings.Join(sdk.userIDProperties, "|") + `)\s*=[^=]`)
			for _, loc := range re.FindAllStringIndex(f.text, -1) {
				end := loc[1] - 1
				value := f.text[end:]
				if stop := strings.IndexAny(value, ";\n"); stop >= 0 {
					value = value[:stop]
				}
				s.add(f, sdk, userIDCall, end, strings.TrimSpace(value), "")
			}
		}
	}
	if f.ext == ".m" || f.ext == ".mm" {
		objc := append(append([]string{}, sdk.objcReceivers...), recv...)
		if len(objc) > 0 {
			prefix := `\[\s*(?:` + strings.Join(objc, "|") + `)\s+`
			if len(sdk.objcEvents) > 0 {
				s.calls(f, sdk, regexp.MustCompile(prefix+`(`+strings.Join(sdk.objcEvents, "|")+`)\s*:`), eventCall, true)
			}
			if len(sdk.objcUserIDs) > 0 {
				s.calls(f, sdk, regexp.MustCompile(prefix+`(`+strings.Join(sdk.objcUserIDs, "|")+`)\s*:`), userIDCall, true)
			}
		}
		for _, c := range sdk.objcConstructors {
			s.calls(f, sdk, regexp.MustCompile(`(`+c+`)\s*:`), tokenCall, true)
		}
	}
	for _, c := range sdk.constructors {
		s.calls(f, sdk, regexp.MustCompile(`(?:^|[^\w$.])(`+c+`)\s*\(`), tokenCall, false)
	}
	for kind, names := range functions {
		re := regexp.MustCompile(`(?:^|[^\w$.])(` + strings.Join(names, "|") + `)\s*\(`)
		for _, m := range re.FindAllStringSubmatchIndex(f.text, -1) {
			args := splitArgs(f.text, m[1], false)
			if sdk.functionArg < len(args) {
				s.sdkCalls[spot(f.rel, m[2])] = true
				s.add(f, sdk, kind, args[sdk.functionArg].start, f.text[args[sdk.functionArg].start:args[sdk.functionArg].end], "")
			}
		}
	}
}

// calls records the calls a pattern finds. Its first group is the method;
// the arguments start where the match ends.
func (s *analyticsScan) calls(f *sourceFile, sdk *analyticsSDK, re *regexp.Regexp, kind analyticsKind, objc bool) {
	for _, m := range re.FindAllStringSubmatchIndex(f.text, -1) {
		method := f.text[m[2]:m[3]]
		s.sdkCalls[spot(f.rel, m[2])] = true
		if kind == eventCall {
			if name, ok := sdk.conveniences[method]; ok {
				s.sightings = append(s.sightings, sighting{sdk: sdk.name, kind: eventCall, name: name, expr: method, file: f.rel, index: f.index, line: lineAt(f.text, m[2])})
				continue
			}
		}
		args := splitArgs(f.text, m[1], objc)
		n := 0
		if kind == eventCall && contains(sdk.nameSecond, f.ext) {
			n = 1
		}
		if n >= len(args) {
			continue
		}
		s.add(f, sdk, kind, args[n].start, f.text[args[n].start:args[n].end], "")
	}
}

// add classifies the code that gives an event's name, a token or a user ID,
// and records it.
func (s *analyticsScan) add(f *sourceFile, sdk *analyticsSDK, kind analyticsKind, at int, code, through string) {
	code = unlabel(strings.TrimSpace(code))
	if inner, ok := fromObject(code, kind); ok {
		code = strings.TrimSpace(inner)
	}
	if code == "" || nothing(code) {
		return
	}
	sight := sighting{sdk: sdk.name, kind: kind, expr: squash(code), file: f.rel, index: f.index, line: lineAt(f.text, at), through: through}
	if kind != userIDCall {
		if value, ok := literal(code, f.ext); ok {
			sight.name, sight.expr = value, ""
		} else {
			if kind == eventCall && sdk.known != nil {
				sight.name = sdk.known(sight.expr, f.ext)
			}
			if sight.name == "" {
				sight.name = s.constant(sight.expr)
			}
		}
		if hasControl(sight.name) || (sight.name == "" && sight.expr == "") {
			return
		}
	}
	if sight.name == "" && through == "" && identifier.MatchString(code) {
		if w, ok := s.wrapperOf(f, sdk, at, code, sight); ok {
			s.wrappers = append(s.wrappers, w)
			return
		}
	}
	s.sightings = append(s.sightings, sight)
}

// receivers are the patterns for what the SDK's calls are made on in a
// file: its own, and the names the file gives them. functions are the SDK's
// functions the file imports in JavaScript, by what they send.
func (s *analyticsScan) receivers(f *sourceFile, sdk *analyticsSDK) ([]string, map[analyticsKind][]string) {
	var recv []string
	for _, r := range sdk.receivers {
		if len(r.exts) == 0 || contains(r.exts, f.ext) {
			recv = append(recv, r.pattern)
		}
	}
	var names []string
	for _, t := range sdk.types {
		t = regexp.QuoteMeta(t)
		names = append(names,
			aliasNames(`\b(\w+)\s*:\s*`+t+`\b`, f.text),
			aliasNames(`\b`+t+`\b[?!]?[ \t]+(\w+)\s*[=;,)]`, f.text),
			aliasNames(`\b`+t+`\s*\*\s*(?:const\s+)?(\w+)`, f.text))
	}
	if len(sdk.factories) > 0 {
		factories := strings.Join(sdk.factories, "|")
		names = append(names,
			aliasNames(`\b(\w+)\s*(?::\s*[\w.<>?!]+\s*)?=\s*(?:await\s+)?(?:new\s+)?(?:`+factories+`)`, f.text),
			aliasNames(`\b(\w+)\s+by\s+lazy\s*\{\s*(?:`+factories+`)`, f.text))
	}
	functions := map[analyticsKind][]string{}
	if sdk.jsModules != "" && isJS(f.ext) {
		module := `\s*['"](?:` + sdk.jsModules + `)['"]`
		names = append(names,
			aliasNames(`import\s+(\w+)\s*(?:,\s*\{[^}]*\})?\s+from`+module, f.text),
			aliasNames(`import\s+\*\s+as\s+(\w+)\s+from`+module, f.text),
			aliasNames(`(?:const|let|var)\s+(\w+)\s*=\s*require\(`+module+`\s*\)`, f.text))
		named := regexp.MustCompile(`import\s*(?:\w+\s*,\s*)?\{([^}]*)\}\s*from` + module + `|(?:const|let|var)\s*\{([^}]*)\}\s*=\s*require\(` + module + `\s*\)`)
		for _, m := range named.FindAllStringSubmatch(f.text, -1) {
			for _, item := range strings.Split(m[1]+","+m[2], ",") {
				fields := strings.Fields(strings.ReplaceAll(item, ":", " as "))
				if len(fields) == 0 {
					continue
				}
				original, local := fields[0], fields[len(fields)-1]
				switch {
				case contains(sdk.events, original):
					functions[eventCall] = append(functions[eventCall], regexp.QuoteMeta(local))
				case contains(sdk.userIDs, original):
					functions[userIDCall] = append(functions[userIDCall], regexp.QuoteMeta(local))
				default:
					names = append(names, local)
				}
			}
		}
	}
	for _, n := range strings.Split(strings.Join(names, " "), " ") {
		if n != "" && !tenjinName(n) && !jsKeywords[n] {
			recv = append(recv, regexp.QuoteMeta(n)+`(?:\s*\(\s*\))?`)
		}
	}
	return dedupe(recv), functions
}

// tenjinName is a name the app gives Tenjin's SDK, which another SDK's
// type cannot be.
func tenjinName(n string) bool {
	return strings.Contains(strings.ToLower(n), "tenjin")
}

var jsKeywords = map[string]bool{"type": true, "default": true, "from": true, "as": true, "this": true, "self": true, "return": true, "new": true}

// aliasNames returns the names a pattern's groups capture, space-separated.
func aliasNames(pattern, text string) string {
	var out []string
	for _, m := range regexp.MustCompile(pattern).FindAllStringSubmatch(text, -1) {
		for _, name := range m[1:] {
			if name != "" {
				out = append(out, name)
			}
		}
	}
	return strings.Join(out, " ")
}

func methods(names []string, conveniences map[string]string) string {
	all := append([]string{}, names...)
	for m := range conveniences {
		all = append(all, m)
	}
	// Longest first, so that logEventWithName is not read as logEvent.
	sort.Slice(all, func(i, j int) bool { return len(all[i]) > len(all[j]) || len(all[i]) == len(all[j]) && all[i] < all[j] })
	return strings.Join(all, "|")
}

// span is an argument's place in the text.
type span struct{ start, end int }

// splitArgs returns the arguments of a call whose list starts at i, just
// past its opening parenthesis, up to the closing one. An Objective-C
// message has one argument here: the text up to the next space or bracket.
func splitArgs(text string, i int, objc bool) []span {
	var args []span
	depth, start := 0, i
	for j := i; j < len(text) && j < i+4000; j++ {
		switch c := text[j]; {
		case c == '"' || c == '\'' || c == '`':
			j = endOfString(text, j) - 1
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			if depth == 0 {
				if objc || strings.TrimSpace(text[start:j]) != "" || len(args) > 0 {
					args = append(args, span{start, j})
				}
				return args
			}
			depth--
		case c == ',' && depth == 0 && !objc:
			args = append(args, span{start, j})
			start = j + 1
		case objc && depth == 0 && (c == ' ' || c == '\n' || c == '\t' || c == ';') && strings.TrimSpace(text[start:j]) != "":
			return append(args, span{start, j})
		}
	}
	return nil
}

var (
	// A Swift, Dart or C# argument label, or a Kotlin named argument.
	argLabel   = regexp.MustCompile(`^[A-Za-z_]\w*\s*(?::[^:]|=[^=>])`)
	identifier = regexp.MustCompile(`^[A-Za-z_$][\w$]*$`)
	// An object or map literal's key that holds the name or the ID.
	nameKey   = regexp.MustCompile(`(?:^|[{(,\s])['"]?(?:name|eventName|eventType|eventId|event)['"]?\s*:\s*`)
	userIDKey = regexp.MustCompile(`(?:^|[{(,\s])['"]?(?:userId|userID|customerUserId|cuid|distinctId)['"]?\s*:\s*`)
	// An event built by a constructor, such as BaseEvent("x") or
	// AppEvents.Name("x").
	eventConstructor = regexp.MustCompile(`^(?:new\s+)?(?:[\w.]*\.)?(?:\w*Event|Name|EventName)\s*\(`)
)

// unlabel takes an argument label off, leaving the value.
func unlabel(code string) string {
	if loc := argLabel.FindStringIndex(code); loc != nil {
		return strings.TrimSpace(code[loc[1]-1:])
	}
	return code
}

// fromObject reads the name or the ID out of an object or map literal, or
// out of the constructor of an event.
func fromObject(code string, kind analyticsKind) (string, bool) {
	key := nameKey
	if kind == userIDCall {
		key = userIDKey
	}
	switch {
	case strings.HasPrefix(code, "{"):
	case kind == eventCall && eventConstructor.MatchString(code):
	default:
		return "", false
	}
	open := strings.IndexAny(code, "({")
	if loc := key.FindStringIndex(code); loc != nil {
		args := splitArgs(code+")", loc[1], false)
		if len(args) > 0 {
			return strings.TrimSpace(code[args[0].start:args[0].end]), true
		}
	}
	if code[open] == '(' {
		if args := splitArgs(code, open+1, false); len(args) > 0 {
			return unlabel(strings.TrimSpace(code[args[0].start:args[0].end])), true
		}
	}
	return "", false
}

// nothing reports code that clears a value rather than giving one.
func nothing(code string) bool {
	switch code {
	case "null", "nil", "NULL", "undefined", "None", `""`, "''", `@""`, "Nil":
		return true
	}
	return false
}

var (
	dollarTemplate = regexp.MustCompile(`\$[A-Za-z_{]`)
	stringLiteral  = regexp.MustCompile(`^@?(?:"(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'|` + "`[^`]*`" + `)$`)
	unescape       = strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\\`, `\`)
)

// literal returns the value of a string literal that is the whole of code,
// and false for anything else, including a string built from variables.
func literal(code, ext string) (string, bool) {
	if !stringLiteral.MatchString(code) {
		return "", false
	}
	body := strings.TrimPrefix(code, "@")
	quote := body[0]
	body = body[1 : len(body)-1]
	if interpolates(body, quote, ext) {
		return "", false
	}
	return unescape.Replace(body), true
}

// interpolates reports whether a string literal's body takes values in, as
// the language of the file reads it.
func interpolates(body string, quote byte, ext string) bool {
	switch ext {
	case ".kt", ".dart":
		return dollarTemplate.MatchString(body)
	case ".swift":
		return strings.Contains(body, `\(`)
	}
	return isJS(ext) && quote == '`' && strings.Contains(body, "${")
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// squash puts code on one line, and shortens it.
func squash(code string) string {
	code = strings.Join(strings.Fields(code), " ")
	if len(code) > maxExpressionLength {
		code = code[:maxExpressionLength] + "…"
	}
	return code
}

// constant resolves a constant of the app's own to its value: one that
// looks like a constant (LEVEL_UP, kLevelUp, or a member of a type, such as
// Events.levelUp) and is set to exactly one string literal in the project.
func (s *analyticsScan) constant(expr string) string {
	expr = strings.TrimSuffix(expr, ".rawValue")
	if !constantLike.MatchString(expr) {
		return ""
	}
	parts := strings.Split(expr, ".")
	last := parts[len(parts)-1]
	qualified := len(parts) > 1 && unicode.IsUpper(rune(parts[len(parts)-2][0]))
	if !qualified && !upperSnake.MatchString(last) && !kConstant.MatchString(last) {
		return ""
	}
	values := s.constants[last]
	if len(values) != 1 {
		return ""
	}
	for v := range values {
		return v
	}
	return ""
}

var (
	constantLike = regexp.MustCompile(`^[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*$`)
	upperSnake   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	kConstant    = regexp.MustCompile(`^k[A-Z]\w*$`)
	stringValue  = `@?("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*')`
	assignment   = regexp.MustCompile(`\b(\w+)\s*(?::\s*[\w.?<>]+\s*)?=\s*` + stringValue + `\s*(?:[;,)}\n]|$)`)
	property     = regexp.MustCompile(`\b(\w+)\s*:\s*` + stringValue + `\s*(?:[,}\n]|$)`)
)

// appConstants maps each name set to a string literal in the app's code to
// the values it is set to.
func appConstants(files []sourceFile) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	add := func(name, code, ext string) {
		value, ok := literal(code, ext)
		if !ok || value == "" {
			return
		}
		if out[name] == nil {
			out[name] = map[string]bool{}
		}
		out[name][value] = true
	}
	for _, f := range files {
		for _, m := range assignment.FindAllStringSubmatch(f.text, -1) {
			add(m[1], m[2], f.ext)
		}
		if isJS(f.ext) {
			for _, m := range property.FindAllStringSubmatch(f.text, -1) {
				add(m[1], m[2], f.ext)
			}
		}
	}
	return out
}

func isJS(ext string) bool {
	return ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx"
}

// family groups the extensions that call each other's functions.
func family(ext string) string {
	switch ext {
	case ".kt", ".java":
		return "jvm"
	case ".m", ".mm":
		return "objc"
	case ".swift", ".dart", ".cs":
		return ext
	}
	if isJS(ext) {
		return "js"
	}
	return ext
}

var (
	declarationKeywords = regexp.MustCompile(`^(?:return|new|throw|else|case|await|yield|in|is|as|if|for|while|switch|catch|using|lock|foreach|synchronized|when|typeof|delete|var|val|let|const)$`)
	declarations        = map[string]*regexp.Regexp{
		"jvm":    regexp.MustCompile(`\bfun\s+(?:<[^>]*>\s*)?(?:[\w.]+\.)?(\w+)\s*\(|(?m)^[ \t]*(?:(?:public|private|protected|static|final|synchronized|abstract)\s+)*([\w.<>\[\]?,]+)\s+(\w+)\s*\(`),
		".swift": regexp.MustCompile(`\bfunc\s+(\w+)\s*(?:<[^>]*>)?\s*\(`),
		"js":     regexp.MustCompile(`\bfunction\s*\*?\s*(\w+)\s*\(|\b(\w+)\s*[=:]\s*(?:async\s+)?(?:function\s*)?\(|(?m)^[ \t]*(?:(?:static|async|public|private|protected|override)\s+)*(\w+)\s*\([^()]*\)\s*(?::\s*[\w<>\[\]|. ]+)?\s*\{`),
		// Dart and C#: a return type, then the name.
		"typed": regexp.MustCompile(`(?m)^[ \t]*(?:(?:public|private|protected|internal|static|final|override|virtual|async|abstract|sealed|external)\s+)*([\w.<>\[\]?,]+)\s+(\w+)\s*\(`),
		"objc":  regexp.MustCompile(`(?m)^[ \t]*[-+]\s*\([^)]*\)\s*(\w+)\s*:\s*\([^)]*\)\s*(\w+)`),
	}
)

// declaration is a function declared in the app's code.
type declaration struct {
	name   string
	nameAt int
	// objcParam is the first parameter of an Objective-C method.
	objcParam string
}

// declarationsIn lists the functions a file declares, in order.
func declarationsIn(f *sourceFile) []declaration {
	fam := family(f.ext)
	re := declarations[fam]
	if fam == ".dart" || fam == ".cs" {
		re = declarations["typed"]
	}
	if re == nil {
		return nil
	}
	var out []declaration
	for _, m := range re.FindAllStringSubmatchIndex(f.text, -1) {
		if fam == "objc" {
			out = append(out, declaration{name: f.text[m[2]:m[3]], nameAt: m[2], objcParam: f.text[m[4]:m[5]]})
			continue
		}
		// The last group that matched is the name; the one before it, when
		// it matched, the return type, which return f( has too.
		last := -1
		for g := 2; g < len(m); g += 2 {
			if m[g] >= 0 {
				last = g
			}
		}
		if last < 0 {
			continue
		}
		name := f.text[m[last]:m[last+1]]
		if declarationKeywords.MatchString(name) || (last > 2 && m[last-2] >= 0 && declarationKeywords.MatchString(f.text[m[last-2]:m[last-1]])) {
			continue
		}
		out = append(out, declaration{name: name, nameAt: m[last]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].nameAt < out[j].nameAt })
	return out
}

// parameter is one of a function's parameters: the name it has inside the
// function, and the label a call gives it, if any.
type parameter struct {
	name, label string
	position    int // among the positional parameters, or -1 for a named one
}

var parameterNoise = regexp.MustCompile(`@\w+|\[[^\]]*\]|\brequired\b|\bfinal\b|\bvararg\b|\bval\b|\bvar\b|\bparams\b|\bconst\b|\bthis\.|[?*&!]`)

// parameters reads a declaration's parameter list.
func parameters(f *sourceFile, d declaration) []parameter {
	if d.objcParam != "" {
		return []parameter{{name: d.objcParam, label: d.name, position: 0}}
	}
	open := strings.IndexByte(f.text[d.nameAt:], '(')
	if open < 0 {
		return nil
	}
	var out []parameter
	position := 0
	for _, raw := range splitParams(f.text[d.nameAt+open+1:]) {
		// Dart's named parameters are in braces, its optional positional
		// ones in brackets.
		if f.ext == ".dart" && (strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[")) {
			named := raw[0] == '{'
			for _, inner := range splitParams(raw[1:]) {
				p, ok := parameterOf(inner, f.ext)
				if !ok {
					continue
				}
				if named {
					p.label, p.position = p.name, -1
				} else {
					p.position = position
					position++
				}
				out = append(out, p)
			}
			continue
		}
		p, ok := parameterOf(raw, f.ext)
		if !ok {
			continue
		}
		p.position = position
		position++
		out = append(out, p)
	}
	return out
}

// parameterOf reads one parameter: name: Type in Kotlin, Swift and
// TypeScript, Type name elsewhere.
func parameterOf(raw, ext string) (parameter, bool) {
	if i := strings.IndexByte(raw, '='); i >= 0 {
		raw = raw[:i]
	}
	raw = parameterNoise.ReplaceAllString(raw, " ")
	var p parameter
	words := strings.Fields(raw)
	if i := strings.IndexByte(raw, ':'); i >= 0 && (ext == ".kt" || ext == ".swift" || isJS(ext)) {
		words = strings.Fields(raw[:i])
		if len(words) > 1 && ext == ".swift" && words[0] != "_" {
			p.label = words[0]
		} else if len(words) == 1 && ext == ".swift" {
			p.label = words[0]
		}
	}
	if len(words) == 0 {
		return p, false
	}
	p.name = words[len(words)-1]
	return p, identifier.MatchString(p.name)
}

// splitParams splits a parameter list, from just past its opening
// parenthesis, at the commas outside brackets and type arguments.
func splitParams(text string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(text) && i < 2000; i++ {
		switch text[i] {
		case '"', '\'', '`':
			i = endOfString(text, i) - 1
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			if depth == 0 {
				if part := strings.TrimSpace(text[start:i]); part != "" {
					out = append(out, part)
				}
				return out
			}
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(text[start:i]))
				start = i + 1
			}
		}
	}
	return out
}

// wrapperOf works out whether the code at a call that gives an SDK a name,
// a bare identifier, is a parameter of the function the call is in: then
// that function is the app's way of sending to the SDK, and its callers
// give the names.
func (s *analyticsScan) wrapperOf(f *sourceFile, sdk *analyticsSDK, at int, ident string, inside sighting) (wrapper, bool) {
	var enclosing *declaration
	decls := s.declarationsOf(f)
	for i := range decls {
		if decls[i].nameAt < at {
			enclosing = &decls[i]
		}
	}
	if enclosing == nil {
		return wrapper{}, false
	}
	for _, p := range parameters(f, *enclosing) {
		if p.name == ident {
			return wrapper{sdk: sdk, kind: inside.kind, name: enclosing.name, family: family(f.ext), param: p.position, label: p.label,
				file: f.rel, line: lineAt(f.text, enclosing.nameAt), container: containerOf(f.text, enclosing.nameAt), inside: inside}, true
		}
	}
	return wrapper{}, false
}

var containers = regexp.MustCompile(`\b(?:class|object|struct|enum|extension|interface|mixin)\s+(\w+)|@implementation\s+(\w+)|\b(?:const|let|var)\s+(\w+)\s*=\s*\{`)

// containerOf names the class, object or JavaScript object literal a
// function is declared in, or "" for a function of its own.
func containerOf(text string, at int) string {
	name := ""
	for _, m := range containers.FindAllStringSubmatchIndex(text[:at], -1) {
		// Only one still open where the function is declared.
		between := text[m[0]:at]
		if strings.HasPrefix(between, "@implementation") {
			if strings.Contains(between, "@end") {
				continue
			}
		} else if strings.Count(between, "{")-strings.Count(between, "}") <= 0 {
			continue
		}
		for g := 2; g < len(m); g += 2 {
			if m[g] >= 0 {
				name = text[m[g]:m[g+1]]
			}
		}
	}
	return name
}

var (
	tenjinReceiverBefore = regexp.MustCompile(`(?i)tenjin\w*(?:\s*\(\s*\))?\s*[!?]*\s*\.\s*$`)
	receiverBefore       = regexp.MustCompile(`\s*[!?]?\s*\.\s*$`)
	chainWord            = regexp.MustCompile(`[A-Za-z_$][\w$]*`)
)

// receiverOf returns the expression a call at offset at is made on, such
// as AnalyticsService.shared, or "" for a call on nothing.
func receiverOf(text string, at int) string {
	loc := receiverBefore.FindStringIndex(text[max(0, at-8):at])
	if loc == nil {
		return ""
	}
	end := max(0, at-8) + loc[0]
	start, depth := end, 0
	for start > 0 && end-start < 200 {
		c := text[start-1]
		switch {
		case c == ')' || c == ']':
			depth++
		case (c == '(' || c == '[') && depth > 0:
			depth--
		case depth > 0, c == '.' || c == '?' || c == '!' || c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		default:
			return text[start:end]
		}
		start--
	}
	return text[start:end]
}

// containerNames are the names a file reaches a wrapper's class or object
// by: the class itself, a variable of that type or made from it, or a
// JavaScript import of the module that declares it.
func (w *wrapper) containerNames(f *sourceFile) map[string]bool {
	names := map[string]bool{w.container: true}
	t := regexp.QuoteMeta(w.container)
	module := regexp.QuoteMeta(strings.TrimSuffix(path.Base(w.file), path.Ext(w.file)))
	for _, n := range strings.Fields(strings.Join([]string{
		aliasNames(`\b(\w+)\s*:\s*`+t+`\b`, f.text),
		aliasNames(`\b`+t+`\b[?!]?[ \t]+(\w+)\s*[=;,)]`, f.text),
		aliasNames(`\b`+t+`\s*\*\s*(\w+)`, f.text),
		aliasNames(`\b(\w+)\s*=\s*(?:new\s+)?`+t+`\b`, f.text),
		aliasNames(`import\s+(?:\*\s+as\s+)?(\w+)\s+from\s+['"][^'"]*\b`+module+`['"]`, f.text),
	}, " ")) {
		names[n] = true
	}
	return names
}

// callsWrapper reports whether a call in a file, made on recv, is to the
// wrapper: on its class or object, on a name the file has for them, or, in
// the file that declares it, on nothing or on itself. A function of its own
// is called on nothing.
func (w *wrapper) callsWrapper(f *sourceFile, recv string, names map[string]bool) bool {
	same := f.rel == w.file
	if recv == "" {
		return same || w.container == ""
	}
	if w.container == "" {
		return false
	}
	for _, word := range chainWord.FindAllString(recv, -1) {
		if names[word] || (same && (word == "this" || word == "self")) {
			return true
		}
	}
	return false
}

// followWrappers finds the calls to the app's own functions that hand a
// name or an ID to an SDK, and reads what each call gives. A wrapper nobody
// calls is reported as the call inside it.
func (s *analyticsScan) followWrappers() {
	seen := map[string]bool{}
	for i := range s.wrappers {
		w := &s.wrappers[i]
		// The same SDK called twice inside one wrapper is one wrapper.
		id := fmt.Sprint(w.sdk.name, w.kind, w.file, w.line, w.param, w.label)
		if seen[id] {
			continue
		}
		seen[id] = true
		for j := range s.files {
			f := &s.files[j]
			if family(f.ext) != w.family || !strings.Contains(f.text, w.name) {
				continue
			}
			s.callersIn(f, w)
		}
		if w.callers == 0 {
			s.sightings = append(s.sightings, w.inside)
		}
	}
}

// declarationsOf is declarationsIn, read once per file.
func (s *analyticsScan) declarationsOf(f *sourceFile) []declaration {
	if decls, ok := s.declared[f.rel]; ok {
		return decls
	}
	decls := declarationsIn(f)
	s.declared[f.rel] = decls
	return decls
}

// callersIn reads the calls to a wrapper in one file.
func (s *analyticsScan) callersIn(f *sourceFile, w *wrapper) {
	declared := map[int]bool{}
	for _, d := range s.declarationsOf(f) {
		declared[d.nameAt] = true
	}
	names := w.containerNames(f)
	name := regexp.QuoteMeta(w.name)
	if w.family == "objc" {
		re := regexp.MustCompile(`\[\s*([^\[\]\s]+|\[[^\[\]]*\])\s+(` + name + `)\s*:`)
		for _, m := range re.FindAllStringSubmatchIndex(f.text, -1) {
			if s.sdkCalls[spot(f.rel, m[4])] || !w.callsWrapper(f, f.text[m[2]:m[3]], names) {
				continue
			}
			if args := splitArgs(f.text, m[1], true); len(args) > 0 {
				s.addFromWrapper(f, w, args[0])
			}
		}
		return
	}
	re := regexp.MustCompile(`(?:^|[^\w$])(` + name + `)\s*\(`)
	for _, m := range re.FindAllStringSubmatchIndex(f.text, -1) {
		if declared[m[2]] || s.sdkCalls[spot(f.rel, m[2])] || tenjinReceiverBefore.MatchString(f.text[max(0, m[2]-60):m[2]]) {
			continue
		}
		if !w.callsWrapper(f, receiverOf(f.text, m[2]), names) {
			continue
		}
		args := splitArgs(f.text, m[1], false)
		if arg, ok := pick(f.text, args, w); ok {
			s.addFromWrapper(f, w, arg)
		}
	}
}

// pick returns the argument a call to a wrapper gives its parameter.
func pick(text string, args []span, w *wrapper) (span, bool) {
	if w.label != "" {
		label := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(w.label) + `\s*:`)
		for _, a := range args {
			if label.MatchString(text[a.start:a.end]) {
				return a, true
			}
		}
		return span{}, false
	}
	position := 0
	for _, a := range args {
		if argLabel.MatchString(strings.TrimSpace(text[a.start:a.end])) && w.family != "jvm" && w.family != "js" {
			continue
		}
		if position == w.param {
			return a, true
		}
		position++
	}
	return span{}, false
}

// addFromWrapper records what a call to a wrapper gives it. A call that
// gives nothing, such as null, is not one of the wrapper's uses: when all of
// them are such, the call inside it is reported.
func (s *analyticsScan) addFromWrapper(f *sourceFile, w *wrapper, a span) {
	before := len(s.sightings)
	s.add(f, w.sdk, w.kind, a.start, f.text[a.start:a.end], w.name+" ("+w.file+":"+strconv.Itoa(w.line)+")")
	if len(s.sightings) > before {
		w.callers++
	}
}

// result merges the sightings: one entry per SDK and name, token or
// expression, the most often sent first.
func (s *analyticsScan) result() Analytics {
	type key struct {
		sdk  string
		kind analyticsKind
		what string
	}
	var order []key
	merged := map[key]*sighting{}
	counts := map[key]int{}
	// In the order of the code, so that each entry is where it is first
	// sent.
	sort.SliceStable(s.sightings, func(i, j int) bool {
		a, b := s.sightings[i], s.sightings[j]
		return a.index < b.index || a.index == b.index && a.line < b.line
	})
	for i := range s.sightings {
		sight := &s.sightings[i]
		k := key{sight.sdk, sight.kind, sight.name}
		if sight.name == "" {
			k.what = "\x00" + sight.expr
		}
		if merged[k] == nil {
			merged[k] = sight
			order = append(order, k)
		}
		counts[k]++
	}
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	out := Analytics{Events: []AnalyticsEvent{}, UserIDs: []AnalyticsUserID{}}
	for _, k := range order {
		sight := merged[k]
		switch k.kind {
		case userIDCall:
			if len(out.UserIDs) < maxAnalyticsUserIDs {
				out.UserIDs = append(out.UserIDs, AnalyticsUserID{Expression: sight.expr, SDK: sight.sdk, File: sight.file, Line: sight.line, Through: sight.through, Count: counts[k]})
			}
		default:
			if len(out.Events) >= maxAnalyticsEvents {
				continue
			}
			e := AnalyticsEvent{Expression: sight.expr, SDK: sight.sdk, File: sight.file, Line: sight.line, Through: sight.through, Count: counts[k]}
			if k.kind == tokenCall {
				e.Token = sight.name
			} else {
				e.Name = sight.name
			}
			out.Events = append(out.Events, e)
		}
	}
	return out
}

func spot(file string, offset int) string { return file + ":" + strconv.Itoa(offset) }

func lineAt(text string, offset int) int {
	return strings.Count(text[:offset], "\n") + 1
}

func contains(values []string, v string) bool {
	for _, candidate := range values {
		if candidate == v {
			return true
		}
	}
	return false
}

// firebaseConveniences are the Firebase methods that send one of its
// recommended events, such as logLevelUp for level_up.
func firebaseConveniences() map[string]string {
	out := map[string]string{}
	for _, event := range []string{"AddPaymentInfo", "AddShippingInfo", "AddToCart", "AddToWishlist", "AdImpression", "AppOpen", "BeginCheckout",
		"CampaignDetails", "EarnVirtualCurrency", "GenerateLead", "JoinGroup", "LevelEnd", "LevelStart", "LevelUp", "Login", "PostScore", "Purchase",
		"Refund", "RemoveFromCart", "ScreenView", "Search", "SelectContent", "SelectItem", "SelectPromotion", "Share", "SignUp",
		"SpendVirtualCurrency", "TutorialBegin", "TutorialComplete", "UnlockAchievement", "ViewCart", "ViewItem", "ViewItemList",
		"ViewPromotion", "ViewSearchResults"} {
		out["log"+event] = snake(event)
	}
	return out
}

var (
	firebaseAndroidConstant = regexp.MustCompile(`^(?:FirebaseAnalytics\s*\.\s*)?Event\s*\.\s*([A-Z][A-Z0-9_]*)$`)
	firebaseAppleConstant   = regexp.MustCompile(`^(?:AnalyticsEvent|kFIREvent)([A-Z]\w*)$`)
	firebaseUnityConstant   = regexp.MustCompile(`^FirebaseAnalytics\s*\.\s*Event([A-Z]\w*)$`)
	appsFlyerConstantRe     = regexp.MustCompile(`^AFInAppEventType\s*\.\s*([A-Z][A-Z0-9_]*)$`)
	appsFlyerAppleConstant  = regexp.MustCompile(`^AFEvent([A-Z]\w*)$`)
)

// firebaseConstant is the name a constant of Firebase's stands for:
// FirebaseAnalytics.Event.LEVEL_UP, AnalyticsEventLevelUp and
// FirebaseAnalytics.EventLevelUp are all level_up.
func firebaseConstant(expr, ext string) string {
	switch {
	case (ext == ".kt" || ext == ".java") && firebaseAndroidConstant.MatchString(expr):
		return strings.ToLower(firebaseAndroidConstant.FindStringSubmatch(expr)[1])
	case (ext == ".swift" || ext == ".m" || ext == ".mm") && firebaseAppleConstant.MatchString(expr):
		return snake(firebaseAppleConstant.FindStringSubmatch(expr)[1])
	case ext == ".cs" && firebaseUnityConstant.MatchString(expr):
		return snake(firebaseUnityConstant.FindStringSubmatch(expr)[1])
	}
	return ""
}

// appsFlyerConstant is the name a constant of AppsFlyer's stands for:
// AFInAppEventType.LEVEL_ACHIEVED and AFEventLevelAchieved are both
// af_level_achieved.
func appsFlyerConstant(expr, ext string) string {
	switch {
	case (ext == ".kt" || ext == ".java") && appsFlyerConstantRe.MatchString(expr):
		return "af_" + strings.ToLower(appsFlyerConstantRe.FindStringSubmatch(expr)[1])
	case (ext == ".swift" || ext == ".m" || ext == ".mm") && appsFlyerAppleConstant.MatchString(expr):
		return "af_" + snake(appsFlyerAppleConstant.FindStringSubmatch(expr)[1])
	}
	return ""
}

// snake turns LevelUp into level_up.
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			prev := rune(s[i-1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
