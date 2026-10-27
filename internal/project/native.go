package project

import (
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	androidAppPlugin   = regexp.MustCompile(`com\.android\.application|plugins\.android\.?[aA]pplication`)
	multiplatform      = regexp.MustCompile(`kotlin\(\s*"multiplatform"\s*\)|org\.jetbrains\.kotlin\.multiplatform|plugins\.kotlin\.?[mM]ultiplatform`)
	applyFalse         = regexp.MustCompile(`(?m)^.*apply\s+false.*$`)
	applicationID      = regexp.MustCompile(`\bapplicationId\s*=?\s*["']([^"']+)["']`)
	applicationSuffix  = regexp.MustCompile(`applicationIdSuffix\s*=?\s*["']([^"']+)["']`)
	productFlavors     = regexp.MustCompile(`productFlavors\s*\{`)
	dotnetMobile       = regexp.MustCompile(`<UseMaui>\s*true|<TargetFrameworks?>[^<]*-(?:android|ios)|Xamarin\.(?:Forms|iOS|Android)`)
	minifyEnabled      = regexp.MustCompile(`(isMinifyEnabled\s*=\s*true|minifyEnabled\s+true)`)
	tenjinMaven        = regexp.MustCompile(`com\.tenjin:android-sdk(?::([0-9][0-9A-Za-z.\-+]*))?`)
	tenjinTOML         = regexp.MustCompile(`group\s*=\s*"com\.tenjin"\s*,\s*name\s*=\s*"android-sdk"`)
	pbxBundleID        = regexp.MustCompile(`PRODUCT_BUNDLE_IDENTIFIER = "?([^";]+)"?;`)
	pbxInfoPlist       = regexp.MustCompile(`INFOPLIST_FILE = "?([^";]+)"?;`)
	pbxGeneratedPlist  = regexp.MustCompile(`GENERATE_INFOPLIST_FILE = YES;`)
	podTenjin          = regexp.MustCompile(`(?m)^\s*pod\s+['"]TenjinSDK['"]`)
	podLockTenjin      = regexp.MustCompile(`(?m)^\s*-\s+TenjinSDK\s+\(([^)]+)\)`)
	spmTenjin          = regexp.MustCompile(`github\.com/tenjin/(tenjin-ios-spm|tenjin-ios-sdk)`)
	linkedTenjin       = regexp.MustCompile(`(libTenjinSDK\w*\.a|TenjinSDK\.xcframework|TenjinSDK\.framework) in (?:Frameworks|Embed Frameworks)`)
	resolvedTenjinVers = regexp.MustCompile(`tenjin-ios-(?:spm|sdk)[^{}]*\{[^{}]*?"version"\s*:\s*"([^"]+)"`) // within Tenjin's own pin
)

// native detects a plain Android and/or iOS project.
func (d *detector) native() bool {
	// A Flutter or React Native app in a subdirectory has android/ and ios/
	// folders that look native. They belong to that app, not to this level.
	d.skipWrapped = true
	android, okAndroid := d.androidTarget(".")
	ios, okIOS := d.iosTarget(".")
	d.skipWrapped = false
	if !okAndroid && !okIOS {
		return false
	}
	d.p.Framework = Native
	d.p.Supported = true
	if okAndroid {
		d.p.Targets = append(d.p.Targets, android)
		d.androidIntegration(android.Android)
	}
	if okIOS {
		d.p.Targets = append(d.p.Targets, ios)
		d.iosIntegration(ios.IOS)
	}
	if shared := d.multiplatformModules(); len(shared) > 0 {
		d.note("Kotlin Multiplatform is used in %s. Tenjin has no multiplatform SDK: integrate the Android and iOS SDKs in each platform's app, not in shared code.", strings.Join(shared, ", "))
	}
	return true
}

// unityExport detects the Gradle or Xcode project that Unity writes on each
// build. Checking it would mislead: the SDK arrives there through the Unity
// plugin, and anything changed is overwritten by the next export.
func (d *detector) unityExport() bool {
	android := d.isDir("unityLibrary")
	ios := d.isDir("Unity-iPhone.xcodeproj")
	if !android && !ios {
		return false
	}
	d.p.Framework = UnityExport
	d.p.Supported = false
	if android {
		if t, ok := d.androidTarget("."); ok {
			d.p.Targets = append(d.p.Targets, t)
		}
	}
	if ios {
		if t, ok := d.iosTarget("."); ok {
			d.p.Targets = append(d.p.Targets, t)
		}
	}
	d.note("This is a build exported from Unity, not the Unity project. Changes made here are overwritten by the next export; run this in the Unity project instead.")
	return true
}

// kotlinMultiplatform detects an app whose Android application module is
// itself a multiplatform module, as Compose Multiplatform projects are. Its
// sources and manifest are not where a native Android app keeps them.
func (d *detector) kotlinMultiplatform() bool {
	var module string
	d.walk(".", 3, func(p string, entry fs.DirEntry) {
		name := path.Base(p)
		if module != "" || entry.IsDir() || (name != "build.gradle" && name != "build.gradle.kts") {
			return
		}
		content := appliedPlugins(d.read(p))
		if androidAppPlugin.MatchString(content) && multiplatform.MatchString(content) {
			module = path.Dir(p)
		}
	})
	if module == "" {
		return false
	}
	d.p.Framework = KotlinMultiplatform
	d.p.Supported = false
	d.note("This is a Kotlin Multiplatform app (%s). Tenjin has no Kotlin Multiplatform SDK. The Android and iOS SDKs can be added to each platform's source set by hand, but this tool does not check that layout.", module)
	return true
}

// multiplatformModules lists the modules that apply the Kotlin
// Multiplatform plugin.
func (d *detector) multiplatformModules() []string {
	var modules []string
	d.walk(".", 3, func(p string, entry fs.DirEntry) {
		name := path.Base(p)
		if entry.IsDir() || (name != "build.gradle" && name != "build.gradle.kts") {
			return
		}
		if multiplatform.MatchString(appliedPlugins(d.read(p))) {
			modules = append(modules, path.Dir(p))
		}
	})
	return modules
}

// appliedPlugins returns a build file without comments and without the
// plugins it only declares for its modules (`apply false`).
func appliedPlugins(content string) string {
	return applyFalse.ReplaceAllString(stripComments(content), "")
}

// dotnet detects a .NET MAUI or Xamarin app, for which Tenjin has no SDK.
func (d *detector) dotnet() bool {
	var project string
	d.walk(".", 2, func(p string, entry fs.DirEntry) {
		if project != "" || entry.IsDir() || !strings.HasSuffix(p, ".csproj") {
			return
		}
		if dotnetMobile.MatchString(d.read(p)) {
			project = p
		}
	})
	if project == "" {
		return false
	}
	d.p.Framework = DotNet
	d.p.Supported = false
	d.note("This is a .NET mobile app (%s). Tenjin has no SDK for .NET MAUI or Xamarin.", project)
	return true
}

// androidTarget finds the Android application module under rel.
func (d *detector) androidTarget(rel string) (Target, bool) {
	var buildFiles []string
	d.walk(rel, 3, func(p string, entry fs.DirEntry) {
		name := path.Base(p)
		if entry.IsDir() || (name != "build.gradle" && name != "build.gradle.kts") {
			return
		}
		if d.skipWrapped && d.insideWrapper(path.Dir(p)) {
			return
		}
		if androidAppPlugin.MatchString(appliedPlugins(d.read(p))) {
			buildFiles = append(buildFiles, p)
		}
	})
	if len(buildFiles) == 0 {
		return Target{}, false
	}
	sort.Strings(buildFiles)
	// The root build file often only declares the plugin for its modules
	// (`apply false`). Prefer a file that sets an application ID, and of
	// those the one that has Tenjin, then the one called app: with several
	// apps in a project, the one being integrated is the one to check.
	buildFile := buildFiles[0]
	best := -1
	for _, f := range buildFiles {
		content := stripComments(d.read(f))
		if !applicationID.MatchString(content) {
			continue
		}
		score := 1
		if path.Base(path.Dir(f)) == "app" {
			score = 2
		}
		if strings.Contains(content, "com.tenjin") || strings.Contains(content, "libs.tenjin") {
			score = 3
		}
		if score > best {
			best, buildFile = score, f
		}
	}
	if modules := countModules(buildFiles, d); modules > 1 {
		d.note("Found %d Android application modules; using %s.", modules, path.Dir(buildFile))
	}

	content := stripComments(d.read(buildFile))
	info := &AndroidInfo{
		ModuleDir: path.Dir(buildFile),
		BuildFile: buildFile,
		Minify:    minifyEnabled.MatchString(content),
		Flavors:   productFlavors.MatchString(content),
	}
	if manifest := path.Join(info.ModuleDir, "src/main/AndroidManifest.xml"); d.exists(manifest) {
		info.Manifest = manifest
	}
	info.LibraryModules = d.libraryModules(info.ModuleDir, content)

	d.androidShape(info, content)

	// Flavors can each set their own application ID, so the default is read
	// from the file without them.
	flavors, rest := cutBlock(content, productFlavors)
	target := Target{Platform: Android, Android: info}
	if m := applicationID.FindStringSubmatch(rest); m != nil {
		target.BundleID = m[1]
		target.Source = buildFile
	} else {
		d.note("Could not read applicationId from %s.", buildFile)
	}
	// Every other ID a build of this module can have: a flavor's own, or the
	// default with a build type's or a flavor's suffix.
	var others []string
	for _, m := range applicationID.FindAllStringSubmatch(flavors, -1) {
		others = append(others, m[1])
	}
	if target.BundleID != "" {
		for _, m := range applicationSuffix.FindAllStringSubmatch(content, -1) {
			others = append(others, target.BundleID+m[1])
		}
	}
	target.OtherBundleIDs = dedupe(others)
	target.Builds = d.androidBuilds(&target, content, flavors, rest)

	// Builds that ship are listed as builds; any other ID, such as a debug
	// suffix, is only worth a note.
	shipped := map[string]bool{}
	for _, b := range target.Builds {
		shipped[b.BundleID] = true
	}
	var unshipped []string
	for _, id := range target.OtherBundleIDs {
		if !shipped[id] {
			unshipped = append(unshipped, id)
		}
	}
	if len(target.Builds) > 1 {
		var list []string
		for _, b := range target.Builds {
			list = append(list, b.BundleID+" on "+StoreName(b.TenjinPlatform)+" ("+strings.Join(b.Variants, ", ")+")")
		}
		d.note("This Android module ships %d builds that are separate apps in Tenjin, each with its own SDK key: %s.", len(target.Builds), strings.Join(list, "; "))
	}
	switch {
	case info.Flavors && len(target.Builds) == 0 && Resolved(target.BundleID):
		d.note("Could not read the application IDs of the product flavors in %s: they are made when the app builds. Create each flavor's Tenjin app with tenjin apps ensure, and pass its key with --sdk-key.", buildFile)
	case len(unshipped) > 0:
		d.note("Other Android builds of this app use the application ID %s. Tenjin treats each as a different app from %s, with its own SDK key.",
			strings.Join(unshipped, ", "), target.BundleID)
	case info.Flavors && len(target.Builds) <= 1:
		d.note("Android product flavors are defined in %s. Each flavor with its own application ID or app store needs its own Tenjin app.", buildFile)
	}
	return target, true
}

// cutBlock splits content into the braced block that start opens and
// everything else. A file without the block comes back whole as the rest.
func cutBlock(content string, start *regexp.Regexp) (block, rest string) {
	loc := start.FindStringIndex(content)
	if loc == nil {
		return "", content
	}
	depth := 1
	for i := loc[1]; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[loc[1]:i], content[:loc[0]] + content[i+1:]
			}
		}
	}
	return content[loc[1]:], content[:loc[0]]
}

func countModules(buildFiles []string, d *detector) int {
	n := 0
	for _, f := range buildFiles {
		if applicationID.MatchString(d.read(f)) {
			n++
		}
	}
	return n
}

// androidIntegration records how the Tenjin Android SDK is declared, if it is.
func (d *detector) androidIntegration(info *AndroidInfo) {
	files := []string{info.BuildFile, info.VersionCatalog}
	for _, m := range info.LibraryModules {
		files = append(files, moduleBuildFile(d, m))
	}
	for _, rel := range dedupe(files) {
		if rel == "" {
			continue
		}
		content := stripComments(d.read(rel))
		loc := tenjinMaven.FindStringSubmatchIndex(content)
		if loc == nil {
			loc = tenjinTOML.FindStringIndex(content)
		}
		if loc == nil {
			continue
		}
		version := ""
		if len(loc) > 3 && loc[2] >= 0 {
			version = content[loc[2]:loc[3]]
		}
		if version == "" {
			version = d.androidVersion(info, rel, content, loc[0])
		}
		d.found(Integration{Package: "com.tenjin:android-sdk", Version: version, Source: rel})
		return
	}
	for _, name := range []string{"tenjin.aar", "tenjin.jar"} {
		if rel := path.Join(info.ModuleDir, "libs", name); d.exists(rel) {
			d.found(Integration{Package: name, Source: rel})
			return
		}
	}
}

// projectDependency is a dependency on another module of the build:
// project(":ads"), project(path = ":libs:ads").
var projectDependency = regexp.MustCompile(`project\(\s*(?:path\s*[:=]\s*)?["']:([\w.:-]+)["']`)

// libraryModules follows a module's dependencies on the build's own
// modules, and theirs, a few levels down. Module :libs:ads is in libs/ads
// under the folder the app module sits in.
func (d *detector) libraryModules(moduleDir, content string) []string {
	root := path.Dir(moduleDir)
	var found []string
	seen := map[string]bool{moduleDir: true}
	var follow func(content string, depth int)
	follow = func(content string, depth int) {
		if depth > 3 {
			return
		}
		for _, m := range projectDependency.FindAllStringSubmatch(content, -1) {
			dir := path.Join(root, strings.ReplaceAll(m[1], ":", "/"))
			build := moduleBuildFile(d, dir)
			if seen[dir] || build == "" {
				continue
			}
			seen[dir] = true
			found = append(found, dir)
			follow(stripComments(d.read(build)), depth+1)
		}
	}
	follow(content, 0)
	return found
}

// moduleBuildFile is a module's build file, or "".
func moduleBuildFile(d *detector, dir string) string {
	for _, name := range []string{"build.gradle.kts", "build.gradle"} {
		if f := path.Join(dir, name); d.exists(f) {
			return f
		}
	}
	return ""
}

var (
	catalogVersion    = regexp.MustCompile(`\bversion\s*=\s*"([^"]+)"`)
	catalogVersionRef = regexp.MustCompile(`\bversion\.ref\s*=\s*"([\w.-]+)"`)
	interpolated      = regexp.MustCompile(`com\.tenjin:android-sdk:\$\{?([A-Za-z_][\w.]*)\}?`)
)

// androidVersion reads the SDK version where the declaration does not
// write it out: in a version catalog, the entry's version or the
// [versions] entry its version.ref names; in a build file, the variable it
// interpolates, set in the build file, the root build file or
// gradle.properties. It is "" when none of that can be read.
func (d *detector) androidVersion(info *AndroidInfo, rel, content string, at int) string {
	line := content[at:]
	if end := strings.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	if strings.HasSuffix(rel, ".toml") {
		if m := catalogVersion.FindStringSubmatch(line); m != nil {
			return m[1]
		}
		if m := catalogVersionRef.FindStringSubmatch(line); m != nil {
			versions := blockOfTOML(content, "versions")
			if v := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(m[1]) + `\s*=\s*"([^"]+)"`).FindStringSubmatch(versions); v != nil {
				return v[1]
			}
		}
		return ""
	}
	m := interpolated.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	name := m[1][strings.LastIndex(m[1], ".")+1:]
	assigned := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b["']?\s*(?:=|,)\s*["']([0-9][^"'\s]*)["']|(?m)^\s*` + regexp.QuoteMeta(name) + `\s*=\s*([0-9][^\s]*)\s*$`)
	root := path.Dir(info.ModuleDir)
	for _, f := range []string{rel, path.Join(root, "build.gradle"), path.Join(root, "build.gradle.kts"), path.Join(root, "gradle.properties")} {
		if v := assigned.FindStringSubmatch(stripComments(d.read(f))); v != nil {
			return firstNonEmpty(v[1], v[2])
		}
	}
	return ""
}

// blockOfTOML returns the body of a [table] in a TOML file.
func blockOfTOML(content, table string) string {
	start := strings.Index(content, "["+table+"]")
	if start < 0 {
		return ""
	}
	body := content[start+len(table)+2:]
	if next := regexp.MustCompile(`(?m)^\s*\[`).FindStringIndex(body); next != nil {
		body = body[:next[0]]
	}
	return body
}

// iosTarget finds the Xcode project under rel.
func (d *detector) iosTarget(rel string) (Target, bool) {
	var projects []string
	d.walk(rel, 3, func(p string, entry fs.DirEntry) {
		if !entry.IsDir() || !strings.HasSuffix(p, ".xcodeproj") || path.Base(p) == "Pods.xcodeproj" {
			return
		}
		if d.skipWrapped && d.insideWrapper(path.Dir(p)) {
			return
		}
		projects = append(projects, p)
	})
	if len(projects) == 0 {
		return Target{}, false
	}
	// Shallowest first, then by name, so the app project wins over samples
	// nested deeper in the tree.
	sort.Slice(projects, func(i, j int) bool {
		di, dj := strings.Count(projects[i], "/"), strings.Count(projects[j], "/")
		if di != dj {
			return di < dj
		}
		return projects[i] < projects[j]
	})
	xcodeproj := projects[0]
	if len(projects) > 1 {
		d.note("Found %d Xcode projects; using %s.", len(projects), xcodeproj)
	}

	pbx := d.read(path.Join(xcodeproj, "project.pbxproj"))
	dir := path.Dir(xcodeproj)
	info := &IOSInfo{
		Xcodeproj:     xcodeproj,
		SwiftPackages: strings.Contains(pbx, "XCRemoteSwiftPackageReference"),
	}
	if podfile := path.Join(dir, "Podfile"); d.exists(podfile) {
		info.Podfile = podfile
	}

	target := Target{Platform: IOS, IOS: info}
	if targets, err := xcodeTargets(pbx); err == nil {
		d.iosAppTarget(&target, targets, dir)
		return target, true
	}
	info.GeneratedInfoPlist = pbxGeneratedPlist.MatchString(pbx)
	var ids []string
	for _, m := range pbxBundleID.FindAllStringSubmatch(pbx, -1) {
		if id := strings.TrimSpace(m[1]); !isTestBundle(id) {
			ids = append(ids, id)
		}
	}
	ids = dedupe(ids)
	// App extensions and watch apps extend the app's ID, so the app is the
	// shortest one.
	sort.SliceStable(ids, func(i, j int) bool { return len(ids[i]) < len(ids[j]) })
	if len(ids) > 0 {
		target.BundleID = ids[0]
		target.OtherBundleIDs = ids[1:]
		target.Source = path.Join(xcodeproj, "project.pbxproj")
		if strings.Contains(target.BundleID, "$(") {
			d.note("The iOS bundle ID is built from build settings (%s); check the resolved value in Xcode.", target.BundleID)
		}
		d.note("Could not read the targets of %s, so the iOS bundle ID %s is the shortest one in its build settings; check that it is the app's and not a framework's or an extension's.", xcodeproj, target.BundleID)
	} else {
		d.note("Could not read PRODUCT_BUNDLE_IDENTIFIER from %s.", xcodeproj)
	}
	d.chooseInfoPlist(info, pbx, dir, target.BundleID)
	return target, true
}

// iosAppTarget takes the bundle ID and Info.plist from the project's iOS app
// target, so that a framework's, an extension's or a watch app's is never
// taken for the app's. Of several app targets, the one named after the
// project is the app, then the one with the shortest bundle ID; the others'
// IDs are other builds of it.
func (d *detector) iosAppTarget(target *Target, targets []xcodeTarget, dir string) {
	info := target.IOS
	var apps []xcodeTarget
	for _, t := range targets {
		if t.iosApp() {
			apps = append(apps, t)
		}
	}
	if len(apps) == 0 {
		d.note("%s has no iOS app target, so there is no bundle ID for Tenjin.", info.Xcodeproj)
		return
	}
	project := strings.TrimSuffix(path.Base(info.Xcodeproj), ".xcodeproj")
	sort.SliceStable(apps, func(i, j int) bool {
		if (apps[i].name == project) != (apps[j].name == project) {
			return apps[i].name == project
		}
		a, b := apps[i].setting("PRODUCT_BUNDLE_IDENTIFIER"), apps[j].setting("PRODUCT_BUNDLE_IDENTIFIER")
		return a != "" && (b == "" || len(a) < len(b))
	})
	app := apps[0]
	if len(apps) > 1 {
		var names []string
		for _, a := range apps {
			names = append(names, a.name)
		}
		d.note("Found %d iOS app targets in %s (%s); using %s.", len(apps), info.Xcodeproj, strings.Join(names, ", "), app.name)
	}

	info.GeneratedInfoPlist = app.setting("GENERATE_INFOPLIST_FILE") == "YES"
	ids := app.values("PRODUCT_BUNDLE_IDENTIFIER")
	for _, other := range apps[1:] {
		ids = append(ids, other.values("PRODUCT_BUNDLE_IDENTIFIER")...)
	}
	ids = dedupe(ids)
	if len(ids) == 0 || app.setting("PRODUCT_BUNDLE_IDENTIFIER") == "" {
		d.note("Could not read PRODUCT_BUNDLE_IDENTIFIER of the %s target in %s.", app.name, info.Xcodeproj)
	} else {
		target.BundleID = ids[0]
		target.OtherBundleIDs = ids[1:]
		target.Source = path.Join(info.Xcodeproj, "project.pbxproj")
		if strings.Contains(target.BundleID, "$(") {
			d.note("The iOS bundle ID is built from build settings (%s); check the resolved value in Xcode.", target.BundleID)
		}
	}

	var plists []string
	for _, p := range app.values("INFOPLIST_FILE") {
		plists = append(plists, resolveSourceRoot(p))
	}
	for _, p := range plists {
		if strings.Contains(p, "$(") || strings.Contains(p, "${") {
			continue
		}
		if rel := path.Join(dir, p); d.exists(rel) {
			info.InfoPlist = rel
			return
		}
	}
	if len(plists) > 0 {
		info.MissingInfoPlist = plists[0]
	}
}

// resolveSourceRoot makes a path in build settings relative to the folder
// of the Xcode project.
func resolveSourceRoot(p string) string {
	p = strings.Trim(strings.TrimSpace(p), `"`)
	for _, root := range []string{"$(SRCROOT)/", "${SRCROOT}/", "$(PROJECT_DIR)/", "${PROJECT_DIR}/"} {
		p = strings.TrimPrefix(p, root)
	}
	return p
}

// pbxBuildSettings is one configuration's build settings in project.pbxproj.
var pbxBuildSettings = regexp.MustCompile(`(?s)buildSettings = \{(.*?)\n\s*\};`)

// chooseInfoPlist picks the app's own Info.plist: the one named in the build
// settings that also set the app's bundle ID, so that a widget's or an
// extension's is not taken for it, with $(SRCROOT) and $(PROJECT_DIR)
// resolved. A plist the settings name that is not there is recorded as
// missing.
func (d *detector) chooseInfoPlist(info *IOSInfo, pbx, dir, bundleID string) {
	resolve := resolveSourceRoot
	var ofApp, others []string
	for _, block := range pbxBuildSettings.FindAllStringSubmatch(pbx, -1) {
		m := pbxInfoPlist.FindStringSubmatch(block[1])
		if m == nil || isTestPath(m[1]) {
			continue
		}
		plist := resolve(m[1])
		if id := pbxBundleID.FindStringSubmatch(block[1]); id != nil && strings.TrimSpace(id[1]) == bundleID {
			ofApp = append(ofApp, plist)
		} else {
			others = append(others, plist)
		}
	}
	// Settings outside any configuration block, as hand-written files have.
	if len(ofApp) == 0 && len(others) == 0 {
		for _, m := range pbxInfoPlist.FindAllStringSubmatch(pbx, -1) {
			if !isTestPath(m[1]) {
				others = append(others, resolve(m[1]))
			}
		}
	}
	candidates := ofApp
	if len(candidates) == 0 {
		candidates = others
	}
	for _, c := range candidates {
		if strings.Contains(c, "$(") || strings.Contains(c, "${") {
			continue
		}
		if p := path.Join(dir, c); d.exists(p) {
			info.InfoPlist = p
			return
		}
	}
	if len(candidates) > 0 {
		info.MissingInfoPlist = candidates[0]
	}
}

func isTestBundle(id string) bool {
	return strings.HasSuffix(id, "Tests") || strings.HasSuffix(id, "UITests") || strings.Contains(id, ".tests")
}

func isTestPath(p string) bool {
	return strings.Contains(p, "Tests/") || strings.Contains(p, "UITests/")
}

// iosIntegration records how the Tenjin iOS SDK is declared, if it is.
func (d *detector) iosIntegration(info *IOSInfo) {
	dir := path.Dir(info.Xcodeproj)
	if info.Podfile != "" && podTenjin.MatchString(stripRubyComments(d.read(info.Podfile))) {
		i := Integration{Package: "TenjinSDK (CocoaPods)", Source: info.Podfile}
		if m := podLockTenjin.FindStringSubmatch(d.read(path.Join(dir, "Podfile.lock"))); m != nil {
			i.Version = m[1]
		}
		d.found(i)
		return
	}
	pbxPath := path.Join(info.Xcodeproj, "project.pbxproj")
	// The SDK can also be dragged into the project as a binary.
	if m := linkedTenjin.FindStringSubmatch(d.read(pbxPath)); m != nil && !spmTenjin.MatchString(d.read(pbxPath)) {
		d.found(Integration{Package: "TenjinSDK (" + m[1] + ", linked manually)", Source: pbxPath})
		return
	}
	if spmTenjin.MatchString(d.read(pbxPath)) {
		i := Integration{Package: "TenjinSDK (Swift Package)", Source: pbxPath}
		for _, resolved := range []string{
			path.Join(info.Xcodeproj, "project.xcworkspace/xcshareddata/swiftpm/Package.resolved"),
			path.Join(dir, "Package.resolved"),
		} {
			if m := resolvedTenjinVers.FindStringSubmatch(d.read(resolved)); m != nil {
				i.Version = m[1]
				break
			}
		}
		d.found(i)
		return
	}
	// A local Swift package of the project's own, which the app links, can
	// be the one that depends on Tenjin.
	var local string
	d.walk(dir, 4, func(p string, entry fs.DirEntry) {
		if local == "" && !entry.IsDir() && path.Base(p) == "Package.swift" && spmTenjin.MatchString(stripComments(d.read(p))) {
			local = p
		}
	})
	if local == "" && d.throughPods(info) {
		return
	}
	if local != "" {
		i := Integration{Package: "TenjinSDK (Swift Package)", Source: local}
		for _, resolved := range []string{
			path.Join(info.Xcodeproj, "project.xcworkspace/xcshareddata/swiftpm/Package.resolved"),
			path.Join(path.Dir(local), "Package.resolved"),
		} {
			if m := resolvedTenjinVers.FindStringSubmatch(d.read(resolved)); m != nil {
				i.Version = m[1]
				break
			}
		}
		d.found(i)
	}
}

// podByPath is a pod the Podfile takes from a folder: pod 'Name', :path => '../'.
var podByPath = regexp.MustCompile(`pod\s+['"]([\w./-]+)['"]\s*,\s*:path\s*=>\s*['"]([^'"]+)['"]`)

// throughPods records the SDK when the app gets it through another pod: a
// local one, whose podspec depends on TenjinSDK and whose code is read for
// the calls, or a published one, which Podfile.lock shows.
func (d *detector) throughPods(info *IOSInfo) bool {
	if info.Podfile == "" {
		return false
	}
	dir := path.Dir(info.Podfile)
	version := ""
	if m := podLockTenjin.FindStringSubmatch(d.read(path.Join(dir, "Podfile.lock"))); m != nil {
		version = m[1]
	}
	for _, m := range podByPath.FindAllStringSubmatch(stripRubyComments(d.read(info.Podfile)), -1) {
		podDir := path.Join(dir, m[2])
		specs, _ := filepath.Glob(filepath.Join(d.root, filepath.FromSlash(podDir), "*.podspec"))
		for _, spec := range specs {
			rel, _ := filepath.Rel(d.root, spec)
			rel = filepath.ToSlash(rel)
			if content := stripRubyComments(d.read(rel)); podspecTenjin.MatchString(content) {
				// Pods in one folder share it: the one with Tenjin is the
				// podspec that names it.
				name := strings.SplitN(m[1], "/", 2)[0]
				if n := podspecName.FindStringSubmatch(content); n != nil {
					name = n[1]
				}
				d.found(Integration{Package: "TenjinSDK (CocoaPods, through " + name + ")", Version: version, Source: rel, Through: name, Local: true})
				d.p.LibraryCode = append(d.p.LibraryCode, podDir)
				return true
			}
		}
	}
	// The lock says which pod brings it; a lock that lists TenjinSDK only
	// for itself is out of date with a Podfile that no longer asks for it.
	if m := podLockParent.FindStringSubmatch(d.read(path.Join(dir, "Podfile.lock"))); m != nil {
		name := strings.SplitN(m[1], "/", 2)[0]
		d.found(Integration{Package: "TenjinSDK (CocoaPods, through " + name + ")", Version: version, Source: path.Join(dir, "Podfile.lock"), Through: name})
		return true
	}
	return false
}

// podLockParent is a pod in Podfile.lock that depends on TenjinSDK:
//
//   - SomeAds (1.2.0):
//   - TenjinSDK (~> 1.15)
var podLockParent = regexp.MustCompile(`(?m)^  - ([\w/.+-]+) \([^)]*\):\n(?:    - .*\n)*?    - TenjinSDK\b`)

var rubyComment = regexp.MustCompile(`(?m)^\s*#.*$`)

// stripComments removes // and /* */ comments, so a commented-out
// dependency is not mistaken for a real one.
func stripComments(s string) string { return BlankComments(s) }

// blankComments removes the same comments and keeps every line break, so
// positions in the result are positions in the file.
func blankComments(s string) string { return BlankComments(s) }

func stripRubyComments(s string) string {
	return rubyComment.ReplaceAllString(s, "")
}

// StripRubyComments removes the comments of a Podfile or a podspec.
func StripRubyComments(s string) string { return stripRubyComments(s) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
