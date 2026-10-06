package project

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Feature is a part of the Tenjin SDK an integration can include. App opens
// are required; the others are chosen, and suggested where the project shows
// it does what they measure.
type Feature struct {
	// ID is how the feature is named in flags: opens, purchases,
	// subscriptions, ad_revenue, consent, deep_links, skan, custom_events,
	// user_id.
	ID       string `json:"id"`
	Name     string `json:"name"`
	Required bool   `json:"required,omitempty"`
	// Suggested means the project shows a reason to include it, given in
	// Evidence.
	Suggested bool       `json:"suggested"`
	Evidence  []Evidence `json:"evidence,omitempty"`
}

// Evidence is one reason a feature is suggested.
type Evidence struct {
	What string `json:"what"`
	File string `json:"file"`
}

// The features, in the order they are offered.
const (
	FeatureOpens         = "opens"
	FeaturePurchases     = "purchases"
	FeatureSubscriptions = "subscriptions"
	FeatureAdRevenue     = "ad_revenue"
	FeatureConsent       = "consent"
	FeatureDeepLinks     = "deep_links"
	FeatureSKAN          = "skan"
	FeatureCustomEvents  = "custom_events"
	FeatureUserID        = "user_id"
)

var featureNames = []struct{ id, name string }{
	{FeatureOpens, "App opens and attribution"},
	{FeaturePurchases, "In-app purchases"},
	{FeatureSubscriptions, "Subscriptions"},
	{FeatureAdRevenue, "Ad revenue (ILRD)"},
	{FeatureConsent, "Consent (GDPR)"},
	{FeatureDeepLinks, "Deep links"},
	{FeatureSKAN, "SKAdNetwork conversion values"},
	{FeatureCustomEvents, "Custom events"},
	{FeatureUserID, "Customer user ID"},
}

// FeatureIDs lists every feature ID, in order.
func FeatureIDs() []string {
	out := make([]string, len(featureNames))
	for i, f := range featureNames {
		out[i] = f.id
	}
	return out
}

// FeatureName is how a feature is called for people.
func FeatureName(id string) string {
	for _, f := range featureNames {
		if f.id == id {
			return f.name
		}
	}
	return id
}

// Feature returns the project's entry for a feature, or nil when it does
// not apply to the project.
func (p *Project) Feature(id string) *Feature {
	for i := range p.Features {
		if p.Features[i].ID == id {
			return &p.Features[i]
		}
	}
	return nil
}

// signal is something in a project that suggests a feature.
type signal struct {
	feature, what string
	pattern       *regexp.Regexp
}

// Signals in the files that declare dependencies.
var dependencySignals = []signal{
	{FeaturePurchases, "Google Play Billing", regexp.MustCompile(`com\.android\.billingclient:billing`)},
	{FeaturePurchases, "RevenueCat", regexp.MustCompile(`com\.revenuecat\.purchases|['"]RevenueCat['"]|- RevenueCat\b|purchases-ios|\bpurchases_flutter\s*:|"react-native-purchases"|"@revenuecat/purchases-capacitor"|"com\.revenuecat\.purchases-unity"`)},
	{FeatureSubscriptions, "RevenueCat, used mostly for subscriptions", regexp.MustCompile(`com\.revenuecat\.purchases|['"]RevenueCat['"]|- RevenueCat\b|purchases-ios|\bpurchases_flutter\s*:|"react-native-purchases"|"@revenuecat/purchases-capacitor"|"com\.revenuecat\.purchases-unity"`)},
	{FeaturePurchases, "in_app_purchase", regexp.MustCompile(`\bin_app_purchase\s*:`)},
	{FeaturePurchases, "a React Native in-app purchase library", regexp.MustCompile(`"react-native-iap"|"expo-in-app-purchases"|"expo-iap"`)},
	{FeaturePurchases, "an in-app purchase plugin", regexp.MustCompile(`"cordova-plugin-purchase"|"@capgo/native-purchases"`)},
	{FeaturePurchases, "Unity IAP", regexp.MustCompile(`"com\.unity\.purchasing"`)},
	{FeatureConsent, "Google's consent SDK (UMP)", regexp.MustCompile(`com\.google\.android\.ump:user-messaging-platform|GoogleUserMessagingPlatform`)},
	{FeatureConsent, "a consent management platform", regexp.MustCompile(`(?i)com\.onetrust|onetrust-publishers|usercentrics|didomi|sourcepoint`)},
}

// Signals in the app's own code.
var codeSignals = []signal{
	{FeaturePurchases, "StoreKit purchases", regexp.MustCompile(`\bSKPaymentQueue\b|\bSKProductsRequest\b|\bProduct\.products\(|\bTransaction\.updates\b|\bAppStore\.sync\(`)},
	{FeaturePurchases, "Google Play Billing", regexp.MustCompile(`\bBillingClient\b`)},
	{FeatureSubscriptions, "subscription products", regexp.MustCompile(`ProductType\.SUBS\b|SkuType\.SUBS\b|\.autoRenewable\b|\bsubscriptionPeriod\b|\bSKProductSubscriptionPeriod\b|ProductType\.Subscription\b`)},
	{FeatureConsent, "a consent form", regexp.MustCompile(`\bUMPConsentInformation\b|ConsentInformation\.getInstance\(|\bUserMessagingPlatform\.|ConsentInformation\.instance\b|\bConsentInformation\.Update\(`)},
}

var (
	featureCodeExts = []string{".kt", ".java", ".swift", ".m", ".mm", ".dart", ".js", ".jsx", ".ts", ".tsx", ".cs"}
	viewIntent      = regexp.MustCompile(`android\.intent\.action\.VIEW`)
	urlSchemes      = regexp.MustCompile(`CFBundleURLSchemes`)
	associated      = regexp.MustCompile(`com\.apple\.developer\.associated-domains`)
)

const maxFeatureScanFiles = 4000

// findFeatures works out which features apply to the project, and which
// it shows a reason to include.
func (d *detector) findFeatures() {
	evidence := map[string][]Evidence{}
	add := func(feature, what, file string) {
		for _, e := range evidence[feature] {
			if e.What == what {
				return
			}
		}
		evidence[feature] = append(evidence[feature], Evidence{What: what, File: file})
	}

	for _, f := range d.dependencyFiles() {
		raw := d.read(f)
		for _, s := range dependencySignals {
			if s.pattern.MatchString(raw) {
				add(s.feature, s.what, f)
			}
		}
	}
	for _, sdk := range d.p.AdSDKs {
		add(FeatureAdRevenue, sdk.Name, sdk.Source)
	}
	for i, rel := range SourceFiles(d.root, ".", featureCodeExts...) {
		if i >= maxFeatureScanFiles {
			break
		}
		raw, err := os.ReadFile(filepath.Join(d.root, filepath.FromSlash(rel)))
		if err != nil || len(raw) > 2<<20 {
			continue
		}
		text := blankComments(string(raw))
		for _, s := range codeSignals {
			if s.pattern.MatchString(text) {
				add(s.feature, s.what, rel)
			}
		}
	}
	for _, t := range d.p.Targets {
		if t.Android != nil {
			for _, m := range d.manifests(t.Android.ModuleDir) {
				if viewIntent.MatchString(d.read(m)) {
					add(FeatureDeepLinks, "an intent filter that opens links", m)
				}
			}
		}
		if t.IOS != nil {
			if t.IOS.InfoPlist != "" && urlSchemes.MatchString(d.read(t.IOS.InfoPlist)) {
				add(FeatureDeepLinks, "a URL scheme", t.IOS.InfoPlist)
			}
			for _, e := range d.entitlements(path.Dir(t.IOS.Xcodeproj)) {
				if associated.MatchString(d.read(e)) {
					add(FeatureDeepLinks, "associated domains", e)
				}
			}
		}
	}

	ios := d.p.Target(IOS) != nil
	d.p.Features = []Feature{}
	for _, f := range featureNames {
		if f.id == FeatureSKAN && !ios {
			continue
		}
		feature := Feature{ID: f.id, Name: f.name, Evidence: evidence[f.id]}
		switch f.id {
		case FeatureOpens:
			feature.Required, feature.Suggested = true, true
		case FeatureSKAN:
			// A conversion value scheme is the developer's to design: offered,
			// never chosen for them.
			feature.Evidence = nil
		default:
			feature.Suggested = len(feature.Evidence) > 0
		}
		d.p.Features = append(d.p.Features, feature)
	}
}

// dependencyFiles are the files that declare a project's dependencies.
func (d *detector) dependencyFiles() []string {
	files := []string{"pubspec.yaml", "package.json", "Packages/manifest.json"}
	for _, t := range d.p.Targets {
		if t.Android != nil {
			files = append(files, t.Android.BuildFile, t.Android.VersionCatalog)
		}
		if t.IOS != nil {
			dir := path.Dir(t.IOS.Xcodeproj)
			files = append(files, t.IOS.Podfile, path.Join(dir, "Podfile.lock"), path.Join(t.IOS.Xcodeproj, "project.pbxproj"))
		}
	}
	var out []string
	for _, f := range dedupe(files) {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// manifests are the Android module's manifests, main and per source set.
func (d *detector) manifests(moduleDir string) []string {
	var out []string
	d.walk(path.Join(moduleDir, "src"), 2, func(p string, _ fs.DirEntry) {
		if path.Base(p) == "AndroidManifest.xml" {
			out = append(out, p)
		}
	})
	return out
}

// entitlements are the Xcode project's entitlement files.
func (d *detector) entitlements(dir string) []string {
	var out []string
	d.walk(dir, 3, func(p string, _ fs.DirEntry) {
		if strings.HasSuffix(p, ".entitlements") {
			out = append(out, p)
		}
	})
	return out
}
