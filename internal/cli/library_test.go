package cli

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

const adsKitPodspec = "Pod::Spec.new do |s|\n  s.name = 'AdsKit'\n  s.source_files = 'AdsKit/**/*.{h,m,swift}'\n  s.dependency 'TenjinSDK', '1.15.1'\nend\n"

// A library is reported as one: no app checks, its pinned SDK version
// looked at, and the example app named. setup has no app to set up.
func TestDoctorOnALibrary(t *testing.T) {
	files := testproject.Files{"AdsKit.podspec": adsKitPodspec}
	for name, content := range testproject.IOSNative() {
		files["Example/"+name] = content
	}
	dir := testproject.Write(t, files)
	h := newHarness(t)

	code, stdout, _ := h.run("doctor", dir, "--offline")
	for _, want := range []string{"(CocoaPods pod)", "AdsKit is a CocoaPods pod that wraps the Tenjin SDK, not an app", "Run tenjin doctor Example", "The Tenjin SDK is at 1.15.1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if code != 0 || strings.Contains(stdout, "NSUserTrackingUsageDescription") || strings.Contains(stdout, "never initialized") {
		t.Errorf("exit = %d, app checks ran:\n%s", code, stdout)
	}

	code, _, stderr := h.run("setup", dir)
	if code != 2 || !strings.Contains(stderr, "not an app") || !strings.Contains(stderr, "tenjin setup Example") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

// An app that gets the SDK through a local pod has the pod's calls read; one
// that gets it through a published pod cannot, and is not told it is broken.
func TestAnAppThroughALibrary(t *testing.T) {
	ios := testproject.IOSNative()
	const delegate = "Sample/AppDelegate.swift"
	local := ios.With(testproject.Files{
		"Podfile":                 "platform :ios, '13.0'\ntarget 'Sample' do\n  pod 'AdsKit', :path => 'AdsKit'\nend\n",
		"Podfile.lock":            "PODS:\n  - AdsKit (1.0.0):\n    - TenjinSDK\n  - TenjinSDK (1.20.0)\n",
		"AdsKit/AdsKit.podspec":   adsKitPodspec,
		"AdsKit/AdsKit/Ads.swift": ios[delegate],
		delegate:                  "import UIKit\n@main\nclass AppDelegate: UIResponder, UIApplicationDelegate {}\n",
	})
	_, stdout, _ := newHarness(t).run("doctor", testproject.Write(t, local), "--offline")
	for _, want := range []string{"TenjinSDK (CocoaPods, through AdsKit) 1.20.0", "The SDK is initialized  AdsKit/AdsKit/Ads.swift"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	published := ios.With(testproject.Files{
		"Podfile":      "platform :ios, '13.0'\ntarget 'Sample' do\n  pod 'AdsKit'\nend\n",
		"Podfile.lock": "PODS:\n  - AdsKit (1.0.0):\n    - TenjinSDK\n  - TenjinSDK (1.20.0)\n",
		delegate:       "import UIKit\n@main\nclass AppDelegate: UIResponder, UIApplicationDelegate {}\n",
	})
	_, stdout, _ = newHarness(t).run("doctor", testproject.Write(t, published), "--offline")
	if !strings.Contains(stdout, "The SDK comes with AdsKit, whose code is not in the project") || strings.Contains(stdout, "never initialized") {
		t.Errorf("stdout:\n%s", stdout)
	}
}
