package project

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"
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

// ReadAnalytics reads what the app sends to other analytics SDKs into
// p.Analytics, once. It reads every source file, so Detect leaves it to
// the commands that show or use it.
func (p *Project) ReadAnalytics() {
	if p.analyticsRead {
		return
	}
	p.analyticsRead = true
	d := &detector{root: p.Root, p: p}
	d.findAnalytics()
}

// ReadAnalyticsFor reads them when the features include custom events or
// the customer user ID, which are what they are for.
func (p *Project) ReadAnalyticsFor(features []string) {
	for _, id := range features {
		if id == FeatureCustomEvents || id == FeatureUserID {
			p.ReadAnalytics()
			return
		}
	}
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
	// hints are words one of which a file must hold for uses to match: a
	// cheap test that leaves most files out before any pattern runs. Each
	// alternative of uses holds one (TestEveryImportHasAHint).
	hints []string
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
	// accessors are calls that reach the SDK from a name the file gives
	// it, as amplitude.getInstance() does.
	accessors string
	// jsModules are the JavaScript packages the SDK is imported from.
	jsModules string
	// events and userIDs are the methods that send an event, by its name,
	// and set the user's ID; objcEvents and objcUserIDs the first part of
	// their selectors. userIDProperties are properties that set the ID.
	events, userIDs         []string
	objcEvents, objcUserIDs []string
	userIDProperties        []string
	// labels are methods that count only when their first argument has
	// this label, as Flurry's log(eventName:) does: its other log calls
	// send errors.
	labels map[string]string
	// nameSecond lists the file extensions where an event's name is the
	// second argument, after a context.
	nameSecond []string
	// functionArg is which argument holds the name or the ID in the SDK's
	// functions imported in JavaScript.
	functionArg int
	// conveniences are methods that send one event of the SDK's own.
	conveniences map[string]string
	// constant reads the SDK's own constants: the name one stands for when
	// its value is known, and whether the expression is the SDK's at all.
	constant func(expr string, f *sourceFile) (string, bool)
	// constructors build an event from a token (Adjust), not a name.
	constructors     []string
	objcConstructors []string

	m *sdkMatchers
}

const getInstanceArgs = `\s*\(\s*(?:[^()]|\([^()]*\))*\)`

var analyticsSDKs = []analyticsSDK{
	{
		name:  "Firebase Analytics",
		hints: []string{"irebase"},
		uses:  regexp.MustCompile(`com\.google\.firebase\.analytics|com\.google\.firebase\.Firebase\b|import\s+Firebase(?:Analytics|Core)?\b|@import\s+Firebase|#import\s+[<"]Firebase|package:firebase_analytics/|using\s+Firebase\.Analytics|@react-native-firebase/analytics|['"]@?firebase/analytics['"]|@capacitor-firebase/analytics|@capacitor-community/firebase-analytics`),
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
		constant:      firebaseConstant,
	},
	{
		name:  "Amplitude",
		hints: []string{"mplitude"},
		uses:  regexp.MustCompile(`com\.amplitude\.|import\s+Amplitude(?:Swift)?\b|@import\s+Amplitude|#import\s+[<"]Amplitude|package:amplitude_flutter/|@amplitude/|amplitude-js|\bAmplitude\s*\.\s*(?:Instance|getInstance|instance)\b`),
		receivers: anywhere(`Amplitude\s*\.\s*getInstance`+getInstanceArgs, `Amplitude\s*\.\s*instance\s*\(\s*\)`, `Amplitude\s*\.\s*Instance`,
			`Amplitude\s*\.\s*instance`),
		objcReceivers: []string{`\[\s*Amplitude\s+instance\s*\]`, `Amplitude`},
		types:         []string{"Amplitude"},
		factories:     []string{`Amplitude\s*\(`, `Amplitude\s*\.\s*getInstance`, `Amplitude\s*\.\s*instance`},
		// amplitude-js: amplitude.getInstance().logEvent(…).
		accessors:   `getInstance`,
		jsModules:   `@amplitude/[\w-]+|amplitude-js`,
		events:      []string{"logEvent", "track"},
		objcEvents:  []string{"logEvent"},
		userIDs:     []string{"setUserId", "setUserID"},
		objcUserIDs: []string{"setUserId"},
	},
	{
		name:  "Mixpanel",
		hints: []string{"ixpanel"},
		uses:  regexp.MustCompile(`com\.mixpanel\.|import\s+Mixpanel\b|@import\s+Mixpanel|#import\s+[<"]Mixpanel|package:mixpanel_flutter/|mixpanel-react-native|mixpanel-browser|using\s+mixpanel\b`),
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
		name:  "AppsFlyer",
		hints: []string{"ppsflyer", "ppsFlyer"},
		uses:  regexp.MustCompile(`com\.appsflyer|import\s+AppsFlyerLib\b|@import\s+AppsFlyerLib|#import\s+[<"]AppsFlyerLib|package:appsflyer_sdk/|react-native-appsflyer|appsflyer-capacitor-plugin|using\s+AppsFlyerSDK`),
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
		constant:         appsFlyerConstant,
	},
	{
		name:         "Adjust",
		hints:        []string{"djust"},
		uses:         regexp.MustCompile(`com\.adjust\.sdk|import\s+Adjust(?:Sdk)?\b|@import\s+Adjust|#import\s+[<"]Adjust|package:adjust_sdk/|react-native-adjust|using\s+(?:com\.adjust\.sdk|AdjustSdk)\b`),
		constructors: []string{`(?:new\s+)?(?:AdjustEvent|ADJEvent)(?:\s*\.\s*init)?`},
		objcConstructors: []string{`\[\s*ADJEvent\s+eventWithEventToken`,
			`\[\s*\[\s*ADJEvent\s+alloc\s*\]\s+initWithEventToken`},
	},
	{
		name:          "GameAnalytics",
		hints:         []string{"ameanalytics", "ameAnalytics"},
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
		name:  "Facebook App Events",
		hints: []string{"acebook", "FBSDKCoreKit", "fbsdk"},
		uses:  regexp.MustCompile(`com\.facebook\.appevents|import\s+FacebookCore\b|import\s+FBSDKCoreKit\b|@import\s+FBSDKCoreKit|#import\s+[<"]FBSDKCoreKit|package:facebook_app_events/|react-native-fbsdk(?:-next)?\b|using\s+Facebook\.Unity`),
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
		constant:         facebookConstant,
	},
	{
		name:          "Flurry",
		hints:         []string{"lurry"},
		uses:          regexp.MustCompile(`com\.flurry\.android|import\s+Flurry_iOS_SDK\b|import\s+FlurryAnalytics\b|#import\s+[<"]Flurry|@import\s+Flurry|package:flutter_flurry_sdk/|react-native-flurry-sdk|using\s+FlurrySDK\b`),
		receivers:     anywhere(`FlurryAgent`, `Flurry`),
		objcReceivers: []string{`Flurry`},
		jsModules:     `react-native-flurry-sdk`,
		// log(eventName:) and set(userId:) in Swift; LogEvent and SetUserId
		// in Unity.
		events:      []string{"logEvent", "LogEvent", "log"},
		objcEvents:  []string{"logEvent"},
		userIDs:     []string{"setUserId", "setUserID", "SetUserId", "set"},
		objcUserIDs: []string{"setUserID"},
		labels:      map[string]string{"log": "eventName", "set": "userId"},
	},
}

// sdkMatchers are an SDK's patterns, compiled once.
type sdkMatchers struct {
	// events, userIDs and properties match a call or an assignment from
	// its dot on; the receiver is read back from there.
	events, userIDs, properties *regexp.Regexp
	// objcEvents and objcUserIDs match a message, the receiver in the first
	// group and the method in the second; objcReceiver says which
	// receivers are the SDK's.
	objcEvents, objcUserIDs *regexp.Regexp
	objcReceiver            *regexp.Regexp
	constructors            []*regexp.Regexp
	objcConstructors        []*regexp.Regexp
	// aliases find the names a file gives the SDK, in their first group.
	aliases   []*regexp.Regexp
	jsAliases []*regexp.Regexp
	jsNamed   *regexp.Regexp
	accessor  *regexp.Regexp

	mu     sync.Mutex
	static map[string]*regexp.Regexp
}

func init() {
	for i := range analyticsSDKs {
		analyticsSDKs[i].compile()
	}
}

func (sdk *analyticsSDK) compile() {
	m := &sdkMatchers{static: map[string]*regexp.Regexp{}}
	if len(sdk.events)+len(sdk.conveniences) > 0 {
		m.events = regexp.MustCompile(`\.\s*(` + methods(sdk.events, sdk.conveniences) + `)\s*\(`)
	}
	if len(sdk.userIDs) > 0 {
		m.userIDs = regexp.MustCompile(`\.\s*(` + methods(sdk.userIDs, nil) + `)\s*\(`)
	}
	if len(sdk.userIDProperties) > 0 {
		m.properties = regexp.MustCompile(`\.\s*(` + strings.Join(sdk.userIDProperties, "|") + `)\s*=[^=]`)
	}
	message := `\[\s*(\[[^\[\]]*\]|[A-Za-z_][\w.]*)\s+(`
	if len(sdk.objcEvents) > 0 {
		m.objcEvents = regexp.MustCompile(message + methods(sdk.objcEvents, nil) + `)\s*:`)
	}
	if len(sdk.objcUserIDs) > 0 {
		m.objcUserIDs = regexp.MustCompile(message + methods(sdk.objcUserIDs, nil) + `)\s*:`)
	}
	objc := append([]string{}, sdk.objcReceivers...)
	for _, r := range sdk.receivers {
		if len(r.exts) == 0 {
			objc = append(objc, r.pattern)
		}
	}
	if len(objc) > 0 {
		m.objcReceiver = regexp.MustCompile(`^(?:` + strings.Join(objc, "|") + `)$`)
	}
	for _, c := range sdk.constructors {
		m.constructors = append(m.constructors, regexp.MustCompile(`(?:^|[^\w$.])(`+c+`)\s*\(`))
	}
	for _, c := range sdk.objcConstructors {
		m.objcConstructors = append(m.objcConstructors, regexp.MustCompile(`(`+c+`)\s*:`))
	}
	for _, t := range sdk.types {
		t = regexp.QuoteMeta(t)
		m.aliases = append(m.aliases,
			regexp.MustCompile(`\b(\w+)\s*:\s*`+t+`\b`),
			regexp.MustCompile(`\b`+t+`\b[?!]?[ \t]+(\w+)\s*[=;,)]`),
			regexp.MustCompile(`\b`+t+`\s*\*\s*(?:const\s+)?(\w+)`))
	}
	if len(sdk.factories) > 0 {
		factories := strings.Join(sdk.factories, "|")
		m.aliases = append(m.aliases,
			regexp.MustCompile(`\b(\w+)\s*(?::\s*[\w.<>?!]+\s*)?=\s*(?:await\s+)?(?:new\s+)?(?:`+factories+`)`),
			regexp.MustCompile(`\b(\w+)\s+by\s+lazy\s*\{\s*(?:`+factories+`)`))
	}
	if sdk.jsModules != "" {
		module := `\s*['"](?:` + sdk.jsModules + `)['"]`
		m.jsAliases = []*regexp.Regexp{
			regexp.MustCompile(`import\s+(\w+)\s*(?:,\s*\{[^}]*\})?\s+from` + module),
			regexp.MustCompile(`import\s+\*\s+as\s+(\w+)\s+from` + module),
			regexp.MustCompile(`(?:const|let|var)\s+(\w+)\s*=\s*require\(` + module + `\s*\)`),
		}
		m.jsNamed = regexp.MustCompile(`import\s*(?:\w+\s*,\s*)?\{([^}]*)\}\s*from` + module + `|(?:const|let|var)\s*\{([^}]*)\}\s*=\s*require\(` + module + `\s*\)`)
	}
	if sdk.accessors != "" {
		m.accessor = regexp.MustCompile(`\s*\.\s*(?:` + sdk.accessors + `)\s*\([^()]*\)$`)
	}
	sdk.m = m
}

// static matches a receiver, as receiverOf reads it, that ends in one of
// the SDK's own receivers for files with that extension.
func (sdk *analyticsSDK) static(ext string) *regexp.Regexp {
	m := sdk.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if re, ok := m.static[ext]; ok {
		return re
	}
	var alternatives []string
	for _, r := range sdk.receivers {
		if len(r.exts) == 0 || contains(r.exts, ext) {
			alternatives = append(alternatives, r.pattern)
		}
	}
	var re *regexp.Regexp
	if len(alternatives) > 0 {
		re = regexp.MustCompile(`(?:^|[^\w$])(?:` + strings.Join(alternatives, "|") + `)\s*[!?]*$`)
	}
	m.static[ext] = re
	return re
}

func (sdk *analyticsSDK) hinted(text string) bool {
	for _, h := range sdk.hints {
		if strings.Contains(text, h) {
			return true
		}
	}
	return false
}

// vendoredSDK matches a file that declares one of the analytics SDKs' own
// classes: the SDK's source, copied into the project, whose calls are not
// the app's. vendoredNames are the names, one of which such a file holds.
var (
	vendoredSDK   = regexp.MustCompile(`(?:@implementation|@interface|\bclass|\bobject|\bstruct)\s+(?:Amplitude|Mixpanel|MixpanelAPI|Flurry|FlurryAgent|AppsFlyerLib|AppsFlyer|GameAnalytics|FirebaseAnalytics|FIRAnalytics|Adjust|ADJEvent|AdjustEvent|FBSDKAppEvents|AppEventsLogger)\b`)
	vendoredNames = []string{"Amplitude", "Mixpanel", "Flurry", "AppsFlyer", "GameAnalytics", "FirebaseAnalytics", "FIRAnalytics", "Adjust", "ADJEvent",
		"FBSDKAppEvents", "AppEventsLogger"}
)

func vendored(text string) bool {
	for _, n := range vendoredNames {
		if strings.Contains(text, n) {
			return vendoredSDK.MatchString(text)
		}
	}
	return false
}

// unityVendorFolders are where SDKs unpack themselves in a Unity project.
var unityVendorFolders = []string{"Plugins", "Firebase", "GameAnalytics", "AppsFlyer", "FacebookSDK", "Adjust", "AdjustSdk", "Mixpanel", "Amplitude",
	"Flurry", "Tenjin", "ExternalDependencyManager", "MaxSdk", "GoogleMobileAds", "LevelPlay", "IronSource", "Samples"}

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
	var rels []string
	for _, rel := range SourceFiles(d.root, ".", featureCodeExts...) {
		if len(rels) >= maxFeatureScanFiles {
			break
		}
		if !TestSource(rel) && (d.p.Framework != Unity || !unityVendor(rel)) && !bundled(rel) {
			rels = append(rels, rel)
		}
	}
	// Reading a file costs more waiting than work: read several at once.
	read := make([]*sourceFile, len(rels))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(8, runtime.GOMAXPROCS(0), len(rels)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				read[i] = readSource(d.root, rels[i])
			}
		}()
	}
	for i := range rels {
		next <- i
	}
	close(next)
	wg.Wait()
	var files []*sourceFile
	for _, f := range read {
		if f != nil {
			f.index = len(files)
			files = append(files, f)
		}
	}
	s := &analyticsScan{files: files, sdkCalls: map[spot]bool{}, patterns: map[string][]*regexp.Regexp{}}
	for _, f := range files {
		for _, sdk := range f.sdks {
			s.scan(f, sdk)
		}
	}
	s.followWrappers()
	d.p.Analytics = s.result()
}

// readSource reads one of the app's source files, or returns nil for one
// that is not the app's own: too large, minified, or an SDK's copied in.
func readSource(root, rel string) *sourceFile {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || len(raw) > 2<<20 {
		return nil
	}
	ext := path.Ext(rel)
	src := decodeSource(raw)
	if isJS(ext) && minified(src) {
		return nil
	}
	text := BlankComments(src)
	if vendored(text) {
		return nil
	}
	f := &sourceFile{rel: rel, ext: ext, text: text, code: BlankStrings(text, ext)}
	for i := range analyticsSDKs {
		if sdk := &analyticsSDKs[i]; sdk.hinted(text) && sdk.uses.MatchString(text) {
			f.sdks = append(f.sdks, sdk)
		}
	}
	return f
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

// bundled reports a JavaScript file a bundler or a minifier wrote, by its
// name.
func bundled(rel string) bool {
	base := path.Base(rel)
	for _, suffix := range []string{".min.js", ".bundle.js", ".chunk.js"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// minified reports a large file of long lines: code a minifier wrote, not
// the app's source, and costly to read.
func minified(src string) bool {
	if len(src) < 64<<10 {
		return false
	}
	return len(src)/(strings.Count(src, "\n")+1) > 500
}

// decodeSource returns a source file as UTF-8 text: from UTF-8, with or
// without a byte-order mark, or from UTF-16 with one, which Visual Studio
// writes C# in.
func decodeSource(raw []byte) string {
	switch {
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		return Text(raw[3:])
	case len(raw) >= 2 && (raw[0] == 0xFF && raw[1] == 0xFE || raw[0] == 0xFE && raw[1] == 0xFF):
		little := raw[0] == 0xFF
		units := make([]uint16, (len(raw)-2)/2)
		for i := range units {
			lo, hi := raw[2+2*i], raw[3+2*i]
			if !little {
				lo, hi = hi, lo
			}
			units[i] = uint16(lo) | uint16(hi)<<8
		}
		return Text([]byte(string(utf16.Decode(units))))
	}
	return Text(raw)
}

// spot is a place in a file: its index and an offset.
type spot struct{ file, offset int }

type analyticsScan struct {
	files     []*sourceFile
	constants *constantTable
	sightings []sighting
	// sdkCalls are the places of the SDK calls found, so that a wrapper
	// named like the SDK's method does not count them again.
	sdkCalls map[spot]bool
	wrappers []wrapper
	// patterns are the compiled patterns for a wrapper's container, by
	// container and file.
	patterns map[string][]*regexp.Regexp
}

// scan finds one SDK's calls in a file. Calls are matched on the code
// without its strings, and their arguments read from the code with them.
func (s *analyticsScan) scan(f *sourceFile, sdk *analyticsSDK) {
	aliases, functions := aliasesIn(f, sdk)
	m := sdk.m
	static := sdk.static(f.ext)
	onSDK := func(dot int) bool {
		_, recv := receiverAt(f.code, dot)
		return isReceiver(sdk, static, aliases, recv)
	}
	if m.events != nil {
		for _, loc := range m.events.FindAllStringSubmatchIndex(f.code, -1) {
			if onSDK(loc[0]) {
				s.call(f, sdk, loc[2], loc[3], loc[1], eventCall, false)
			}
		}
	}
	if m.userIDs != nil {
		for _, loc := range m.userIDs.FindAllStringSubmatchIndex(f.code, -1) {
			if onSDK(loc[0]) {
				s.call(f, sdk, loc[2], loc[3], loc[1], userIDCall, false)
			}
		}
	}
	if m.properties != nil {
		for _, loc := range m.properties.FindAllStringIndex(f.code, -1) {
			if !onSDK(loc[0]) {
				continue
			}
			end := loc[1] - 1
			value := f.text[end:]
			if stop := strings.IndexAny(value, ";\n"); stop >= 0 {
				value = value[:stop]
			}
			s.add(f, sdk, userIDCall, end, strings.TrimSpace(value), "")
		}
	}
	if f.ext == ".m" || f.ext == ".mm" {
		for kind, re := range []*regexp.Regexp{eventCall: m.objcEvents, userIDCall: m.objcUserIDs} {
			if re == nil {
				continue
			}
			for _, loc := range re.FindAllStringSubmatchIndex(f.code, -1) {
				recv := f.code[loc[2]:loc[3]]
				if (m.objcReceiver != nil && m.objcReceiver.MatchString(recv)) || aliases[recv] {
					s.call(f, sdk, loc[4], loc[5], loc[1], analyticsKind(kind), true)
				}
			}
		}
		for _, re := range m.objcConstructors {
			for _, loc := range re.FindAllStringSubmatchIndex(f.code, -1) {
				s.call(f, sdk, loc[2], loc[3], loc[1], tokenCall, true)
			}
		}
	}
	for _, re := range m.constructors {
		for _, loc := range re.FindAllStringSubmatchIndex(f.code, -1) {
			s.call(f, sdk, loc[2], loc[3], loc[1], tokenCall, false)
		}
	}
	for kind, names := range functions {
		for _, name := range names {
			for _, c := range f.calls()[name] {
				if c.member {
					continue
				}
				args := splitArgs(f.text, c.open, false)
				if sdk.functionArg < len(args) {
					s.sdkCalls[spot{f.index, c.at}] = true
					a := args[sdk.functionArg]
					s.add(f, sdk, kind, a.start, f.text[a.start:a.end], "")
				}
			}
		}
	}
}

// call records a call: its method at [start, end), its arguments from
// argsAt.
func (s *analyticsScan) call(f *sourceFile, sdk *analyticsSDK, start, end, argsAt int, kind analyticsKind, objc bool) {
	method := f.code[start:end]
	s.sdkCalls[spot{f.index, start}] = true
	if kind == eventCall {
		if name, ok := sdk.conveniences[method]; ok {
			s.sightings = append(s.sightings, sighting{sdk: sdk.name, kind: eventCall, name: name, expr: method, file: f.rel, index: f.index, line: f.line(start)})
			return
		}
	}
	args := splitArgs(f.text, argsAt, objc)
	n := 0
	if kind == eventCall && contains(sdk.nameSecond, f.ext) {
		n = 1
	}
	if n >= len(args) {
		return
	}
	if label, ok := sdk.labels[method]; ok && !labelled(f.text[args[0].start:args[0].end], label) {
		return
	}
	s.add(f, sdk, kind, args[n].start, f.text[args[n].start:args[n].end], "")
}

func labelled(arg, label string) bool {
	arg = strings.TrimSpace(arg)
	return strings.HasPrefix(arg, label) && strings.HasPrefix(strings.TrimSpace(arg[len(label):]), ":")
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
	sight := sighting{sdk: sdk.name, kind: kind, expr: squash(code), file: f.rel, index: f.index, line: f.line(at), through: through}
	if kind != userIDCall {
		if value, ok := literal(code, f.ext); ok {
			sight.name, sight.expr = value, ""
		} else {
			owned := false
			if kind == eventCall {
				sight.name, owned = sdkConstant(sdk, sight.expr, f)
			}
			if sight.name == "" && !owned {
				sight.name = s.constant(sight.expr, f, at)
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

// aliasesIn returns the names a file gives an SDK, and the SDK's functions
// it imports in JavaScript, by what they send.
func aliasesIn(f *sourceFile, sdk *analyticsSDK) (map[string]bool, map[analyticsKind][]string) {
	aliases := map[string]bool{}
	add := func(re *regexp.Regexp) {
		for _, m := range re.FindAllStringSubmatch(f.text, -1) {
			if n := m[1]; n != "" && !tenjinName(n) && !jsKeywords[n] {
				aliases[n] = true
			}
		}
	}
	for _, re := range sdk.m.aliases {
		add(re)
	}
	functions := map[analyticsKind][]string{}
	if !isJS(f.ext) || sdk.m.jsNamed == nil {
		return aliases, functions
	}
	for _, re := range sdk.m.jsAliases {
		add(re)
	}
	for _, m := range sdk.m.jsNamed.FindAllStringSubmatch(f.text, -1) {
		for _, item := range strings.Split(m[1]+","+m[2], ",") {
			fields := strings.Fields(strings.ReplaceAll(item, ":", " as "))
			if len(fields) == 0 {
				continue
			}
			original, local := fields[0], fields[len(fields)-1]
			switch {
			case contains(sdk.events, original):
				functions[eventCall] = append(functions[eventCall], local)
			case contains(sdk.userIDs, original):
				functions[userIDCall] = append(functions[userIDCall], local)
			case !tenjinName(local) && !jsKeywords[local]:
				aliases[local] = true
			}
		}
	}
	return aliases, functions
}

// isReceiver reports whether recv, what a call is made on, is the SDK: one
// of its own receivers, or ends in a name the file gives it.
func isReceiver(sdk *analyticsSDK, static *regexp.Regexp, aliases map[string]bool, recv string) bool {
	if recv == "" {
		return false
	}
	if static != nil && static.MatchString(recv) {
		return true
	}
	if len(aliases) == 0 {
		return false
	}
	if aliasAtEnd(recv, aliases) {
		return true
	}
	if sdk.m.accessor != nil {
		if loc := sdk.m.accessor.FindStringIndex(recv); loc != nil {
			return aliasAtEnd(recv[:loc[0]], aliases)
		}
	}
	return false
}

// aliasAtEnd reports whether recv ends in one of the names, called with no
// arguments or not.
func aliasAtEnd(recv string, aliases map[string]bool) bool {
	r := strings.TrimRight(recv, "!? \t\n")
	if strings.HasSuffix(r, ")") {
		open := strings.LastIndexByte(r, '(')
		if open < 0 || strings.TrimSpace(r[open+1:len(r)-1]) != "" {
			return false
		}
		r = strings.TrimRight(r[:open], " \t\n")
	}
	i := len(r)
	for i > 0 && wordByte(r[i-1]) {
		i--
	}
	return i < len(r) && aliases[r[i:]]
}

// tenjinName is a name the app gives Tenjin's SDK, which another SDK's
// type cannot be.
func tenjinName(n string) bool {
	return strings.Contains(strings.ToLower(n), "tenjin")
}

var jsKeywords = map[string]bool{"type": true, "default": true, "from": true, "as": true, "this": true, "self": true, "return": true, "new": true}

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

func isJS(ext string) bool {
	return ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx"
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

func contains(values []string, v string) bool {
	for _, candidate := range values {
		if candidate == v {
			return true
		}
	}
	return false
}
