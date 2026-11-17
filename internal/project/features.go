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

var featureNames = []struct{ id, name, about string }{
	{FeatureOpens, "App opens and attribution", "installs and opens, by campaign"},
	{FeaturePurchases, "In-app purchases", "the app sells items: revenue per campaign"},
	{FeatureSubscriptions, "Subscriptions", "the app sells subscriptions: their revenue"},
	{FeatureAdRevenue, "Ad revenue (ILRD)", "the app shows ads: ad revenue per user"},
	{FeatureConsent, "Consent (GDPR)", "the app asks for consent: Tenjin follows it"},
	{FeatureDeepLinks, "Deep links", "an ad opens a screen of the app, even on install"},
	{FeatureSKAN, "SKAdNetwork conversion values", "iOS: in-app actions in Apple's SKAN postbacks"},
	{FeatureCustomEvents, "Custom events", "your own events, such as level_complete"},
	{FeatureUserID, "Customer user ID", "your user IDs on Tenjin's data, to join with yours"},
}

// FeatureIDs lists every feature ID, in order.
func FeatureIDs() []string {
	out := make([]string, len(featureNames))
	for i, f := range featureNames {
		out[i] = f.id
	}
	return out
}

// FeatureAbout says, for people choosing, what a feature is for: when an app
// needs it, and what it gives.
func FeatureAbout(id string) string {
	for _, f := range featureNames {
		if f.id == id {
			return f.about
		}
	}
	return ""
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
	// hints are words one of which a source file must hold for pattern to
	// run over it: a cheap test that leaves most files out, since the
	// pattern over every file is most of what detection costs in a large
	// project. Each alternative of pattern holds one
	// (TestEveryCodeSignalHasAHint).
	hints []string
}

// hinted reports whether text holds one of the signal's hints.
func (s signal) hinted(text string) bool {
	for _, h := range s.hints {
		if strings.Contains(text, h) {
			return true
		}
	}
	return false
}

// Signals in the files that declare dependencies.
var dependencySignals = []signal{
	{FeaturePurchases, "Google Play Billing", regexp.MustCompile(`com\.android\.billingclient:billing`), nil},
	{FeaturePurchases, "RevenueCat", regexp.MustCompile(`com\.revenuecat\.purchases|['"]RevenueCat['"]|- RevenueCat\b|purchases-ios|\bpurchases_flutter\s*:|"react-native-purchases"|"@revenuecat/purchases-capacitor"|"com\.revenuecat\.purchases-unity"`), nil},
	{FeatureSubscriptions, "RevenueCat, used mostly for subscriptions", regexp.MustCompile(`com\.revenuecat\.purchases|['"]RevenueCat['"]|- RevenueCat\b|purchases-ios|\bpurchases_flutter\s*:|"react-native-purchases"|"@revenuecat/purchases-capacitor"|"com\.revenuecat\.purchases-unity"`), nil},
	{FeaturePurchases, "in_app_purchase", regexp.MustCompile(`\bin_app_purchase\s*:`), nil},
	{FeaturePurchases, "a React Native in-app purchase library", regexp.MustCompile(`"react-native-iap"|"expo-in-app-purchases"|"expo-iap"`), nil},
	{FeaturePurchases, "an in-app purchase plugin", regexp.MustCompile(`"cordova-plugin-purchase"|"@capgo/native-purchases"`), nil},
	{FeaturePurchases, "Unity IAP", regexp.MustCompile(`"com\.unity\.purchasing"`), nil},
	{FeatureConsent, "Google's consent SDK (UMP)", regexp.MustCompile(`com\.google\.android\.ump:user-messaging-platform|GoogleUserMessagingPlatform`), nil},
	{FeatureConsent, "a consent management platform", regexp.MustCompile(`(?i)com\.onetrust|onetrust-publishers|usercentrics|didomi|sourcepoint`), nil},
}

// Signals in the app's own code.
var codeSignals = []signal{
	{FeaturePurchases, "StoreKit purchases", regexp.MustCompile(`\bSKPaymentQueue\b|\bSKProductsRequest\b|\bProduct\.products\(|\bTransaction\.updates\b|\bAppStore\.sync\(`),
		[]string{"SKPaymentQueue", "SKProductsRequest", "Product.products(", "Transaction.updates", "AppStore.sync("}},
	{FeaturePurchases, "Google Play Billing", regexp.MustCompile(`\bBillingClient\b`), []string{"BillingClient"}},
	{FeatureSubscriptions, "subscription products", regexp.MustCompile(`ProductType\.SUBS\b|SkuType\.SUBS\b|\.autoRenewable\b|\bsubscriptionPeriod\b|\bSKProductSubscriptionPeriod\b|ProductType\.Subscription\b`),
		[]string{"ProductType.SUBS", "SkuType.SUBS", ".autoRenewable", "ubscriptionPeriod", "ProductType.Subscription"}},
	{FeatureConsent, "a consent form", regexp.MustCompile(`\bUMPConsentInformation\b|ConsentInformation\.getInstance\(|\bUserMessagingPlatform\.|ConsentInformation\.instance\b|\bConsentInformation\.Update\(`),
		[]string{"ConsentInformation", "UserMessagingPlatform."}},
}

var (
	featureCodeExts = []string{".kt", ".java", ".swift", ".m", ".mm", ".dart", ".js", ".jsx", ".ts", ".tsx", ".cs"}
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
		raw := d.readDependencies(f)
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
		if !d.appCode(rel) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(d.root, filepath.FromSlash(rel)))
		if err != nil || len(raw) > 2<<20 {
			continue
		}
		// A hint absent from the file is absent from it without its
		// comments too, so most files are left before anything is blanked.
		var hinted []signal
		for _, s := range codeSignals {
			if s.hinted(string(raw)) {
				hinted = append(hinted, s)
			}
		}
		if len(hinted) == 0 {
			continue
		}
		text := blankComments(Text(raw))
		for _, s := range hinted {
			if s.pattern.MatchString(text) {
				add(s.feature, s.what, rel)
			}
		}
	}
	for _, t := range d.p.Targets {
		if t.Android != nil {
			for _, m := range d.manifests(t.Android.ModuleDir) {
				if manifest, ok := d.manifestXML(m); ok && manifest.opensLinks() {
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

// dependencyFiles are the files that declare a project's dependencies: the
// app's own, and the build files of the library modules it depends on,
// whose dependencies are the app's when it is built.
func (d *detector) dependencyFiles() []string {
	files := []string{"pubspec.yaml", "package.json", "Packages/manifest.json"}
	for _, t := range d.p.Targets {
		if t.Android != nil {
			files = append(files, t.Android.BuildFile, t.Android.VersionCatalog)
			for _, m := range t.Android.LibraryModules {
				files = append(files, moduleBuildFile(d, m))
			}
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
