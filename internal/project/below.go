package project

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Below finds projects in the folders under root, up to two levels down, for
// when root itself is not one: a repository that keeps its app in a folder
// of its own, such as app/ or mobile/. It returns their paths relative to
// root, the shallowest first, at most five.
func Below(root string) []string {
	var found []string
	var look func(dir string, depth int)
	look = func(dir string, depth int) {
		if depth > 2 || len(found) >= 5 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || skippedDirs[name] {
				continue
			}
			sub := filepath.Join(dir, name)
			if hasProjectMarker(sub) {
				if p, err := Detect(sub); err == nil && p.Framework != Unknown {
					rel, _ := filepath.Rel(root, sub)
					found = append(found, filepath.ToSlash(rel))
					continue
				}
			}
			look(sub, depth+1)
		}
	}
	look(root, 1)
	sort.SliceStable(found, func(i, j int) bool { return strings.Count(found[i], "/") < strings.Count(found[j], "/") })
	return found
}

// skippedDirs hold dependencies or build output, never the app.
var skippedDirs = map[string]bool{"node_modules": true, "Pods": true, "build": true, "dist": true, "vendor": true, "Library": true, "Temp": true}

// hasProjectMarker reports whether a folder holds a file that starts one of
// the projects Detect knows.
func hasProjectMarker(dir string) bool {
	for _, marker := range []string{"pubspec.yaml", "package.json", "settings.gradle", "settings.gradle.kts", "build.gradle", "build.gradle.kts", "ProjectSettings/ProjectVersion.txt"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.xcodeproj"))
	uproject, _ := filepath.Glob(filepath.Join(dir, "*.uproject"))
	return len(matches) > 0 || len(uproject) > 0
}
