// Package keyfill puts SDK keys into a project without a coding agent ever
// seeing them. The agent writes a placeholder where a key belongs, one per
// build (project.Placeholder names them); this package finds the
// placeholders and replaces them with the keys the developer gave the CLI.
// It touches nothing else.
package keyfill

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

// Placeholder is what an agent writes, as a string literal, where the SDK key
// of a platform's main build belongs.
func Placeholder(platform project.Platform) string {
	return project.Placeholder(string(platform))
}

var token = regexp.MustCompile(`__TENJIN_(ANDROID|IOS)_SDK_KEY(?:_[A-Z0-9_]+?)?__`)

// IsPlaceholder reports whether a string is one of the placeholders.
func IsPlaceholder(s string) bool {
	return token.MatchString(s) && token.FindString(s) == s
}

// Spot is one placeholder in the project.
type Spot struct {
	Platform project.Platform `json:"platform"`
	// Placeholder is the placeholder itself, which names the build.
	Placeholder string `json:"placeholder"`
	File        string `json:"file"`
	Line        int    `json:"line"`
}

// The files a key can end up in: source code in every language the SDKs
// support, and the build and configuration files a project may keep it in.
var exts = []string{
	".kt", ".kts", ".java", ".gradle", ".properties",
	".swift", ".m", ".mm", ".h", ".plist", ".xcconfig",
	".dart", ".js", ".jsx", ".ts", ".tsx", ".vue", ".json",
	".cs", ".cpp", ".ini", ".xml", ".yaml", ".yml",
}

const maxFileSize = 2 << 20

// Find lists every placeholder in the project, in file order.
func Find(root string) []Spot {
	var spots []Spot
	for _, rel := range files(root) {
		text, ok := read(root, rel)
		if !ok {
			continue
		}
		for _, m := range token.FindAllStringSubmatchIndex(text, -1) {
			spots = append(spots, Spot{
				Platform:    project.Platform(strings.ToLower(text[m[2]:m[3]])),
				Placeholder: text[m[0]:m[1]],
				File:        rel,
				Line:        strings.Count(text[:m[0]], "\n") + 1,
			})
		}
	}
	return spots
}

// Fill replaces the placeholders for which a key is given, keyed by
// placeholder, and returns the spots it filled. A placeholder with no key is
// left as it is.
func Fill(root string, keys map[string]string) ([]Spot, error) {
	var filled []Spot
	byFile := map[string][]Spot{}
	for _, s := range Find(root) {
		if keys[s.Placeholder] != "" {
			byFile[s.File] = append(byFile[s.File], s)
		}
	}
	names := make([]string, 0, len(byFile))
	for name := range byFile {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, rel := range names {
		text, ok := read(root, rel)
		if !ok {
			continue
		}
		replaced := token.ReplaceAllStringFunc(text, func(placeholder string) string {
			if key := keys[placeholder]; key != "" {
				return key
			}
			return placeholder
		})
		if err := writeFile(filepath.Join(root, filepath.FromSlash(rel)), replaced); err != nil {
			return filled, err
		}
		filled = append(filled, byFile[rel]...)
	}
	return filled, nil
}

func files(root string) []string {
	return project.SourceFiles(root, ".", exts...)
}

func read(root, rel string) (string, bool) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxFileSize {
		return "", false
	}
	raw, err := os.ReadFile(path)
	// Cheap filter: every placeholder starts this way.
	if err != nil || !strings.Contains(string(raw), "__TENJIN_") {
		return "", false
	}
	return string(raw), true
}

// writeFile replaces a file's content through a temporary file in the same
// folder, so the file is never left half written, and keeps its mode.
func writeFile(path, content string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tenjin-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
