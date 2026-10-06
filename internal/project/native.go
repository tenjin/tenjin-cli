package project

import (
	"io/fs"
	"path"
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
	resolvedTenjinVers = regexp.MustCompile(`(?s)tenjin-ios-(?:spm|sdk).*?"version"\s*:\s*"([^"]+)"`)
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
	// (`apply false`). Prefer a file that also sets an application ID.
	buildFile := buildFiles[0]
	for _, f := range buildFiles {
		if applicationID.MatchString(d.read(f)) {
			buildFile = f
			break
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
	for _, rel := range dedupe([]string{info.BuildFile, info.VersionCatalog}) {
		content := stripComments(d.read(rel))
		if m := tenjinMaven.FindStringSubmatch(content); m != nil {
			d.found(Integration{Package: "com.tenjin:android-sdk", Version: m[1], Source: rel})
			return
		}
		if tenjinTOML.MatchString(content) {
			d.found(Integration{Package: "com.tenjin:android-sdk", Source: rel})
			return
		}
	}
	for _, name := range []string{"tenjin.aar", "tenjin.jar"} {
		if rel := path.Join(info.ModuleDir, "libs", name); d.exists(rel) {
			d.found(Integration{Package: name, Source: rel})
			return
		}
	}
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
		Xcodeproj:          xcodeproj,
		GeneratedInfoPlist: pbxGeneratedPlist.MatchString(pbx),
		SwiftPackages:      strings.Contains(pbx, "XCRemoteSwiftPackageReference"),
	}
	for _, m := range pbxInfoPlist.FindAllStringSubmatch(pbx, -1) {
		if candidate := path.Join(dir, m[1]); !isTestPath(m[1]) && d.exists(candidate) {
			info.InfoPlist = candidate
			break
		}
	}
	if podfile := path.Join(dir, "Podfile"); d.exists(podfile) {
		info.Podfile = podfile
	}

	target := Target{Platform: IOS, IOS: info}
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
	} else {
		d.note("Could not read PRODUCT_BUNDLE_IDENTIFIER from %s.", xcodeproj)
	}
	return target, true
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
	}
}

var (
	lineComment  = regexp.MustCompile(`(?m)^\s*//.*$`)
	rubyComment  = regexp.MustCompile(`(?m)^\s*#.*$`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	// Unlike lineComment, this cannot reach back over blank lines.
	lineCommentInPlace = regexp.MustCompile(`(?m)^[ \t]*//.*$`)
)

// stripComments removes whole-line // comments and block comments, so a
// commented-out dependency is not mistaken for a real one.
func stripComments(s string) string {
	return lineComment.ReplaceAllString(blockComment.ReplaceAllString(s, ""), "")
}

// blankComments removes the same comments and keeps every line break, so
// positions in the result are positions in the file.
func blankComments(s string) string {
	s = blockComment.ReplaceAllStringFunc(s, func(comment string) string {
		return strings.Repeat("\n", strings.Count(comment, "\n"))
	})
	return lineCommentInPlace.ReplaceAllString(s, "")
}

func stripRubyComments(s string) string {
	return rubyComment.ReplaceAllString(s, "")
}
