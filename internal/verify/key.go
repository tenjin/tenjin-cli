package verify

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/track"
	"github.com/tenjin/tenjin-cli/internal/project"
)

// checkKey asks Tenjin whether each of the target's builds has an SDK key
// it accepts, for that build's bundle ID and platform. Findings never
// contain a key itself, only its last four characters.
func (r *runner) checkKey(t *project.Target, src source) {
	if r.opts.Keys == nil {
		r.offlineKeys(t, src)
		return
	}
	if len(t.Builds) == 0 {
		r.add(Finding{
			ID: "key.unchecked", Severity: Info, Platform: t.Platform,
			Title: "The SDK key was not checked because the bundle ID could not be read",
		})
		return
	}
	multi := t.SeveralBuilds()
	for _, b := range t.Builds {
		if !r.checkBuildKey(t, b, src, multi) {
			return // Tenjin could not be reached; once is enough to say so
		}
	}
}

// offlineKeys says, without asking Tenjin, which builds have a key written
// in the project: something an offline run can still tell.
func (r *runner) offlineKeys(t *project.Target, src source) {
	multi := t.SeveralBuilds()
	for _, b := range t.Builds {
		candidates, _ := r.candidateKeys(src, b.ID, multi)
		if len(candidates) == 0 {
			continue
		}
		f := Finding{
			ID: "key.unchecked", Severity: Info, Platform: t.Platform,
			Title: "An SDK key is in the project (" + maskAll(candidates) + "), and was not checked against Tenjin",
			File:  candidates[0].File, Line: candidates[0].Line,
		}
		if multi {
			f.Build = b.ID
			f.Title = "An SDK key for " + buildLabel(b) + " is in the project (" + maskAll(candidates) + "), and was not checked against Tenjin"
		}
		r.add(f)
	}
}

// checkBuildKey checks one build's key, and reports whether Tenjin could be
// reached. With several builds, keys kept in build configuration count too,
// since that is where a key per flavor usually lives.
func (r *runner) checkBuildKey(t *project.Target, b project.Build, src source, multi bool) bool {
	scope := Finding{Platform: t.Platform}
	label := ""
	if multi {
		scope.Build = b.ID
		label = " (" + buildLabel(b) + ")"
	}
	candidates, source := r.candidateKeys(src, b.ID, multi)
	if len(candidates) == 0 {
		f := scope
		f.ID, f.Severity = "key.unchecked", Info
		f.Title = "The SDK key was not checked" + label
		f.Detail = "It is not written as a string literal in the source."
		f.Fix = "Pass --sdk-key, or set TENJIN_SDK_KEY, to check it against Tenjin."
		if multi {
			f.Detail = "No key Tenjin could check for this build was found in the project."
			f.Fix = "Pass --sdk-key " + b.ID + "=<key>, or set " + KeyVariable(b.ID) + ", to check it against Tenjin."
		}
		r.add(f)
		return true
	}

	// The project may hold one key per build. A build is fine if any of
	// them is valid for it; otherwise report the most informative refusal.
	var worst *Finding
	for _, k := range candidates {
		result, err := r.opts.Keys.CheckKey(r.ctx, k.Value, b.BundleID, b.TenjinPlatform)
		if err != nil {
			f := scope
			f.Build = ""
			f.ID, f.Severity = "key.unreachable", Warning
			f.Title = "Could not reach Tenjin to check the SDK key"
			f.Detail = err.Error()
			r.add(f)
			return false
		}
		if result.Status == track.KeyValid {
			title := "Tenjin accepts the SDK key " + mask(k.Value)
			// Without this, a key given from outside reads as if it were
			// the one in the source, which may be a placeholder.
			if source != "" {
				title += " (from " + source + ")"
			}
			f := scope
			f.ID, f.Severity = "key.valid", Pass
			f.Title = title + " for " + b.BundleID + label
			f.File, f.Line = k.File, k.Line
			r.add(f)
			return true
		}
		if f := keyFinding(scope, b, k, result); worst == nil || rank(f.ID) > rank(worst.ID) {
			worst = &f
		}
	}
	if multi && worst.ID == "key.rejected" {
		worst.Title = "No SDK key in the project is accepted for " + b.BundleID + label
		worst.Detail = "Each build is its own app in Tenjin, with its own key. Checked: " + maskAll(candidates) + "."
		worst.Fix = "Use the SDK key from the " + project.StoreName(b.TenjinPlatform) + " app's page in the Tenjin dashboard for this build, or run tenjin setup."
		worst.File, worst.Line = "", 0
	}
	r.add(*worst)
	return true
}

// buildLabel names a build for people: "the amazon build, Amazon Appstore".
func buildLabel(b project.Build) string {
	name := b.Name()
	if name == "" {
		name = "main"
	}
	return "the " + name + " build, " + project.StoreName(b.TenjinPlatform)
}

// KeyVariable is the environment variable that gives a build's key:
// TENJIN_SDK_KEY_ANDROID, TENJIN_SDK_KEY_ANDROID_AMAZON_PROD.
func KeyVariable(build string) string {
	platform, name, _ := strings.Cut(build, ":")
	v := "TENJIN_SDK_KEY_" + strings.ToUpper(platform)
	if name != "" {
		v += "_" + project.Shout(name)
	}
	return v
}

func maskAll(keys []keyLiteral) string {
	var out []string
	for _, k := range keys {
		out = append(out, mask(k.Value))
	}
	return strings.Join(out, ", ")
}

// candidateKeys returns the keys to check for a build, and where they came
// from when that is outside the project. A key given from outside wins over
// the ones written in source, unless the options prefer the source. wide
// adds the keys kept in build configuration.
func (r *runner) candidateKeys(src source, build string, wide bool) (keys []keyLiteral, source string) {
	seen := map[string]bool{}
	add := func(k keyLiteral) {
		if !isPlaceholder(k.Value) && !seen[k.Value] {
			seen[k.Value] = true
			keys = append(keys, k)
		}
	}
	for _, k := range src.keys {
		add(k)
	}
	if wide {
		for _, k := range r.configuredKeys() {
			add(k)
		}
	}
	given, ok := r.opts.Given.For(build)
	if !ok || (r.opts.Given.PreferSource && len(keys) > 0) {
		return keys, ""
	}
	return []keyLiteral{{Value: given.Value}}, given.Source
}

// Keys kept outside the init call: a flavor's buildConfigField, a properties
// file, a constant. Only literals on a line that names Tenjin are taken, so
// that no other service's secret, such as one on the next line, is ever sent
// to Tenjin.
var (
	keyShaped    = regexp.MustCompile(`(?:["']|=\s*)([A-Z0-9]{24,64})\b`)
	configExts   = []string{".kt", ".kts", ".java", ".gradle", ".properties", ".swift", ".m", ".dart", ".js", ".jsx", ".ts", ".tsx", ".json", ".xcconfig", ".plist", ".cs"}
	nearTenjin   = 0
	maxKeyChecks = 8
)

func (r *runner) configuredKeys() []keyLiteral {
	if r.configured != nil {
		return *r.configured
	}
	keys := []keyLiteral{}
	r.configured = &keys
	for _, rel := range project.SourceFiles(r.p.Root, ".", configExts...) {
		path := filepath.Join(r.p.Root, filepath.FromSlash(rel))
		if info, err := os.Stat(path); err != nil || info.Size() > maxSourceFileSize {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil || !strings.Contains(strings.ToLower(string(raw)), "tenjin") {
			continue
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			named := false
			for j := max(0, i-nearTenjin); j <= i; j++ {
				if strings.Contains(strings.ToLower(lines[j]), "tenjin") {
					named = true
				}
			}
			if !named {
				continue
			}
			for _, m := range keyShaped.FindAllStringSubmatch(line, -1) {
				keys = append(keys, keyLiteral{Value: m[1], File: rel, Line: i + 1})
			}
			if len(keys) >= maxKeyChecks {
				return keys
			}
		}
	}
	return keys
}

func keyFinding(scope Finding, b project.Build, k keyLiteral, result track.KeyCheck) Finding {
	f := scope
	f.File, f.Line, f.Detail = k.File, k.Line, "Tenjin answered: "+result.Detail+"."
	switch result.Status {
	case track.KeyNoApp:
		f.ID = "key.no_app"
		f.Severity = Error
		f.Title = "Tenjin has no " + b.TenjinPlatform + " app with the bundle ID " + b.BundleID
		f.Detail = "The SDK key " + mask(k.Value) + " is recognised, but the organization it belongs to has no app with this bundle ID on this platform, so events from this build are refused. Either the build's bundle ID is not the app's, or the key comes from a different organization than the app."
		f.Fix = "Use the SDK key from this app's own page in the dashboard, or correct the bundle ID: a debug suffix or product flavor makes it a different app. If the app does not exist yet, create it with exactly this bundle ID and platform."
	case track.KeyRejected:
		f.ID = "key.rejected"
		f.Severity = Error
		f.Title = "Tenjin does not accept the SDK key " + mask(k.Value) + " for " + b.BundleID
		f.Detail = "Either the key does not exist, or it belongs to a different app. Each app, one per platform and store, has its own key."
		if !b.Main() || b.TenjinPlatform != string(b.ID) {
			f.Detail = "Either the key does not exist, or it belongs to a different app. Each build is its own app in Tenjin, with its own key."
		}
		f.Fix = "Copy the SDK key from this app's page in the Tenjin dashboard."
	case track.KeyDisabled:
		f.ID = "key.disabled"
		f.Severity = Error
		f.Title = "The SDK key " + mask(k.Value) + " is disabled"
		f.Fix = "Enable the key in the Tenjin dashboard, or use another of the app's keys."
	default:
		f.ID = "key.unclear"
		f.Severity = Warning
		f.Title = "Tenjin gave an unexpected answer when checking the SDK key " + mask(k.Value)
	}
	return f
}

// rank orders refusals by how much they tell the user. "No such app" names
// the exact mismatch, so it beats a bare rejection.
func rank(id string) int {
	switch id {
	case "key.no_app":
		return 3
	case "key.disabled":
		return 2
	case "key.rejected":
		return 1
	}
	return 0
}

// mask shows only the end of a key, enough to tell two keys apart.
func mask(key string) string {
	if len(key) <= 4 {
		return "…"
	}
	return "…" + key[len(key)-4:]
}
