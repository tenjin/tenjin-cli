package project

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
	"unicode"
)

var (
	gradleRootName  = regexp.MustCompile(`(?m)^\s*rootProject\.name\s*=\s*["']([^"'\n]+)["']`)
	capacitorTSName = regexp.MustCompile(`appName\s*:\s*['"]([^'"\n]+)['"]`)
	unityName       = regexp.MustCompile(`(?m)^\s*productName:\s*(.+?)\s*$`)
)

// AppName is the name the project gives its app on a platform, read from
// where its tools keep it: Gradle's rootProject.name, the Xcode project's
// name, pubspec.yaml's name, app.json, the Capacitor appName, or Unity's
// productName. "" when there is none to read.
func AppName(p *Project, platform Platform) string {
	d := &detector{root: p.Root, p: p}
	var name string
	switch p.Framework {
	case Flutter:
		name = firstMatch(pubspecName, d.read("pubspec.yaml"))
	case ReactNative:
		var app struct {
			DisplayName string `json:"displayName"`
			Name        string `json:"name"`
			Expo        struct {
				Name string `json:"name"`
			} `json:"expo"`
		}
		if json.Unmarshal([]byte(d.read("app.json")), &app) == nil {
			name = firstNonEmpty(app.Expo.Name, app.DisplayName, app.Name)
		}
	case Capacitor:
		var config struct {
			AppName string `json:"appName"`
		}
		if json.Unmarshal([]byte(d.read("capacitor.config.json")), &config) == nil && config.AppName != "" {
			name = config.AppName
		} else {
			name = firstMatch(capacitorTSName, stripComments(d.read("capacitor.config.ts")))
		}
	case Unity:
		name = firstMatch(unityName, d.read("ProjectSettings/ProjectSettings.asset"))
	default:
		t := p.Target(platform)
		switch {
		case t == nil:
		case t.Android != nil:
			for _, dir := range []string{".", path.Dir(t.Android.ModuleDir)} {
				for _, file := range []string{"settings.gradle.kts", "settings.gradle"} {
					if name == "" {
						name = firstMatch(gradleRootName, stripComments(d.read(path.Join(dir, file))))
					}
				}
			}
		case t.IOS != nil && t.IOS.Xcodeproj != "":
			name = strings.TrimSuffix(path.Base(t.IOS.Xcodeproj), ".xcodeproj")
		}
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return ""
	}
	return name
}

func firstMatch(re *regexp.Regexp, text string) string {
	if m := re.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}
