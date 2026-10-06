// Package verify checks a Tenjin SDK integration: what is on disk, and
// whether Tenjin accepts the SDK key for the app. It never changes the
// project.
package verify

import (
	"context"

	"github.com/tenjin/tenjin-cli/internal/track"
	"github.com/tenjin/tenjin-cli/internal/keyfill"
	"github.com/tenjin/tenjin-cli/internal/project"
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
	r := &runner{ctx: ctx, p: p, opts: opts, report: &Report{Project: p, Findings: []Finding{}}}
	r.run()
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
	switch r.p.Framework {
	case project.Native:
		// Each platform has its own SDK, its own source and its own key.
		for i := range r.p.Targets {
			t := &r.p.Targets[i]
			spec := nativeSpecs[t.Platform]
			r.checkDependency(spec, t.Platform)
			src := r.scanSource(spec, spec.sourceDirs(t))
			r.checkCalls(spec, src, t.Platform)
			r.checkPlatform(spec, t, src)
			r.checkKey(t, src)
		}
	case project.Flutter, project.ReactNative, project.Capacitor:
		// One shared codebase drives both platforms.
		spec := wrapperSpecs[r.p.Framework]
		r.checkDependency(spec, "")
		src := r.scanSource(spec, spec.sourceDirs(nil))
		r.checkCalls(spec, src, "")
		for i := range r.p.Targets {
			t := &r.p.Targets[i]
			r.checkPlatform(spec, t, src)
			r.checkKey(t, src)
		}
	case project.Unity:
		r.checkUnity()
	default:
		r.add(Finding{
			ID:       "framework.unchecked",
			Severity: Info,
			Title:    "Integration checks for " + string(r.p.Framework) + " projects are not available yet",
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
func (r *runner) checkDependency(spec frameworkSpec, platform project.Platform) {
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
		Fix:      spec.installFix,
	})
}

// checkUnfilledKeys reports the key placeholders a coding agent left in the
// project, which the CLI fills with the developer's keys, one per build.
func (r *runner) checkUnfilledKeys() {
	reported := map[string]bool{}
	for _, spot := range keyfill.Find(r.p.Root) {
		if reported[spot.Placeholder] {
			continue
		}
		reported[spot.Placeholder] = true
		f := Finding{
			ID: "sdk.key.unfilled", Severity: Error, Platform: spot.Platform,
			Title:  "The " + platformName(spot.Platform) + " SDK key is still the placeholder " + spot.Placeholder,
			Detail: "A coding agent leaves this placeholder so that it never sees the key. Until the key is written in its place, Tenjin refuses the app's events.",
			File:   spot.File, Line: spot.Line,
			Fix: "Run tenjin setup, which asks for the key and writes it in its place. In a script, tenjin project set-keys --sdk-key " + string(spot.Platform) + "=<key> does the same.",
		}
		t, b := r.buildFor(spot.Placeholder)
		switch {
		case b == nil && spot.Placeholder == keyfill.Placeholder(spot.Platform):
			// A platform's main placeholder, in a project whose bundle ID
			// could not be read: still the platform's key.
		case b == nil:
			f.Title = "The placeholder " + spot.Placeholder + " names no build of this project"
			f.Detail = "Each build that is its own app in Tenjin has its own placeholder; tenjin project detect lists them. This one matches none, so no key can be written for it."
			f.Fix = "Replace it with the placeholder of the build it is for."
		case t.SeveralBuilds():
			f.Build = b.ID
			f.Title = "The SDK key of " + buildLabel(*b) + " is still the placeholder " + spot.Placeholder
			f.Fix = "Run tenjin setup, which asks for the key and writes it in its place. In a script, tenjin project set-keys --sdk-key " + b.ID + "=<key> does the same."
		}
		r.add(f)
	}
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
