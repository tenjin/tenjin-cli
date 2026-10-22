package project

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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
			if skipDirs[entry.Name()] || webCopy(relPath) {
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

// webCopy reports whether a folder is the copy of a web app's build that
// Capacitor puts in the native projects on each sync: output, which may be
// older than the source.
func webCopy(rel string) bool {
	rel = "/" + rel
	return strings.HasSuffix(rel, "/src/main/assets/public") || strings.HasSuffix(rel, "/App/App/public")
}

func isBundle(name string) bool {
	for _, suffix := range bundleSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// SourceFiles returns the project's source files with one of the given
// extensions, relative to root and slash-separated.
func SourceFiles(root, rel string, exts ...string) []string {
	d := &detector{root: root}
	var files []string
	d.walk(rel, 12, func(path string, entry fs.DirEntry) {
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
		case part == "test", part == "tests", part == "__tests__", part == "integration_test",
			strings.HasPrefix(part, "androidTest"), strings.HasPrefix(part, "test") && len(part) > 4 && part[4] >= 'A' && part[4] <= 'Z',
			strings.HasSuffix(part, "Tests"), strings.HasSuffix(part, "UITests"):
			return true
		}
	}
	base := path.Base(rel)
	for _, suffix := range []string{".test.ts", ".test.tsx", ".test.js", ".test.jsx", ".spec.ts", ".spec.tsx", ".spec.js", ".spec.jsx", "_test.dart", "Tests.swift", "Test.kt", "Test.java"} {
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
