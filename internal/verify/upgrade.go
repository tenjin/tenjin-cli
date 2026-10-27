package verify

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
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

// Ahead reports whether the version in use is newer than Target, the
// newest release this CLI knows of.
func (u Upgrade) Ahead() bool {
	have, ok := readRelease(u.Current)
	want, okTarget := readRelease(u.Target)
	return ok && okTarget && have.compare(want) > 0
}

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

// declaration is what a project's declaration of the SDK says: the version
// or range as written, the version in use, and the versions it admits (nil
// when that cannot be read). why is set when the upgrade cannot be planned.
type declaration struct {
	text    string
	current string
	admits  rule
	why     string
}

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
		if reason := byHandOnly(i, live); reason != "" {
			plan.Unchecked = append(plan.Unchecked, UpgradeNote{i.Package, i.Source, reason})
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
		d := readDeclaration(p.Root, i, via, prefix, target)
		if d.why == "" {
			if _, ok := readRelease(d.current); !ok {
				d.why = unreadVersion(via, d.text)
			}
		}
		if d.why != "" {
			plan.Unchecked = append(plan.Unchecked, UpgradeNote{i.Package, i.Source, d.why})
			continue
		}
		u := Upgrade{Package: prefix, Platform: platformOf(prefix), File: i.Source, Declared: d.text, Current: d.current, Target: target, Releases: releaseNotes[prefix]}
		have, _ := readRelease(d.current)
		want, _ := readRelease(target)
		if have.compare(want) >= 0 {
			plan.Current = append(plan.Current, u)
			continue
		}
		u.Major = want.parts[0] > have.parts[0]
		u.Changes, u.Code = majorChangesBetween(prefix, release{parts: have.parts}.String(), target)
		// A declaration that cannot be read is changed: the agent, or the
		// developer, sees what it says.
		u.Edit = d.admits == nil || !d.admits.admits(want.parts)
		u.Then = installCommand(p.Root, via, prefix, i.Source, u.Edit)
		plan.Upgrades = append(plan.Upgrades, u)
	}
	return plan
}

// byHandOnly is, for a form of the SDK the plan cannot change through a
// package manager, how to upgrade it by hand; "" for any other.
func byHandOnly(i project.Integration, live map[string]string) string {
	switch i.Package {
	case "tenjin.aar", "tenjin.jar":
		return "The SDK is a file in the project, not a dependency, so nothing records its version. Download " + i.Package + " " + latestVersion("com.tenjin:android-sdk", live) + " from " + releaseNotes["com.tenjin:android-sdk"] + " and replace " + i.Source + " with it, or declare com.tenjin:android-sdk:" + latestVersion("com.tenjin:android-sdk", live) + " from Maven Central instead and delete the file."
	case "Tenjin (.unitypackage)":
		return "It was imported from a .unitypackage, which keeps no version. Download the .unitypackage of " + latestVersion("com.tenjin.sdk", live) + " from " + releaseNotes["com.tenjin.sdk"] + " and import it over " + i.Source + "."
	case "TenjinSDK (Unreal plugin)":
		return "The Unreal plugin is copied into the project. Download its newest release from https://github.com/tenjin/tenjin-unreal-sdk/releases and replace the plugin's folder, " + path.Dir(i.Source) + ", with it."
	case project.CommunityFlutterPackage:
		return "tenjin_sdk is a community plugin, not Tenjin's, so this plan does not upgrade it. Replace it with tenjin_plugin " + latestVersion("tenjin_plugin", live) + " (flutter pub add tenjin_plugin), as tenjin doctor says."
	}
	return ""
}

// unreadVersion says why the version in use could not be read.
func unreadVersion(via, declared string) string {
	lock := map[string]string{viaPods: "Podfile.lock (pod install)", viaPub: "pubspec.lock (flutter pub get)", viaNPM: "a lock file or node_modules (npm install)", viaSPM: "Package.resolved (resolve the packages in Xcode)"}[via]
	if declared == "" && lock != "" {
		return "No version is declared, and nothing says which one is installed: install it once, which writes " + lock + ", then plan the upgrade again."
	}
	return "Its version could not be read, so the upgrade was not planned."
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

// readDeclaration reads what the declaration says and which version is in
// use.
func readDeclaration(root string, i project.Integration, via, pkg, target string) declaration {
	content := readProjectFile(root, i.Source)
	switch via {
	case viaGradle:
		return exactDeclaration(i.Version)
	case viaUnity:
		return unityDeclaration(content, i.Version, target)
	case viaPods:
		if strings.Contains(i.Package, "linked manually") {
			return declaration{why: "It is linked into the Xcode project by hand: download the new release from " + releaseNotes[pkg] + " and replace it."}
		}
		// A podspec's version is its requirement; a Podfile's comes from
		// Podfile.lock.
		if strings.HasSuffix(i.Source, ".podspec") {
			return podDeclaration(project.StripRubyComments(content), podspecTenjinLine, "")
		}
		return podDeclaration(project.StripRubyComments(content), podfileTenjin, i.Version)
	case viaSPM:
		return spmDeclaration(content, i.Version, target)
	case viaPub:
		return pubDeclaration(content, i.Version)
	case viaNPM:
		return npmDeclaration(root, i, pkg, content)
	}
	return declaration{}
}

// exactDeclaration is a declaration of one version, which admits only it.
func exactDeclaration(v string) declaration {
	d := declaration{text: v, current: v}
	if r, ok := readRelease(v); ok {
		d.admits = exactly(r)
	}
	return d
}

// unityDeclaration reads Packages/manifest.json's entry: a Git address
// ending in a release tag, or a version from a registry.
func unityDeclaration(manifest, tag, target string) declaration {
	if tag != "" {
		return exactDeclaration(tag)
	}
	var m struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	_ = json.Unmarshal([]byte(manifest), &m)
	ref := strings.TrimSpace(m.Dependencies["com.tenjin.sdk"])
	switch {
	case strings.HasPrefix(ref, "file:"):
		return declaration{why: "It is taken from a folder (" + ref + "), not a release: replace that copy with release " + target + " from " + releaseNotes["com.tenjin.sdk"] + "."}
	case strings.Contains(ref, "://") || strings.HasPrefix(ref, "git@"):
		return declaration{why: "It is taken from the Git repository without a release tag, so Unity installs whatever the repository's default branch holds: add #" + target + " to the address in Packages/manifest.json to pin the newest release."}
	}
	return exactDeclaration(ref)
}

var (
	podfileTenjin     = regexp.MustCompile(`(?m)^[ \t]*pod\s+['"]TenjinSDK(?:/[\w-]+)?['"]([^\n]*)`)
	podspecTenjinLine = regexp.MustCompile(`\.dependency\s+['"]TenjinSDK(?:/[\w-]+)?['"]([^\n]*)`)
	podOption         = regexp.MustCompile(`,\s*:?(\w+)\s*(?:=>|:[^:])`)
	quoted            = regexp.MustCompile(`['"]([^'"]*)['"]`)
)

// podDeclaration reads the requirements after the pod's name, all of which
// must hold, or the folder or repository it is taken from.
func podDeclaration(content string, line *regexp.Regexp, locked string) declaration {
	m := line.FindStringSubmatch(content)
	if m == nil {
		return declaration{current: locked}
	}
	rest, _, _ := strings.Cut(m[1], "#")
	if o := podOption.FindStringSubmatchIndex(rest); o != nil {
		switch rest[o[2]:o[3]] {
		case "path":
			return declaration{why: "It is taken from a folder (:path), not a release: replace that copy with the new release from " + releaseNotes["TenjinSDK"] + "."}
		case "git", "podspec":
			return declaration{why: "It is taken from a Git repository or a podspec of its own (:" + rest[o[2]:o[3]] + "), not from a release on CocoaPods: point it at the new release's tag, or declare the pod with a version instead."}
		}
		rest = rest[:o[0]]
	}
	var reqs []string
	for _, q := range quoted.FindAllStringSubmatch(rest, -1) {
		reqs = append(reqs, strings.TrimSpace(q[1]))
	}
	d := declaration{text: strings.Join(reqs, ", "), current: locked}
	if r, ok := podRule(reqs); ok {
		d.admits = r
	}
	if d.current == "" && len(reqs) > 0 {
		d.current = baseVersion(reqs[0])
	}
	return d
}

// The requirement Xcode records for a Swift package, and those written in a
// Package.swift.
var (
	pbxTenjinRequirement = regexp.MustCompile(`(?s)repositoryURL\s*=\s*"?[^";\n]*github\.com/tenjin/tenjin-ios-(?:spm|sdk)[^";\n]*"?\s*;\s*requirement\s*=\s*\{([^}]*)\}`)
	pbxField             = regexp.MustCompile(`(\w+)\s*=\s*"?([^";]*)"?\s*;`)
	swiftPackageCall     = regexp.MustCompile(`\.package\s*\(`)
	spmTenjinURL         = regexp.MustCompile(`github\.com/tenjin/tenjin-ios-(?:spm|sdk)`)
	swiftRequirements    = []struct {
		kind    string
		pattern *regexp.Regexp
	}{
		{"upToNextMinorVersion", regexp.MustCompile(`upToNextMinor\s*\(\s*from:\s*"([^"]+)"`)},
		{"upToNextMajorVersion", regexp.MustCompile(`(?:upToNextMajor\s*\(\s*)?from:\s*"([^"]+)"`)},
		{"exactVersion", regexp.MustCompile(`(?:exact:\s*|\.exact\s*\(\s*)"([^"]+)"`)},
		{"branch", regexp.MustCompile(`(?:branch:\s*|\.branch\s*\(\s*)"([^"]+)"`)},
		{"revision", regexp.MustCompile(`(?:revision:\s*|\.revision\s*\(\s*)"([^"]+)"`)},
		{"versionRange", regexp.MustCompile(`"([^"]+)"\s*\.\.<\s*"([^"]+)"`)},
		{"closedRange", regexp.MustCompile(`"([^"]+)"\s*\.\.\.\s*"([^"]+)"`)},
	}
)

// spmDeclaration reads the Swift package's requirement, from the Xcode
// project or from a Package.swift.
func spmDeclaration(content, resolved, target string) declaration {
	fields := map[string]string{}
	if m := pbxTenjinRequirement.FindStringSubmatch(content); m != nil {
		for _, f := range pbxField.FindAllStringSubmatch(m[1], -1) {
			fields[f[1]] = strings.TrimSpace(f[2])
		}
	} else if args := tenjinPackageCall(project.BlankComments(content)); args != "" {
		for _, r := range swiftRequirements {
			if m := r.pattern.FindStringSubmatch(args); m != nil {
				fields["kind"] = r.kind
				switch r.kind {
				case "exactVersion":
					fields["version"] = m[1]
				case "branch", "revision":
					fields[r.kind] = m[1]
				case "versionRange", "closedRange":
					fields["minimumVersion"], fields["maximumVersion"] = m[1], m[2]
				default:
					fields["minimumVersion"] = m[1]
				}
				break
			}
		}
	}
	d := declaration{current: resolved}
	low, n, okLow := partial(fields["minimumVersion"])
	switch fields["kind"] {
	case "branch", "revision":
		what := "the branch " + fields["branch"]
		if fields["kind"] == "revision" {
			what = "the commit " + fields["revision"]
		}
		return declaration{why: "It follows " + what + " of the package's repository, not a release, so it has no version to plan from. To move to a release, set its dependency rule to Up to Next Major Version from " + target + " (in Xcode, the project's Package Dependencies)."}
	case "exactVersion":
		d = exactDeclaration(fields["version"])
		d.text = "Exact Version " + fields["version"]
		if resolved != "" {
			d.current = resolved
		}
		return d
	case "upToNextMajorVersion", "upToNextMinorVersion":
		if !okLow || n == 0 {
			return d
		}
		keep, name := 1, "Up to Next Major Version"
		if fields["kind"] == "upToNextMinorVersion" {
			keep, name = 2, "Up to Next Minor Version"
		}
		d.text = name + " from " + fields["minimumVersion"]
		d.admits = rule{{{">=", low}, {"<", next(low, keep)}}}
	case "versionRange", "closedRange":
		high, _, okHigh := partial(fields["maximumVersion"])
		if !okLow || !okHigh {
			return d
		}
		op, sep := "<", "..<"
		if fields["kind"] == "closedRange" {
			op, sep = "<=", "..."
		}
		d.text = "Range " + fields["minimumVersion"] + sep + fields["maximumVersion"]
		d.admits = rule{{{">=", low}, {op, high}}}
	default:
		return d
	}
	if d.current == "" {
		d.current = fields["minimumVersion"]
	}
	return d
}

// tenjinPackageCall is the arguments of the .package(...) call in a
// Package.swift that names Tenjin's package, or "".
func tenjinPackageCall(swift string) string {
	for _, loc := range swiftPackageCall.FindAllStringIndex(swift, -1) {
		depth, start := 1, loc[1]
		for i := start; i < len(swift); i++ {
			switch swift[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				if args := swift[start:i]; spmTenjinURL.MatchString(args) {
					return args
				}
				break
			}
		}
	}
	return ""
}

// pubspecEntry is tenjin_plugin in pubspec.yaml, with what follows it on its
// line.
var pubspecEntry = regexp.MustCompile(`(?m)^([ \t]+)tenjin_plugin[ \t]*:[ \t]*(.*)$`)

// pubDeclaration reads tenjin_plugin's entry: a range on its line, or a
// block below it with a version, a folder or a Git repository.
func pubDeclaration(pubspec, locked string) declaration {
	loc := pubspecEntry.FindStringSubmatchIndex(pubspec)
	if loc == nil {
		return declaration{current: locked}
	}
	value := yamlScalar(pubspec[loc[4]:loc[5]])
	if value == "" {
		indent := loc[3] - loc[2]
		for _, line := range strings.Split(pubspec[loc[1]:], "\n")[1:] {
			trimmed := strings.TrimLeft(line, " \t")
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if len(line)-len(trimmed) <= indent {
				break
			}
			key, rest, _ := strings.Cut(trimmed, ":")
			switch key {
			case "path", "git", "sdk":
				return declaration{why: "It is taken from " + map[string]string{"path": "a folder", "git": "a Git repository", "sdk": "an SDK"}[key] + " (" + key + ":), not from a release on pub.dev, so it has no version to plan from. Declare tenjin_plugin with a version to upgrade it here."}
			case "version":
				value = yamlScalar(rest)
			}
		}
	}
	d := declaration{text: value, current: locked}
	if r, ok := nodeRule(value); ok {
		d.admits = r
	}
	if d.current == "" {
		d.current = baseVersion(value)
	}
	return d
}

// yamlScalar is a YAML value written on one line, without its quotes or a
// comment after it.
func yamlScalar(s string) string {
	s = strings.TrimSpace(s)
	if s != "" && (s[0] == '"' || s[0] == '\'') {
		if end := strings.IndexByte(s[1:], s[0]); end >= 0 {
			return s[1 : end+1]
		}
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// npmDeclaration reads the package's entry in package.json and the version
// installed.
func npmDeclaration(root string, i project.Integration, pkg, packageJSON string) declaration {
	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	_ = json.Unmarshal([]byte(packageJSON), &manifest)
	spec, ok := manifest.Dependencies[pkg]
	if !ok {
		spec, ok = manifest.DevDependencies[pkg]
	}
	if !ok {
		spec = i.Version
	}
	spec = strings.TrimSpace(spec)
	if alias, ok := strings.CutPrefix(spec, "npm:"+pkg+"@"); ok {
		spec = alias
	}
	if where := npmSource(spec); where != "" {
		return declaration{why: "It is taken from " + where + " (" + spec + "), not from a release on npm, so it has no version to plan from. Declare " + pkg + " with a version to upgrade it here."}
	}
	d := declaration{text: spec, current: npmInstalled(root, i.Source, pkg)}
	if r, ok := nodeRule(spec); ok {
		d.admits = r
	}
	if d.current == "" {
		d.current = baseVersion(spec)
	}
	return d
}

// npmSource names where a dependency comes from when it is not a release on
// the registry: a folder, a workspace package, or a Git repository.
func npmSource(spec string) string {
	switch {
	case strings.HasPrefix(spec, "file:"), strings.HasPrefix(spec, "link:"), strings.HasPrefix(spec, "portal:"),
		strings.HasPrefix(spec, "."), strings.HasPrefix(spec, "/"), strings.HasPrefix(spec, "~/"):
		return "a folder"
	case strings.HasPrefix(spec, "workspace:"):
		return "a package of the workspace"
	case strings.HasPrefix(spec, "git"), strings.Contains(spec, "://"),
		strings.HasPrefix(spec, "github:"), strings.HasPrefix(spec, "gitlab:"), strings.HasPrefix(spec, "bitbucket:"),
		strings.Contains(spec, "/") && !strings.ContainsAny(spec, " <>=^~"):
		return "a Git repository"
	}
	return ""
}

// npmInstalled is the version of the package in use: the installed copy
// that Node would load, looking up from package.json's folder as Node
// does, or else the one a lock file names, as far up as the first folder
// that has one, the workspace's root.
func npmInstalled(root, source, pkg string) string {
	dir := filepath.Join(root, filepath.FromSlash(path.Dir(source)))
	below := ""
	for {
		var installed struct {
			Version string `json:"version"`
		}
		if raw, err := os.ReadFile(filepath.Join(dir, "node_modules", pkg, "package.json")); err == nil && json.Unmarshal(raw, &installed) == nil && installed.Version != "" {
			return installed.Version
		}
		if manager, _ := lockIn(dir); manager != "" {
			return lockedVersion(dir, below, pkg)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		below = path.Join(filepath.Base(dir), below)
		dir = parent
	}
}

// lockIn names the package manager whose lock file is in dir, and the file.
func lockIn(dir string) (manager, file string) {
	for _, lock := range []struct{ file, manager string }{{"package-lock.json", "npm"}, {"yarn.lock", "yarn"}, {"pnpm-lock.yaml", "pnpm"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}} {
		if _, err := os.Stat(filepath.Join(dir, lock.file)); err == nil {
			return lock.manager, lock.file
		}
	}
	return "", ""
}

// lockedVersion reads the package's version from the lock file in dir.
// below is the path from dir to the package.json's folder.
func lockedVersion(dir, below, pkg string) string {
	if raw, err := os.ReadFile(filepath.Join(dir, "package-lock.json")); err == nil {
		var lock struct {
			Packages map[string]struct {
				Version string `json:"version"`
			} `json:"packages"`
		}
		if json.Unmarshal(raw, &lock) == nil {
			for _, key := range []string{path.Join(below, "node_modules", pkg), path.Join("node_modules", pkg)} {
				if v := lock.Packages[key].Version; v != "" {
					return v
				}
			}
		}
	}
	name := regexp.QuoteMeta(pkg)
	if raw, err := os.ReadFile(filepath.Join(dir, "yarn.lock")); err == nil {
		entry := regexp.MustCompile(`(?m)^"?` + name + `@[^\n]*:\n(?:[ \t]+[^\n]*\n)*?[ \t]+version:?[ \t]+"?([^"\s]+)"?`)
		if m := entry.FindStringSubmatch(project.Text(raw)); m != nil {
			return m[1]
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "pnpm-lock.yaml")); err == nil {
		entry := regexp.MustCompile(`(?m)^[ \t]+'?/?` + name + `@([0-9][^:(\s']*)`)
		if m := entry.FindStringSubmatch(project.Text(raw)); m != nil {
			return m[1]
		}
	}
	return ""
}

// npmManager is the package manager that installs the package: the one
// whose lock file is nearest above package.json, else the one package.json
// names, else npm. berry is true for Yarn 2 and later, whose commands
// differ from Yarn 1's.
func npmManager(root, source string) (manager string, berry bool) {
	dir := filepath.Join(root, filepath.FromSlash(path.Dir(source)))
	named := ""
	for {
		if named == "" {
			var pkg struct {
				PackageManager string `json:"packageManager"`
			}
			if raw, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil && json.Unmarshal(raw, &pkg) == nil {
				named = pkg.PackageManager
			}
		}
		if _, err := os.Stat(filepath.Join(dir, ".yarnrc.yml")); err == nil {
			berry = true
		}
		if m, _ := lockIn(dir); m != "" {
			manager = m
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	name, version, _ := strings.Cut(named, "@")
	if manager == "" {
		manager = "npm"
		if name == "yarn" || name == "pnpm" || name == "bun" {
			manager = name
		}
	}
	if major, err := strconv.Atoi(strings.SplitN(version, ".", 2)[0]); err == nil && name == "yarn" && major >= 2 {
		berry = true
	}
	return manager, berry && manager == "yarn"
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
	return project.Text(raw)
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
		manager, berry := npmManager(root, source)
		run := manager + " install"
		if !edited {
			run = map[string]string{"npm": "npm update ", "yarn": "yarn upgrade ", "pnpm": "pnpm update ", "bun": "bun update "}[manager] + pkg
			if berry {
				// Yarn 2 and later have no upgrade; up -R stays within the
				// range package.json gives.
				run = "yarn up -R " + pkg
			}
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
