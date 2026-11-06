package verify

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/keyfill"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/track"
)

// checkKey asks Tenjin whether each of the target's builds has an SDK key
// it accepts, for that build's bundle ID and platform. Findings never
// contain a key itself, only its last four characters.
func (r *runner) checkKey(t *project.Target, src source) {
	r.givenPassedOver(t)
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

// givenPassedOver says that a key given without naming a build was not
// checked for a target with several builds (GivenKeys.For), and how to give
// each build's. When the source is preferred the caller has said so itself.
func (r *runner) givenPassedOver(t *project.Target) {
	if !r.opts.Given.PassedOver(t) || r.opts.Given.PreferSource {
		return
	}
	var flags, variables []string
	for _, b := range t.Builds {
		flags = append(flags, "--sdk-key "+b.ID+"=<key>")
		variables = append(variables, KeyVariable(b.ID))
	}
	r.add(Finding{
		ID: "key.unchecked", Severity: Info, Platform: t.Platform,
		Title:  "The SDK key from " + r.opts.Given.All.Source + " was not checked: this project's " + platformName(t.Platform) + " builds are each an app of its own in Tenjin, with its own key",
		Detail: "A key given without naming a build serves a platform with one build only.",
		Fix:    "Give each build's key: " + strings.Join(flags, " ") + ", or in " + strings.Join(variables, " and ") + ".",
	})
}

// offlineKeys says, without asking Tenjin, which builds have a key written
// in the project: something an offline run can still tell.
func (r *runner) offlineKeys(t *project.Target, src source) {
	multi := t.SeveralBuilds()
	for _, b := range t.Builds {
		candidates, _, source := r.candidateKeys(t, src, b.ID, multi)
		if len(candidates) == 0 || source != "" {
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
	candidates, unsure, source := r.candidateKeys(t, src, b.ID, multi)
	if len(candidates) == 0 {
		f := scope
		f.ID, f.Severity = "key.unchecked", Info
		f.Title = "The SDK key was not checked" + label
		f.Detail = "It is not written as a string literal in the source."
		f.Fix = "To check it against Tenjin, give the key with --sdk-key <key> or TENJIN_SDK_KEY."
		if r.builds() > 1 {
			// One key without a build would be checked against every app.
			f.Fix = "To check it against Tenjin, give the key with --sdk-key " + b.ID + "=<key> or " + KeyVariable(b.ID) + "."
		}
		if multi {
			f.Detail = "No key Tenjin could check for this build was found in the project."
			f.Fix = "To check it against Tenjin, give the key with --sdk-key " + b.ID + "=<key> or " + KeyVariable(b.ID) + "."
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
			// A check that cannot tell which key the build sends must not
			// say it passed.
			if unsure != "" {
				f.ID, f.Severity = "key.unverified", Info
				f.Title += ", but whether the app sends it could not be read"
				f.Detail = unsure
				f.Fix = "Make sure this is the key the build passes to the SDK."
				if source == "" {
					f.Fix = "To have the key checked as this build's, put it in the build's own flavor block of the build file, as buildConfigField(\"String\", \"TENJIN_SDK_KEY\", \"\\\"<key>\\\"\"), or in the flavor's source set."
				}
			}
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
// builds counts the project's builds, each its own app in Tenjin.
func (r *runner) builds() int {
	n := 0
	for _, t := range r.p.Targets {
		n += len(t.Builds)
	}
	return n
}

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

// candidateKeys returns the keys to check for a build, why the check cannot
// tell whether the build sends them ("" when it can), and where they came
// from when that is outside the project. A key given from outside wins over
// the ones written in source, unless the options prefer the source. wide
// adds the keys kept in build configuration, and ties each key to the
// builds it goes into, so that one build's key never passes for another.
func (r *runner) candidateKeys(t *project.Target, src source, build string, wide bool) (keys []keyLiteral, unsure, source string) {
	seen := map[string]bool{}
	var all []keyLiteral
	// With several builds one key may sit in two builds' blocks, and each
	// place counts for its own build.
	add := func(k keyLiteral) {
		id := k.Value
		if wide {
			id += "|" + k.File + "|" + strconv.Itoa(k.Line)
		}
		if !isPlaceholder(k.Value) && !strayWhitespace(k.Value) && !seen[id] {
			seen[id] = true
			all = append(all, k)
		}
	}
	for _, k := range src.keys {
		add(k)
	}
	keys = all
	if wide {
		for _, k := range r.configuredKeys() {
			add(k)
		}
		keys, unsure = r.buildsOwnKeys(t, build, all)
	}
	given, ok := r.opts.Given.For(t, build)
	if !ok || (r.opts.Given.PreferSource && len(keys) > 0) {
		return keys, unsure, ""
	}
	// The project initialises the SDK with a key that is not a literal, and
	// holds no placeholder for the CLI to write the given key over: what the
	// app sends is that value, which a check of the given key says nothing
	// about.
	if len(src.inits) > 0 && len(keys) == 0 && len(src.keys) == 0 && !r.placeholderInProject(project.Placeholder(build)) {
		unsure = "The SDK is initialized with a key that is not written as a string literal, such as a BuildConfig field or a variable, so the key the project holds could not be read. The key checked is " + mask(given.Value) + ", from " + given.Source + "."
		if given.Source == "" {
			unsure = "The SDK is initialized with a key that is not written as a string literal, such as a BuildConfig field or a variable, so the key the project holds could not be read."
		}
	} else {
		unsure = ""
	}
	return []keyLiteral{{Value: given.Value}}, unsure, given.Source
}

// buildsOwnKeys narrows a target's keys to one build's: those in its own
// flavor block or source set, otherwise those every build shares, otherwise
// those that cannot be tied to any build, with why the check is then unsure.
// A key tied to another build is never this one's.
func (r *runner) buildsOwnKeys(t *project.Target, build string, all []keyLiteral) ([]keyLiteral, string) {
	var own, shared, loose []keyLiteral
	for _, k := range all {
		builds, everyBuild := r.keyBuilds(t, k)
		switch {
		case builds[build]:
			own = append(own, k)
		case len(builds) > 0:
		case everyBuild:
			shared = append(shared, k)
		default:
			loose = append(loose, k)
		}
	}
	own, shared, loose = distinct(own), distinct(shared), distinct(loose)
	switch {
	case len(own) > 0:
		return own, ""
	case len(shared) == 1:
		return shared, ""
	case len(shared) > 1:
		return shared, "The project holds " + maskAll(shared) + " where every build reads them, and which of them this build sends could not be read."
	case len(loose) > 0:
		return loose, "The key is in " + loose[0].File + ", which the checks cannot tie to this build or to every build."
	}
	return nil, ""
}

// distinct keeps the first of each key.
func distinct(keys []keyLiteral) []keyLiteral {
	seen := map[string]bool{}
	var out []keyLiteral
	for _, k := range keys {
		if !seen[k.Value] {
			seen[k.Value] = true
			out = append(out, k)
		}
	}
	return out
}

// keyBuilds says which builds of an Android target a key literal goes into:
// the builds whose flavor block or source set holds it, or every build that
// has no key of its own, for the module's main source set and the rest of
// its build file. Neither means it cannot be told.
func (r *runner) keyBuilds(t *project.Target, k keyLiteral) (builds map[string]bool, everyBuild bool) {
	builds = map[string]bool{}
	if t.Android == nil {
		return builds, false
	}
	owners := func(name string) {
		for _, b := range t.Builds {
			if b.HasSourceSet(name) {
				builds[b.ID] = true
			}
		}
	}
	for _, module := range append([]string{t.Android.ModuleDir}, t.Android.LibraryModules...) {
		prefix := module + "/"
		if module == "." {
			prefix = ""
		}
		if k.File == prefix+"build.gradle" || k.File == prefix+"build.gradle.kts" {
			for _, block := range r.flavorBlocks(k.File) {
				if k.Line >= block.FirstLine && k.Line <= block.EndLine {
					owners(block.Name)
					return builds, false
				}
			}
			return builds, true
		}
		if rest, ok := strings.CutPrefix(k.File, prefix+"src/"); ok {
			set, _, _ := strings.Cut(rest, "/")
			if set == "main" || set == "release" || set == "debug" {
				return builds, true
			}
			owners(set)
			return builds, false
		}
	}
	return builds, false
}

// flavorBlocks reads the flavors' blocks of a build file, once.
func (r *runner) flavorBlocks(rel string) []project.FlavorBlock {
	if r.flavors == nil {
		r.flavors = map[string][]project.FlavorBlock{}
	}
	blocks, ok := r.flavors[rel]
	if !ok {
		blocks = project.FlavorBlocks(blankComments(r.readRel(rel)))
		r.flavors[rel] = blocks
	}
	return blocks
}

// placeholderInProject reports whether a build's placeholder is written in
// the project, for the CLI to write the key over.
func (r *runner) placeholderInProject(placeholder string) bool {
	for _, spot := range r.spots() {
		if spot.Placeholder == placeholder {
			return true
		}
	}
	return false
}

func (r *runner) spots() []keyfill.Spot {
	if r.placeholders == nil {
		spots := keyfill.Find(r.p.Root)
		r.placeholders = &spots
	}
	return *r.placeholders
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
		if err != nil || testSource(rel) || !strings.Contains(strings.ToLower(string(raw)), "tenjin") {
			continue
		}
		// A key in a comment is not one the app sends, but a comment that
		// names Tenjin still says whose the key beside it is.
		lines := strings.Split(project.Text(raw), "\n")
		code := strings.Split(withoutComments(rel, project.Text(raw)), "\n")
		for i := range lines {
			named := false
			for j := max(0, i-nearTenjin); j <= i; j++ {
				if strings.Contains(strings.ToLower(lines[j]), "tenjin") {
					named = true
				}
			}
			if !named {
				continue
			}
			for _, m := range keyShaped.FindAllStringSubmatch(code[i], -1) {
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
		f.Detail = "The SDK key " + mask(k.Value) + " is recognized, but the organization it belongs to has no app with this bundle ID on this platform, so events from this build are refused. Either the build's bundle ID is not the app's, or the key comes from a different organization than the app."
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
