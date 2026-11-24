package project

import (
	"encoding/json"
	"fmt"
	"html"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
)

var (
	// rootProject.name = "…" or rootProject.setName('…'), the literal alone.
	gradleRootName = regexp.MustCompile(`(?m)^[ \t]*rootProject\.(?:name[ \t]*=[ \t]*|setName\([ \t]*)("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*')[ \t]*\)?[ \t;]*\r?$`)
	// A JavaScript or TypeScript property set to a string literal alone.
	capacitorTSName = regexp.MustCompile(`\bappName\s*:\s*` + jsString + `[ \t]*(?:[,}\r\n]|$)`)
	expoConfigName  = regexp.MustCompile(`(?m)^[ \t]*name\s*:\s*` + jsString + `[ \t]*(?:[,}\r\n]|$)`)
	unityName       = regexp.MustCompile(`(?m)^[ \t]*productName:[ \t]*(.*?)[ \t\r]*$`)
	godotName       = regexp.MustCompile(`(?m)^config/name="((?:[^"\\\n]|\\.)*)"[ \t]*$`)
	plistDisplay    = regexp.MustCompile(`<key>CFBundleDisplayName</key>\s*<string>([^<]*)</string>`)
	manifestLabel   = regexp.MustCompile(`<application\b[^>]*?\bandroid:label\s*=\s*"([^"]*)"`)
	stringResource  = `<string\s+name\s*=\s*"%s"[^>/]*>([^<]*)</string>`
)

const jsString = "(\"(?:[^\"\\\\\\n]|\\\\.)*\"|'(?:[^'\\\\\\n]|\\\\.)*'|`(?:[^`\\\\]|\\\\.)*`)"

// AppName is the name the project gives its app on a platform, read from
// where its tools keep it: Gradle's rootProject.name, the Xcode project's
// name, a Flutter app's display name or label (else pubspec.yaml's name),
// Expo's app config or app.json, the Capacitor appName, Unity's
// productName, or Godot's config/name. Only a literal counts. "" when there is none to read.
func AppName(p *Project, platform Platform) string {
	d := &detector{root: p.Root, p: p}
	switch p.Framework {
	case Flutter:
		ios, android := flutterIOSName(d, p), flutterAndroidName(d, p)
		if platform == IOS {
			return firstNonEmpty(ios, android, appName(firstMatch(pubspecName, d.read("pubspec.yaml"))))
		}
		return firstNonEmpty(android, ios, appName(firstMatch(pubspecName, d.read("pubspec.yaml"))))
	case ReactNative:
		for _, file := range []string{"app.config.ts", "app.config.js"} {
			if name := jsLiteral(expoConfigName, stripComments(d.read(file))); name != "" {
				return name
			}
		}
		var app struct {
			DisplayName string `json:"displayName"`
			Name        string `json:"name"`
			Expo        struct {
				Name string `json:"name"`
			} `json:"expo"`
		}
		if json.Unmarshal([]byte(d.read("app.json")), &app) == nil {
			return firstNonEmpty(appName(app.Expo.Name), appName(app.DisplayName), appName(app.Name))
		}
	case Capacitor:
		var config struct {
			AppName string `json:"appName"`
		}
		if json.Unmarshal([]byte(d.read("capacitor.config.json")), &config) == nil && appName(config.AppName) != "" {
			return appName(config.AppName)
		}
		for _, file := range []string{"capacitor.config.ts", "capacitor.config.js"} {
			if name := jsLiteral(capacitorTSName, stripComments(d.read(file))); name != "" {
				return name
			}
		}
	case Unity:
		if m := unityName.FindStringSubmatch(d.read("ProjectSettings/ProjectSettings.asset")); m != nil {
			if name, ok := yamlScalar(m[1]); ok {
				return appName(name)
			}
		}
	case Godot:
		if m := godotName.FindStringSubmatch(d.read("project.godot")); m != nil {
			return appName(godotUnquote(m[1]))
		}
	default:
		t := p.Target(platform)
		switch {
		case t == nil:
		case t.Android != nil:
			for _, dir := range []string{".", path.Dir(t.Android.ModuleDir)} {
				for _, file := range []string{"settings.gradle.kts", "settings.gradle"} {
					for _, m := range gradleRootName.FindAllStringSubmatch(stripComments(d.read(path.Join(dir, file))), -1) {
						if name, ok := unquote(m[1], m[1][0] == '"', false); ok && appName(name) != "" {
							return appName(name)
						}
					}
				}
			}
		case t.IOS != nil && t.IOS.Xcodeproj != "":
			return appName(strings.TrimSuffix(path.Base(t.IOS.Xcodeproj), ".xcodeproj"))
		}
	}
	return ""
}

// appName is a name fit to show and send, or "".
func appName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 || strings.IndexFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
		return ""
	}
	return name
}

// flutterIOSName is the iOS app's CFBundleDisplayName, when it is written
// out rather than taken from a build setting.
func flutterIOSName(d *detector, p *Project) string {
	plist := "ios/Runner/Info.plist"
	if t := p.Target(IOS); t != nil && t.IOS != nil && t.IOS.InfoPlist != "" {
		plist = t.IOS.InfoPlist
	}
	name := html.UnescapeString(firstMatch(plistDisplay, stripXMLComments(d.read(plist))))
	if strings.Contains(name, "$(") || strings.Contains(name, "${") {
		return ""
	}
	return appName(name)
}

// flutterAndroidName is the Android app's label: a literal, or a string
// resource of the app's own.
func flutterAndroidName(d *detector, p *Project) string {
	manifest := "android/app/src/main/AndroidManifest.xml"
	if t := p.Target(Android); t != nil && t.Android != nil && t.Android.Manifest != "" {
		manifest = t.Android.Manifest
	}
	label := firstMatch(manifestLabel, stripXMLComments(d.read(manifest)))
	if res, ok := strings.CutPrefix(label, "@string/"); ok {
		re := regexp.MustCompile(fmt.Sprintf(stringResource, regexp.QuoteMeta(res)))
		label = firstMatch(re, stripXMLComments(d.read(path.Join(path.Dir(manifest), "res/values/strings.xml"))))
		if strings.HasPrefix(label, `"`) && strings.HasSuffix(label, `"`) && len(label) >= 2 {
			label = label[1 : len(label)-1]
		}
		if label = androidUnescape(label); label == "" {
			return ""
		}
	}
	if strings.HasPrefix(label, "@") || strings.HasPrefix(label, "?") || strings.Contains(label, "${") {
		return ""
	}
	return appName(html.UnescapeString(label))
}

// androidUnescape reads the backslash escapes of a string resource, "" for
// one it does not know.
func androidUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i++; i >= len(s) || !strings.ContainsRune(`'"\@?`, rune(s[i])) {
			return ""
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// jsLiteral is the string literal re captures, unquoted, or "" when it is
// not a plain literal.
func jsLiteral(re *regexp.Regexp, text string) string {
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	name, ok := unquote(m[1], false, m[1][0] == '`')
	if !ok {
		return ""
	}
	return appName(name)
}

// unquote reads a quoted literal with backslash escapes. A string that
// interpolates is refused: with dollar any $, with template a ${.
func unquote(lit string, dollar, template bool) (string, bool) {
	if len(lit) < 2 {
		return "", false
	}
	body := lit[1 : len(lit)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '$' && (dollar || template && i+1 < len(body) && body[i+1] == '{'):
			return "", false
		case c != '\\':
			b.WriteByte(c)
			continue
		}
		if i++; i >= len(body) {
			return "", false
		}
		switch e := body[i]; e {
		case '"', '\'', '\\', '$', '`', '/':
			b.WriteByte(e)
		case 'u':
			if i+4 >= len(body) {
				return "", false
			}
			r, ok := hexRune(body[i+1 : i+5])
			if !ok {
				return "", false
			}
			b.WriteRune(r)
			i += 4
		default:
			return "", false
		}
	}
	return b.String(), true
}

// yamlScalar reads a one-line YAML scalar: plain, single-quoted (” is a
// quote) or double-quoted (backslash escapes, Unity's \u for anything
// outside ASCII).
func yamlScalar(s string) (string, bool) {
	switch {
	case s == "":
		return "", true
	case s[0] == '\'':
		if len(s) < 2 || s[len(s)-1] != '\'' {
			return "", false
		}
		body := s[1 : len(s)-1]
		if strings.Contains(strings.ReplaceAll(body, "''", ""), "'") {
			return "", false
		}
		return strings.ReplaceAll(body, "''", "'"), true
	case s[0] == '"':
		return yamlDoubleQuoted(s)
	case strings.ContainsRune("|>&*!%@`", rune(s[0])):
		return "", false
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s), true
}

func yamlDoubleQuoted(s string) (string, bool) {
	var units []rune
	simple := map[byte]rune{'0': 0, 'a': '\a', 'b': '\b', 't': '\t', 'n': '\n', 'v': '\v', 'f': '\f', 'r': '\r', 'e': 0x1b,
		' ': ' ', '"': '"', '/': '/', '\\': '\\', 'N': 0x85, '_': 0xa0, 'L': 0x2028, 'P': 0x2029}
	widths := map[byte]int{'x': 2, 'u': 4, 'U': 8}
	for i := 1; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			if i != len(s)-1 {
				return "", false
			}
			return string(utf16Pairs(units)), true
		case '\\':
			if i+1 >= len(s) {
				return "", false
			}
			e := s[i+1]
			if r, ok := simple[e]; ok {
				units = append(units, r)
				i++
				continue
			}
			n, ok := widths[e]
			if !ok || i+2+n > len(s) {
				return "", false
			}
			v, ok := hexRune(s[i+2 : i+2+n])
			if !ok {
				return "", false
			}
			units = append(units, v)
			i += 1 + n
		default:
			r := []rune(s[i:])[0]
			units = append(units, r)
			i += len(string(r)) - 1
		}
	}
	return "", false
}

// utf16Pairs joins the surrogate pairs \u escapes write for characters
// beyond the Basic Multilingual Plane.
func utf16Pairs(rs []rune) []rune {
	var out []rune
	for i := 0; i < len(rs); i++ {
		if i+1 < len(rs) && utf16.IsSurrogate(rs[i]) {
			if r := utf16.DecodeRune(rs[i], rs[i+1]); r != unicode.ReplacementChar {
				out = append(out, r)
				i++
				continue
			}
		}
		out = append(out, rs[i])
	}
	return out
}

// hexRune reads a character's code written in hexadecimal digits.
func hexRune(hex string) (rune, bool) {
	var r rune
	for _, c := range hex {
		var d rune
		switch c = unicode.ToLower(c); {
		case '0' <= c && c <= '9':
			d = c - '0'
		case 'a' <= c && c <= 'f':
			d = c - 'a' + 10
		default:
			return 0, false
		}
		if r > unicode.MaxRune/16 {
			return 0, false
		}
		r = r*16 + d
	}
	return r, hex != ""
}

func firstMatch(re *regexp.Regexp, text string) string {
	if m := re.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}
