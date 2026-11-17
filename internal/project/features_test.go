package project_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// suggested lists the suggested features, each with its evidence.
func suggested(p *project.Project) string {
	var out []string
	for _, f := range p.Features {
		if !f.Suggested {
			continue
		}
		var why []string
		for _, e := range f.Evidence {
			why = append(why, e.What)
		}
		sort.Strings(why)
		out = append(out, f.ID+"("+strings.Join(why, "; ")+")")
	}
	return strings.Join(out, " ")
}

func offered(p *project.Project) string {
	var out []string
	for _, f := range p.Features {
		out = append(out, f.ID)
	}
	return strings.Join(out, " ")
}

func TestAProjectThatShowsNoFeature(t *testing.T) {
	p := detect(t, testproject.AndroidNative())
	if got := suggested(p); got != "opens()" {
		t.Errorf("suggested = %s", got)
	}
	// SKAN conversion values are for iOS only.
	if got := offered(p); got != "opens purchases subscriptions ad_revenue consent deep_links custom_events user_id" {
		t.Errorf("offered = %s", got)
	}
	if f := p.Feature(project.FeatureOpens); f == nil || !f.Required {
		t.Errorf("opens = %+v", f)
	}
}

func TestAndroidFeatureSignals(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	const manifest = "app/src/main/AndroidManifest.xml"
	p := detect(t, base.With(testproject.Files{
		build: strings.Replace(base[build], "dependencies {", `dependencies {
    implementation("com.android.billingclient:billing-ktx:7.1.1")
    implementation("com.applovin:applovin-sdk:13.0.1")
    implementation("com.google.android.ump:user-messaging-platform:3.1.0")`, 1),
		manifest: strings.Replace(base[manifest], "</application>", `    <activity android:name=".LinkActivity">
            <intent-filter>
                <action android:name="android.intent.action.VIEW" />
                <data android:scheme="sample" />
            </intent-filter>
        </activity>
    </application>`, 1),
		"app/src/main/java/com/example/sample/Store.kt": "package com.example.sample\n\nval type = BillingClient.ProductType.SUBS\n",
	}))
	want := "opens() purchases(Google Play Billing) subscriptions(subscription products) ad_revenue(AppLovin MAX) consent(Google's consent SDK (UMP)) deep_links(an intent filter that opens links)"
	if got := suggested(p); got != want {
		t.Errorf("suggested:\n%s\nwant:\n%s", got, want)
	}
	if e := p.Feature(project.FeatureAdRevenue).Evidence[0]; e.File != build {
		t.Errorf("evidence = %+v", e)
	}
}

func TestIOSFeatureSignals(t *testing.T) {
	base := testproject.IOSNative()
	p := detect(t, base.With(testproject.Files{
		"Sample/Info.plist": strings.Replace(base["Sample/Info.plist"], "</dict>\n</plist>", `	<key>CFBundleURLTypes</key>
	<array><dict><key>CFBundleURLSchemes</key><array><string>sample</string></array></dict></array>
</dict>
</plist>`, 1),
		"Sample/Store.swift": "import StoreKit\n\nfunc load() async throws {\n    let products = try await Product.products(for: [\"pro\"])\n    _ = products.first?.subscription?.subscriptionPeriod\n}\n",
	}))
	want := "opens() purchases(StoreKit purchases) subscriptions(subscription products) deep_links(a URL scheme)"
	if got := suggested(p); got != want {
		t.Errorf("suggested:\n%s\nwant:\n%s", got, want)
	}
	// Offered on iOS, never chosen for the developer.
	if f := p.Feature(project.FeatureSKAN); f == nil || f.Suggested {
		t.Errorf("skan = %+v", f)
	}
}

func TestRevenueCatSuggestsSubscriptions(t *testing.T) {
	base := testproject.Flutter()
	p := detect(t, base.With(testproject.Files{
		"pubspec.yaml": strings.Replace(base["pubspec.yaml"], "dependencies:\n", "dependencies:\n  purchases_flutter: ^8.0.0\n", 1),
	}))
	if got := suggested(p); !strings.Contains(got, "purchases(RevenueCat)") || !strings.Contains(got, "subscriptions(RevenueCat, used mostly for subscriptions)") {
		t.Errorf("suggested = %s", got)
	}
}

// The dependencies of a library module the app depends on are the app's
// when it is built, and suggest its features.
func TestFeatureSignalsInALibraryModule(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	const ads = "ads/build.gradle.kts"
	p := detect(t, base.With(testproject.Files{
		"settings.gradle.kts": "include(\":app\", \":ads\")\n",
		build:                 strings.Replace(base[build], "dependencies {", "dependencies {\n    implementation(project(\":ads\"))", 1),
		ads: `plugins {
    id("com.android.library")
}

dependencies {
    implementation("com.applovin:applovin-sdk:13.0.1")
    implementation("com.android.billingclient:billing-ktx:7.1.1")
}
`,
	}))
	want := "opens() purchases(Google Play Billing) ad_revenue(AppLovin MAX)"
	if got := suggested(p); got != want {
		t.Errorf("suggested:\n%s\nwant:\n%s", got, want)
	}
	if e := p.Feature(project.FeaturePurchases).Evidence[0]; e.File != ads {
		t.Errorf("evidence = %+v", e)
	}
	if len(p.AdSDKs) != 1 || p.AdSDKs[0].Source != ads {
		t.Errorf("ad SDKs = %+v", p.AdSDKs)
	}
}

// A VIEW action in <queries> is what the app asks the system about, not
// what it receives: only an intent filter on an activity opens links.
func TestAQueryForViewIsNotADeepLink(t *testing.T) {
	base := testproject.AndroidNative()
	const manifest = "app/src/main/AndroidManifest.xml"
	queries := `    <queries>
        <intent>
            <action android:name="android.intent.action.VIEW" />
            <data android:scheme="https" />
        </intent>
    </queries>
    <application`
	p := detect(t, base.With(testproject.Files{manifest: strings.Replace(base[manifest], "    <application", queries, 1)}))
	if got := suggested(p); got != "opens()" {
		t.Errorf("suggested = %s", got)
	}
	// On an alias, as on an activity, it counts.
	alias := `        <activity-alias android:name=".Links" android:targetActivity=".MainActivity" android:exported="true">
            <intent-filter>
                <action android:name="android.intent.action.VIEW" />
                <data android:scheme="https" android:host="example.com" />
            </intent-filter>
        </activity-alias>
    </application>`
	p = detect(t, base.With(testproject.Files{manifest: strings.Replace(base[manifest], "    </application>", alias, 1)}))
	if got := suggested(p); got != "opens() deep_links(an intent filter that opens links)" {
		t.Errorf("suggested = %s", got)
	}
}

// An Android app with 2000 source files of ordinary code, none of which
// shows a feature, and one that does: the feature scan is most of what
// detection spends on a project this size.
func BenchmarkDetectALargeProject(b *testing.B) {
	base := testproject.AndroidNative()
	var body strings.Builder
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&body, "    fun step%d(x: Int): Int {\n        val y = x * %d / 3 // a comment about step %d\n        return listOf(y, x).sum()\n    }\n", i, i+1, i)
	}
	for i := 0; i < 2000; i++ {
		base[fmt.Sprintf("app/src/main/java/com/example/sample/feature%d/Screen%d.kt", i%40, i)] = fmt.Sprintf("package com.example.sample.feature%d\n\nclass Screen%d {\n%s}\n", i%40, i, body.String())
	}
	base["app/src/main/java/com/example/sample/Store.kt"] = "package com.example.sample\n\nval type = BillingClient.ProductType.SUBS\n"
	root := b.TempDir()
	for rel, content := range base {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		p, err := project.Detect(root)
		if err != nil {
			b.Fatal(err)
		}
		if got := suggested(p); got != "opens() purchases(Google Play Billing) subscriptions(subscription products)" {
			b.Fatalf("suggested = %s", got)
		}
	}
}

// Words in comments are not signals.
func TestCommentsAreNotSignals(t *testing.T) {
	base := testproject.AndroidNative()
	p := detect(t, base.With(testproject.Files{
		"app/src/main/java/com/example/sample/Later.kt": "package com.example.sample\n\n// TODO: BillingClient for subscriptions, ProductType.SUBS\n",
	}))
	if got := suggested(p); got != "opens()" {
		t.Errorf("suggested = %s", got)
	}
}
