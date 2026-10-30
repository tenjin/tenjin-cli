// Package project works out what kind of mobile project a directory holds:
// its framework, platforms, bundle IDs, and whether the Tenjin SDK is already
// there. It only reads files and never touches the network.
package project

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Framework is how the app is built.
type Framework string

const (
	Native      Framework = "native"
	Flutter     Framework = "flutter"
	ReactNative Framework = "react-native"
	Capacitor   Framework = "capacitor"
	Unity       Framework = "unity"
	Unreal      Framework = "unreal"
	Cordova     Framework = "cordova"
	// UnityExport is the Android or Xcode project that Unity generates on
	// each build. It is output, not source.
	UnityExport Framework = "unity-export"
	// KotlinMultiplatform and DotNet are recognised so they can be reported
	// as having no Tenjin SDK, instead of being mistaken for native apps.
	KotlinMultiplatform Framework = "kotlin-multiplatform"
	DotNet              Framework = "dotnet"
	Unknown             Framework = "unknown"
)

// Platform is an operating system the app ships on.
type Platform string

const (
	IOS     Platform = "ios"
	Android Platform = "android"
)

// Project is what Detect found.
type Project struct {
	Root      string        `json:"root"`
	Framework Framework     `json:"framework"`
	Supported bool          `json:"supported"`
	Targets   []Target      `json:"targets"`
	Tenjin    []Integration `json:"tenjin"`
	// PackageManager is the JavaScript package manager, for the frameworks
	// that have one.
	PackageManager string `json:"package_manager,omitempty"`
	// GeneratedIOS is an Expo project's ios/ folder that git ignores: npx
	// expo prebuild writes it again from the app config, so changes made in
	// it by hand do not last.
	GeneratedIOS bool `json:"generated_ios,omitempty"`
	// AdSDKs are the advertising SDKs found among the dependencies.
	AdSDKs []AdSDK `json:"ad_sdks"`
	// TrackingPrompt is where the app already asks for tracking permission
	// on iOS. Nil when no prompt was found.
	TrackingPrompt *TrackingPrompt `json:"tracking_prompt,omitempty"`
	// Features are the parts of the SDK an integration can include, each
	// suggested or not from what the project already does.
	Features []Feature `json:"features"`
	// Analytics is what the app already sends to other analytics SDKs. It
	// is empty until ReadAnalytics reads it: only the commands that show or
	// use it pay for reading every source file again.
	Analytics     Analytics `json:"analytics"`
	analyticsRead bool
	// sentToTenjin is what the app sends Tenjin's own SDK, read by
	// SentToTenjin.
	sentToTenjin     Analytics
	sentToTenjinRead bool
	// LibraryCode are the folders of local libraries the app gets the SDK
	// through (a pod or a package by path), relative to the root and
	// possibly above it: their calls are the app's.
	LibraryCode []string `json:"-"`
	// Library is set when the project is a library that wraps the Tenjin
	// SDK rather than an app.
	Library *Library `json:"library,omitempty"`
	Notes   []string `json:"notes"`
	// Omitted are the builds KeepBuilds left out: their keys and
	// placeholders are not this run's to settle.
	Omitted []Build `json:"-"`
}

// Target is one platform build of the project. A Tenjin app is per platform,
// so each target needs its own app.
type Target struct {
	Platform Platform `json:"platform"`
	// BundleID is what Tenjin must be told the app is called. Empty when it
	// could not be read.
	BundleID       string   `json:"bundle_id,omitempty"`
	OtherBundleIDs []string `json:"other_bundle_ids,omitempty"`
	Source         string   `json:"bundle_id_source,omitempty"`
	// Builds are the target's shipped builds that are each their own app in
	// Tenjin. Most targets have one; an Android module with product flavors
	// for other stores or bundle IDs has several.
	Builds []Build `json:"builds"`

	Android *AndroidInfo `json:"android,omitempty"`
	IOS     *IOSInfo     `json:"ios,omitempty"`
}

// AndroidInfo locates the files of an Android application module. Paths are
// relative to the project root.
type AndroidInfo struct {
	ModuleDir string `json:"module_dir"`
	BuildFile string `json:"build_file"`
	// DSL is the build file's language: "kotlin" or "groovy".
	DSL string `json:"dsl"`
	// VersionCatalog is the Gradle version catalog, when the build has one.
	VersionCatalog string `json:"version_catalog,omitempty"`
	Manifest       string `json:"manifest,omitempty"`
	// LauncherActivity is the class the home screen starts, and
	// LauncherSource its file when it is part of this module.
	LauncherActivity string `json:"launcher_activity,omitempty"`
	LauncherSource   string `json:"launcher_source,omitempty"`
	Minify           bool   `json:"minify"`
	Flavors          bool   `json:"flavors"`
	// LibraryModules are the folders of the project's own modules the app
	// depends on (project(":ads")), directly or through each other. Their
	// dependencies, code and manifests become the app's when it is built.
	LibraryModules []string `json:"library_modules,omitempty"`
}

// IOSInfo locates the files of an Xcode project. Paths are relative to the
// project root.
type IOSInfo struct {
	Xcodeproj string `json:"xcodeproj"`
	InfoPlist string `json:"info_plist,omitempty"`
	// MissingInfoPlist is the Info.plist the app's build settings name when
	// it could not be found, so that its keys cannot be checked.
	MissingInfoPlist string `json:"-"`
	// GeneratedInfoPlist means Xcode builds the plist from build settings.
	GeneratedInfoPlist bool `json:"generated_info_plist"`
	// InfoPlistSettings are the Info.plist keys the app target's build
	// settings set (INFOPLIST_KEY_<key>), with their values.
	InfoPlistSettings map[string]string `json:"-"`
	Podfile           string            `json:"podfile,omitempty"`
	SwiftPackages     bool              `json:"swift_packages"`
	// Lifecycle is how the app starts: "swiftui" for an App struct,
	// "app-delegate" otherwise. EntryPoint is the file that holds it.
	Lifecycle  string `json:"lifecycle,omitempty"`
	EntryPoint string `json:"entry_point,omitempty"`
}

// Integration is one place the Tenjin SDK is declared.
type Integration struct {
	Package string `json:"package"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source"`
	// Through names the library the SDK comes with, when the app does not
	// declare it itself. Local is true when that library's code is in the
	// project, where its calls can be read.
	Through string `json:"through,omitempty"`
	Local   bool   `json:"local,omitempty"`
}

// Build returns the build with this ID, or nil.
func (p *Project) Build(id string) (*Target, *Build) {
	for i := range p.Targets {
		for j := range p.Targets[i].Builds {
			if p.Targets[i].Builds[j].ID == id {
				return &p.Targets[i], &p.Targets[i].Builds[j]
			}
		}
	}
	return nil, nil
}

// KeepBuilds narrows the project to the builds with these IDs, matched as
// the build flags match them (see Shout). It returns the IDs that name no
// build, and leaves the project as it is when there are any.
func (p *Project) KeepBuilds(ids []string) (unknown []string) {
	want := map[string]bool{}
	for _, id := range ids {
		want[Shout(id)] = true
	}
	found := map[string]bool{}
	for _, b := range p.Builds() {
		if want[Shout(b.ID)] {
			found[Shout(b.ID)] = true
		}
	}
	for _, id := range ids {
		if !found[Shout(id)] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return unknown
	}
	// A target none of whose builds is kept is left out with them.
	targets := []Target{}
	for _, t := range p.Targets {
		kept := []Build{}
		for _, b := range t.Builds {
			if want[Shout(b.ID)] {
				kept = append(kept, b)
			} else {
				p.Omitted = append(p.Omitted, b)
			}
		}
		if len(t.Builds) > 0 && len(kept) == 0 {
			continue
		}
		t.Builds = kept
		targets = append(targets, t)
	}
	p.Targets = targets
	return nil
}

// IsOmitted reports whether a placeholder is that of a build KeepBuilds
// left out.
func (p *Project) IsOmitted(placeholder string) bool {
	for _, b := range p.Omitted {
		if b.SDKKeyPlaceholder == placeholder {
			return true
		}
	}
	return false
}

// Builds returns every build of every target, in target order.
func (p *Project) Builds() []Build {
	var out []Build
	for _, t := range p.Targets {
		out = append(out, t.Builds...)
	}
	return out
}

// Target returns the target for a platform, or nil.
func (p *Project) Target(platform Platform) *Target {
	for i := range p.Targets {
		if p.Targets[i].Platform == platform {
			return &p.Targets[i]
		}
	}
	return nil
}

// Detect inspects dir. It fails only when dir cannot be read; a directory
// with no recognisable project comes back as Unknown.
func Detect(dir string) (*Project, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	p := &Project{Root: root, Framework: Unknown, Targets: []Target{}, Tenjin: []Integration{}, AdSDKs: []AdSDK{}, Features: []Feature{},
		Analytics: Analytics{Events: []AnalyticsEvent{}, UserIDs: []AnalyticsUserID{}}, Notes: []string{}}
	d := &detector{root: root, p: p}
	lib := d.library()
	matched := d.detect()
	if matched {
		d.finish()
	}
	if lib == nil && (!matched || len(p.Targets) == 0) {
		lib = d.androidLibrary()
	}
	if lib != nil {
		lib.Example = d.exampleApp()
		p.Library = lib
		p.Supported = true
		return p, nil
	}
	if !matched {
		d.suggestSubprojects()
	}
	return p, nil
}

// exampleApp finds the app a library's repository keeps to show it, in one
// of the folders such apps are usually in.
func (d *detector) exampleApp() string {
	// Names as the folder has them: a file system that ignores case would
	// otherwise answer for Example when asked about example.
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return ""
	}
	has := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			has[e.Name()] = true
		}
	}
	for _, name := range []string{"example", "Example", "sample", "Sample", "demo", "Demo", "app"} {
		if !has[name] {
			continue
		}
		sub := &detector{root: d.path(name), p: &Project{}}
		if sub.wrapper() || sub.native() {
			return name
		}
	}
	return ""
}

// detect tries each kind of project in turn and reports whether one matched.
// The order matters: cross-platform projects contain android/ and ios/
// folders that look native, so native comes last.
func (d *detector) detect() bool {
	return d.wrapper() || d.native()
}

// wrapper matches everything that is not a plain native project.
func (d *detector) wrapper() bool {
	for _, detect := range []func() bool{
		d.unity, d.unityExport, d.unreal, d.flutter, d.reactNative, d.capacitor, d.cordova, d.dotnet, d.kotlinMultiplatform,
	} {
		if detect() {
			return true
		}
	}
	return false
}

type detector struct {
	root string
	p    *Project

	// skipWrapped makes the native scan ignore projects that sit inside a
	// cross-platform app in a subdirectory.
	skipWrapped bool
	wrapped     map[string]bool
	// skip are folders, relative to the root, that a walk does not enter.
	skip map[string]bool
	// sources makes a walk leave out what Unity exports into a project.
	sources bool
}

// insideWrapper reports whether rel, a directory below the root, is inside a
// cross-platform project that starts below the root.
func (d *detector) insideWrapper(rel string) bool {
	if d.wrapped == nil {
		d.wrapped = map[string]bool{}
	}
	for dir := rel; dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		isWrapper, known := d.wrapped[dir]
		if !known {
			sub := &detector{root: d.path(dir), p: &Project{}}
			isWrapper = sub.wrapper()
			d.wrapped[dir] = isWrapper
		}
		if isWrapper {
			return true
		}
	}
	return false
}

// nativeRoot reports whether the directory itself is the top of a Gradle
// build or holds an Xcode project, as opposed to merely containing one
// further down.
func (d *detector) nativeRoot() bool {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if name == "settings.gradle" || name == "settings.gradle.kts" || strings.HasSuffix(name, ".xcodeproj") || strings.HasSuffix(name, ".xcworkspace") {
			return true
		}
	}
	return false
}

func (d *detector) note(format string, a ...any) {
	d.p.Notes = append(d.p.Notes, fmt.Sprintf(format, a...))
}

func (d *detector) found(i Integration) {
	d.p.Tenjin = append(d.p.Tenjin, i)
}

// path joins root with a slash-separated relative path.
func (d *detector) path(rel string) string {
	return filepath.Join(d.root, filepath.FromSlash(rel))
}

func (d *detector) exists(rel string) bool {
	_, err := os.Stat(d.path(rel))
	return err == nil
}

func (d *detector) isDir(rel string) bool {
	info, err := os.Stat(d.path(rel))
	return err == nil && info.IsDir()
}

// read returns a file's contents, or "" when it cannot be read.
func (d *detector) read(rel string) string {
	b, err := os.ReadFile(d.path(rel))
	if err != nil {
		return ""
	}
	return Text(b)
}

// Text is a file's content with Windows line endings read as plain ones,
// which every pattern that reads lines expects. Line numbers stay the same.
func Text(raw []byte) string {
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

// suggestSubprojects helps when the command was run too high, as in a
// monorepo: it names the directories, up to two levels down, that do hold a
// project.
func (d *detector) suggestSubprojects() {
	var found []string
	// look reports whether it found anything under rel.
	var look func(rel string, depth int) bool
	look = func(rel string, depth int) bool {
		entries, err := os.ReadDir(d.path(rel))
		if err != nil {
			return false
		}
		before := len(found)
		for _, e := range entries {
			if !e.IsDir() || skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			child := path.Join(rel, e.Name())
			sub := &detector{root: d.path(child), p: &Project{}}
			switch {
			case sub.wrapper(), sub.nativeRoot() && sub.native():
				found = append(found, fmt.Sprintf("%s (%s)", child, sub.p.Framework))
			// A folder of apps can itself look native, because native
			// projects are searched for a few levels down. Naming the apps
			// inside it is more use than naming the folder.
			case depth > 1 && look(child, depth-1):
			case sub.native():
				found = append(found, fmt.Sprintf("%s (%s)", child, sub.p.Framework))
			}
		}
		return len(found) > before
	}
	look(".", 2)
	if len(found) > 0 {
		sort.Strings(found)
		d.note("No project at this level. Found: %s. Run the command in one of them, or pass its path.", strings.Join(found, ", "))
	}
}
