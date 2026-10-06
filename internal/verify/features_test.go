package verify_test

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

const activity = "app/src/main/java/com/example/sample/MainActivity.kt"

// withMAX is the Android sample with AppLovin MAX among its dependencies,
// and the activity's onResume body replaced by body.
func withMAX(body string) testproject.Files {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	return base.With(testproject.Files{
		build:    strings.Replace(base[build], "dependencies {", "dependencies {\n    implementation(\"com.applovin:applovin-sdk:13.0.1\")", 1),
		activity: strings.Replace(base[activity], "        tenjin.connect()\n", "        tenjin.connect()\n"+body, 1),
	})
}

func featureFinding(t *testing.T, r *verify.Report, id string) *verify.Finding {
	t.Helper()
	for i := range r.Findings {
		if r.Findings[i].ID == id {
			return &r.Findings[i]
		}
	}
	return nil
}

func TestAdRevenueFromADetectedNetwork(t *testing.T) {
	// Not chosen: a suggestion.
	r := run(t, withMAX(""), verify.Options{})
	f := featureFinding(t, r, "feature.ad_revenue")
	if f == nil || f.Severity != verify.Info || !strings.Contains(f.Title, "Ad revenue from AppLovin MAX: could be sent to Tenjin") || !strings.Contains(f.Fix, "tenjin sdk guide android") {
		t.Fatalf("finding = %+v", f)
	}
	// Chosen: a warning.
	r = run(t, withMAX(""), verify.Options{Features: []string{project.FeatureOpens, project.FeatureAdRevenue}})
	if f := featureFinding(t, r, "feature.ad_revenue"); f == nil || f.Severity != verify.Warning || !strings.Contains(f.Title, "chosen for the integration") {
		t.Fatalf("finding = %+v", f)
	}
	// Sent: a pass, wherever the call is.
	r = run(t, withMAX("        tenjin.eventAdImpressionAppLovin(ad)\n"), verify.Options{Features: []string{project.FeatureAdRevenue}})
	if f := featureFinding(t, r, "feature.ad_revenue"); f == nil || f.Severity != verify.Pass || f.File != activity {
		t.Fatalf("finding = %+v", f)
	}
}

// A generic name counts only on a Tenjin receiver.
func TestAPurchaseIsATenjinCall(t *testing.T) {
	chosen := verify.Options{Features: []string{project.FeaturePurchases}}
	r := run(t, withMAX("        db.transaction(a, b, c, d)\n"), chosen)
	if f := featureFinding(t, r, "feature.purchases"); f == nil || f.Severity != verify.Warning {
		t.Fatalf("a database transaction passed for a purchase: %+v", f)
	}
	r = run(t, withMAX("        tenjin.transaction(\"pro\", \"USD\", 1, 9.99)\n"), chosen)
	if f := featureFinding(t, r, "feature.purchases"); f == nil || f.Severity != verify.Pass {
		t.Fatalf("finding = %+v", f)
	}
}

func TestFeatureCallsAcrossSDKs(t *testing.T) {
	ios := testproject.IOSNative()
	const delegate = "Sample/AppDelegate.swift"
	r := run(t, ios.With(testproject.Files{
		"Sample/Store.m": "#import <TenjinSDK/TenjinSDK.h>\n\nvoid bought(id t, id r) {\n    [TenjinSDK transaction:t andReceipt:r];\n}\n",
		delegate:         strings.Replace(ios[delegate], "TenjinSDK.connect()", "TenjinSDK.connect()\n            TenjinSDK.updatePostbackConversionValue(1)", 1),
	}), verify.Options{Features: []string{project.FeaturePurchases, project.FeatureSKAN}})
	for _, id := range []string{"feature.purchases", "feature.skan"} {
		if f := featureFinding(t, r, id); f == nil || f.Severity != verify.Pass {
			t.Errorf("%s = %+v", id, f)
		}
	}

	unity := testproject.Unity()
	var script string
	for path, content := range unity {
		if strings.HasSuffix(path, ".cs") && strings.Contains(content, "Tenjin.getInstance") {
			script = path
		}
	}
	if script == "" {
		t.Fatal("the Unity fixture has no script that starts Tenjin")
	}
	r = run(t, unity.With(testproject.Files{
		script: strings.Replace(unity[script], "instance.Connect();", "instance.Connect();\n        instance.Transaction(\"pro\", \"USD\", 1, 9.99, null, null, null);", 1),
	}), verify.Options{Features: []string{project.FeaturePurchases}})
	if f := featureFinding(t, r, "feature.purchases"); f == nil || f.Severity != verify.Pass {
		t.Errorf("unity purchases = %+v", f)
	}
}

// A feature nothing suggests and nobody chose is not mentioned.
func TestUnchosenFeaturesTheProjectDoesNotSuggest(t *testing.T) {
	r := run(t, testproject.AndroidNative(), verify.Options{})
	for _, f := range r.Findings {
		if strings.HasPrefix(f.ID, "feature.") {
			t.Errorf("finding = %+v", f)
		}
	}
	r = run(t, testproject.AndroidNative(), verify.Options{Features: []string{project.FeatureCustomEvents}})
	if f := featureFinding(t, r, "feature.custom_events"); f == nil || f.Severity != verify.Warning || f.Detail != "" {
		t.Errorf("finding = %+v", f)
	}
}

// Android's other ways of starting the SDK are initialisation too.
func TestGetInstanceVariantsInitialise(t *testing.T) {
	base := testproject.AndroidNative()
	r := run(t, base.With(testproject.Files{
		activity: strings.Replace(base[activity], "TenjinSDK.getInstance(this, ", "TenjinSDK.getInstanceWithSharedSecret(this, ", 1),
	}), verify.Options{})
	if f := featureFinding(t, r, "sdk.init"); f == nil || f.Severity != verify.Pass {
		t.Errorf("sdk.init = %+v", f)
	}
}

// Calls that only the SDKs' sources name, not the guides, count too.
func TestCallsFromTheSDKSources(t *testing.T) {
	ios := testproject.IOSNative()
	const base = "Sample/Ads.m"
	r := run(t, ios.With(testproject.Files{
		"Podfile": strings.Replace(ios["Podfile"], "pod 'TenjinSDK'", "pod 'TenjinSDK'\n  pod 'Google-Mobile-Ads-SDK'", 1),
		base:      "#import <TenjinSDK/TenjinSDK.h>\n\nvoid paid(id view, id value) {\n    [TenjinSDK handleAdMobILRD:view :value];\n}\n",
	}), verify.Options{Features: []string{project.FeatureAdRevenue}})
	if f := featureFinding(t, r, "feature.ad_revenue"); f == nil || f.Severity != verify.Pass {
		t.Errorf("ad revenue = %+v", f)
	}
}
