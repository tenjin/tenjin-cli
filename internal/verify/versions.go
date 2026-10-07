package verify

import (
	"strconv"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

// The newest release of each SDK when this version of the CLI was built,
// read from Maven Central, CocoaPods, pub.dev and npm on 2 October 2026.
// A fix that says "add the SDK" names these, so that neither a person nor a
// coding agent has to guess a version. Update them with each CLI release.
var latestSDK = []struct{ packagePrefix, version string }{
	{"com.tenjin:android-sdk", "1.24.0"},
	{"TenjinSDK", "1.19.1"}, // the pod and the Swift package share a version
	{"tenjin_plugin", "1.5.0"},
	{"react-native-tenjin", "1.7.0"},
	{"ionic-capacitor-tenjin", "2.2.0"},
	{"com.tenjin.sdk", "1.22.1"}, // Unity, from the GitHub releases
}

// The Play Services libraries the Android SDK reads identifiers through.
const (
	adsIdentifierVersion = "18.3.0"
	appSetVersion        = "16.1.0"
)

// LatestVersion is the newest release of a Tenjin SDK package this CLI
// knows, or "" for a package it does not.
func LatestVersion(pkg string) string { return latestVersion(pkg) }

func latestVersion(pkg string) string {
	for _, sdk := range latestSDK {
		if strings.HasPrefix(pkg, sdk.packagePrefix) {
			return sdk.version
		}
	}
	return ""
}

// checkVersion warns when the declared SDK is older than the newest release
// this CLI knows. It stays silent when it cannot tell: an unreadable version,
// or a range that already admits the newest release.
func (r *runner) checkVersion(i project.Integration, platform project.Platform) {
	latest := latestVersion(i.Package)
	if latest == "" || !olderThan(i.Version, latest) {
		return
	}
	r.add(Finding{
		ID: "sdk.version", Severity: Warning, Platform: platform,
		Title:  "The Tenjin SDK is at " + i.Version + "; the current release is " + latest,
		Detail: "Later releases carry fixes for attribution and for store policy changes. " + latest + " was the newest when this version of the Tenjin CLI was built.",
		File:   i.Source,
		Fix:    "Update " + i.Package + " to " + latest + ".",
	})
}

// olderThan reports whether a declared version is certainly older than
// latest. declared may be exact ("1.16.5", "v1.16.5") or a caret or tilde
// range ("^1.5.0", "~1.5.0"); anything else is not judged.
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
	have, ok := parseVersion(declared)
	if !ok {
		return false
	}
	for n := 0; n < pinned; n++ {
		if have[n] != want[n] {
			return have[n] < want[n]
		}
	}
	return false
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
