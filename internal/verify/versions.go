package verify

import (
	"strconv"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

// The newest release of each SDK when this version of the CLI was built,
// read from Maven Central, CocoaPods, pub.dev, npm and GitHub on 6 October
// 2026. A fix that says "add the SDK" names these, so that neither a person
// nor a coding agent has to guess a version. They are also what doctor falls
// back to offline; online it looks for newer releases of the same major
// version (Options.Latest).
var latestSDK = []struct{ packagePrefix, version string }{
	{"com.tenjin:android-sdk", "2.0.0"},
	{"TenjinSDK", "1.20.0"}, // the pod and the Swift package share a version
	{"tenjin_plugin", "1.5.0"},
	{"react-native-tenjin", "1.7.0"},
	{"ionic-capacitor-tenjin", "2.2.0"},
	{"com.tenjin.sdk", "1.22.1"}, // Unity, from the GitHub releases
}

// initializeSince is the release of each SDK that brought the initializer
// replacing its deprecated one (frameworkSpec.deprecatedInit): an older
// one has to be updated before the replacement compiles.
var initializeSince = map[string]string{
	"TenjinSDK":     "1.12.0", // initialize: and initialize(_:)
	"tenjin_plugin": "1.3.0",  // initialize(sdkKey:)
}

// The Play Services libraries the Android SDK reads identifiers through,
// and the Play Billing library its automatic purchase tracking reads
// purchases through: the version the SDK's own test apps use (it is built
// against 7.1.1).
const (
	adsIdentifierVersion = "18.3.0"
	appSetVersion        = "16.1.0"
	playBillingVersion   = "9.0.0"
)

// LatestVersion is the newest release of a Tenjin SDK package, as the
// checks given live (Options.Latest) take it: the one this CLI was built
// with, or a newer one of the same major version from live. "" for a
// package it does not know.
func LatestVersion(pkg string, live map[string]string) string { return latestVersion(pkg, live) }

// latestVersion is the newest release of a package: the one this CLI was
// built with, or a newer one of the same major version from live.
func latestVersion(pkg string, live map[string]string) string {
	for _, sdk := range latestSDK {
		if !strings.HasPrefix(pkg, sdk.packagePrefix) {
			continue
		}
		known, _ := parseVersion(sdk.version)
		if found, ok := parseVersion(live[sdk.packagePrefix]); ok && found[0] == known[0] && olderThan(sdk.version, live[sdk.packagePrefix]) {
			return live[sdk.packagePrefix]
		}
		return sdk.version
	}
	return ""
}

// sdkVersionToken stands for the newest release in a framework's installFix.
const sdkVersionToken = "{sdk_version}"

// installFix is how to add a framework's SDK, at its newest release.
func installFix(spec frameworkSpec, live map[string]string) string {
	if len(spec.packages) == 0 {
		return spec.installFix
	}
	return strings.ReplaceAll(spec.installFix, sdkVersionToken, latestVersion(spec.packages[0], live))
}

// checkVersion warns when the declared SDK is older than the newest release.
// It stays silent when it cannot tell: an unreadable version, or a range
// that already admits the newest release.
func (r *runner) checkVersion(i project.Integration, platform project.Platform) {
	latest := latestVersion(i.Package, r.opts.Latest)
	if latest == "" || !olderThan(i.Version, latest) {
		return
	}
	f := Finding{
		ID: "sdk.version", Severity: Warning, Platform: platform,
		Title:  "The Tenjin SDK is at " + i.Version + "; the current release is " + latest,
		Detail: "Later releases carry fixes for attribution and for store policy changes.",
		File:   i.Source,
		Fix:    "Update " + i.Package + " to " + latest + ".",
	}
	if changes, code := majorChangesBetween(i.Package, i.Version, latest); changes != "" {
		f.Detail = latest + " is a new major version. " + changes
		if code {
			f.Fix = "Update " + i.Package + " to " + latest + ", and change the app's code as said above where it uses what changed."
		}
	}
	// A form of the SDK no version number upgrades is upgraded as sdk
	// upgrade says.
	if byHand := upgradeByHand(r.p.Root, i, r.opts.Latest); byHand != "" {
		f.Fix = byHand
	}
	r.add(f)
}

// upgradeByHand is how to upgrade a form of the SDK a version number does
// not: a folder, a Git repository, a file copied into the project, or a
// package another one brings. "" for a declared version.
func upgradeByHand(root string, i project.Integration, live map[string]string) string {
	if i.Through != "" {
		return "It comes with " + i.Through + ", which decides its version: upgrade that package instead."
	}
	if reason := byHandOnly(i, live); reason != "" {
		return reason
	}
	prefix, via := packageOf(i.Package)
	if prefix == "" {
		return ""
	}
	return readDeclaration(root, i, via, prefix, latestVersion(prefix, live)).why
}

// majorChangesBetween says what the major versions after declared, up to
// latest, change for the app's code, and whether its code may need changes.
func majorChangesBetween(pkg, declared, latest string) (string, bool) {
	prefix, _ := packageOf(pkg)
	have, ok := parseVersion(baseVersion(declared))
	want, okLatest := parseVersion(latest)
	if !ok || !okLatest {
		return "", false
	}
	var texts []string
	code := false
	for m := have[0] + 1; m <= want[0]; m++ {
		if c, ok := majorChanges[prefix][m]; ok {
			texts = append(texts, c.text)
			code = code || c.code
		}
	}
	return strings.Join(texts, " "), code
}

// olderThan reports whether a declared version is certainly older than
// latest. declared may be exact ("1.16.5", "v1.16.5") or a caret or tilde
// range ("^1.5.0", "~1.5.0"); anything else is not judged. A prerelease,
// such as 1.3.0-beta.1, is older than its release.
func olderThan(declared, latest string) bool {
	declared = strings.TrimSpace(declared)
	want, ok := parseVersion(latest)
	if !ok || declared == "" {
		return false
	}
	// How many leading parts a range pins: ^1.5.0 allows any 1.x, ~1.5.0 any
	// 1.5.x. A range whose pinned parts match the newest release may already
	// resolve to it.
	pinned := 3
	switch declared[0] {
	case '^':
		pinned, declared = 1, declared[1:]
	case '~':
		pinned, declared = 2, declared[1:]
	case 'v', '=':
		declared = declared[1:]
	}
	// A space may follow the operator, as in a CocoaPods requirement.
	declared = strings.TrimSpace(declared)
	declared, _, _ = strings.Cut(declared, "+")
	declared, pre, prerelease := strings.Cut(declared, "-")
	have, ok := parseVersion(declared)
	if !ok || prerelease && pre == "" {
		return false
	}
	for n := 0; n < pinned; n++ {
		if have[n] != want[n] {
			return have[n] < want[n]
		}
	}
	return prerelease && pinned == 3
}

// atLeast reports whether an exact declared version is min or later. A range
// or an unreadable version is not judged.
func atLeast(declared, min string) bool {
	have, ok := parseVersion(strings.TrimPrefix(strings.TrimSpace(declared), "v"))
	want, okMin := parseVersion(min)
	if !ok || !okMin {
		return false
	}
	for n := range have {
		if have[n] != want[n] {
			return have[n] > want[n]
		}
	}
	return true
}

// parseVersion reads "1.24.0" or "1.24" into three numbers.
func parseVersion(s string) (parts [3]int, ok bool) {
	fields := strings.Split(s, ".")
	if len(fields) < 2 || len(fields) > 3 {
		return parts, false
	}
	for n, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil || value < 0 {
			return parts, false
		}
		parts[n] = value
	}
	return parts, true
}
