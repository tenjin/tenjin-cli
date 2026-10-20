// Package testproject builds small mobile projects on disk for tests. Each
// builder returns a correct Tenjin integration as a map of path to content;
// tests change or delete entries to create the fault they want to exercise.
package testproject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Key is a well-formed SDK key that belongs to nobody.
const Key = "QWERTY1234ASDFGH5678ZXCVBN9012MNBV"

// Files maps slash-separated relative paths to file contents.
type Files map[string]string

// Write creates the files under a new temporary directory and returns it.
func Write(t *testing.T, files Files) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// With returns a copy of files with the given entries replaced.
func (f Files) With(changes Files) Files {
	out := Files{}
	for k, v := range f {
		out[k] = v
	}
	for k, v := range changes {
		out[k] = v
	}
	return out
}

// Without returns a copy of files with the given paths removed.
func (f Files) Without(paths ...string) Files {
	out := f.With(nil)
	for _, p := range paths {
		delete(out, p)
	}
	return out
}

const AndroidManifest = `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <uses-permission android:name="android.permission.INTERNET" />
    <uses-permission android:name="android.permission.ACCESS_NETWORK_STATE" />
    <uses-permission android:name="com.google.android.gms.permission.AD_ID" />
    <application android:label="Sample">
        <activity android:name=".MainActivity" android:exported="true">
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
                <category android:name="android.intent.category.LAUNCHER" />
            </intent-filter>
        </activity>
    </application>
</manifest>
`

// AndroidNative is a native Android app, Kotlin DSL, with Tenjin integrated.
func AndroidNative() Files {
	return Files{
		"settings.gradle.kts": `include(":app")` + "\n",
		"build.gradle.kts": `plugins {
    id("com.android.application") version "8.7.0" apply false
}
`,
		"app/build.gradle.kts": `plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.example.sample"
    defaultConfig {
        applicationId = "com.example.sample"
        minSdk = 24
    }
    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }
}

dependencies {
    implementation("com.tenjin:android-sdk:2.0.0")
    implementation("com.google.android.gms:play-services-ads-identifier:18.1.0")
    implementation("com.google.android.gms:play-services-appset:16.1.0")
}
`,
		"app/src/main/AndroidManifest.xml": AndroidManifest,
		"app/src/main/java/com/example/sample/MainActivity.kt": `package com.example.sample

import android.app.Activity
import com.tenjin.android.TenjinSDK

class MainActivity : Activity() {
    override fun onResume() {
        super.onResume()
        val tenjin = TenjinSDK.getInstance(this, "` + Key + `")
        tenjin.connect()
    }
}
`,
	}
}

// The SDK keys of the two apps a multi-store Android project ships: one for
// Google Play, one for the Amazon Appstore.
const (
	PlayKey   = "PLAYKEY1234567890ABCDEFGHIJKLPLAY"
	AmazonKey = "AMAZONKEY234567890ABCDEFGHIJKLAMZN"
)

// AndroidMultiStore is AndroidNative shipped to Google Play and the Amazon
// Appstore: a flavor per store, each declaring its store in its own
// manifest and carrying its own key in a buildConfigField.
func AndroidMultiStore() Files {
	base := AndroidNative()
	const build = "app/build.gradle.kts"
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	return base.With(Files{
		build: strings.Replace(base[build], "    buildTypes {", `    buildFeatures {
        buildConfig = true
    }
    flavorDimensions += "store"
    productFlavors {
        create("play") {
            dimension = "store"
            buildConfigField("String", "TENJIN_SDK_KEY", "\"`+PlayKey+`\"")
        }
        create("amazon") {
            dimension = "store"
            buildConfigField("String", "TENJIN_SDK_KEY", "\"`+AmazonKey+`\"")
        }
    }
    buildTypes {`, 1),
		"app/src/play/AndroidManifest.xml":   StoreManifest("googleplay"),
		"app/src/amazon/AndroidManifest.xml": StoreManifest("amazon"),
		activity:                             strings.Replace(base[activity], `"`+Key+`"`, "BuildConfig.TENJIN_SDK_KEY", 1),
	})
}

// StoreManifest is a flavor's manifest that only declares its app store.
func StoreManifest(store string) string {
	return `<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <application>
        <meta-data android:name="TENJIN_APP_STORE" android:value="` + store + `" />
    </application>
</manifest>
`
}

const InfoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>Sample</string>
	<key>NSUserTrackingUsageDescription</key>
	<string>We use this to measure which ads bring people to the app.</string>
	<key>NSAdvertisingAttributionReportEndpoint</key>
	<string>https://tenjin-skan.com</string>
</dict>
</plist>
`

// pbxproj returns the parts of an Xcode project file that detection reads:
// the app target and its test target, each with its build settings.
func pbxproj(bundleID, infoPlist string) string {
	return PBXProj(PBXTarget{Name: "Sample", ProductType: "com.apple.product-type.application", BundleID: bundleID, InfoPlist: infoPlist},
		PBXTarget{Name: "SampleTests", ProductType: "com.apple.product-type.bundle.unit-test", BundleID: bundleID + ".SampleTests"})
}

// PBXTarget is one target of an Xcode project, built in Debug and Release
// with the same settings.
type PBXTarget struct {
	Name, ProductType, BundleID, InfoPlist string
}

// PBXProj is a project.pbxproj with these targets, in the form Xcode writes.
func PBXProj(targets ...PBXTarget) string {
	var objects, ids strings.Builder
	for i, t := range targets {
		n := string(rune('A' + i))
		settings := "\t\t\t\tPRODUCT_BUNDLE_IDENTIFIER = " + t.BundleID + ";\n"
		if t.InfoPlist != "" {
			settings = "\t\t\t\tINFOPLIST_FILE = " + t.InfoPlist + ";\n" + settings
		}
		for _, config := range []string{"Debug", "Release"} {
			objects.WriteString("\t\t" + n + config + " /* " + config + " */ = {\n\t\t\tisa = XCBuildConfiguration;\n\t\t\tbuildSettings = {\n" + settings + "\t\t\t};\n\t\t\tname = " + config + ";\n\t\t};\n")
		}
		objects.WriteString("\t\t" + n + "List /* Build configuration list for PBXNativeTarget \"" + t.Name + "\" */ = {\n\t\t\tisa = XCConfigurationList;\n\t\t\tbuildConfigurations = (\n\t\t\t\t" + n + "Debug /* Debug */,\n\t\t\t\t" + n + "Release /* Release */,\n\t\t\t);\n\t\t\tdefaultConfigurationName = Release;\n\t\t};\n")
		objects.WriteString("\t\t" + n + "Target /* " + t.Name + " */ = {\n\t\t\tisa = PBXNativeTarget;\n\t\t\tbuildConfigurationList = " + n + "List;\n\t\t\tname = " + t.Name + ";\n\t\t\tproductName = " + t.Name + ";\n\t\t\tproductType = \"" + t.ProductType + "\";\n\t\t};\n")
		ids.WriteString("\t\t\t\t" + n + "Target /* " + t.Name + " */,\n")
	}
	return "// !$*UTF8*$!\n{\n\tarchiveVersion = 1;\n\tobjectVersion = 56;\n\tobjects = {\n" + objects.String() +
		"\t\tROOT /* Project object */ = {\n\t\t\tisa = PBXProject;\n\t\t\ttargets = (\n" + ids.String() + "\t\t\t);\n\t\t};\n\t};\n\trootObject = ROOT /* Project object */;\n}\n"
}

// IOSNative is a native iOS app using CocoaPods, with Tenjin integrated.
func IOSNative() Files {
	return Files{
		"Sample.xcodeproj/project.pbxproj": pbxproj("com.example.sample", "Sample/Info.plist"),
		"Podfile": `platform :ios, '13.0'

target 'Sample' do
  use_frameworks!
  pod 'TenjinSDK'
end
`,
		"Podfile.lock": `PODS:
  - TenjinSDK (1.20.0)

DEPENDENCIES:
  - TenjinSDK
`,
		"Sample/Info.plist": InfoPlist,
		"Sample/AppDelegate.swift": `import UIKit
import AppTrackingTransparency
import TenjinSDK

@main
class AppDelegate: UIResponder, UIApplicationDelegate {
    func applicationDidBecomeActive(_ application: UIApplication) {
        #if DEBUG
        TenjinSDK.debugLogs()
        #endif
        TenjinSDK.getInstance("` + Key + `")
        ATTrackingManager.requestTrackingAuthorization { _ in
            TenjinSDK.connect()
        }
    }
}
`,
	}
}

// Flutter is a Flutter app for both platforms, with Tenjin integrated.
func Flutter() Files {
	return Files{
		"pubspec.yaml": `name: sample
environment:
  sdk: ^3.7.2

dependencies:
  flutter:
    sdk: flutter
  tenjin_plugin: ^1.5.0
`,
		"pubspec.lock": `packages:
  tenjin_plugin:
    dependency: "direct main"
    description:
      name: tenjin_plugin
      url: "https://pub.dev"
    source: hosted
    version: "1.5.0"
`,
		"lib/main.dart": `import 'dart:io';
import 'package:flutter/material.dart';
import 'package:tenjin_plugin/tenjin_sdk.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  TenjinSDK.instance.initialize(sdkKey: '` + Key + `');
  if (Platform.isIOS) {
    await TenjinSDK.instance.requestTrackingAuthorization();
  }
  TenjinSDK.instance.connect();
  runApp(const Placeholder());
}
`,
		"android/build.gradle.kts": "// root build file\n",
		"android/app/build.gradle.kts": `plugins {
    id("com.android.application")
    id("dev.flutter.flutter-gradle-plugin")
}

android {
    namespace = "com.example.sample"
    defaultConfig {
        applicationId = "com.example.sample"
    }
}
`,
		"android/app/src/main/AndroidManifest.xml": AndroidManifest,
		"ios/Runner.xcodeproj/project.pbxproj":     pbxproj("com.example.sample", "Runner/Info.plist"),
		"ios/Runner/Info.plist":                    InfoPlist,
		// Flutter shrinks release builds, and the plugin brings no rules.
		"android/app/proguard-rules.pro": "-keep class com.tenjin.** { *; }\n",
	}
}

// Unity is a Unity project for both platforms, with Tenjin installed from
// its Git address and integrated in code, as the Unity SDK's README shows.
func Unity() Files {
	return Files{
		"ProjectSettings/ProjectVersion.txt": "m_EditorVersion: 2022.3.62f3\n",
		"ProjectSettings/ProjectSettings.asset": `PlayerSettings:
  applicationIdentifier:
    Android: com.example.sample
    iPhone: com.example.sample
  buildNumber:
    Standalone: 0
  AndroidMinifyRelease: 0
  AndroidMinifyDebug: 0
`,
		"Packages/manifest.json": `{
  "dependencies": {
    "com.google.external-dependency-manager": "1.2.186",
    "com.tenjin.sdk": "https://github.com/tenjin/tenjin-unity-sdk.git#1.22.1",
    "com.unity.ugui": "1.0.0"
  }
}
`,
		"Assets/Scripts/TenjinBootstrap.cs": `using UnityEngine;

public class TenjinBootstrap : MonoBehaviour
{
    const string TenjinKey = "` + Key + `";

    void Start()
    {
        TenjinConnect();
    }

    void OnApplicationPause(bool pauseStatus)
    {
        if (!pauseStatus)
        {
            TenjinConnect();
        }
    }

    void TenjinConnect()
    {
        BaseTenjin instance = Tenjin.getInstance(TenjinKey);
        instance.SetAppStoreType(AppStoreType.googleplay);
#if UNITY_IOS
        instance.RequestTrackingAuthorizationWithCompletionHandler((status) =>
        {
            instance.Connect();
        });
#else
        instance.Connect();
#endif
    }
}
`,
		"Assets/Editor/TenjinPostProcess.cs": `using System.IO;
using UnityEditor;
using UnityEditor.Callbacks;
#if UNITY_IOS
using UnityEditor.iOS.Xcode;
#endif

public static class TenjinPostProcess
{
    [PostProcessBuild(200)]
    public static void OnPostProcessBuild(BuildTarget target, string path)
    {
#if UNITY_IOS
        if (target != BuildTarget.iOS) return;
        var plistPath = Path.Combine(path, "Info.plist");
        var plist = new PlistDocument();
        plist.ReadFromFile(plistPath);
        plist.root.SetString("NSAdvertisingAttributionReportEndpoint", "https://tenjin-skan.com");
        File.WriteAllText(plistPath, plist.WriteToString());
#endif
    }
}
`,
	}
}
