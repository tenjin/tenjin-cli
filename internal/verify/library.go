package verify

import (
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

// checkLibrary reports on a library that wraps the Tenjin SDK. What an app
// needs for the SDK to work (permissions, Info.plist keys, the key, the
// launch) is set in the app, so those checks are for the app that uses the
// library; here, only what the library declares: which SDK, at what version.
func (r *runner) checkLibrary(lib *project.Library) {
	f := Finding{
		ID: "project.library", Severity: Info, File: lib.Source,
		Title:  "This is a " + project.LibraryKindName(lib.Kind) + " that wraps the Tenjin SDK, not an app",
		Detail: "What the SDK needs is set in the app that uses the library: its permissions and Info.plist keys, its SDK key, and its launch. Those checks are for that app.",
		Fix:    "Run tenjin doctor in an app that uses this library.",
	}
	if lib.Name != "" {
		f.Title = lib.Name + " is a " + project.LibraryKindName(lib.Kind) + " that wraps the Tenjin SDK, not an app"
	}
	if lib.Example != "" {
		f.Fix = "Run tenjin doctor " + lib.Example + ", the example app in this repository, or in another app that uses the library."
	}
	r.add(f)

	i := lib.Tenjin
	i.Version = constraintFloor(i.Version)
	title := "The library depends on " + i.Package
	if lib.Tenjin.Version != "" {
		title += " " + lib.Tenjin.Version
	}
	r.pass("sdk.dependency", "", title, i.Source, 0)
	r.checkVersion(i, "")
}

// constraintFloor turns a CocoaPods version constraint into the form the
// version check reads: "~> 1.15" allows any 1.x from 1.15, like "^1.15";
// "~> 1.15.1" any 1.15.x, like "~1.15.1". One that only sets a lower bound
// (">= 1.15") is not judged.
func constraintFloor(v string) string {
	v = strings.TrimSpace(v)
	switch {
	case strings.HasPrefix(v, "~>"):
		v = strings.TrimSpace(strings.TrimPrefix(v, "~>"))
		if strings.Count(v, ".") >= 2 {
			return "~" + v
		}
		return "^" + v
	case strings.HasPrefix(v, ">"):
		return ""
	}
	return v
}
