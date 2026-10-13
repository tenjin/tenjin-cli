package project_test

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// A library that wraps the SDK is recognised for what it is, in each form,
// with its example app named.
func TestLibrariesThatWrapTheSDK(t *testing.T) {
	cases := []struct {
		name  string
		files testproject.Files
		kind  string
		want  string // the Tenjin package and version it declares
	}{
		{"CocoaPods pod", testproject.Files{
			"AdsKit.podspec": "Pod::Spec.new do |s|\n  s.name = 'AdsKit'\n  s.subspec 'Tenjin' do |t|\n    t.dependency 'TenjinSDK', '~> 1.15'\n  end\nend\n",
		}, "cocoapods", "TenjinSDK ~> 1.15"},
		{"Swift package", testproject.Files{
			"Package.swift": "let package = Package(name: \"AdsKit\", products: [.library(name: \"AdsKit\", targets: [\"AdsKit\"])], dependencies: [.package(url: \"https://github.com/tenjin/tenjin-ios-spm\", from: \"1.20.0\")])\n",
		}, "swift_package", "TenjinSDK (Swift Package)"},
		{"Flutter plugin", testproject.Files{
			"pubspec.yaml": "name: ads_kit\nenvironment:\n  sdk: '>=3.0.0 <4.0.0'\ndependencies:\n  flutter:\n    sdk: flutter\n  tenjin_plugin: ^1.5.0\nflutter:\n  plugin:\n    platforms:\n      android:\n        package: com.example.adskit\n",
		}, "flutter_plugin", "tenjin_plugin 1.5.0"},
		{"React Native package", testproject.Files{
			"package.json": `{"name": "rn-ads-kit", "peerDependencies": {"react-native": "*"}, "dependencies": {"react-native-tenjin": "^1.7.0"}}`,
		}, "react_native_library", "react-native-tenjin 1.7.0"},
		{"Android library", testproject.Files{
			"settings.gradle.kts":  "include(\":ads\")\n",
			"ads/build.gradle.kts": "plugins { id(\"com.android.library\") }\ndependencies { implementation(\"com.tenjin:android-sdk:2.0.0\") }\n",
		}, "android_library", "com.tenjin:android-sdk 2.0.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := detect(t, c.files)
			if p.Library == nil || p.Library.Kind != c.kind || strings.TrimSpace(p.Library.Tenjin.Package+" "+p.Library.Tenjin.Version) != c.want {
				t.Errorf("library = %+v", p.Library)
			}
		})
	}

	withExample := testproject.Files{"AdsKit.podspec": "Pod::Spec.new do |s|\n  s.name = 'AdsKit'\n  s.dependency 'TenjinSDK'\nend\n"}
	for name, content := range testproject.IOSNative() {
		withExample["Example/"+name] = content
	}
	if p := detect(t, withExample); p.Library == nil || p.Library.Example != "Example" {
		t.Errorf("library = %+v", p.Library)
	}
}

// An app that gets the SDK through a library: a local one, whose code is
// read, or a published one, which the lock shows.
func TestTheSDKThroughALibrary(t *testing.T) {
	ios := testproject.IOSNative()
	published := ios.With(testproject.Files{
		"Podfile":      "platform :ios, '13.0'\ntarget 'Sample' do\n  pod 'AdsKit'\nend\n",
		"Podfile.lock": "PODS:\n  - AdsKit (1.0.0):\n    - TenjinSDK\n  - TenjinSDK (1.20.0)\n",
	})
	p := detect(t, published)
	if len(p.Tenjin) != 1 || p.Tenjin[0].Through != "AdsKit" || p.Tenjin[0].Local || p.Tenjin[0].Version != "1.20.0" {
		t.Errorf("Tenjin = %+v", p.Tenjin)
	}

	flutter := testproject.Flutter()
	lock := "packages:\n  tenjin_plugin:\n    dependency: transitive\n    description:\n      name: tenjin_plugin\n      url: \"https://pub.dev\"\n    source: hosted\n    version: \"1.5.0\"\n"
	p = detect(t, flutter.With(testproject.Files{
		"pubspec.yaml": strings.Replace(flutter["pubspec.yaml"], "tenjin_plugin:", "ads_kit:", 1),
		"pubspec.lock": lock,
	}))
	if len(p.Tenjin) != 1 || p.Tenjin[0].Through != "another package" || p.Tenjin[0].Version != "1.5.0" {
		t.Errorf("Tenjin = %+v", p.Tenjin)
	}
}
