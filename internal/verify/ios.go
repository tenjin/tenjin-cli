package verify

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

const (
	plistATT      = "NSUserTrackingUsageDescription"
	plistSKAN     = "NSAdvertisingAttributionReportEndpoint"
	tenjinSKANURL = "https://tenjin-skan.com"
)

func (r *runner) checkIOS(spec frameworkSpec, t *project.Target, src source) {
	if t.IOS == nil {
		r.add(Finding{
			ID: "ios.project", Severity: Info, Platform: project.IOS,
			Title: "No iOS project folder yet, so its Info.plist was not checked",
		})
		return
	}
	if t.IOS.InfoPlist == "" && t.IOS.MissingInfoPlist != "" && !t.IOS.GeneratedInfoPlist {
		r.add(Finding{
			ID: "ios.plist", Severity: Info, Platform: project.IOS, File: t.IOS.Xcodeproj,
			Title: "The app's Info.plist (" + t.IOS.MissingInfoPlist + ") was not found, so its keys were not checked",
		})
		return
	}
	plist := r.readRel(t.IOS.InfoPlist)
	pbx := r.readRel(t.IOS.Xcodeproj + "/project.pbxproj")
	if t.IOS.InfoPlist != "" && !strings.Contains(plist, "<plist") {
		r.add(Finding{
			ID: "ios.plist", Severity: Info, Platform: project.IOS,
			Title: "Info.plist is not in XML form, so its keys were not checked", File: t.IOS.InfoPlist,
		})
		return
	}
	plistFile := t.IOS.InfoPlist
	if plistFile == "" {
		plistFile = t.IOS.Xcodeproj
	}

	_, hasATT := plistString(plist, plistATT)
	if hasATT || strings.Contains(pbx, "INFOPLIST_KEY_"+plistATT) {
		r.pass("ios.plist.att_description", project.IOS, "The tracking prompt text ("+plistATT+") is set", plistFile, 0)
	} else {
		r.add(Finding{
			ID: "ios.plist.att_description", Severity: Warning, Platform: project.IOS,
			Title:  plistATT + " is missing",
			Detail: "iOS needs this text to show the tracking prompt. Without the prompt the IDFA is all zeros; requesting it without the text crashes the app.",
			File:   plistFile,
			Fix:    "Add " + plistATT + " to Info.plist with a sentence explaining why the app asks to track.",
		})
	}

	endpoint, hasSKAN := plistString(plist, plistSKAN)
	switch {
	case !hasSKAN:
		r.add(Finding{
			ID: "ios.plist.skan_endpoint", Severity: Warning, Platform: project.IOS,
			Title:  plistSKAN + " is missing",
			Detail: "Without it Apple does not send SKAdNetwork postback copies to Tenjin.",
			File:   plistFile,
			Fix:    skanFix(t),
		})
	case strings.TrimSuffix(strings.TrimSpace(endpoint), "/") != tenjinSKANURL:
		r.add(Finding{
			ID: "ios.plist.skan_endpoint", Severity: Warning, Platform: project.IOS,
			Title:  plistSKAN + " points somewhere other than Tenjin",
			Detail: "It is set to " + quote(endpoint) + ". Only one endpoint receives postback copies; another SDK may have set this.",
			File:   plistFile,
			Fix:    "Set it to " + tenjinSKANURL + " if Tenjin should receive SKAdNetwork postbacks.",
		})
	default:
		r.pass("ios.plist.skan_endpoint", project.IOS, "SKAdNetwork postback copies go to Tenjin", plistFile, 0)
	}

	r.checkATTRequest(spec, src)
	r.checkDebugLogs(spec, src)
}

// checkDebugLogs looks for the call that turns the SDK's request logs on.
// Without it the iOS SDK logs nothing about its requests, so a launch cannot
// be checked from the device; with it outside a debug-only block, every
// request body, device identifiers included, is logged on customers' phones.
func (r *runner) checkDebugLogs(spec frameworkSpec, src source) {
	if spec.debugLogsCall == nil {
		return
	}
	for _, c := range src.debugLogs {
		if !debugOnly(c) {
			r.add(Finding{
				ID: "ios.debug_logs", Severity: Warning, Platform: project.IOS,
				Title:  "TenjinSDK.debugLogs() is on in release builds",
				Detail: "It logs every request and response body, device identifiers included, on the devices of the app's users.",
				File:   c.File, Line: c.Line,
				Fix: "Put the call inside #if DEBUG … #endif (Swift) or #ifdef DEBUG … #endif (Objective-C).",
			})
			return
		}
	}
	if len(src.debugLogs) > 0 {
		r.pass("ios.debug_logs", project.IOS, "Debug builds turn on the SDK's logs", src.debugLogs[0].File, src.debugLogs[0].Line)
		return
	}
	r.add(Finding{
		ID: "ios.debug_logs", Severity: Info, Platform: project.IOS,
		Title:  "Debug builds do not turn on the SDK's logs",
		Detail: "Without TenjinSDK.debugLogs() the iOS SDK logs nothing about its requests, so a launch cannot be checked from the device log. This does not stop events reaching Tenjin.",
		Fix:    spec.debugLogsFix,
	})
}

// debugOnly reports whether a call sits inside a conditional block on DEBUG.
func debugOnly(c call) bool {
	var open []bool
	for i, line := range strings.Split(c.Text, "\n") {
		if i+1 >= c.Line {
			break
		}
		switch t := strings.TrimSpace(line); {
		case strings.HasPrefix(t, "#if"):
			open = append(open, strings.Contains(t, "DEBUG") && !strings.Contains(t, "!"))
		case strings.HasPrefix(t, "#endif") && len(open) > 0:
			open = open[:len(open)-1]
		case strings.HasPrefix(t, "#else") && len(open) > 0:
			open[len(open)-1] = false
		}
	}
	for _, debug := range open {
		if debug {
			return true
		}
	}
	return false
}

// skanFix says where the SKAdNetwork endpoint goes. A project whose
// Info.plist Xcode generates has no file to add it to yet.
func skanFix(t *project.Target) string {
	if t.IOS.InfoPlist == "" && t.IOS.GeneratedInfoPlist {
		return "Xcode generates this target's Info.plist, and this key has no build setting. In Xcode, add " + plistSKAN +
			" under the target's Info tab with the value " + tenjinSKANURL + "; Xcode then creates an Info.plist file for it."
	}
	return "Add " + plistSKAN + " to Info.plist with the value " + tenjinSKANURL + "."
}

// checkATTRequest looks for a request for tracking permission, which must
// come before connect() for the IDFA to be available.
func (r *runner) checkATTRequest(spec frameworkSpec, src source) {
	switch {
	case spec.attCall != nil && len(src.att) > 0:
		r.pass("ios.att_request", project.IOS, "The app asks for tracking permission", src.att[0].File, src.att[0].Line)
	// The request is often in a file of its own that never names Tenjin;
	// detection looks for it everywhere.
	case spec.attCall != nil && r.p.TrackingPrompt != nil && r.p.TrackingPrompt.File != "":
		r.pass("ios.att_request", project.IOS, "The app asks for tracking permission", r.p.TrackingPrompt.File, r.p.TrackingPrompt.Line)
	case spec.attCall != nil:
		r.add(Finding{
			ID: "ios.att_request", Severity: Warning, Platform: project.IOS,
			Title:  "The app never asks for tracking permission",
			Detail: "No requestTrackingAuthorization call was found. Without it the IDFA is all zeros and attribution falls back to weaker signals.",
			Fix:    "Request tracking authorization at launch and call connect() in its completion handler.",
		})
	case len(spec.attPackages) > 0:
		if pkg := r.installedPackage(spec.attPackages); pkg != "" {
			r.pass("ios.att_request", project.IOS, "A tracking-permission package is installed: "+pkg, "package.json", 0)
			return
		}
		// The prompt can also be requested from the native iOS code.
		if prompt := r.p.TrackingPrompt; prompt != nil && prompt.File != "" {
			r.pass("ios.att_request", project.IOS, "The app asks for tracking permission", prompt.File, prompt.Line)
			return
		}
		r.add(Finding{
			ID: "ios.att_request", Severity: Warning, Platform: project.IOS,
			Title:  "No package for the iOS tracking prompt is installed",
			Detail: "This Tenjin package has no tracking-permission call of its own. Without the prompt the IDFA is all zeros.",
			Fix:    "Install " + spec.attPackages[0] + ", request permission at launch, then call connect().",
		})
	}
}

// installedPackage returns the first of names that package.json depends on.
func (r *runner) installedPackage(names []string) string {
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal([]byte(r.readRel("package.json")), &pkg) != nil {
		return ""
	}
	for _, name := range names {
		if _, ok := pkg.Dependencies[name]; ok {
			return name
		}
		if _, ok := pkg.DevDependencies[name]; ok {
			return name
		}
	}
	return ""
}

// plistString returns the string value of a top-level key in an XML plist.
func plistString(plist, key string) (string, bool) {
	re := regexp.MustCompile(`<key>` + regexp.QuoteMeta(key) + `</key>\s*<string>([^<]*)</string>`)
	m := re.FindStringSubmatch(plist)
	if m == nil {
		return "", false
	}
	return m[1], true
}
