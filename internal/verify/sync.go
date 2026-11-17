package verify

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

// checkNativeSync reports the framework's Tenjin plugin missing from a
// native project although package.json declares it: npx cap sync (Capacitor)
// or pod install (React Native on iOS) has not run since it was added, so
// the app built from that project has no SDK and every call to it fails.
// Only the file that step writes is read; where there is none, nothing is
// said. Flutter's build updates its native projects itself, and React
// Native's Android build links its plugins as it goes.
func (r *runner) checkNativeSync(spec frameworkSpec, t *project.Target) {
	pkg := spec.packages[0]
	declared := false
	for _, i := range r.p.Tenjin {
		// A plugin another package brings is that package's to link.
		declared = declared || (i.Package == pkg && i.Through == "")
	}
	if !declared {
		return
	}
	var file, step string
	switch {
	case r.p.Framework == project.Capacitor && t.Android != nil:
		file, step = path.Join(path.Dir(t.Android.ModuleDir), "capacitor.settings.gradle"), "npx cap sync android"
	case r.p.Framework == project.Capacitor && t.IOS != nil:
		// Capacitor 6 and later can use Swift packages instead of CocoaPods.
		file, step = path.Join(path.Dir(t.IOS.Xcodeproj), "CapApp-SPM", "Package.swift"), "npx cap sync ios"
		if !r.fileExists(file) {
			file = t.IOS.Podfile
		}
	case r.p.Framework == project.ReactNative && t.IOS != nil && t.IOS.Podfile != "":
		dir := path.Dir(t.IOS.Podfile)
		file, step = path.Join(dir, "Podfile.lock"), "pod install"
		if dir != "." {
			step += " in " + dir + "/"
		}
	}
	if file == "" || !r.fileExists(file) || strings.Contains(r.readSynced(file), pkg) {
		return
	}
	fix := "Run " + step + ", then build the app again."
	if !installed(r.p.Root, pkg) {
		manager, _ := npmManager(r.p.Root, "package.json")
		fix = "Run " + manager + " install, then " + step + ", then build the app again."
	}
	name := platformName(t.Platform)
	r.add(Finding{
		ID: "sdk.native_sync", Severity: Error, Platform: t.Platform,
		Title:  pkg + " is not in the " + name + " project yet",
		Detail: "package.json declares it, but " + file + " does not include it: " + step + " has not run since. Until it does, the " + name + " app has no Tenjin SDK and every call to it fails.",
		File:   file,
		Fix:    fix,
	})
}

// readSynced reads a file a sync writes without its comments, as its own
// tool reads it: a plugin line commented out is not there.
func (r *runner) readSynced(rel string) string {
	content := readProjectFile(r.p.Root, rel)
	switch path.Base(rel) {
	case "Podfile":
		return project.StripRubyComments(content)
	case "Podfile.lock":
		return content
	}
	return project.BlankComments(content)
}

// installed reports whether a JavaScript package is installed where Node
// finds it: in node_modules here or in a folder above, as a workspace
// hoists it, and through a link, as pnpm installs it. It only looks for
// the package, so it may follow a link out of the project.
func installed(root, pkg string) bool {
	dir, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	for ; ; dir = filepath.Dir(dir) {
		if info, err := os.Stat(filepath.Join(dir, "node_modules", filepath.FromSlash(pkg), "package.json")); err == nil && !info.IsDir() {
			return true
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}

// fileExists reports whether the project has a file at rel, which, like
// readProjectFile, cannot lead out of the project.
func (r *runner) fileExists(rel string) bool {
	root, err := os.OpenRoot(r.p.Root)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(filepath.FromSlash(rel))
	return err == nil && !info.IsDir()
}
