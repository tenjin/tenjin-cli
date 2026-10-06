// Package guides holds the Tenjin SDK integration guides, from
// github.com/tenjin/sdk-llm-guides, at the revision this CLI was tested
// with. They are embedded so that they read the same offline, and match the
// checks of the same tenjin version. scripts/sync-guides.sh updates them.
package guides

import (
	"embed"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

//go:embed data
var data embed.FS

// Guide is one platform's integration guide.
type Guide struct {
	// Name is how the guide is asked for: android, ios, flutter,
	// react-native, capacitor or unity.
	Name string
	// Package is the Tenjin SDK package the guide integrates, as the
	// project's dependencies name it.
	Package string
	Content string
}

var files = []struct{ name, file, pkg string }{
	{"android", "android.md", "com.tenjin:android-sdk"},
	{"ios", "ios.md", "TenjinSDK"},
	{"flutter", "flutter.md", "tenjin_plugin"},
	{"react-native", "react-native.md", "react-native-tenjin"},
	{"capacitor", "ionic.md", "ionic-capacitor-tenjin"},
	{"unity", "unity.md", "com.tenjin.sdk"},
}

// Names lists the guides there are.
func Names() []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.name
	}
	return out
}

// For returns a guide by name. "ionic" is accepted for capacitor.
func For(name string) (Guide, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "ionic" {
		name = "capacitor"
	}
	for _, f := range files {
		if f.name != name {
			continue
		}
		raw, err := data.ReadFile("data/" + f.file)
		if err != nil {
			return Guide{}, false
		}
		return Guide{Name: f.name, Package: f.pkg, Content: string(raw)}, true
	}
	return Guide{}, false
}

// Revision is the sdk-llm-guides commit the guides were taken from.
func Revision() string {
	raw, _ := data.ReadFile("data/REVISION")
	return strings.TrimSpace(string(raw))
}

// License is the guides' licence, which goes with them.
func License() string {
	raw, _ := data.ReadFile("data/LICENSE.txt")
	return string(raw)
}

// ForProject names the guide for a project: "" when it has none, or when a
// native project builds for both platforms and has two.
func ForProject(p *project.Project) string {
	switch p.Framework {
	case project.Flutter:
		return "flutter"
	case project.ReactNative:
		return "react-native"
	case project.Capacitor:
		return "capacitor"
	case project.Unity:
		return "unity"
	case project.Native:
		if len(p.Targets) == 1 {
			return string(p.Targets[0].Platform)
		}
	}
	return ""
}
