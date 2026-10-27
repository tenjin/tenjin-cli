package verify

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/guides"
	"github.com/tenjin/tenjin-cli/internal/project"
)

// featureCalls are the Tenjin calls that send each feature, across the SDKs.
// Distinct names are Tenjin's own and count wherever they appear in a file
// that mentions Tenjin. Generic names, such as transaction, count only when
// called on a Tenjin receiver, so that a database's transaction() is not
// taken for a purchase.
var featureCalls = map[string]struct{ distinct, generic []string }{
	project.FeaturePurchases: {
		distinct: []string{"transactionWithReceipt", "transactionWithDataSignature", "transactionAmazon", "transactionWithProductName", "enableAutoPurchaseTracking"},
		generic:  []string{"transaction", "Transaction"},
	},
	project.FeatureSubscriptions: {
		distinct: []string{"subscriptionWithStoreKit", "subscriptionWithStoreKitForProductId", "subscriptionWithProductName", "SubscriptionWithStoreKit", "enableAutoSubscriptionTracking", "handleSubscriptionPurchase"},
		generic:  []string{"subscription", "Subscription"},
	},
	project.FeatureConsent: {
		distinct: []string{"optInParams", "optOutParams", "optInGoogleDMA", "optOutGoogleDMA", "optInOutUsingCMP", "setGoogleDMAParameters", "setGoogleDMAParametersWithAdPersonalization", "OptInParams", "OptOutParams", "OptInGoogleDMA", "OptOutGoogleDMA", "OptInOutUsingCMP", "SetGoogleDMAParameters"},
		generic:  []string{"optIn", "optOut", "OptIn", "OptOut"},
	},
	project.FeatureDeepLinks: {
		distinct: []string{"registerDeepLinkHandler", "getAttributionInfo", "GetAttributionInfo", "connectWithDeferredDeeplink", "handleOpenURL", "handleOpenURLString", "handleOpenIntent", "handleOpenUrl"},
		generic:  []string{"getDeeplink", "GetDeeplink"},
	},
	project.FeatureSKAN: {
		distinct: []string{"updatePostbackConversionValue", "UpdatePostbackConversionValue", "updateSkAdNetworkConversionValue"},
		generic:  []string{"updateConversionValue"},
	},
	project.FeatureCustomEvents: {
		distinct: []string{"eventWithNameAndValue", "sendEventWithName"},
		generic:  []string{"eventWithName", "sendEvent", "SendEvent"},
	},
	// Other SDKs (AppsFlyer, for one) have a setCustomerUserId too: it
	// counts only on a Tenjin receiver.
	project.FeatureUserID: {
		generic: []string{"setCustomerUserId", "SetCustomerUserId"},
	},
}

// adRevenueCalls are the calls that pass one ad network's impressions to
// Tenjin, by the name project detection gives the network. They are taken
// from the SDKs' sources (Android, iOS, Unity, and the wrappers), not only
// the guides: the iOS SDK's handleAdMobILRD:, for one, is in no guide.
var adRevenueCalls = map[string][]string{
	"AppLovin MAX": {"eventAdImpressionAppLovin", "eventAdImpressionDataAppLovin", "subscribeAppLovinImpressions", "SubscribeAppLovinImpressions",
		"appLovinImpressionFromJSON", "AppLovinImpressionFromJSON"},
	"Google AdMob": {"eventAdImpressionAdMob", "eventAdImpressionAdMobNextGen", "eventAdImpressionDataAdMob", "adMobImpressionFromJSON", "AdMobImpressionFromJSON",
		"handleAdMobILRD", "SubscribeAdMobBannerViewImpressions", "SubscribeAdMobInterstitialAdImpressions", "SubscribeAdMobRewardedAdImpressions", "SubscribeAdMobRewardedInterstitialAdImpressions"},
	"ironSource LevelPlay": {"eventAdImpressionIronSource", "eventAdImpressionDataIronSource", "subscribeIronSourceImpressions", "SubscribeIronSourceImpressions",
		"ironSourceImpressionFromJSON", "IronSourceImpressionFromJSON", "SubscribeLevelPlayBannerAdImpressions", "SubscribeLevelPlayBannerImpressions",
		"SubscribeLevelPlayInterstitialAdImpressions", "SubscribeLevelPlayInterstitialImpressions", "SubscribeLevelPlayRewardedAdImpressions", "SubscribeLevelPlayRewardedImpressions"},
	"TopOn": {"eventAdImpressionTopOn", "eventAdImpressionDataTopOn", "topOnImpressionFromDict", "topOnImpressionFromJSON", "TopOnImpressionFromJSON", "SubscribeTopOnImpressions"},
	"TradPlus": {"eventAdImpressionTradPlus", "eventAdImpressionTradPlusAdInfo", "eventAdImpressionDataTradPlus", "subscribeTradPlusImpressions", "SubscribeTradPlusImpressions",
		"tradPlusImpressionFromJSON", "TradPlusImpressionFromJSON", "handleTradPlusILRD"},
	"CAS": {"eventAdImpressionCAS", "eventAdImpressionDataCAS", "subscribeCASImpressions", "subscribeCASBannerImpressions", "SubscribeCASImpressions", "SubscribeCASBannerImpressions",
		"casImpressionFromJSON", "CASImpressionFromJSON", "handleCASILRD"},
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
	featureSourceExts = []string{".kt", ".java", ".swift", ".m", ".mm", ".dart", ".js", ".jsx", ".ts", ".tsx", ".cs"}
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

func (r *runner) tenjinCode() *tenjinCode {
	if r.code != nil {
		return r.code
	}
	r.code = &tenjinCode{}
	for _, rel := range project.SourceFiles(r.p.Root, ".", featureSourceExts...) {
		path := filepath.Join(r.p.Root, filepath.FromSlash(rel))
		if info, err := os.Stat(path); err != nil || info.Size() > maxSourceFileSize {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil || !strings.Contains(strings.ToLower(string(raw)), "tenjin") {
			continue
		}
		text := blankComments(project.Text(raw))
		receivers := []string{`TenjinSDK\s*\.\s*instance`, "TenjinSDK", "Tenjin", "instance", "tenjin", "tenjinSDK", `getInstance\([^()]*(?:\([^()]*\)[^()]*)*\)`}
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
	for _, f := range r.p.Features {
		switch f.ID {
		case project.FeatureOpens:
			continue
		case project.FeatureAdRevenue:
			r.checkAdRevenue(f, chosen[f.ID], code)
			continue
		}
		calls := featureCalls[f.ID]
		if file, line, ok := code.find(calls.distinct, calls.generic); ok {
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
