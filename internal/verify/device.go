package verify

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/collector"
	"github.com/tenjin/tenjin-cli/internal/devicelog"
	"github.com/tenjin/tenjin-cli/internal/project"
)

// DeviceLookup asks Tenjin whether it has processed a device's install.
type DeviceLookup interface {
	LookupDevice(ctx context.Context, key, bundleID, platform, installationID string) (collector.Attribution, error)
}

// DeviceInput is what a connected device, or a saved log, says about the
// running app.
type DeviceInput struct {
	// Log is the parsed device log. It is nil when Problem is set.
	Log *devicelog.Log
	// Problem is why the log could not be read.
	Problem *devicelog.Unavailable
	// Lookup confirms on Tenjin's side that the device was processed. Nil
	// skips that check.
	Lookup DeviceLookup
	// Platform is the platform of the device that was read, or of the SDK
	// that wrote a saved log. The device findings belong to its target.
	Platform project.Platform
}

const zeroAdvertisingID = "00000000-0000-0000-0000-000000000000"

// checkDevice reports what the SDK did on the device: whether it connected,
// what Tenjin answered, and whether Tenjin went on to process the install.
// It reports on the target whose platform the device is.
func (r *runner) checkDevice(t *project.Target, src source) {
	d := r.opts.Device
	if d == nil || d.Platform != t.Platform {
		return
	}
	platform := t.Platform
	if d.Problem != nil {
		r.add(Finding{
			ID: "device.unavailable", Severity: Warning, Platform: platform,
			Title: d.Problem.Reason, Fix: d.Problem.Fix,
		})
		return
	}
	if !d.Log.SawSDK {
		f := Finding{
			ID: "device.no_sdk_output", Severity: Warning, Platform: platform,
			Title:  "The device log has no output from the Tenjin SDK",
			Detail: "Either the app has not been launched since the log began, or the SDK never ran.",
			Fix:    "Launch the app on the device, then run this again.",
		}
		if platform == project.IOS {
			f.Detail = "Either the app has not been launched in the last 10 minutes, the SDK never ran, or the build does not call TenjinSDK.debugLogs(): without it the iOS SDK logs nothing about its requests."
			f.Fix = "In the iOS app's launch code, call TenjinSDK.debugLogs() inside #if DEBUG … #endif, launch the app on the simulator, then run this again."
		}
		r.add(f)
		return
	}

	connect, ours := lastConnectOf(d.Log, t)
	// A connect skipped under the 30-second rule after the last one sent
	// is what the newest launch did.
	if connect != nil && d.Log.DedupedLine > connect.Line {
		connect = nil
	}
	switch {
	case connect != nil && !ours:
		r.add(Finding{
			ID: "device.connect", Severity: Warning, Platform: platform,
			Title:  "The log shows connects only from other apps (" + connect.Params["bundle_id"] + "), none from this project's",
			Detail: "Another app with the Tenjin SDK is running on this device, and this project's app has sent nothing since the log began.",
			Fix:    "Launch this project's debug build, then run this again.",
		})
	case connect != nil:
		r.add(connectFinding(connect, platform))
		r.checkRunningBuild(t, connect)
		if connect.Accepted() {
			r.checkProcessed(t, src, connect)
		}
	case d.Log.Deduped > 0:
		detail := "The SDK leaves at least 30 seconds between connects, also across launches, so nothing was sent this time."
		if platform == project.IOS {
			detail = "The SDK leaves at least 30 seconds between connects, so nothing was sent this time."
		}
		r.add(Finding{
			ID: "device.connect.deduped", Severity: Info, Platform: platform,
			Title:  "The SDK skipped its connect, because it sent one less than 30 seconds earlier",
			Detail: detail,
			Fix:    "Wait 30 seconds, launch the app again, then run this again.",
		})
	case platform == project.IOS && refusedUnread(d.Log) != nil:
		// Whatever it was, a refusal says the key or the app is wrong.
		req := refusedUnread(d.Log)
		f := Finding{
			ID: "device.connect", Severity: Error, Platform: platform,
			Title:  fmt.Sprintf("Tenjin refused the app's request with HTTP %d", req.Code),
			Detail: "Without TenjinSDK.debugLogs() the request's contents are not logged. A 401 means Tenjin does not accept the SDK key for this app; a 404, that it has no app with this bundle ID.",
			Fix:    "Check that the SDK key is this app's, from its page in the Tenjin dashboard. To see the whole answer, call TenjinSDK.debugLogs() inside #if DEBUG … #endif and launch again.",
		}
		r.add(f)
	case platform == project.IOS && hasUnread(d.Log):
		r.add(Finding{
			ID: "device.no_debug_logs", Severity: Warning, Platform: platform,
			Title:  "The SDK sent requests, but their contents are not in the log",
			Detail: "Without TenjinSDK.debugLogs() the iOS SDK logs only that a request was sent and its HTTP status, so a connect cannot be told from an event.",
			Fix:    "In the iOS app's launch code, call TenjinSDK.debugLogs() inside #if DEBUG … #endif, launch the app again, then run this again.",
		})
	default:
		r.add(Finding{
			ID: "device.no_connect", Severity: Warning, Platform: platform,
			Title:  "The SDK ran on the device but sent no connect request",
			Detail: "connect() may not be reached on this launch, or the request has not completed yet.",
			Fix:    "Check that connect() runs on every launch, then launch the app and run this again.",
		})
	}
	r.checkOtherRequests(d.Log, platform)
}

// lastConnectOf picks the newest connect from the target's own app: one of
// its builds' IDs, or one of them with a suffix such as .debug, or a connect
// that does not say (an iOS log without debug logs). ours is false when the
// log has connects only from other apps, and the newest of them is returned.
func lastConnectOf(log *devicelog.Log, t *project.Target) (connect *devicelog.Request, ours bool) {
	ids := []string{t.BundleID}
	for _, b := range t.Builds {
		ids = append(ids, b.BundleID)
	}
	isOurs := func(running string) bool {
		if running == "" {
			return true
		}
		for _, id := range ids {
			if id != "" && (running == id || strings.HasPrefix(running, id+".")) {
				return true
			}
		}
		return t.BundleID == ""
	}
	var other *devicelog.Request
	for i := len(log.Requests) - 1; i >= 0; i-- {
		c := &log.Requests[i]
		if !c.IsConnect() {
			continue
		}
		if isOurs(c.Params["bundle_id"]) {
			return c, true
		}
		if other == nil {
			other = c
		}
	}
	return other, false
}

// connectFinding explains the collector's answer to the connect request.
func connectFinding(c *devicelog.Request, platform project.Platform) (f Finding) {
	f = Finding{ID: "device.connect", Platform: platform, Severity: Error}
	bundle, sent := c.Params["bundle_id"], c.Params["platform"]
	answer := fmt.Sprintf("The collector answered %d", c.Code)
	if c.Message != "" {
		answer += " " + quote(c.Message)
	}
	f.Detail = answer + "."
	// When it was sent tells an old launch from this one.
	when := ""
	if c.Time != "" {
		when = " Sent at " + c.Time + "."
	}
	defer func() { f.Detail = strings.TrimSpace(f.Detail + when) }()

	switch {
	case c.Accepted():
		f.Severity = Pass
		f.Title = "Tenjin accepted the app's connect request"
		if v := c.Params["sdk_version"]; v != "" {
			f.Title += " (SDK " + v + ")"
		}
		f.Detail = ""
	case c.Code == 401:
		f.Title = "Tenjin rejected the app's connect request: it does not accept this SDK key for " + bundle
		f.Fix = "Use the SDK key from this app's page in the Tenjin dashboard. Each app, one per platform and store, has its own key."
	case c.Code == 404:
		f.Title = "Tenjin has no " + sent + " app with the bundle ID " + bundle
		f.Detail = answer + ". The key is recognised, but the build that ran is not an app Tenjin knows."
		f.Fix = "Create the app in Tenjin with exactly this bundle ID and platform, or run a build that uses the registered one."
	case c.Message == "invalid device identifier":
		f.Title = "Tenjin refused the connect request because it carried no device identifier"
		if platform == project.Android {
			f.Fix = "Declare the AD_ID permission and add play-services-ads-identifier, so the SDK can read the advertising ID."
		}
	case c.Message == "not logged":
		f.Title = "Tenjin refused the connect request because the SDK key is disabled"
		f.Fix = "Enable the key in the Tenjin dashboard, or use another of the app's keys."
	case c.Message == "ignored":
		f.Severity = Warning
		f.Title = "Tenjin ignored the connect request because of an app-version rule"
		f.Detail = answer + ". The app's settings in Tenjin exclude this app version."
	default:
		f.Severity = Warning
		f.Title = "Tenjin gave an unexpected answer to the connect request"
		if c.Code == 0 {
			f.Detail = "The response was " + quote(truncate(c.Response, 120)) + "."
		}
	}
	return f
}

// checkRunningBuild compares the build that ran with the project on disk,
// and reports the identifiers a test-device registration needs.
func (r *runner) checkRunningBuild(t *project.Target, c *devicelog.Request) {
	shipped := func(id string) bool {
		for _, b := range t.Builds {
			if b.BundleID == id {
				return true
			}
		}
		return id == t.BundleID
	}
	if running := c.Params["bundle_id"]; running != "" && t.BundleID != "" && !shipped(running) {
		f := Finding{
			ID: "device.bundle_id", Severity: Warning, Platform: t.Platform,
			Title:  "The build that ran is " + running + ", but the project's application ID is " + t.BundleID,
			Detail: "Tenjin sees the ID of the build that runs. A debug suffix or a product flavor makes it a different app from the one in the project file.",
		}
		if t.Platform == project.IOS {
			f.Title = "The build that ran is " + running + ", but the project's bundle ID is " + t.BundleID
			f.Detail = "Tenjin sees the ID of the build that runs. A different build configuration or scheme can make it a different app from the one in the project file."
		}
		r.add(f)
	}
	if t.Platform == project.IOS {
		r.checkIOSIdentifiers(c)
		return
	}

	advertisingID := c.Params["advertising_id"]
	if advertisingID == "" || advertisingID == zeroAdvertisingID {
		f := Finding{
			ID: "device.advertising_id", Severity: Warning, Platform: project.Android,
			Title:  "The request carried no advertising ID",
			Detail: "Attribution falls back to weaker signals without it.",
			Fix:    "Declare the AD_ID permission and add play-services-ads-identifier. On an emulator, use an image with Google Play services.",
		}
		if c.Params["limit_ad_tracking"] == "true" {
			f.Severity = Info
			f.Detail = "The device has ad tracking limited, so this is expected on it."
			f.Fix = ""
		}
		r.add(f)
		return
	}
	ids := "Advertising ID " + advertisingID
	if dev := c.Params["developer_device_id"]; dev != "" {
		ids += ", developer device ID " + dev
	}
	r.add(Finding{
		ID: "device.identifiers", Severity: Info, Platform: project.Android,
		Title:  "This device's identifiers, for registering it as a test device",
		Detail: ids + ".",
	})
}

// iOS tracking statuses, as the SDK sends them in tracking_status.
const (
	attNotDetermined = "0"
	attAuthorized    = "3"
)

// checkIOSIdentifiers explains an all-zeros IDFA, which iOS sends whenever
// the user has not allowed tracking, and lists the identifiers a test-device
// registration needs.
func (r *runner) checkIOSIdentifiers(c *devicelog.Request) {
	idfa := c.Params["advertising_id"]
	if idfa == "" || idfa == zeroAdvertisingID {
		f := Finding{
			ID: "device.advertising_id", Severity: Info, Platform: project.IOS,
			Title:  "The request carried no IDFA, because tracking is not allowed",
			Detail: "iOS sends an all-zeros IDFA until the user allows tracking. That is expected for users who decline, and on a simulator.",
		}
		switch c.Params["tracking_status"] {
		case attNotDetermined:
			f.Severity = Warning
			f.Title = "connect() ran before the user was asked for tracking permission"
			f.Detail = "The request went out with an all-zeros IDFA because the tracking prompt had not been answered yet. Attribution falls back to weaker signals."
			f.Fix = "Call TenjinSDK.connect() in the completion handler of ATTrackingManager.requestTrackingAuthorization."
		case attAuthorized:
			f.Severity = Warning
			f.Title = "Tracking is allowed, but the request carried no IDFA"
			f.Detail = "This is unusual. On a simulator, or with the device's tracking settings restricted, iOS can still return zeros."
		}
		r.add(f)
	}
	var ids []string
	if idfa != "" && idfa != zeroAdvertisingID {
		ids = append(ids, "IDFA "+idfa)
	}
	if dev := c.Params["developer_device_id"]; dev != "" {
		ids = append(ids, "IDFV "+dev)
	}
	if len(ids) > 0 {
		r.add(Finding{
			ID: "device.identifiers", Severity: Info, Platform: project.IOS,
			Title:  "This device's identifiers, for registering it as a test device",
			Detail: strings.Join(ids, ", ") + ".",
		})
	}
}

// checkProcessed asks Tenjin whether it went on to process the install. An
// accepted request has only been queued; this is the confirmation that it
// came out the other end.
func (r *runner) checkProcessed(t *project.Target, src source, c *devicelog.Request) {
	lookup := r.opts.Device.Lookup
	installationID := c.Params["analytics_installation_id"]
	bundle := c.Params["bundle_id"]
	if bundle == "" {
		bundle = t.BundleID
	}
	// Ask about the app the request went to: on Android that depends on the
	// store the build declares, and on the device's maker.
	platform := requestPlatform(c.Params["platform"], c.Params["source_app_store"])
	if platform == "" {
		platform = string(t.Platform)
	}
	var keys []keyLiteral
	seen := map[string]bool{}
	for _, b := range t.Builds {
		candidates, _ := r.candidateKeys(src, b.ID, t.SeveralBuilds())
		for _, k := range candidates {
			if !seen[k.Value] {
				seen[k.Value] = true
				keys = append(keys, k)
			}
		}
	}
	if len(t.Builds) == 0 {
		keys, _ = r.candidateKeys(src, string(t.Platform), false)
	}
	unchecked := func(why string) {
		r.add(Finding{
			ID: "device.processed.unchecked", Severity: Info, Platform: t.Platform,
			Title: "Whether Tenjin processed the install was not checked: " + why,
		})
	}
	switch {
	case lookup == nil:
		unchecked("the check was offline")
		return
	case installationID == "":
		unchecked("the request carries no installation ID")
		return
	case bundle == "":
		unchecked("the bundle ID is not known")
		return
	case len(keys) == 0:
		unchecked("no SDK key was found to ask with")
		return
	}
	var failed error
	for _, k := range keys {
		attribution, err := lookup.LookupDevice(r.ctx, k.Value, bundle, platform, installationID)
		if err != nil {
			// A key for another platform, or a collector hiccup: another
			// key may still answer.
			failed = err
			continue
		}
		failed = nil
		if attribution.Known {
			title := "Tenjin has processed this device's install"
			if source := firstNonEmpty(attribution.CampaignName, attribution.AdNetwork); source != "" {
				title += ", attributed to " + source
			}
			r.pass("device.processed", t.Platform, title, "", 0)
			return
		}
	}
	if failed != nil {
		r.add(Finding{
			ID: "device.lookup", Severity: Warning, Platform: t.Platform,
			Title:  "Tenjin could not be asked whether it processed the install",
			Detail: "The lookup failed: " + failed.Error() + ".",
			Fix:    "Check the network, and that the SDK key in the project is this app's, then run this again.",
		})
		return
	}
	r.add(Finding{
		ID: "device.processed", Severity: Info, Platform: t.Platform,
		Title:  "Tenjin accepted the request but shows no install for this device yet",
		Detail: "Processing usually takes a few seconds after the first launch. If it stays this way, requests are being accepted and not attributed.",
		Fix:    "Run this again in a moment.",
	})
}

// requestPlatform is the platform the collector files a request under,
// from the platform and store the SDK sent. It mirrors the collector's rule.
func requestPlatform(sent, store string) string {
	switch strings.ToLower(sent) {
	case "ios":
		return "ios"
	case "android":
		return project.TenjinPlatform(project.Android, store)
	case "amazon", "kindle", "fireos":
		return "amazon"
	case "android_other", "other":
		return "android_other"
	}
	return ""
}

// checkOtherRequests summarises everything the SDK sent besides connect.
func (r *runner) checkOtherRequests(log *devicelog.Log, platform project.Platform) {
	accepted := map[string]int{}
	var refused []string
	for _, req := range log.Requests {
		if req.IsConnect() {
			continue
		}
		if req.Accepted() {
			accepted[req.Endpoint]++
			continue
		}
		refused = append(refused, strings.TrimSpace(fmt.Sprintf("%s: %d %s", req.Endpoint, req.Code, req.Message)))
	}
	if len(refused) > 0 {
		shown := refused
		if len(shown) > 3 {
			shown = append(shown[:3:3], fmt.Sprintf("and %d more", len(refused)-3))
		}
		r.add(Finding{
			ID: "device.requests", Severity: Warning, Platform: platform,
			Title:  countNoun(len(refused), "other request was", "other requests were") + " not accepted",
			Detail: strings.Join(shown, "; ") + ".",
		})
		return
	}
	if len(accepted) == 0 {
		return
	}
	kinds := make([]string, 0, len(accepted))
	total := 0
	for endpoint, n := range accepted {
		kinds = append(kinds, fmt.Sprintf("%d %s", n, endpoint))
		total += n
	}
	sort.Strings(kinds)
	r.pass("device.requests", platform,
		countNoun(total, "other request was", "other requests were")+" accepted: "+strings.Join(kinds, ", "), "", 0)
}

func countNoun(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// hasUnread reports whether the log has requests whose contents were not
// logged.
func hasUnread(log *devicelog.Log) bool {
	for _, req := range log.Requests {
		if req.Unread {
			return true
		}
	}
	return false
}

// refusedUnread is the newest unread request Tenjin refused, or nil.
func refusedUnread(log *devicelog.Log) *devicelog.Request {
	for i := len(log.Requests) - 1; i >= 0; i-- {
		if req := &log.Requests[i]; req.Unread && req.Code != 0 && !req.Accepted() {
			return req
		}
	}
	return nil
}
