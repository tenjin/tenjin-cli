package project

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// skipDirs are never descended into: dependencies, build output and caches.
var skipDirs = map[string]bool{
	".git": true, ".gradle": true, ".idea": true, ".dart_tool": true, ".build": true,
	"node_modules": true, "Pods": true, "Carthage": true, "vendor": true,
	"build": true, "DerivedData": true, "DerivedDataCache": true,
	"Library": true, "Intermediate": true, "Binaries": true, "Saved": true,
}

// bundleSuffixes are directories that look like folders but are single
// artifacts; their contents are not project source.
var bundleSuffixes = []string{".xcodeproj", ".xcworkspace", ".framework", ".xcframework", ".app", ".xcassets"}

// walk visits files and directories under rel (slash-separated, relative to
// root), at most maxDepth levels down, in lexical order. fn receives the
// slash-separated path relative to root.
func (d *detector) walk(rel string, maxDepth int, fn func(path string, entry fs.DirEntry)) {
	start := d.path(rel)
	_ = filepath.WalkDir(start, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if path == start {
			return nil
		}
		relPath, _ := filepath.Rel(d.root, path)
		relPath = filepath.ToSlash(relPath)
		fromStart, _ := filepath.Rel(start, path)
		depth := strings.Count(filepath.ToSlash(fromStart), "/") + 1

		if entry.IsDir() {
			if skipDirs[entry.Name()] || webCopy(relPath) || d.skip[relPath] || d.sources && unityExport(path, entry.Name()) {
				return filepath.SkipDir
			}
			fn(relPath, entry)
			if depth >= maxDepth || isBundle(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		fn(relPath, entry)
		return nil
	})
}

// unityExport reports whether a folder is a project Unity wrote on a build:
// an Xcode project (beside Unity-iPhone.xcodeproj), or the Unity module of
// a Gradle one. Each holds copies of the plugins' code, the SDK's bridge
// included, which call everything the SDK has.
func unityExport(dir, name string) bool {
	if name == "unityLibrary" {
		return true
	}
	info, err := os.Stat(filepath.Join(dir, "Unity-iPhone.xcodeproj"))
	return err == nil && info.IsDir()
}

// appCode reports whether a source file is the app's own code: not a test,
// and in a Unity project not the Tenjin package's.
func (d *detector) appCode(rel string) bool {
	return !TestSource(rel) && (d.p.Framework != Unity || !UnitySDKFile(rel))
}

// UnitySDKFile reports whether a file of a Unity project is the Tenjin
// package's own: imported into Assets/Tenjin, embedded in Packages, or its
// iOS bridge, wherever it was copied.
func UnitySDKFile(rel string) bool {
	return strings.HasPrefix(rel, "Assets/Tenjin/") || strings.HasPrefix(rel, "Packages/com.tenjin.sdk/") || path.Base(rel) == "TenjinUnityInterface.mm"
}

// webCopy reports whether a folder is the copy of a web app's build that
// Capacitor puts in the native projects on each sync: output, which may be
// older than the source.
func webCopy(rel string) bool {
	rel = "/" + rel
	return strings.HasSuffix(rel, "/src/main/assets/public") || strings.HasSuffix(rel, "/App/App/public")
}

var (
	buildScript  = regexp.MustCompile(`"build"\s*:\s*"`)
	webDir       = regexp.MustCompile(`["']?webDir["']?\s*:\s*["']([^"']+)["']`)
	outputPath   = regexp.MustCompile(`"outputPath"\s*:\s*(?:\{[^{}]*"base"\s*:\s*)?"([^"]+)"`)
	outDirFlag   = regexp.MustCompile(`--out-?[dD]ir[= ]+['"]?([\w./-]+)`)
	distFolder   = regexp.MustCompile(`^dist[-_]`)
	bundlerFiles = []string{"angular.json", "ionic.config.json", "vite.config.ts", "vite.config.js", "vite.config.mjs", "webpack.config.js",
		"rollup.config.js", "rollup.config.mjs", "next.config.js", "next.config.mjs", "nuxt.config.ts", "svelte.config.js", "vue.config.js"}
)

// webOutput lists the folders, relative to root, that a web app's build
// writes: Capacitor's webDir, Angular's outputPath, a --outDir in
// package.json, www, dist and dist-*. Only when
// the project builds its web app, with a build script or a bundler: without
// one, webDir holds the web app as written, and is read.
func webOutput(root string) map[string]bool {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return ""
		}
		return Text(b)
	}
	pkg := read("package.json")
	if pkg == "" {
		return nil
	}
	built := buildScript.MatchString(pkg)
	for _, name := range bundlerFiles {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			built = true
		}
	}
	if !built {
		return nil
	}
	out := map[string]bool{"www": true, "dist": true}
	add := func(dir string) {
		dir = path.Clean(strings.TrimPrefix(filepath.ToSlash(dir), "./"))
		if dir != "." && !path.IsAbs(dir) && !strings.HasPrefix(dir, "..") {
			out[dir] = true
		}
	}
	for _, name := range []string{"capacitor.config.ts", "capacitor.config.js", "capacitor.config.json"} {
		if m := webDir.FindStringSubmatch(read(name)); m != nil {
			add(m[1])
		}
	}
	for _, m := range outputPath.FindAllStringSubmatch(read("angular.json"), -1) {
		add(m[1])
	}
	// A build for another target or for tests often writes beside dist:
	// vite build --outDir dist-staging.
	for _, m := range outDirFlag.FindAllStringSubmatch(pkg, -1) {
		add(m[1])
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.IsDir() && distFolder.MatchString(e.Name()) {
			out[e.Name()] = true
		}
	}
	return out
}

func isBundle(name string) bool {
	for _, suffix := range bundleSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// sourceDepth is how many folders down source files are looked for: enough
// for a module's package folders, as in
// feature/home/src/main/kotlin/com/company/app/feature/home/ui/screen.
const sourceDepth = 24

// SourceFiles returns the project's source files with one of the given
// extensions, relative to root and slash-separated. A web build's output,
// and a project Unity exported, are not source.
func SourceFiles(root, rel string, exts ...string) []string {
	d := &detector{root: root, skip: webOutput(root), sources: true}
	var files []string
	d.walk(rel, sourceDepth, func(path string, entry fs.DirEntry) {
		if entry.IsDir() {
			return
		}
		for _, ext := range exts {
			if strings.HasSuffix(path, ext) {
				files = append(files, path)
				return
			}
		}
	})
	return files
}

// TestSource reports whether a file is test code, which says nothing about
// what the app does: Android's test source sets, Xcode test targets, and
// the usual JavaScript and Dart test folders and files.
func TestSource(rel string) bool {
	for _, part := range strings.Split(path.Dir(rel), "/") {
		switch {
		case part == "test", part == "tests", part == "__tests__", part == "__mocks__", part == "integration_test", part == "e2e",
			strings.HasPrefix(part, "androidTest"), strings.HasPrefix(part, "test") && len(part) > 4 && part[4] >= 'A' && part[4] <= 'Z',
			strings.HasSuffix(part, "Tests"), strings.HasSuffix(part, "UITests"):
			return true
		}
	}
	base := path.Base(rel)
	for _, suffix := range []string{".test.ts", ".test.tsx", ".test.js", ".test.jsx", ".spec.ts", ".spec.tsx", ".spec.js", ".spec.jsx",
		".e2e.ts", ".e2e.tsx", ".e2e.js", ".e2e.jsx", "_test.dart", "Tests.swift", "Test.kt", "Test.java"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// maxFingerprintSize keeps large binaries out of a fingerprint; source and
// configuration files are far smaller.
const maxFingerprintSize = 4 << 20

// Fingerprint maps each of the project's files to a hash of its content,
// skipping dependencies and build output. Comparing two fingerprints shows
// what changed in between.
func Fingerprint(root string) map[string]string {
	d := &detector{root: root}
	prints := map[string]string{}
	d.walk(".", 16, func(path string, entry fs.DirEntry) {
		if entry.IsDir() {
			return
		}
		info, err := entry.Info()
		if err != nil || info.Size() > maxFingerprintSize {
			return
		}
		raw, err := os.ReadFile(d.path(path))
		if err != nil {
			return
		}
		sum := sha256.Sum256(raw)
		prints[path] = hex.EncodeToString(sum[:])
	})
	return prints
}

// Changed lists the files that differ between two fingerprints: added,
// modified or removed. The result is sorted.
func Changed(before, after map[string]string) []string {
	var changed []string
	for path, hash := range after {
		if before[path] != hash {
			changed = append(changed, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}
