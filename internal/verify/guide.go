package verify

import (
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

// GuidePart is what one platform, or the shared code of a cross-platform
// project, needs for the SDK to be integrated by hand.
type GuidePart struct {
	// Platform is empty for the shared code of a cross-platform project.
	Platform project.Platform
	// File is where the SDK is started, when detection found it.
	File  string
	Steps []string
}

// Guide returns the by-hand integration steps a project still needs: the
// instructions the checks give as fixes, in the order to follow them, without
// the ones the report shows are already done. It is empty for a framework
// the checks do not cover, and when nothing of the kind is left.
func Guide(report *Report) []GuidePart {
	p := report.Project
	done := func(id string, platform project.Platform) bool {
		for _, f := range report.Findings {
			if f.ID == id && f.Platform == platform {
				return f.Severity == Pass
			}
		}
		return false
	}
	// missing reports whether the report gives exactly this fix, which only
	// a call not found at all is given.
	missing := func(id string, platform project.Platform, fix string) bool {
		for _, f := range report.Findings {
			if f.ID == id && f.Platform == platform && f.Fix == fix {
				return true
			}
		}
		return false
	}
	steps := func(spec frameworkSpec, platform project.Platform) []string {
		var out []string
		if !done("sdk.dependency", platform) {
			out = append(out, installFix(spec, report.latest))
		}
		if !done("sdk.init", platform) {
			out = append(out, spec.initFix)
		}
		// Where initialising and connecting are one statement, the second
		// instruction would only repeat the first.
		if !done("sdk.connect", platform) && !strings.Contains(spec.initFix, ".connect()") {
			out = append(out, spec.connectFix)
		}
		if spec.debugLogsFix != "" && !done("ios.debug_logs", platform) {
			out = append(out, spec.debugLogsFix)
		}
		return out
	}
	var parts []GuidePart
	add := func(part GuidePart) {
		if len(part.Steps) > 0 {
			parts = append(parts, part)
		}
	}
	switch p.Framework {
	case project.Native:
		for _, t := range p.Targets {
			part := GuidePart{Platform: t.Platform, Steps: steps(nativeSpecs[t.Platform], t.Platform)}
			switch {
			case t.Android != nil:
				part.File = t.Android.LauncherSource
			case t.IOS != nil:
				part.File = t.IOS.EntryPoint
			}
			add(part)
		}
	case project.Flutter, project.ReactNative, project.Capacitor:
		add(GuidePart{Steps: steps(wrapperSpecs[p.Framework], "")})
		// The iOS SDK's logs are turned on in the native code, where the
		// framework's code cannot reach.
		if t := p.Target(project.IOS); t != nil {
			if fix := wrapperDebugLogsFix(p, t); fix != "" && missing("ios.debug_logs", project.IOS, fix) {
				add(GuidePart{Platform: project.IOS, File: t.IOS.EntryPoint, Steps: []string{fix}})
			}
		}
	case project.Unity:
		add(GuidePart{Steps: steps(unitySpec, "")})
	}
	return parts
}
