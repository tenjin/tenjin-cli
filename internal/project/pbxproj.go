package project

import (
	"errors"
	"regexp"
	"sort"
	"strings"
)

// project.pbxproj is an old-style (OpenStep) property list: dictionaries in
// braces, arrays in parentheses, strings bare or quoted, and comments. The
// parser below reads it as Xcode writes it, into maps, slices and strings.

var errPlist = errors.New("not an old-style property list")

type plistParser struct {
	s string
	i int
}

func parseOldPlist(s string) (map[string]any, error) {
	p := &plistParser{s: s}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	dict, ok := v.(map[string]any)
	if !ok {
		return nil, errPlist
	}
	return dict, nil
}

// space skips whitespace and comments.
func (p *plistParser) space() {
	for p.i < len(p.s) {
		switch {
		case strings.ContainsRune(" \t\r\n", rune(p.s[p.i])):
			p.i++
		case strings.HasPrefix(p.s[p.i:], "//"):
			end := strings.IndexByte(p.s[p.i:], '\n')
			if end < 0 {
				p.i = len(p.s)
				return
			}
			p.i += end
		case strings.HasPrefix(p.s[p.i:], "/*"):
			end := strings.Index(p.s[p.i+2:], "*/")
			if end < 0 {
				p.i = len(p.s)
				return
			}
			p.i += 2 + end + 2
		default:
			return
		}
	}
}

func (p *plistParser) value() (any, error) {
	p.space()
	if p.i >= len(p.s) {
		return nil, errPlist
	}
	switch p.s[p.i] {
	case '{':
		p.i++
		dict := map[string]any{}
		for {
			p.space()
			if p.i < len(p.s) && p.s[p.i] == '}' {
				p.i++
				return dict, nil
			}
			key, err := p.str()
			if err != nil {
				return nil, err
			}
			if !p.expect('=') {
				return nil, errPlist
			}
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			dict[key] = v
			if !p.expect(';') {
				return nil, errPlist
			}
		}
	case '(':
		p.i++
		var list []any
		for {
			p.space()
			if p.i < len(p.s) && p.s[p.i] == ')' {
				p.i++
				return list, nil
			}
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			list = append(list, v)
			p.space()
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
			}
		}
	}
	return p.str()
}

func (p *plistParser) expect(c byte) bool {
	p.space()
	if p.i < len(p.s) && p.s[p.i] == c {
		p.i++
		return true
	}
	return false
}

// str reads a quoted or bare string, or <hex data> as its text.
func (p *plistParser) str() (string, error) {
	p.space()
	if p.i >= len(p.s) {
		return "", errPlist
	}
	switch p.s[p.i] {
	case '"', '\'':
		quote := p.s[p.i]
		var b strings.Builder
		for j := p.i + 1; j < len(p.s); j++ {
			switch c := p.s[j]; {
			case c == '\\' && j+1 < len(p.s):
				j++
				switch p.s[j] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(p.s[j])
				}
			case c == quote:
				p.i = j + 1
				return b.String(), nil
			default:
				b.WriteByte(c)
			}
		}
		return "", errPlist
	case '<':
		end := strings.IndexByte(p.s[p.i:], '>')
		if end < 0 {
			return "", errPlist
		}
		v := p.s[p.i : p.i+end+1]
		p.i += end + 1
		return v, nil
	}
	start := p.i
	for p.i < len(p.s) && !strings.ContainsRune(" \t\r\n;,=(){}\"'<>", rune(p.s[p.i])) {
		p.i++
	}
	if p.i == start {
		return "", errPlist
	}
	return p.s[start:p.i], nil
}

// xcodeTarget is a target of an Xcode project with the build settings of
// each of its configurations.
type xcodeTarget struct {
	name, productType string
	// configs are the configurations' build settings, by name, with the
	// project's own settings under them where the target sets none.
	configs map[string]map[string]string
	// defaultConfig is the configuration xcodebuild builds when none is
	// named, usually Release.
	defaultConfig string
}

// appProductType is an iOS (or macOS) app. App extensions, App Clips, watch
// apps, iMessage apps, frameworks and test bundles have types of their own.
const appProductType = "com.apple.product-type.application"

// xcodeTargets reads the native targets of a project.pbxproj. It fails when
// the file cannot be parsed or names no target.
func xcodeTargets(pbx string) ([]xcodeTarget, error) {
	root, err := parseOldPlist(pbx)
	if err != nil {
		return nil, err
	}
	objects, _ := root["objects"].(map[string]any)
	object := func(id any) map[string]any {
		s, _ := id.(string)
		o, _ := objects[s].(map[string]any)
		return o
	}
	configList := func(id any) (map[string]map[string]string, string) {
		list := object(id)
		configs := map[string]map[string]string{}
		ids, _ := list["buildConfigurations"].([]any)
		for _, cid := range ids {
			c := object(cid)
			name, _ := c["name"].(string)
			settings := map[string]string{}
			if bs, ok := c["buildSettings"].(map[string]any); ok {
				for k, v := range bs {
					if s, ok := v.(string); ok {
						settings[k] = s
					}
				}
			}
			configs[name] = settings
		}
		def, _ := list["defaultConfigurationName"].(string)
		return configs, def
	}

	// The project's settings apply to every target that does not set its own.
	projectConfigs := map[string]map[string]string{}
	for _, o := range objects {
		if o, ok := o.(map[string]any); ok && o["isa"] == "PBXProject" {
			projectConfigs, _ = configList(o["buildConfigurationList"])
		}
	}

	var targets []xcodeTarget
	for _, o := range objects {
		o, ok := o.(map[string]any)
		if !ok || o["isa"] != "PBXNativeTarget" {
			continue
		}
		t := xcodeTarget{}
		t.name, _ = o["name"].(string)
		t.productType, _ = o["productType"].(string)
		t.configs, t.defaultConfig = configList(o["buildConfigurationList"])
		for name, settings := range t.configs {
			for k, v := range projectConfigs[name] {
				if _, set := settings[k]; !set {
					settings[k] = v
				}
			}
		}
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		return nil, errPlist
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].name < targets[j].name })
	return targets, nil
}

// setting returns a build setting of the configuration that ships: the
// default one, then Release, then any other that sets it, in name order.
func (t xcodeTarget) setting(key string) string {
	for _, name := range t.configOrder() {
		if v := strings.TrimSpace(t.configs[name][key]); v != "" {
			return v
		}
	}
	return ""
}

// values returns every distinct value the configurations give a setting,
// the shipped one first.
func (t xcodeTarget) values(key string) []string {
	var out []string
	for _, name := range t.configOrder() {
		out = append(out, strings.TrimSpace(t.configs[name][key]))
	}
	return dedupe(out)
}

func (t xcodeTarget) configOrder() []string {
	var names []string
	for name := range t.configs {
		names = append(names, name)
	}
	sort.Strings(names)
	order := []string{}
	for _, first := range []string{t.defaultConfig, "Release"} {
		if _, ok := t.configs[first]; ok && first != "" {
			order = append(order, first)
		}
	}
	return dedupe(append(order, names...))
}

// iosApp reports whether a target builds an iOS app: an application whose
// product type is exactly that (a watch app's or an App Clip's extends it),
// not built for another platform alone, as a macOS, watchOS, tvOS or
// visionOS companion is.
func (t xcodeTarget) iosApp() bool {
	if t.productType != appProductType {
		return false
	}
	switch t.setting("SDKROOT") {
	case "macosx", "watchos", "appletvos", "xros":
		return false
	}
	if platforms := t.setting("SUPPORTED_PLATFORMS"); platforms != "" && !strings.Contains(platforms, "iphoneos") {
		return false
	}
	return true
}

// pbxAppProduct is an app target's product type written whole in a
// project.pbxproj.
var pbxAppProduct = regexp.MustCompile(`productType = "?` + regexp.QuoteMeta(appProductType) + `"?;`)

// hasIOSApp reports whether a project.pbxproj has an iOS app target. Where
// the file cannot be read as a project, a product type written whole stands
// in for one; a watch app's, which extends it, does not.
func hasIOSApp(pbx string) bool {
	targets, err := xcodeTargets(pbx)
	if err != nil {
		return pbxAppProduct.MatchString(pbx)
	}
	for _, t := range targets {
		if t.iosApp() {
			return true
		}
	}
	return false
}
