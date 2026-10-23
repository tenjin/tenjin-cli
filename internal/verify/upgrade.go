package verify

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

// Upgrade is what moving one declared Tenjin SDK to its newest release takes.
type Upgrade struct {
	Package  string           `json:"package"`
	Platform project.Platform `json:"platform,omitempty"`
	// File is where the SDK is declared, and Declared the version or range
	// written there, when it can be read.
	File     string `json:"file"`
	Declared string `json:"declared,omitempty"`
	// Current is the version in use, from the lock file where there is one,
	// and Target the newest release.
	Current string `json:"current"`
	Target  string `json:"target"`
	// Major is true when Target is a new major version. Changes says what
	// that version changes for the app's code, and Code whether the app's
	// code may need changes for it.
	Major   bool   `json:"major"`
	Changes string `json:"changes,omitempty"`
	Code    bool   `json:"code_changes"`
	// Edit is true when the declaration must change to allow Target.
	Edit bool `json:"edit"`
	// Then is what the developer runs afterwards to install it.
	Then     string `json:"then"`
	Releases string `json:"release_notes"`
}

// Agent reports whether the upgrade needs changes to the project's files:
// the declaration, or the app's code for a new major version.
func (u Upgrade) Agent() bool { return u.Edit || u.Code }

// UpgradeNote is a declared SDK whose upgrade the CLI cannot plan, and why.
type UpgradeNote struct {
	Package string `json:"package"`
	File    string `json:"file"`
	Reason  string `json:"reason"`
}

// UpgradePlan is what `tenjin sdk upgrade` reads: the SDKs to upgrade, those
// already at their newest release, and those it cannot judge.
type UpgradePlan struct {
	Upgrades  []Upgrade     `json:"upgrades"`
	Current   []Upgrade     `json:"current"`
	Unchecked []UpgradeNote `json:"unchecked"`
}

// majorChanges says, for each SDK and major version, what that version
// changes for an app's code, from its release notes. A major version in
// latestSDK needs an entry here (a test checks): it is a CLI change.
var majorChanges = map[string]map[int]struct {
	text string
	code bool
}{
	"com.tenjin:android-sdk": {
		2: {"Version 2 ships minified. The documented API (TenjinSDK, its callbacks, Transaction, AdvertiserInfo, UserProfileData, AppStoreType) is unchanged, but classes that are public in Java and internal to the SDK, such as TenjinConsts, UserProfileManager and those in com.tenjin.android.utils.adnetwork, may have lost members the SDK does not use. Code that uses them must move to the documented API. The SDK now brings its own ProGuard rules, so the app's Tenjin keep rules can stay.", true},
	},
	"ionic-capacitor-tenjin": {
		2: {"Version 2 adds support for Capacitor 8 and still works with earlier Capacitor versions. The plugin's API is unchanged, so the app's code needs no change.", false},
	},
}

// releaseNotes are where each SDK publishes what each release changes.
var releaseNotes = map[string]string{
	"com.tenjin:android-sdk": "https://github.com/tenjin/tenjin-android-sdk/releases",
	"TenjinSDK":              "https://github.com/tenjin/tenjin-ios-sdk/releases",
	"tenjin_plugin":          "https://github.com/tenjin/flutter-sdk/releases",
	"react-native-tenjin":    "https://github.com/tenjin/tenjin-react-native-sdk/releases",
	"ionic-capacitor-tenjin": "https://github.com/tenjin/tenjin-ionic-sdk/releases",
	"com.tenjin.sdk":         "https://github.com/tenjin/tenjin-unity-sdk/releases",
}

// How a package is declared, which decides how its range is read and how
// it is installed afterwards.
const (
	viaGradle = "gradle"
	viaPods   = "pods"
	viaSPM    = "spm"
	viaPub    = "pub"
	viaNPM    = "npm"
	viaUnity  = "unity"
)

var (
	podfileTenjin = regexp.MustCompile(`(?m)^\s*pod\s+['"]TenjinSDK['"]\s*(?:,\s*['"]([^'"]+)['"])?`)
	pubspecRange  = regexp.MustCompile(`(?m)^\s+tenjin_plugin\s*:[ \t]*['"]?([^'"\s#]*)`)
)

// Upgrades plans moving each Tenjin SDK the project declares to its newest
// release: the one this CLI was built with, or a newer one of the same
// major version in live.
func Upgrades(p *project.Project, live map[string]string) UpgradePlan {
	plan := UpgradePlan{Upgrades: []Upgrade{}, Current: []Upgrade{}, Unchecked: []UpgradeNote{}}
	declared := p.Tenjin
	if p.Library != nil {
		declared = []project.Integration{p.Library.Tenjin}
	}
	for _, i := range declared {
		if i.Through != "" {
			plan.Unchecked = append(plan.Unchecked, UpgradeNote{i.Package, i.Source, "It comes with " + i.Through + ", which decides its version: upgrade that package instead."})
			continue
		}
		prefix, via := packageOf(i.Package)
		if prefix == "" {
			continue
		}
		target := latestVersion(prefix, live)
		if target == "" {
			continue
		}
		u := Upgrade{Package: prefix, Platform: platformOf(prefix), File: i.Source, Target: target, Releases: releaseNotes[prefix]}
		u.Declared, u.Current = declaration(p.Root, i, via)
		if _, ok := parseVersion(u.Current); !ok {
			reason := "Its version could not be read, so the upgrade was not planned."
			if strings.Contains(i.Package, "linked manually") {
				reason = "It is linked into the Xcode project by hand: download the new release from " + releaseNotes[prefix] + " and replace it."
			}
			plan.Unchecked = append(plan.Unchecked, UpgradeNote{i.Package, i.Source, reason})
			continue
		}
		if !olderThan(u.Current, target) {
			plan.Current = append(plan.Current, u)
			continue
		}
		have, _ := parseVersion(u.Current)
		want, _ := parseVersion(target)
		u.Major = want[0] > have[0]
		u.Changes, u.Code = majorChangesBetween(prefix, u.Current, target)
		// Xcode adds a package as "Up to Next Major Version", which the
		// project file records out of reach of this plan.
		u.Edit = !allows(via, u.Declared, target)
		if via == viaSPM {
			u.Edit = u.Major
		}
		u.Then = installCommand(p.Root, via, prefix, i.Source, u.Edit)
		plan.Upgrades = append(plan.Upgrades, u)
	}
	return plan
}

// packageOf is a declared package's name in latestSDK, and how it is
// declared; "" for one this plan does not know.
func packageOf(pkg string) (string, string) {
	switch {
	case strings.HasPrefix(pkg, "com.tenjin:android-sdk"):
		return "com.tenjin:android-sdk", viaGradle
	case pkg == "TenjinSDK (Swift Package)":
		return "TenjinSDK", viaSPM
	case pkg == "TenjinSDK", pkg == "TenjinSDK (CocoaPods)", strings.Contains(pkg, "linked manually"):
		return "TenjinSDK", viaPods
	case pkg == "tenjin_plugin":
		return "tenjin_plugin", viaPub
	case pkg == "react-native-tenjin", pkg == "ionic-capacitor-tenjin":
		return pkg, viaNPM
	case pkg == "com.tenjin.sdk":
		return pkg, viaUnity
	}
	return "", ""
}

func platformOf(prefix string) project.Platform {
	switch prefix {
	case "com.tenjin:android-sdk":
		return project.Android
	case "TenjinSDK":
		return project.IOS
	}
	return ""
}

// declaration reads what the declaration says and which version is in use.
func declaration(root string, i project.Integration, via string) (declared, current string) {
	read := func(rel string) string { return readProjectFile(root, rel) }
	switch via {
	case viaPods:
		if strings.HasSuffix(i.Source, ".podspec") {
			return i.Version, baseVersion(i.Version)
		}
		if m := podfileTenjin.FindStringSubmatch(project.StripRubyComments(read(i.Source))); m != nil {
			declared = m[1]
		}
		return declared, i.Version
	case viaPub:
		if m := pubspecRange.FindStringSubmatch(read(i.Source)); m != nil {
			declared = m[1]
		}
		return declared, i.Version
	case viaNPM:
		// The installed copy says what is in use; the range in package.json
		// is all there is before an install.
		var installed struct {
			Version string `json:"version"`
		}
		pkg, _ := packageOf(i.Package)
		if json.Unmarshal([]byte(read(path.Join(path.Dir(i.Source), "node_modules", pkg, "package.json"))), &installed) == nil && installed.Version != "" {
			return i.Version, installed.Version
		}
		return i.Version, baseVersion(i.Version)
	case viaSPM:
		return "", i.Version
	}
	return i.Version, i.Version
}

// readProjectFile reads a file of the project, or "" when it cannot. A path
// from the project's own files cannot lead the read out of it.
func readProjectFile(root, rel string) string {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return ""
	}
	defer func() { _ = dir.Close() }()
	raw, err := dir.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		return ""
	}
	return string(raw)
}

// baseVersion is the version a range starts at: 1.7.0 for ^1.7.0 or ~> 1.7.0.
func baseVersion(declared string) string {
	v := strings.TrimSpace(declared)
	v = strings.TrimLeft(v, "^~>=v ")
	if i := strings.IndexAny(v, " ,<"); i >= 0 {
		v = v[:i]
	}
	return v
}

// allows reports whether a declaration already admits version target, so
// that the package manager installs it with no change to the file. One it
// cannot read does not.
func allows(via, declared, target string) bool {
	want, ok := parseVersion(target)
	if !ok {
		return false
	}
	d := strings.TrimSpace(declared)
	// An exact version allows only itself.
	exact := func() bool {
		have, ok := parseVersion(strings.TrimLeft(d, "=v "))
		return ok && have == want
	}
	switch via {
	case viaGradle, viaUnity:
		return exact()
	case viaPods:
		if d == "" {
			return true
		}
		if strings.HasPrefix(d, "~>") {
			base := strings.TrimSpace(strings.TrimPrefix(d, "~>"))
			return pessimistic(base, want)
		}
		if strings.HasPrefix(d, ">=") && !strings.ContainsAny(d, "<,") {
			return !olderThan(target, strings.TrimSpace(strings.TrimPrefix(d, ">=")))
		}
		return exact()
	case viaPub, viaNPM:
		switch {
		case d == "any", d == "*", d == "latest":
			return true
		case strings.HasPrefix(d, "^"):
			have, ok := parseVersion(d[1:])
			if !ok || have[0] != want[0] || (have[0] == 0 && have[1] != want[1]) {
				return false
			}
			return !olderThan(target, d[1:])
		case strings.HasPrefix(d, "~") && !strings.HasPrefix(d, "~>"):
			have, ok := parseVersion(d[1:])
			return ok && have[0] == want[0] && have[1] == want[1] && have[2] <= want[2]
		case strings.HasPrefix(d, ">=") && !strings.ContainsAny(d, "< "):
			return !olderThan(target, strings.TrimPrefix(d, ">="))
		}
		return exact()
	}
	return false
}

// pessimistic is CocoaPods' ~>: "~> 1.19" allows any 1.x from 1.19, and
// "~> 1.19.1" any 1.19.x from 1.19.1.
func pessimistic(base string, want [3]int) bool {
	have, ok := parseVersion(base)
	if !ok {
		return false
	}
	if strings.Count(base, ".") == 1 {
		return have[0] == want[0] && have[1] <= want[1]
	}
	return have[0] == want[0] && have[1] == want[1] && have[2] <= want[2]
}

// installCommand is what installs the new version once the declaration
// allows it. edited says the declaration was changed: npm and its kin then
// install what it says; otherwise they update within the range.
func installCommand(root, via, pkg, source string, edited bool) string {
	switch via {
	case viaGradle:
		return "Sync the project with its Gradle files (in Android Studio, File > Sync Project with Gradle Files), then build it."
	case viaPods:
		if dir := path.Dir(source); dir != "." {
			return "In " + dir + ", run pod update TenjinSDK."
		}
		return "Run pod update TenjinSDK."
	case viaSPM:
		return "In Xcode, choose File > Packages > Update to Latest Package Versions."
	case viaPub:
		return "Run flutter pub upgrade tenjin_plugin."
	case viaNPM:
		manager := "npm"
		for _, lock := range []struct{ file, manager string }{{"yarn.lock", "yarn"}, {"pnpm-lock.yaml", "pnpm"}, {"bun.lockb", "bun"}, {"bun.lock", "bun"}} {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path.Join(path.Dir(source), lock.file)))); err == nil {
				manager = lock.manager
				break
			}
		}
		run := manager + " install"
		if !edited {
			run = map[string]string{"npm": "npm update ", "yarn": "yarn upgrade ", "pnpm": "pnpm update ", "bun": "bun update "}[manager] + pkg
		}
		if pkg == "ionic-capacitor-tenjin" {
			return "Run " + run + ", then npx cap sync."
		}
		return "Run " + run + ", then pod install in ios/."
	case viaUnity:
		return "Go back to the Unity editor: its Package Manager fetches the new version."
	}
	return ""
}
