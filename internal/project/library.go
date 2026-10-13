package project

import (
	"encoding/json"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Library is a project that is not an app but a library that wraps the
// Tenjin SDK, for apps to depend on: a CocoaPods pod, a Swift package, an
// Android library, a Flutter plugin or a React Native package. What the SDK
// needs (permissions, Info.plist keys, the SDK key, the launch) is set in
// the app that uses it, so a library is checked for what it declares only.
type Library struct {
	// Kind is "cocoapods", "swift_package", "android_library",
	// "flutter_plugin" or "react_native_library".
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	// Source is the file that declares the library and its Tenjin
	// dependency.
	Source string      `json:"source"`
	Tenjin Integration `json:"tenjin"`
	// Example is the folder of an app in the repository that uses the
	// library, when there is one: the app to check instead.
	Example string `json:"example,omitempty"`
}

// LibraryKindName is how a kind of library is called for people.
func LibraryKindName(kind string) string {
	switch kind {
	case "cocoapods":
		return "CocoaPods pod"
	case "swift_package":
		return "Swift package"
	case "android_library":
		return "Android library"
	case "flutter_plugin":
		return "Flutter plugin"
	case "react_native_library":
		return "React Native package"
	}
	return "library"
}

var (
	// s.dependency 'TenjinSDK', '~> 1.15', in a podspec or a subspec.
	podspecTenjin  = regexp.MustCompile(`\.dependency\s+['"]TenjinSDK(?:/[\w-]+)?['"](?:\s*,\s*['"]([^'"]+)['"])?`)
	podspecName    = regexp.MustCompile(`\.name\s*=\s*['"]([\w.-]+)['"]`)
	swiftLibrary   = regexp.MustCompile(`\.library\s*\(`)
	swiftPackageID = regexp.MustCompile(`Package\s*\(\s*name\s*:\s*"([^"]+)"`)
	androidLibrary = regexp.MustCompile(`com\.android\.library|android\.library`)
	// flutter:
	//   plugin:
	flutterPlugin = regexp.MustCompile(`(?m)^flutter:\s*\n(?:[ \t]+.*\n|\s*\n)*?[ \t]+plugin:`)
	pubspecName   = regexp.MustCompile(`(?m)^name:\s*([\w-]+)`)
	// tenjin_plugin: ^1.5.0, or a git or path dependency on it.
	pubspecTenjinVersion = regexp.MustCompile(`(?m)^\s+tenjin_plugin\s*:\s*\^?([0-9][\w.+-]*)?`)
)

// library recognises a library that wraps the Tenjin SDK at the root. It
// runs before the apps are looked for, and does not stop them: a library's
// repository often has an example app, which is found as usual and named.
func (d *detector) library() *Library {
	for _, find := range []func() *Library{d.podLibrary, d.swiftLibrary, d.flutterPluginLibrary, d.reactNativeLibrary} {
		if lib := find(); lib != nil {
			return lib
		}
	}
	return nil
}

func (d *detector) podLibrary() *Library {
	matches, _ := filepath.Glob(filepath.Join(d.root, "*.podspec"))
	for _, m := range matches {
		rel := filepath.Base(m)
		content := stripRubyComments(d.read(rel))
		dep := podspecTenjin.FindStringSubmatch(content)
		if dep == nil {
			continue
		}
		lib := &Library{Kind: "cocoapods", Source: rel, Tenjin: Integration{Package: "TenjinSDK", Version: dep[1], Source: rel}}
		if n := podspecName.FindStringSubmatch(content); n != nil {
			lib.Name = n[1]
		}
		return lib
	}
	return nil
}

func (d *detector) swiftLibrary() *Library {
	content := stripComments(d.read("Package.swift"))
	if content == "" || !swiftLibrary.MatchString(content) || !spmTenjin.MatchString(content) {
		return nil
	}
	lib := &Library{Kind: "swift_package", Source: "Package.swift", Tenjin: Integration{Package: "TenjinSDK (Swift Package)", Source: "Package.swift"}}
	if m := resolvedTenjinVers.FindStringSubmatch(d.read("Package.resolved")); m != nil {
		lib.Tenjin.Version = m[1]
	}
	if n := swiftPackageID.FindStringSubmatch(content); n != nil {
		lib.Name = n[1]
	}
	return lib
}

func (d *detector) flutterPluginLibrary() *Library {
	pubspec := d.read("pubspec.yaml")
	if !flutterPlugin.MatchString(pubspec) {
		return nil
	}
	lib := &Library{Kind: "flutter_plugin", Source: "pubspec.yaml"}
	if n := pubspecName.FindStringSubmatch(pubspec); n != nil {
		lib.Name = n[1]
	}
	if m := pubspecTenjinVersion.FindStringSubmatch(pubspec); m != nil {
		lib.Tenjin = Integration{Package: "tenjin_plugin", Version: m[1], Source: "pubspec.yaml"}
		return lib
	}
	// A plugin of its own around the native SDKs.
	if native := d.nativeSDKOf("android", "ios"); native != nil {
		lib.Tenjin = *native
		return lib
	}
	return nil
}

func (d *detector) reactNativeLibrary() *Library {
	var pkg struct {
		Name             string            `json:"name"`
		Dependencies     map[string]string `json:"dependencies"`
		PeerDependencies map[string]string `json:"peerDependencies"`
	}
	if json.Unmarshal([]byte(d.read("package.json")), &pkg) != nil {
		return nil
	}
	_, peer := pkg.PeerDependencies["react-native"]
	_, app := pkg.Dependencies["react-native"]
	if !peer || app {
		return nil
	}
	lib := &Library{Kind: "react_native_library", Name: pkg.Name, Source: "package.json"}
	if v, ok := pkg.Dependencies["react-native-tenjin"]; ok {
		lib.Tenjin = Integration{Package: "react-native-tenjin", Version: strings.TrimLeft(v, "^~"), Source: "package.json"}
		return lib
	}
	// A native module of its own around the native SDKs.
	if native := d.nativeSDKOf("android", "ios", "."); native != nil {
		lib.Tenjin = *native
		return lib
	}
	return nil
}

// androidLibrary recognises a repository whose only Android module that
// uses Tenjin is a library, with no app: what d.native finds nothing in.
func (d *detector) androidLibrary() *Library {
	var lib *Library
	d.walk(".", 3, func(p string, entry fs.DirEntry) {
		name := path.Base(p)
		if lib != nil || entry.IsDir() || (name != "build.gradle" && name != "build.gradle.kts") {
			return
		}
		content := stripComments(d.read(p))
		if !androidLibrary.MatchString(appliedPlugins(content)) {
			return
		}
		if m := tenjinMaven.FindStringSubmatch(content); m != nil {
			lib = &Library{Kind: "android_library", Name: path.Base(path.Dir(p)), Source: p, Tenjin: Integration{Package: "com.tenjin:android-sdk", Version: m[1], Source: p}}
		}
	})
	return lib
}

// nativeSDKOf finds the native SDK declared in a plugin's or a package's
// own native folders: an Android build file, or a podspec.
func (d *detector) nativeSDKOf(dirs ...string) *Integration {
	for _, dir := range dirs {
		for _, name := range []string{"build.gradle", "build.gradle.kts"} {
			rel := path.Join(dir, name)
			if m := tenjinMaven.FindStringSubmatch(stripComments(d.read(rel))); m != nil {
				return &Integration{Package: "com.tenjin:android-sdk", Version: m[1], Source: rel}
			}
		}
		specs, _ := filepath.Glob(filepath.Join(d.root, filepath.FromSlash(dir), "*.podspec"))
		for _, spec := range specs {
			rel, _ := filepath.Rel(d.root, spec)
			if m := podspecTenjin.FindStringSubmatch(stripRubyComments(d.read(filepath.ToSlash(rel)))); m != nil {
				return &Integration{Package: "TenjinSDK", Version: m[1], Source: filepath.ToSlash(rel)}
			}
		}
	}
	return nil
}
