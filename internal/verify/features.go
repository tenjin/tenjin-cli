package verify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/tenjin/tenjin-cli/internal/guides"
	"github.com/tenjin/tenjin-cli/internal/project"
)

// featureCalls are the Tenjin calls that send each feature, across the SDKs.
// Distinct names are Tenjin's own and count wherever they appear in a file
// that mentions Tenjin. Generic names, such as transaction, count only when
// called on a Tenjin receiver, so that a database's transaction() is not
// taken for a purchase. Properties are Kotlin's names for a getter and
// setter pair, set on a Tenjin receiver: tenjin.customerUserId = id.
var featureCalls = map[string]struct{ distinct, generic, property []string }{
	project.FeaturePurchases: {
		distinct: []string{"transactionWithReceipt", "transactionWithDataSignature", "transactionAmazon", "transactionWithProductName", "enableAutoPurchaseTracking", "transaction_with_receipt", "transaction_with_data_signature"},
		generic:  []string{"transaction", "Transaction"},
	},
	project.FeatureSubscriptions: {
		distinct: []string{"subscriptionWithStoreKit", "subscriptionWithStoreKitForProductId", "subscriptionWithProductName", "SubscriptionWithStoreKit", "enableAutoSubscriptionTracking", "handleSubscriptionPurchase", "subscription_with_storekit"},
		generic:  []string{"subscription", "Subscription"},
	},
	project.FeatureConsent: {
		distinct: []string{"optInParams", "optOutParams", "optInGoogleDMA", "optOutGoogleDMA", "optInOutUsingCMP", "setGoogleDMAParameters", "setGoogleDMAParametersWithAdPersonalization", "OptInParams", "OptOutParams", "OptInGoogleDMA", "OptOutGoogleDMA", "OptInOutUsingCMP", "SetGoogleDMAParameters", "opt_in_params", "opt_out_params", "opt_in_google_dma", "opt_out_google_dma", "opt_in_out_using_cmp", "set_google_dma_parameters"},
		generic:  []string{"optIn", "optOut", "OptIn", "OptOut", "opt_in", "opt_out"},
	},
	// handleOpenURL is UIKit's too (application:handleOpenURL:, the delegate
	// method that receives a URL), so the handlers count on Tenjin only.
	project.FeatureDeepLinks: {
		distinct: []string{"registerDeepLinkHandler", "getAttributionInfo", "GetAttributionInfo", "connectWithDeferredDeeplink", "handleOpenIntent", "register_deep_link_handler", "get_attribution_info", "connect_sdk_with_deferred_deeplink"},
		generic:  []string{"getDeeplink", "GetDeeplink", "handleOpenURL", "handleOpenURLString", "handleOpenUrl"},
	},
	project.FeatureSKAN: {
		distinct: []string{"updatePostbackConversionValue", "UpdatePostbackConversionValue", "updateSkAdNetworkConversionValue", "update_postback_conversion_value"},
		generic:  []string{"updateConversionValue"},
	},
	project.FeatureCustomEvents: {
		distinct: []string{"eventWithNameAndValue", "sendEventWithName", "event_with_name_and_value"},
		generic:  []string{"eventWithName", "sendEvent", "SendEvent", "event_with_name"},
	},
	// Other SDKs (AppsFlyer, for one) have a setCustomerUserId too: it
	// counts only on a Tenjin receiver.
	project.FeatureUserID: {
		generic:  []string{"setCustomerUserId", "SetCustomerUserId", "set_customer_user_id"},
		property: []string{"customerUserId"},
	},
}

// adRevenueCalls are the calls that pass one ad network's impressions to
// Tenjin, by the name project detection gives the network. They are taken
// from the SDKs' sources (Android, iOS, Unity, Godot, and the wrappers), not only
// the guides: the iOS SDK's handleAdMobILRD:, for one, is in no guide.
var adRevenueCalls = map[string][]string{
	"AppLovin MAX": {"eventAdImpressionAppLovin", "eventAdImpressionDataAppLovin", "subscribeAppLovinImpressions", "SubscribeAppLovinImpressions",
		"appLovinImpressionFromJSON", "AppLovinImpressionFromJSON", "event_ad_impression_applovin"},
	"Google AdMob": {"eventAdImpressionAdMob", "eventAdImpressionAdMobNextGen", "eventAdImpressionDataAdMob", "adMobImpressionFromJSON", "AdMobImpressionFromJSON",
		"handleAdMobILRD", "SubscribeAdMobBannerViewImpressions", "SubscribeAdMobInterstitialAdImpressions", "SubscribeAdMobRewardedAdImpressions", "SubscribeAdMobRewardedInterstitialAdImpressions", "event_ad_impression_admob"},
	"ironSource LevelPlay": {"eventAdImpressionIronSource", "eventAdImpressionDataIronSource", "subscribeIronSourceImpressions", "SubscribeIronSourceImpressions",
		"ironSourceImpressionFromJSON", "IronSourceImpressionFromJSON", "SubscribeLevelPlayBannerAdImpressions", "SubscribeLevelPlayBannerImpressions",
		"SubscribeLevelPlayInterstitialAdImpressions", "SubscribeLevelPlayInterstitialImpressions", "SubscribeLevelPlayRewardedAdImpressions", "SubscribeLevelPlayRewardedImpressions", "event_ad_impression_ironsource"},
	"TopOn": {"eventAdImpressionTopOn", "eventAdImpressionDataTopOn", "topOnImpressionFromDict", "topOnImpressionFromJSON", "TopOnImpressionFromJSON", "SubscribeTopOnImpressions", "event_ad_impression_topon"},
	"TradPlus": {"eventAdImpressionTradPlus", "eventAdImpressionTradPlusAdInfo", "eventAdImpressionDataTradPlus", "subscribeTradPlusImpressions", "SubscribeTradPlusImpressions",
		"tradPlusImpressionFromJSON", "TradPlusImpressionFromJSON", "handleTradPlusILRD", "event_ad_impression_tradplus"},
	"CAS": {"eventAdImpressionCAS", "eventAdImpressionDataCAS", "subscribeCASImpressions", "subscribeCASBannerImpressions", "SubscribeCASImpressions", "SubscribeCASBannerImpressions",
		"casImpressionFromJSON", "CASImpressionFromJSON", "handleCASILRD", "event_ad_impression_cas"},
}

// featureSent says, for each feature, that the project sends it.
var featureSent = map[string]string{
	project.FeaturePurchases:     "In-app purchases are sent to Tenjin",
	project.FeatureSubscriptions: "Subscriptions are sent to Tenjin",
	project.FeatureConsent:       "The user's consent is passed to Tenjin",
	project.FeatureDeepLinks:     "Deep links are read from Tenjin",
	project.FeatureSKAN:          "SKAdNetwork conversion values are updated through Tenjin",
	project.FeatureCustomEvents:  "Custom events are sent to Tenjin",
	project.FeatureUserID:        "The customer user ID is passed to Tenjin",
}

// featureTopics name the guide section for each feature.
var featureTopics = map[string]string{
	project.FeaturePurchases:     "purchase event tracking",
	project.FeatureSubscriptions: "subscriptions",
	project.FeatureAdRevenue:     "impression level ad revenue (ILRD)",
	project.FeatureConsent:       "GDPR and privacy compliance",
	project.FeatureDeepLinks:     "deep links",
	project.FeatureSKAN:          "SKAdNetwork conversion values",
	project.FeatureCustomEvents:  "custom events",
	project.FeatureUserID:        "user identity",
}

var (
	tenjinReceiverVar = regexp.MustCompile(`\b(\w+)\s*(?::\s*[\w?]+\s*)?=\s*(?:TenjinSDK|Tenjin)\s*\.\s*(?:getInstance|instance|sharedInstance)\b|\b(?:TenjinSDK|BaseTenjin)\s+(\w+)\s*=`)
	// featureSourceExts are the source files of every framework, for the
	// checks that read the whole project's code: the native languages,
	// Dart, the JavaScript wrappers' (an Ionic Vue app sends from .vue
	// components), Unity's C# and Godot's GDScript.
	featureSourceExts = []string{".kt", ".java", ".swift", ".m", ".mm", ".dart", ".js", ".jsx", ".ts", ".tsx", ".vue", ".cs", ".gd"}
)

// tenjinCode is the text of every source file that mentions Tenjin.
type tenjinCode struct {
	files []codeFile
}

type codeFile struct {
	rel, text string
	// generic matches a generic call on one of the file's Tenjin receivers.
	receivers []string
}

// tenjinCode reads the project's code, and that of the local libraries the
// app gets the SDK through (project.LibraryCode, declared as dependencies
// and possibly above the root), whose calls are the app's too.
func (r *runner) tenjinCode() *tenjinCode {
	if r.code != nil {
		return r.code
	}
	r.code = &tenjinCode{}
	seen := map[string]bool{}
	var files []string
	for _, dir := range append([]string{"."}, r.p.LibraryCode...) {
		for _, rel := range project.SourceFiles(r.p.Root, dir, featureSourceExts...) {
			if !seen[rel] {
				seen[rel] = true
				files = append(files, rel)
			}
		}
	}
	for _, rel := range files {
		if testSource(rel) || r.p.Framework == project.Unity && project.UnitySDKFile(rel) || r.p.Framework == project.Godot && project.GodotSDKFile(rel) {
			continue
		}
		path := filepath.Join(r.p.Root, filepath.FromSlash(rel))
		if info, err := os.Stat(path); err != nil || info.Size() > maxSourceFileSize {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil || !strings.Contains(strings.ToLower(string(raw)), "tenjin") {
			continue
		}
		text := blankComments(project.Text(raw))
		if strings.HasSuffix(rel, ".gd") {
			text = project.BlankHashComments(text)
		}
		// Another SDK's getInstance() is not Tenjin's, nor is Other.instance,
		// nor an instance the file sets from something else.
		receivers := []string{`TenjinSDK\s*\.\s*instance`, "TenjinSDK", "Tenjin", "tenjin", "tenjinSDK", `(?:TenjinSDK|Tenjin)\s*\.\s*getInstance\w*\([^()]*(?:\([^()]*\)[^()]*)*\)`}
		for _, name := range project.TenjinBareNames(text) {
			receivers = append(receivers, `(?:(?m:^)|[^\w$.\n])`+regexp.QuoteMeta(name))
		}
		for _, name := range helperAliases(text) {
			receivers = append(receivers, regexp.QuoteMeta(name))
		}
		for _, m := range tenjinReceiverVar.FindAllStringSubmatch(text, -1) {
			for _, name := range m[1:] {
				if name != "" {
					receivers = append(receivers, regexp.QuoteMeta(name))
				}
			}
		}
		r.code.files = append(r.code.files, codeFile{rel: rel, text: text, receivers: receivers})
	}
	return r.code
}

// assigns returns where one of the properties is set on a Tenjin receiver,
// if anywhere.
func (c *tenjinCode) assigns(properties []string) (file string, line int, ok bool) {
	for _, f := range c.files {
		re := regexp.MustCompile(`(?:` + strings.Join(f.receivers, "|") + `)\s*\.\s*(?:` + strings.Join(properties, "|") + `)\s*=[^=]`)
		if loc := re.FindStringIndex(f.text); loc != nil {
			return f.rel, lineAt(f.text, loc[0]), true
		}
	}
	return "", 0, false
}

// find returns where one of the calls is made, if anywhere.
func (c *tenjinCode) find(distinct, generic []string) (file string, line int, ok bool) {
	for _, f := range c.files {
		var alternatives []string
		if len(distinct) > 0 {
			alternatives = append(alternatives, `\b(?:`+strings.Join(distinct, "|")+`)\b`)
		}
		if len(generic) > 0 {
			calls := strings.Join(generic, "|")
			alternatives = append(alternatives,
				`(?:`+strings.Join(f.receivers, "|")+`)\s*\.\s*(?:`+calls+`)\s*\(`,
				`\[\s*(?:TenjinSDK|\[TenjinSDK\s+sharedInstance\])\s+(?:`+calls+`)\b`)
		}
		re := regexp.MustCompile(strings.Join(alternatives, "|"))
		if loc := re.FindStringIndex(f.text); loc != nil {
			return f.rel, lineAt(f.text, loc[0]), true
		}
	}
	return "", 0, false
}

// checkFeatures reports, for each feature beyond app opens, whether the
// project sends it to Tenjin. A chosen feature that is not sent is a
// warning; one the project only suggests is a suggestion. One that is
// neither is not mentioned unless it is sent.
func (r *runner) checkFeatures() {
	chosen := map[string]bool{}
	for _, id := range r.opts.Features {
		chosen[id] = true
	}
	code := r.tenjinCode()
	if len(r.p.Tenjin) == 0 {
		// Without the SDK, a call of the same name is another library's.
		code = &tenjinCode{}
	}
	for _, f := range r.p.Features {
		switch f.ID {
		case project.FeatureOpens:
			continue
		case project.FeatureAdRevenue:
			r.checkAdRevenue(f, chosen[f.ID], code)
			continue
		case project.FeatureCustomEvents:
			if len(r.opts.Events) > 0 {
				r.checkChosenEvents()
				continue
			}
		case project.FeatureUserID:
			if r.checkChosenUserID() {
				continue
			}
		}
		calls := featureCalls[f.ID]
		file, line, ok := code.find(calls.distinct, calls.generic)
		if !ok && len(calls.property) > 0 {
			file, line, ok = code.assigns(calls.property)
		}
		if ok {
			r.pass("feature."+f.ID, "", featureSent[f.ID], file, line)
			if f.ID == project.FeaturePurchases || f.ID == project.FeatureSubscriptions {
				r.checkAutoTracking(code)
			}
			continue
		}
		r.unsent(f, chosen[f.ID], f.Name, evidenceText(f.Evidence))
	}
}

// checkAdRevenue looks for each ad network's call separately: revenue from
// one network says nothing about another.
func (r *runner) checkAdRevenue(f project.Feature, chosen bool, code *tenjinCode) {
	if len(f.Evidence) == 0 {
		if chosen {
			r.unsent(f, true, f.Name, "")
		}
		return
	}
	for _, e := range f.Evidence {
		calls, known := adRevenueCalls[e.What]
		if !known {
			continue
		}
		// eventAdImpression(network, data) takes any network's impressions.
		if file, line, ok := code.find(calls, []string{"eventAdImpression"}); ok {
			r.pass("feature.ad_revenue", "", "Ad revenue from "+e.What+" is sent to Tenjin", file, line)
			continue
		}
		r.unsent(f, chosen, "Ad revenue from "+e.What, e.What+" ("+e.File+")")
	}
}

// unsent reports a feature that nothing sends to Tenjin.
func (r *runner) unsent(f project.Feature, chosen bool, what, evidence string) {
	if !chosen && !f.Suggested {
		return
	}
	finding := Finding{ID: "feature." + f.ID, Severity: Info, Fix: featureFix(r.p, f.ID)}
	if chosen {
		finding.Severity = Warning
		finding.Title = what + ": chosen for the integration, but not sent to Tenjin"
	} else {
		finding.Title = what + ": could be sent to Tenjin"
		finding.Detail = "It was not chosen for the integration. The project suggests it: " + evidence + "."
	}
	if chosen && evidence != "" {
		finding.Detail = "The project has " + evidence + "."
	}
	r.add(finding)
}

func evidenceText(evidence []project.Evidence) string {
	var out []string
	for _, e := range evidence {
		out = append(out, e.What+" ("+e.File+")")
	}
	return strings.Join(out, ", ")
}

// featureFix points at the guide section for a feature.
func featureFix(p *project.Project, id string) string {
	topic := featureTopics[id]
	switch name := guides.ForProject(p); {
	case name != "":
		return "Follow the section on " + topic + " in tenjin sdk guide " + name + "."
	case p.Framework == project.Native:
		return "Follow the section on " + topic + " in tenjin sdk guide android and tenjin sdk guide ios."
	case p.Framework == project.Godot:
		return "Follow the section on " + topic + " in the Godot SDK's README, https://github.com/tenjin/tenjin-godot-sdk#usage."
	}
	return "Follow the section on " + topic + " in the Tenjin SDK guide for this platform."
}

// checkAutoTracking looks at the SDKs' automatic purchase tracking, once:
// on Android it needs the Play Billing library at runtime, which the SDK
// does not bring; on iOS, alongside the transaction calls that pass no
// transaction ID, it reports the same purchase twice.
func (r *runner) checkAutoTracking(code *tenjinCode) {
	if r.autoTrackingChecked {
		return
	}
	r.autoTrackingChecked = true
	auto := []string{"enableAutoPurchaseTracking", "enableAutoSubscriptionTracking"}
	file, line, ok := code.find(auto, nil)
	if !ok {
		return
	}
	if t := r.p.Target(project.Android); t != nil && t.Android != nil {
		deps := r.androidDeps(t)
		if !strings.Contains(deps, "com.android.billingclient") && !strings.Contains(deps, "billing-ktx") {
			r.add(Finding{
				ID: "feature.auto_tracking.billing", Severity: Warning, Platform: project.Android,
				Title:  "Automatic purchase tracking is on, and Google Play Billing is not a dependency",
				Detail: "The Android SDK reads purchases through the Play Billing library, which it does not bring itself; without it nothing is tracked.",
				File:   file, Line: line,
				Fix: `Add implementation("com.android.billingclient:billing:` + playBillingVersion + `") to the app module, or turn automatic tracking off.`,
			})
		}
	}
	if r.p.Target(project.IOS) != nil {
		if f, l, ok := code.find([]string{"transactionWithProductName"}, nil); ok {
			r.add(Finding{
				ID: "feature.auto_tracking.double", Severity: Warning, Platform: project.IOS,
				Title:  "Automatic purchase tracking is on alongside transaction calls that pass no transaction ID",
				Detail: "The iOS SDK cannot tell those purchases from the ones it tracks itself, so each is reported twice.",
				File:   f, Line: l,
				Fix: "Keep one: remove the transactionWithProductName calls, or turn automatic purchase tracking off.",
			})
		}
	}
}

// sentToTenjin is what the app sends Tenjin as custom events and customer
// user IDs: nothing without the SDK, where a call of the same name is
// another library's.
func (r *runner) sentToTenjin() project.Analytics {
	if len(r.p.Tenjin) == 0 {
		return project.Analytics{}
	}
	return r.p.SentToTenjin()
}

// checkChosenEvents reports the custom events chosen that the app does not
// send to Tenjin by name. The names come from the developer or the app's
// other analytics and reach a coding agent's task, so the finding says them
// as JSON strings.
func (r *runner) checkChosenEvents() {
	sent := r.sentToTenjin()
	var missing []string
	for _, name := range r.opts.Events {
		if len(sent.EventsNamed(name)) == 0 {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		first := sent.EventsNamed(r.opts.Events[0])[0]
		r.pass("feature."+project.FeatureCustomEvents, "", featureSent[project.FeatureCustomEvents]+", each one chosen", first.File, first.Line)
		return
	}
	detail := "Not sent to Tenjin by name, each a JSON string: " + jsonStrings(missing) + "."
	var runtime []string
	for _, e := range sent.Events {
		if e.Name == "" && e.Token == "" {
			runtime = append(runtime, fmt.Sprintf("%s:%d", e.File, e.Line))
		}
	}
	if len(runtime) > 0 {
		detail += " The app also sends Tenjin events named at runtime (" + strings.Join(runtime[:min(len(runtime), 3)], ", ") + "), whose names this check cannot read."
	}
	r.add(Finding{
		ID: "feature." + project.FeatureCustomEvents, Severity: Warning,
		Title:  fmt.Sprintf("Custom events chosen but not sent to Tenjin: %d of %d", len(missing), len(r.opts.Events)),
		Detail: detail,
		Fix:    featureFix(r.p, project.FeatureCustomEvents),
	})
}

// checkChosenUserID reports whether the customer user ID chosen is the one
// the app passes Tenjin, and says whether it reported. None passed at all is
// left to the feature's own check.
func (r *runner) checkChosenUserID() bool {
	chosen := strings.TrimSpace(r.opts.UserID)
	if chosen == "" {
		return false
	}
	passed := r.sentToTenjin().UserIDs
	if len(passed) == 0 {
		return false
	}
	for _, u := range passed {
		if sameExpression(u.Expression, chosen) {
			r.pass("feature."+project.FeatureUserID, "", "The customer user ID chosen is passed to Tenjin", u.File, u.Line)
			return true
		}
	}
	if !r.opts.UserIDFromCode && strings.ContainsFunc(chosen, unicode.IsSpace) {
		// Perhaps words, such as "the account ID from the login response":
		// whether the code passed is that value cannot be told.
		r.add(Finding{
			ID: "feature." + project.FeatureUserID, Severity: Info,
			Title:  "A customer user ID is passed to Tenjin; whether it is the one chosen, as said, is not checked",
			Detail: "Chosen, as a JSON string: " + jsonStrings([]string{chosen}) + ".",
			File:   passed[0].File, Line: passed[0].Line,
		})
		return true
	}
	var where []string
	for _, u := range passed[:min(len(passed), 3)] {
		where = append(where, fmt.Sprintf("%s (%s:%d)", jsonStrings([]string{u.Expression}), u.File, u.Line))
	}
	r.add(Finding{
		ID: "feature." + project.FeatureUserID, Severity: Warning,
		Title:  "The customer user ID passed to Tenjin is not the one chosen",
		Detail: "Chosen, as a JSON string: " + jsonStrings([]string{chosen}) + ". Passed to Tenjin: " + strings.Join(where, ", ") + ".",
		File:   passed[0].File, Line: passed[0].Line,
		Fix: featureFix(r.p, project.FeatureUserID),
	})
	return true
}

// sameExpression reports whether the code the app passes is the expression
// chosen, as a whole: user.id is not previous.user.id, and a transformation
// of it, such as user.id!!, is not taken for the same value. Only what
// cannot change the value is left out of the comparison: the spaces between
// tokens, and parentheses around the whole, so (user.id) is user.id.
func sameExpression(passed, chosen string) bool {
	return bareExpression(passed) == bareExpression(chosen)
}

// bareExpression is expr without spaces between tokens, except one between
// two words (x as String), and without parentheses that group all of it.
// Text inside a string literal is kept as it is.
func bareExpression(expr string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		switch {
		case quote != 0:
			b.WriteByte(c)
			if c == '\\' && i+1 < len(expr) {
				i++
				b.WriteByte(expr[i])
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
			b.WriteByte(c)
		case isSpace(c):
			j := skipSpace(expr, i)
			if out := b.String(); out != "" && isWord(out[len(out)-1]) && j < len(expr) && isWord(expr[j]) {
				b.WriteByte(' ')
			}
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	bare := b.String()
	for len(bare) >= 2 && bare[0] == '(' && groupEnd(bare) == len(bare)-1 {
		bare = bare[1 : len(bare)-1]
	}
	return bare
}

// groupEnd returns the index of the parenthesis that closes the one that
// starts expr, or -1. Parentheses inside string literals do not count.
func groupEnd(expr string) int {
	depth := 0
	var quote byte
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// jsonStrings writes values from the developer or the app's code as JSON
// strings, comma-separated.
func jsonStrings(values []string) string {
	out := make([]string, len(values))
	for i, v := range values {
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(v)
		out[i] = strings.TrimSuffix(b.String(), "\n")
	}
	return strings.Join(out, ", ")
}
