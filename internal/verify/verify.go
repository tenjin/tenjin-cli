// Package verify checks a Tenjin SDK integration: what is on disk, and
// whether Tenjin accepts the SDK key for the app. It never changes the
// project.
package verify

import (
	"context"
	"strconv"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/keyfill"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/track"
)

// Severity ranks a finding.
type Severity string

const (
	// Error means events will not reach Tenjin, or will be wrong.
	Error Severity = "error"
	// Warning means attribution quality suffers, or a store may object.
	Warning Severity = "warning"
	// Info is something to know that needs no action.
	Info Severity = "info"
	// Pass is a check that came out right.
	Pass Severity = "pass"
)

// Finding is the result of one check. IDs are stable so scripts and agents
// can match on them.
type Finding struct {
	ID       string           `json:"id"`
	Severity Severity         `json:"severity"`
	Platform project.Platform `json:"platform,omitempty"`
	// Build is the ID of the build the finding is about, when the target
	// ships more than one (see project.Build).
	Build  string `json:"build,omitempty"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// Summary counts findings by severity.
type Summary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Passed   int `json:"passed"`
}

// Report is everything a doctor run found.
type Report struct {
	Project  *project.Project `json:"project"`
	Findings []Finding        `json:"findings"`
	Summary  Summary          `json:"summary"`
	// latest is Options.Latest, for the guide built from the report.
	latest map[string]string
}

// KeyChecker asks Tenjin whether an SDK key is valid for an app.
type KeyChecker interface {
	CheckKey(ctx context.Context, key, bundleID, platform string) (track.KeyCheck, error)
}

// Options controls a run.
type Options struct {
	// Given holds SDK keys supplied from outside the project. They are
	// needed when the project reads its key from build configuration instead
	// of a string literal, and before the key has been written at all.
	Given GivenKeys
	// Keys checks SDK keys against Tenjin. Nil skips the check.
	Keys KeyChecker
	// Device adds what a device's log says about the running app. Nil
	// leaves the device out.
	Device *DeviceInput
	// Features are the IDs of the features chosen for the integration (see
	// project.Feature). A chosen feature that is not sent is a warning; nil
	// means no choice was made, and unsent features are only suggested.
	Features []string
	// Events are the custom events chosen, by name: each must be sent to
	// Tenjin, or the feature is open. UserID is the customer user ID
	// chosen, as code or in words; as code, it must be what is passed.
	Events []string
	UserID string
	// UserIDFromCode says UserID was read from the app's own code, as what
	// it gives another SDK: code, even with spaces in it (user?.uid ?? "").
	// Otherwise one with spaces may be words, which no code can be compared
	// with.
	UserIDFromCode bool
	// Latest holds newer SDK releases found in the registries, keyed by
	// package prefix (see sdkrelease). One counts only within the major
	// version this CLI knows: a new major can change what the checks look
	// for. Nil keeps to the versions the CLI was built with.
	Latest map[string]string
}

// GivenKey is an SDK key supplied from outside the project.
type GivenKey struct {
	Value string
	// Source says where it came from, such as a flag or an environment
	// variable, for the report to say so. Empty leaves it unsaid.
	Source string
}

// GivenKeys are the SDK keys supplied from outside the project. Each build is
// its own Tenjin app with its own key, so they are kept by build ID ("ios",
// "android", "android:amazon"); All serves a project with one build, or is
// an organization-wide key.
type GivenKeys struct {
	All     GivenKey
	ByBuild map[string]GivenKey
	// PreferSource makes a key written in the project win over a given one.
	// The check then covers what the app will really send, and the given key
	// only fills in where the source has none.
	PreferSource bool
}

// For returns the key given for a build, if there is one. A key given for
// the platform's main build never stands in for another build's: using one
// app's key in another app's build is the mistake to catch.
func (g GivenKeys) For(build string) (GivenKey, bool) {
	if k, ok := g.ByBuild[build]; ok && k.Value != "" {
		return k, true
	}
	return g.All, g.All.Value != ""
}

// Run checks the project and returns what it found. Frameworks that are not
// checked yet produce a single informational finding saying so.
func Run(ctx context.Context, p *project.Project, opts Options) *Report {
	r := &runner{ctx: ctx, p: p, opts: opts, report: &Report{Project: p, Findings: []Finding{}, latest: opts.Latest}}
	r.run()
	r.dropUncheckedBesidePlaceholders()
	for _, f := range r.report.Findings {
		switch f.Severity {
		case Error:
			r.report.Summary.Errors++
		case Warning:
			r.report.Summary.Warnings++
		case Pass:
			r.report.Summary.Passed++
		}
	}
	return r.report
}

type runner struct {
	ctx    context.Context
	p      *project.Project
	opts   Options
	report *Report
	// configured caches the keys found in build configuration, and code the
	// source files that mention Tenjin.
	configured *[]keyLiteral
	code       *tenjinCode
	// flavors caches the flavor blocks of build files, and placeholders
	// the key placeholders in the project.
	flavors      map[string][]project.FlavorBlock
	placeholders *[]keyfill.Spot
	// autoTrackingChecked is set once automatic purchase tracking is looked
	// at, which purchases and subscriptions share.
	autoTrackingChecked bool
}

// dropUncheckedBesidePlaceholders leaves out "the key was not checked"
// where a finding already says the key is a placeholder, or has spaces in
// it: there is no key to check, and that finding says what to do. Only for
// the builds that finding is about, and never where a real key was found:
// another build's key must stay in the report.
func (r *runner) dropUncheckedBesidePlaceholders() {
	var placeholders []Finding
	for _, f := range r.report.Findings {
		if f.ID == "sdk.key.placeholder" || f.ID == "sdk.key.unfilled" || f.ID == "sdk.key.whitespace" {
			placeholders = append(placeholders, f)
		}
	}
	about := func(u Finding) bool {
		for _, p := range placeholders {
			if (p.Platform == "" || p.Platform == u.Platform) && (p.Build == "" || p.Build == u.Build) {
				return true
			}
		}
		return false
	}
	kept := r.report.Findings[:0]
	for _, f := range r.report.Findings {
		if f.ID == "key.unchecked" && f.File == "" && about(f) {
			continue
		}
		kept = append(kept, f)
	}
	r.report.Findings = kept
}

func (r *runner) add(f Finding) {
	r.report.Findings = append(r.report.Findings, f)
}

func (r *runner) pass(id string, platform project.Platform, title, file string, line int) {
	r.add(Finding{ID: id, Severity: Pass, Platform: platform, Title: title, File: file, Line: line})
}

func (r *runner) run() {
	r.checkUnfilledKeys()
	if d := r.opts.Device; d != nil && r.p.Target(d.Platform) == nil {
		r.add(Finding{
			ID: "device.unsupported", Severity: Info,
			Title: "The device log is from " + platformName(d.Platform) + ", and this project does not build for " + platformName(d.Platform),
		})
	}
	if r.p.Library != nil {
		r.checkLibrary(r.p.Library)
		return
	}
	framework := r.p.Framework
	if wrapper, ok := wrapperSpecs[framework]; ok && !r.declares(wrapper) && r.declaresNative() {
		// The native SDKs added straight to the native projects, with no
		// framework package: checked as the native apps they are.
		r.add(Finding{
			ID: "sdk.native_in_wrapper", Severity: Info,
			Title:  "The Tenjin SDK is added to the native projects directly, not through " + wrapper.packages[0] + ", so they are checked as native apps",
			Detail: "That works; the framework's own code cannot call the SDK then, only the native code can.",
		})
		framework = project.Native
	}
	switch framework {
	case project.Native:
		// Each platform has its own SDK, its own source and its own key.
		for i := range r.p.Targets {
			t := &r.p.Targets[i]
			spec := nativeSpecs[t.Platform]
			r.checkDependency(spec, t.Platform)
			// The code of a local library the app gets the SDK through is
			// the app's too.
			src := r.scanSource(spec, append(spec.sourceDirs(t), r.p.LibraryCode...))
			r.checkCalls(spec, src, t.Platform)
			r.checkPlatform(spec, t, src)
			r.checkKey(t, src)
		}
	case project.Flutter, project.ReactNative, project.Capacitor:
		// One shared codebase drives both platforms.
		spec := wrapperSpecs[r.p.Framework]
		r.checkDependency(spec, "")
		src := r.scanSource(spec, append(spec.sourceDirs(nil), r.p.LibraryCode...))
		r.checkCalls(spec, src, "")
		native := false
		for i := range r.p.Targets {
			t := &r.p.Targets[i]
			native = native || t.Android != nil || t.IOS != nil
			r.checkPlatform(spec, t, src)
			r.checkNativeSync(spec, t)
			r.checkKey(t, src)
		}
		// An Expo project that generates its native folders at build time
		// has none to check here.
		if !native {
			fix := map[project.Framework]string{
				project.ReactNative: "Run npx expo prebuild, then run this again to check them.",
				project.Capacitor:   "Run npx cap add android and npx cap add ios, then run this again to check them.",
				project.Flutter:     "Run flutter create . to add them, then run this again to check them.",
			}[r.p.Framework]
			r.add(Finding{
				ID: "native.unchecked", Severity: Info,
				Title:  "There are no android/ or ios/ folders, so the native settings were not checked",
				Detail: "Permissions, Info.plist keys and the native SDK's requirements live there. An Expo project that generates them at build time has none in the repository.",
				Fix:    fix,
			})
		}
	case project.Unity:
		r.checkUnity()
	default:
		r.add(Finding{
			ID:       "framework.unchecked",
			Severity: Info,
			Title:    "Integration checks for " + frameworkTitle(r.p.Framework) + " projects are not available yet",
			Detail:   "The project was detected, but this version only checks native Android, native iOS, Flutter, React Native, Capacitor and Unity projects.",
		})
		return
	}
	r.checkFeatures()
}

func (r *runner) checkPlatform(spec frameworkSpec, t *project.Target, src source) {
	switch t.Platform {
	case project.Android:
		r.checkAndroid(spec, t, src)
		r.checkDevice(t, src)
	case project.IOS:
		r.checkIOS(spec, t, src)
		r.checkDevice(t, src)
	}
}

// checkDependency reports whether the SDK package is declared. For native
// projects platform selects which SDK to look for; wrappers pass "".
// declares reports whether the project declares this spec's package. The
// community Flutter plugin counts as Flutter's: it is checked as one, and
// named.
func (r *runner) declares(spec frameworkSpec) bool {
	for _, i := range r.p.Tenjin {
		if spec.ownsPackage(i.Package) || (i.Package == project.CommunityFlutterPackage && spec.ownsPackage("tenjin_plugin")) {
			return true
		}
	}
	return false
}

// declaresNative reports whether the project's native projects declare a
// native SDK themselves. One a framework plugin brings (Flutter and React
// Native plugins are pods) does not count: it is the plugin's.
func (r *runner) declaresNative() bool {
	for _, spec := range nativeSpecs {
		for _, i := range r.p.Tenjin {
			if spec.ownsPackage(i.Package) && i.Through == "" {
				return true
			}
		}
	}
	return false
}

func (r *runner) checkDependency(spec frameworkSpec, platform project.Platform) {
	for _, i := range r.p.Tenjin {
		if i.Package == project.CommunityFlutterPackage {
			r.add(Finding{
				ID: "sdk.dependency", Severity: Warning, Platform: platform,
				Title:  "The project uses tenjin_sdk, a community plugin, not Tenjin's own tenjin_plugin",
				Detail: "Tenjin does not publish or support tenjin_sdk, and these checks follow tenjin_plugin's API.",
				File:   i.Source,
				Fix:    "Replace tenjin_sdk with tenjin_plugin in pubspec.yaml (flutter pub add tenjin_plugin), and change the import to package:tenjin_plugin/tenjin_sdk.dart.",
			})
			return
		}
	}
	for _, i := range r.p.Tenjin {
		if !spec.ownsPackage(i.Package) {
			continue
		}
		title := "Tenjin SDK is declared: " + i.Package
		if i.Version != "" {
			title += " " + i.Version
		}
		r.pass("sdk.dependency", platform, title, i.Source, 0)
		r.checkVersion(i, platform)
		return
	}
	r.add(Finding{
		ID:       "sdk.dependency",
		Severity: Error,
		Platform: platform,
		Title:    "The Tenjin SDK is not a dependency of this project",
		Fix:      installFix(spec, r.opts.Latest),
	})
}

// checkUnfilledKeys reports the key placeholders a coding agent left in the
// project, which the CLI fills with the developer's keys, one per build.
func (r *runner) checkUnfilledKeys() {
	reported := map[string]bool{}
	for _, spot := range r.spots() {
		// Each place the CLI cannot write to is the developer's to fill.
		seen := spot.Placeholder
		if spot.Blocked != "" {
			seen += "|" + spot.File
		}
		// A build left out with --build is not this run's to check.
		if reported[seen] || r.p.IsOmitted(spot.Placeholder) {
			continue
		}
		reported[seen] = true
		f := Finding{
			ID: "sdk.key.unfilled", Severity: Error, Platform: spot.Platform,
			Title:  "The " + platformName(spot.Platform) + " SDK key is still the placeholder " + spot.Placeholder,
			Detail: "A coding agent leaves this placeholder so that it never sees the key. Until the key is written in its place, Tenjin refuses the app's events.",
			File:   spot.File, Line: spot.Line,
			Fix: "Run tenjin setup, which asks for the key and writes it in its place. In a script, tenjin project set-keys --sdk-key " + string(spot.Platform) + "=<key> does the same.",
		}
		t, b := r.buildFor(spot.Placeholder)
		unread := b == nil && spot.Placeholder == keyfill.Placeholder(spot.Platform) && r.buildsUnread(spot.Platform)
		switch {
		case unread:
			// A platform's main placeholder, in a project whose builds
			// could not be read: still the platform's key.
		case b == nil:
			f.Title = "The placeholder " + spot.Placeholder + " names no build of this project"
			f.Detail = "Each build that is its own app in Tenjin has its own placeholder; tenjin project detect lists them. This one matches none, so no key can be written for it."
			f.Fix = "Replace it with the placeholder of the build it is for."
			if list := r.placeholdersOf(spot.Platform); list != "" {
				f.Fix = "Replace it with the placeholder of the build it is for: " + list + "."
			}
		case t.SeveralBuilds():
			f.Build = b.ID
			f.Title = "The SDK key of " + buildLabel(*b) + " is still the placeholder " + spot.Placeholder
			f.Fix = "Run tenjin setup, which asks for the key and writes it in its place. In a script, tenjin project set-keys --sdk-key " + b.ID + "=<key> does the same."
		}
		if spot.Blocked != "" && (b != nil || unread) {
			f.Detail = "The Tenjin CLI cannot write the key there: " + spot.Blocked + ". Until the key is written in its place, Tenjin refuses the app's events."
			f.Fix = "Write the key from the app's page in the Tenjin dashboard over the placeholder at " + spot.File + ":" + strconv.Itoa(spot.Line) + " yourself."
			if strings.HasPrefix(spot.Blocked, "Unity") {
				f.Fix += " Or set Edit > Project Settings > Editor > Asset Serialization to Force Text, and run tenjin setup again."
			} else {
				f.Fix += " Or put the file itself in the project in place of the link, and run tenjin setup again."
			}
		}
		r.add(f)
	}
}

// buildsUnread reports whether the platform's builds could not be read,
// where its main placeholder still stands for its one key.
func (r *runner) buildsUnread(platform project.Platform) bool {
	t := r.p.Target(platform)
	return t == nil || len(t.Builds) == 0
}

// placeholdersOf lists the platform's builds' placeholders, for a fix.
func (r *runner) placeholdersOf(platform project.Platform) string {
	t := r.p.Target(platform)
	if t == nil {
		return ""
	}
	var list []string
	for _, b := range t.Builds {
		list = append(list, b.SDKKeyPlaceholder+" ("+b.ID+")")
	}
	return strings.Join(list, ", ")
}

// buildFor returns the build whose placeholder this is.
func (r *runner) buildFor(placeholder string) (*project.Target, *project.Build) {
	for i := range r.p.Targets {
		for j := range r.p.Targets[i].Builds {
			if r.p.Targets[i].Builds[j].SDKKeyPlaceholder == placeholder {
				return &r.p.Targets[i], &r.p.Targets[i].Builds[j]
			}
		}
	}
	return nil, nil
}

func platformName(p project.Platform) string {
	if p == project.IOS {
		return "iOS"
	}
	return "Android"
}

// frameworkTitle names a framework the checks do not cover yet, as people
// write it.
func frameworkTitle(f project.Framework) string {
	if f == project.Unreal {
		return "Unreal Engine"
	}
	return string(f)
}
