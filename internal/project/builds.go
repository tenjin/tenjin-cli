package project

import (
	"path"
	"regexp"
	"strings"
	"unicode"
)

// Build is one shipped build of a target that is its own app in Tenjin.
// Tenjin tells apps apart by bundle ID and platform, and for Android the
// platform depends on the app store the build declares: a Google Play build,
// an Amazon build and an other-store build of one bundle ID are three apps,
// each with its own SDK key. Product flavors that share both share one build.
type Build struct {
	// ID names the build in flags and environment variables: the platform
	// ("android", "ios") for a target's main build, "android:<variant>" for
	// another.
	ID string `json:"id"`
	// Variants are the Android build variants, flavors combined, that
	// produce this build. Empty for a target without flavors.
	Variants []string `json:"variants,omitempty"`
	BundleID string   `json:"bundle_id"`
	// Store is what the build declares in TENJIN_APP_STORE, and StoreSource
	// the file it was read from. Empty when it is not declared.
	Store       string `json:"store,omitempty"`
	StoreSource string `json:"store_source,omitempty"`
	// TenjinPlatform is the platform of the build's app in Tenjin: "ios",
	// "android", "amazon" or "android_other".
	TenjinPlatform string `json:"tenjin_platform"`
	// SDKKeyPlaceholder is what a coding agent writes where this build's
	// SDK key belongs. The CLI writes the key in its place.
	SDKKeyPlaceholder string `json:"sdk_key_placeholder"`
	// Declared is each variant's TENJIN_APP_STORE and where it was read,
	// for the checks: variants merged into one build may differ in it.
	Declared []DeclaredStore `json:"-"`
}

// DeclaredStore is what one variant declares in TENJIN_APP_STORE.
type DeclaredStore struct {
	Variant, Store, Source string
}

// Main reports whether this is the target's main build, the one named by
// the platform alone.
func (b Build) Main() bool { return !strings.Contains(b.ID, ":") }

// SeveralBuilds reports whether builds of this target are told apart: it
// ships more than one, or the one left is not the platform's main build.
func (t *Target) SeveralBuilds() bool {
	return len(t.Builds) > 1 || (len(t.Builds) == 1 && !t.Builds[0].Main())
}

// Name is the part of the ID after the platform, "" for the main build.
func (b Build) Name() string {
	_, name, _ := strings.Cut(b.ID, ":")
	return name
}

// Platform is the operating system of a build ID.
func BuildPlatform(id string) Platform {
	platform, _, _ := strings.Cut(id, ":")
	return Platform(platform)
}

// Placeholder is what stands in for a build's SDK key in the project until
// the CLI writes the key: __TENJIN_ANDROID_SDK_KEY__ for the main Android
// build, __TENJIN_ANDROID_SDK_KEY_AMAZON_PROD__ for the variant amazonProd.
func Placeholder(id string) string {
	platform, name, _ := strings.Cut(id, ":")
	p := "__TENJIN_" + strings.ToUpper(platform) + "_SDK_KEY"
	if name != "" {
		p += "_" + Shout(name)
	}
	return p + "__"
}

// Shout turns a variant name into the form environment variables and
// placeholders use: amazonProd becomes AMAZON_PROD.
func Shout(name string) string {
	var b strings.Builder
	prevLower := false
	for _, r := range name {
		switch {
		case unicode.IsUpper(r) && prevLower:
			b.WriteByte('_')
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
		default:
			b.WriteByte('_')
		}
		prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
	}
	return b.String()
}

// SDKStore is the store the Android SDK reads from a TENJIN_APP_STORE
// value: it lowercases it, drops whitespace and takes it as one of its
// AppStoreType names (SourceAppStoreLoader). Anything else is
// "unspecified", which Tenjin files under Google Play.
func SDKStore(declared string) string {
	v := strings.ToLower(strings.Join(strings.Fields(declared), ""))
	switch v {
	case "googleplay", "amazon", "other", "unspecified":
		return v
	}
	return "unspecified"
}

// TenjinPlatform is the platform the collector files an Android build
// under, given the store it declares in TENJIN_APP_STORE, as the SDK reads
// it. It mirrors the collector's own rule.
func TenjinPlatform(platform Platform, store string) string {
	if platform != Android {
		return string(platform)
	}
	switch SDKStore(store) {
	case "amazon":
		return "amazon"
	case "other":
		return "android_other"
	}
	return "android"
}

// StoreName is how a store is called for people.
func StoreName(tenjinPlatform string) string {
	switch tenjinPlatform {
	case "amazon":
		return "Amazon Appstore"
	case "android_other":
		return "another Android store"
	case "ios":
		return "App Store"
	}
	return "Google Play"
}

func mainBuild(t *Target) Build {
	id := string(t.Platform)
	return Build{ID: id, BundleID: t.BundleID, TenjinPlatform: string(t.Platform), SDKKeyPlaceholder: Placeholder(id)}
}

var (
	flavorDimensions = regexp.MustCompile(`flavorDimensions`)
	quoted           = regexp.MustCompile(`["']([\w-]+)["']`)
	flavorDimension  = regexp.MustCompile(`\bdimension\s*=?\s*["']([\w-]+)["']`)
	// A flavor block: create("amazon") { in Kotlin, amazon { in Groovy.
	namedBlock   = regexp.MustCompile(`(?:(?:create|register|maybeCreate|getByName)\(\s*"([\w-]+)"\s*\)|\b([A-Za-z_]\w*))\s*\{`)
	placeholders = regexp.MustCompile(`["']?([A-Za-z_]\w*)["']?\s*(?:\]\s*=|:|\bto\b|,)\s*["']([^"'\n]*)["']`)
	tenjinStore  = regexp.MustCompile(`android:name\s*=\s*"TENJIN_APP_STORE"[^>]*?android:value\s*=\s*"([^"]*)"|android:value\s*=\s*"([^"]*)"[^>]*?android:name\s*=\s*"TENJIN_APP_STORE"`)
	placeholder  = regexp.MustCompile(`^\$\{(\w+)\}$`)
)

// flavor is one product flavor as the build file declares it.
type flavor struct {
	name, dimension string
	appID, suffix   string
	placeholders    map[string]string
}

// androidBuilds lists the builds of an Android module: one per distinct
// bundle ID and store across its build variants. Release builds are meant;
// a build type's suffix, such as .debug, is not a shipped build.
func (d *detector) androidBuilds(t *Target, content, flavorsBlock, rest string) []Build {
	info := t.Android
	defaults := manifestPlaceholders(blockNamed(rest, "defaultConfig"))
	defaultSuffix := ""
	if m := applicationSuffix.FindStringSubmatch(blockNamed(rest, "defaultConfig")); m != nil {
		defaultSuffix = m[1]
	}
	mainStore, mainStoreSource := d.declaredStore(path.Join(info.ModuleDir, "src/main/AndroidManifest.xml"), defaults)

	flavors := parseFlavors(flavorsBlock)
	if len(flavors) == 0 {
		if t.BundleID == "" {
			return nil
		}
		b := mainBuild(t)
		b.BundleID = t.BundleID + defaultSuffix
		b.Store, b.StoreSource = mainStore, mainStoreSource
		b.TenjinPlatform = TenjinPlatform(Android, mainStore)
		b.Declared = []DeclaredStore{{Store: mainStore, Source: mainStoreSource}}
		return []Build{b}
	}

	var builds []Build
	index := map[string]int{}
	for _, variant := range variants(flavors, dimensionOrder(content)) {
		appID, suffix := t.BundleID, defaultSuffix
		// The first dimension's flavor takes precedence for the ID; suffixes
		// add up in dimension order.
		for i := len(variant) - 1; i >= 0; i-- {
			if variant[i].appID != "" {
				appID = variant[i].appID
			}
		}
		for _, f := range variant {
			suffix += f.suffix
		}
		if appID == "" {
			continue
		}
		bundle := appID + suffix

		// The store comes from the most specific manifest that declares it,
		// with placeholders filled from the flavors, then the defaults.
		values := map[string]string{}
		for k, v := range defaults {
			values[k] = v
		}
		for i := len(variant) - 1; i >= 0; i-- {
			for k, v := range variant[i].placeholders {
				values[k] = v
			}
		}
		name := variantName(variant)
		store, storeSource := d.declaredStore(path.Join(info.ModuleDir, "src", name, "AndroidManifest.xml"), values)
		for _, f := range variant {
			if store != "" {
				break
			}
			store, storeSource = d.declaredStore(path.Join(info.ModuleDir, "src", f.name, "AndroidManifest.xml"), values)
		}
		if store == "" {
			store, storeSource = d.declaredStore(path.Join(info.ModuleDir, "src/main/AndroidManifest.xml"), values)
		}

		platform := TenjinPlatform(Android, store)
		declared := DeclaredStore{Variant: name, Store: store, Source: storeSource}
		key := bundle + "|" + platform
		if i, ok := index[key]; ok {
			builds[i].Variants = append(builds[i].Variants, name)
			builds[i].Declared = append(builds[i].Declared, declared)
			continue
		}
		index[key] = len(builds)
		builds = append(builds, Build{
			Variants: []string{name}, BundleID: bundle,
			Store: store, StoreSource: storeSource, TenjinPlatform: platform,
			Declared: []DeclaredStore{declared},
		})
	}

	// The build with the default bundle ID on Google Play keeps the plain
	// name; the others are named after their first variant.
	mainFound := false
	for i := range builds {
		id := string(Android) + ":" + builds[i].Variants[0]
		if !mainFound && builds[i].BundleID == t.BundleID+defaultSuffix && builds[i].TenjinPlatform == "android" {
			id, mainFound = string(Android), true
		}
		builds[i].ID = id
		builds[i].SDKKeyPlaceholder = Placeholder(id)
	}
	return builds
}

// declaredStore reads TENJIN_APP_STORE from a manifest, resolving a
// ${placeholder}. It returns "" when the manifest does not declare it, or
// declares a placeholder nothing fills.
func (d *detector) declaredStore(manifest string, values map[string]string) (store, source string) {
	m := tenjinStore.FindStringSubmatch(stripXMLComments(d.read(manifest)))
	if m == nil {
		return "", ""
	}
	value := m[1] + m[2]
	if p := placeholder.FindStringSubmatch(value); p != nil {
		value = values[p[1]]
	}
	if value == "" {
		return "", ""
	}
	return value, manifest
}

var xmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

func stripXMLComments(s string) string { return xmlComment.ReplaceAllString(s, "") }

// parseFlavors reads the flavors declared in a productFlavors block.
func parseFlavors(block string) []flavor {
	var out []flavor
	for _, child := range childBlocks(block) {
		f := flavor{name: child.name, placeholders: manifestPlaceholders(child.body)}
		if m := flavorDimension.FindStringSubmatch(child.body); m != nil {
			f.dimension = m[1]
		}
		if m := applicationID.FindStringSubmatch(child.body); m != nil {
			f.appID = m[1]
		}
		if m := applicationSuffix.FindStringSubmatch(child.body); m != nil {
			f.suffix = m[1]
		}
		out = append(out, f)
	}
	return out
}

type block struct{ name, body string }

// childBlocks returns the named blocks directly inside content, skipping
// anything nested deeper.
func childBlocks(content string) []block {
	var out []block
	depth := 0
	for i := 0; i < len(content); {
		switch content[i] {
		case '{':
			depth++
			i++
			continue
		case '}':
			depth--
			i++
			continue
		}
		if depth != 0 {
			i++
			continue
		}
		loc := namedBlock.FindStringSubmatchIndex(content[i:])
		if loc == nil {
			break
		}
		// Only a match that starts before the next brace is at this depth.
		next := strings.IndexAny(content[i:], "{}")
		if next >= 0 && loc[0] > next {
			i += next
			continue
		}
		name := ""
		if loc[2] >= 0 {
			name = content[i+loc[2] : i+loc[3]]
		} else {
			name = content[i+loc[4] : i+loc[5]]
		}
		start := i + loc[1]
		end := matchingBrace(content, start)
		out = append(out, block{name: name, body: content[start:end]})
		i = end + 1
	}
	return out
}

// matchingBrace returns the index of the brace that closes the block whose
// body starts at start.
func matchingBrace(content string, start int) int {
	depth := 1
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(content)
}

// blockNamed returns the body of the first block with this name, at any
// depth, or "".
func blockNamed(content, name string) string {
	start := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*\{`)
	b, _ := cutBlock(content, start)
	return b
}

// manifestPlaceholders reads manifestPlaceholders assignments in a block,
// in any of the ways Gradle lets them be written.
func manifestPlaceholders(content string) map[string]string {
	values := map[string]string{}
	for i := strings.Index(content, "anifestPlaceholders"); i >= 0; {
		start := i + len("anifestPlaceholders")
		assignment := statement(content, start)
		for _, m := range placeholders.FindAllStringSubmatch(assignment, -1) {
			values[m[1]] = m[2]
		}
		next := strings.Index(content[start:], "anifestPlaceholders")
		if next < 0 {
			break
		}
		i = start + next
	}
	return values
}

// statement returns a Gradle statement from offset start to its end: the
// end of the line, or, when brackets are open there, the line where they
// close, so that a list or a map written over several lines is read whole.
func statement(content string, start int) string {
	depth := 0
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return content[start:i]
			}
			depth--
		case '\n':
			if depth == 0 {
				return content[start:i]
			}
		}
	}
	return content[start:]
}

// dimensionOrder reads flavorDimensions, in order.
func dimensionOrder(content string) []string {
	var out []string
	for _, loc := range flavorDimensions.FindAllStringIndex(content, -1) {
		for _, m := range quoted.FindAllStringSubmatch(statement(content, loc[1]), -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// variants combines the flavors into build variants: one flavor from each
// dimension, in dimension order. Flavors with no dimension form one.
func variants(flavors []flavor, order []string) [][]flavor {
	groups := map[string][]flavor{}
	var dims []string
	for _, d := range order {
		if _, ok := groups[d]; !ok {
			groups[d] = nil
			dims = append(dims, d)
		}
	}
	for _, f := range flavors {
		d := f.dimension
		if len(order) <= 1 {
			d = "" // with one dimension, it need not be named on each flavor
		}
		if _, ok := groups[d]; !ok {
			dims = append(dims, d)
		}
		groups[d] = append(groups[d], f)
	}
	combos := [][]flavor{{}}
	for _, d := range dims {
		if len(groups[d]) == 0 {
			continue
		}
		var next [][]flavor
		for _, c := range combos {
			for _, f := range groups[d] {
				combo := append(append([]flavor{}, c...), f)
				next = append(next, combo)
			}
		}
		combos = next
	}
	if len(combos) == 1 && len(combos[0]) == 0 {
		return nil
	}
	return combos
}

// variantName is Gradle's name for a flavor combination: amazon and prod
// make amazonProd.
func variantName(variant []flavor) string {
	var b strings.Builder
	for i, f := range variant {
		if i == 0 {
			b.WriteString(f.name)
			continue
		}
		b.WriteString(strings.ToUpper(f.name[:1]) + f.name[1:])
	}
	return b.String()
}
