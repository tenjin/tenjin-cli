package project_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

const androidDir = "app/src/main/java/com/example/sample/"

// Detection leaves the analytics unread: only what uses them reads them.
func TestDetectLeavesTheAnalyticsUnread(t *testing.T) {
	p := detect(t, testproject.AndroidNative().With(testproject.Files{
		androidDir + "Game.kt": `package com.example.sample

import com.google.firebase.analytics.FirebaseAnalytics

class Game(private val analytics: FirebaseAnalytics) {
    fun win() { analytics.logEvent("won", null) }
}
`,
	}))
	if len(p.Analytics.Events) != 0 || p.Analytics.Events == nil || p.Analytics.UserIDs == nil {
		t.Fatalf("analytics before reading = %+v", p.Analytics)
	}
	p.ReadAnalyticsFor([]string{project.FeatureOpens, project.FeaturePurchases})
	if len(p.Analytics.Events) != 0 {
		t.Fatalf("read for features that do not use them: %+v", p.Analytics)
	}
	p.ReadAnalyticsFor([]string{project.FeatureCustomEvents})
	if names := p.Analytics.Names(); strings.Join(names, ",") != "won" {
		t.Errorf("names = %q", names)
	}
}

// The SDKs' constants stand for the names their SDKs define, which their
// names do not always spell; one the table does not know is reported as
// written, never guessed.
func TestAnalyticsSDKConstants(t *testing.T) {
	p := analyze(t, testproject.AndroidNative().With(testproject.Files{
		androidDir + "Shop.kt": `package com.example.sample

import android.content.Context
import com.appsflyer.AFInAppEventType
import com.appsflyer.AppsFlyerLib
import com.facebook.appevents.AppEventsConstants
import com.facebook.appevents.AppEventsLogger

class Shop(private val context: Context) {
    fun buy() {
        AppsFlyerLib.getInstance().logEvent(context, AFInAppEventType.ADD_TO_WISH_LIST, mapOf())
        AppsFlyerLib.getInstance().logEvent(context, AFInAppEventType.SPENT_CREDIT, mapOf())
        AppsFlyerLib.getInstance().logEvent(context, AFInAppEventType.NOT_ONE_OF_THEM, mapOf())
        AppEventsLogger.newLogger(context).logEvent(AppEventsConstants.EVENT_NAME_PURCHASED)
    }
}
`,
	}))
	wantLines(t, "Android", events(p),
		"AppsFlyer name=af_add_to_wishlist (AFInAppEventType.ADD_TO_WISH_LIST) @"+androidDir+"Shop.kt:11 x1",
		"AppsFlyer name=af_spent_credits (AFInAppEventType.SPENT_CREDIT) @"+androidDir+"Shop.kt:12 x1",
		"AppsFlyer expr=AFInAppEventType.NOT_ONE_OF_THEM @"+androidDir+"Shop.kt:13 x1",
		"Facebook App Events name=fb_mobile_purchase (AppEventsConstants.EVENT_NAME_PURCHASED) @"+androidDir+"Shop.kt:14 x1",
	)

	p = analyze(t, testproject.IOSNative().With(testproject.Files{
		"Sample/Shop.swift": `import AppsFlyerLib
import FirebaseAnalytics

final class Shop {
    func buy() {
        AppsFlyerLib.shared().logEvent(AFEventSpentCredits, withValues: [:])
        AppsFlyerLib.shared().logEvent(AFEventTutorial_completion, withValues: [:])
        AppsFlyerLib.shared().logEvent(AFEventLocation, withValues: [:])
        Analytics.logEvent(AnalyticsEventInAppPurchase, parameters: nil)
    }
}
`,
	}))
	wantLines(t, "iOS", events(p),
		"AppsFlyer name=af_spent_credits (AFEventSpentCredits) @Sample/Shop.swift:6 x1",
		"AppsFlyer name=af_tutorial_completion (AFEventTutorial_completion) @Sample/Shop.swift:7 x1",
		"AppsFlyer name=af_location_coordinates (AFEventLocation) @Sample/Shop.swift:8 x1",
		"Firebase Analytics name=in_app_purchase (AnalyticsEventInAppPurchase) @Sample/Shop.swift:9 x1",
	)
}

// A constant is resolved in the type that qualifies it, and an SDK's
// constant never by one of the app's that has its last name.
func TestAnalyticsConstantsResolveByTheirType(t *testing.T) {
	p := analyze(t, testproject.Unity().With(testproject.Files{
		"Assets/Scripts/Shop.cs": `using System.Collections.Generic;
using AppsFlyerSDK;

public static class Names
{
    public const string PURCHASE = "purchase_complete";
}

public class Shop
{
    void Buy()
    {
        AppsFlyer.sendEvent(AFInAppEvents.PURCHASE, new Dictionary<string, string>());
        AppsFlyer.sendEvent(Names.PURCHASE, new Dictionary<string, string>());
        AppsFlyer.sendEvent(AFInAppEvents.ADD_TO_WISH_LIST, new Dictionary<string, string>());
    }
}
`,
	}))
	wantLines(t, "Unity", events(p),
		"AppsFlyer name=af_purchase (AFInAppEvents.PURCHASE) @Assets/Scripts/Shop.cs:13 x1",
		"AppsFlyer name=purchase_complete (Names.PURCHASE) @Assets/Scripts/Shop.cs:14 x1",
		"AppsFlyer name=af_add_to_wishlist (AFInAppEvents.ADD_TO_WISH_LIST) @Assets/Scripts/Shop.cs:15 x1",
	)

	p = analyze(t, testproject.AndroidNative().With(testproject.Files{
		androidDir + "Events.kt": `package com.example.sample

object ShopEvents {
    const val OPENED = "shop_opened"
}

object MenuEvents {
    const val OPENED = "menu_opened"
}

const val LEVEL = "top_level"
`,
		androidDir + "Game.kt": `package com.example.sample

import com.google.firebase.analytics.FirebaseAnalytics

class Game(private val analytics: FirebaseAnalytics) {
    fun open() {
        analytics.logEvent(ShopEvents.OPENED, null)
        analytics.logEvent(MenuEvents.OPENED, null)
        analytics.logEvent(LEVEL, null)
        analytics.logEvent(Other.LEVEL, null)
    }
}

object Other {
    const val LEVEL = "other_level"
}
`,
	}))
	wantLines(t, "Android", events(p),
		"Firebase Analytics name=shop_opened (ShopEvents.OPENED) @"+androidDir+"Game.kt:7 x1",
		"Firebase Analytics name=menu_opened (MenuEvents.OPENED) @"+androidDir+"Game.kt:8 x1",
		"Firebase Analytics name=top_level (LEVEL) @"+androidDir+"Game.kt:9 x1",
		"Firebase Analytics name=other_level (Other.LEVEL) @"+androidDir+"Game.kt:10 x1",
	)
}

// A constant imported by name resolves whether it is a Kotlin top-level
// constant, whose import names only its package, or a companion object's,
// whose import goes through Companion.
func TestAnalyticsConstantsImportedByName(t *testing.T) {
	p := analyze(t, testproject.AndroidNative().With(testproject.Files{
		androidDir + "analytics/Names.kt": `package com.example.sample.analytics

const val LEVEL_UP = "level_up"

class Events {
    companion object {
        const val SHOP_OPENED = "shop_opened"
    }
}
`,
		androidDir + "Game.kt": `package com.example.sample

import com.example.sample.analytics.Events.Companion.SHOP_OPENED
import com.example.sample.analytics.LEVEL_UP
import com.google.firebase.analytics.FirebaseAnalytics

class Game(private val analytics: FirebaseAnalytics) {
    fun play() {
        analytics.logEvent(LEVEL_UP, null)
        analytics.logEvent(SHOP_OPENED, null)
    }
}
`,
	}))
	wantLines(t, "events", events(p),
		"Firebase Analytics name=level_up (LEVEL_UP) @"+androidDir+"Game.kt:9 x1",
		"Firebase Analytics name=shop_opened (SHOP_OPENED) @"+androidDir+"Game.kt:10 x1",
	)
}

// Event is Firebase's only where the file imports Firebase's.
func TestAnalyticsEventIsFirebasesOnlyWhenImported(t *testing.T) {
	p := analyze(t, testproject.AndroidNative().With(testproject.Files{
		androidDir + "Own.kt": `package com.example.sample

import com.google.firebase.analytics.FirebaseAnalytics

object Event {
    const val LEVEL_UP = "LevelUp"
}

class Own(private val fa: FirebaseAnalytics) {
    fun up() { fa.logEvent(Event.LEVEL_UP, null) }
}
`,
		androidDir + "Imported.kt": `package com.example.sample.imported

import com.google.firebase.analytics.FirebaseAnalytics
import com.google.firebase.analytics.FirebaseAnalytics.Event

class Imported(private val fa: FirebaseAnalytics) {
    fun up() { fa.logEvent(Event.LEVEL_START, null) }
}
`,
	}))
	wantLines(t, "events", events(p),
		"Firebase Analytics name=level_start (Event.LEVEL_START) @"+androidDir+"Imported.kt:7 x1",
		"Firebase Analytics name=LevelUp (Event.LEVEL_UP) @"+androidDir+"Own.kt:10 x1",
	)
}

// A call written inside a string is text, not a call.
func TestAnalyticsCallsInStringsDoNotCount(t *testing.T) {
	p := analyze(t, testproject.AndroidNative().With(testproject.Files{
		androidDir + "Game.kt": `package com.example.sample

import android.util.Log
import com.google.firebase.analytics.FirebaseAnalytics

class Game(private val analytics: FirebaseAnalytics) {
    fun win() {
        Log.d("Game", "analytics.logEvent(\"in_a_log\", null)")
        val help = """
            analytics.logEvent("in_a_raw_string", null)
        """
        analytics.logEvent("real", null)
    }
}
`,
	}))
	wantLines(t, "Kotlin", events(p), "Firebase Analytics name=real @"+androidDir+"Game.kt:12 x1")

	p = analyze(t, testproject.Flutter().With(testproject.Files{
		"lib/game.dart": `import 'package:firebase_analytics/firebase_analytics.dart';

const help = '''
FirebaseAnalytics.instance.logEvent(name: 'in_a_dart_string');
''';

void win() {
  FirebaseAnalytics.instance.logEvent(name: r'raw_name');
}
`,
	}))
	wantLines(t, "Dart", events(p), "Firebase Analytics name=raw_name @lib/game.dart:8 x1")

	p = analyze(t, testproject.Files{
		"package.json":             `{"dependencies": {"react-native": "0.79.2"}}`,
		"android/app/build.gradle": "apply plugin: \"com.android.application\"\nandroid { defaultConfig { applicationId \"com.example.rn\" } }\n",
		"src/game.ts": "import analytics from '@react-native-firebase/analytics';\n" +
			"console.log(\"analytics().logEvent('in_a_log')\");\n" +
			"const help = `\n  analytics().logEvent('in_a_template')\n`;\n" +
			"analytics().logEvent('real');\n",
	})
	wantLines(t, "JavaScript", events(p), "Firebase Analytics name=real @src/game.ts:6 x1")
}

// A function of the app's own is followed only from the calls that reach
// it: not from another function of the same name.
func TestAnalyticsWrappersOnlyFromTheirCallers(t *testing.T) {
	const track = "app/src/main/java/com/example/sample/track/"
	p := analyze(t, testproject.AndroidNative().With(testproject.Files{
		track + "Track.kt": `package com.example.sample.track

import com.google.firebase.Firebase
import com.google.firebase.analytics.analytics

fun logEvent(name: String) {
    Firebase.analytics.logEvent(name, null)
}
`,
		track + "Screen.kt": `package com.example.sample.track

class Screen {
    fun open() { logEvent("same_package") }
}
`,
		androidDir + "Audio.kt": `package com.example.sample

class Audio {
    private fun logEvent(message: String) { println(message) }

    fun play() { logEvent("audio started playing") }
}
`,
		androidDir + "Menu.kt": `package com.example.sample

fun menu() { logEvent("not_imported") }
`,
		androidDir + "Shop.kt": `package com.example.sample

import com.example.sample.track.logEvent

fun shop() { logEvent("imported") }
`,
	}))
	wantLines(t, "Kotlin", events(p),
		"Firebase Analytics name=imported @"+androidDir+"Shop.kt:5 x1 via logEvent ("+track+"Track.kt:6)",
		"Firebase Analytics name=same_package @"+track+"Screen.kt:4 x1 via logEvent ("+track+"Track.kt:6)",
	)

	p = analyze(t, testproject.Files{
		"package.json":             `{"dependencies": {"react-native": "0.79.2"}}`,
		"android/app/build.gradle": "apply plugin: \"com.android.application\"\nandroid { defaultConfig { applicationId \"com.example.rn\" } }\n",
		"src/analytics.ts": `import analytics from '@react-native-firebase/analytics';

export function track(name: string) {
  analytics().logEvent(name);
}
`,
		"src/screen.ts": `import { track } from './analytics';

track('screen_opened');
`,
		"src/player.ts": `import { track } from './audio';

track('not_analytics');
`,
	})
	wantLines(t, "JavaScript", events(p), "Firebase Analytics name=screen_opened @src/screen.ts:3 x1 via track (src/analytics.ts:3)")
}

// A web build's output is not source; without a build, the web folder is
// the app as written.
func TestAnalyticsLeaveOutTheWebBuild(t *testing.T) {
	files := testproject.Files{
		"package.json":             `{"scripts": {"build": "ng build"}, "dependencies": {"@capacitor/core": "^8.0.0"}}`,
		"capacitor.config.ts":      `const config = { appId: 'com.example.cap', appName: 'Cap', webDir: 'www/browser' };`,
		"angular.json":             `{"projects": {"app": {"architect": {"build": {"options": {"outputPath": {"base": "out/app"}}}}}}}`,
		"android/app/build.gradle": "apply plugin: 'com.android.application'\nandroid { defaultConfig { applicationId \"com.example.cap\" } }\n",
		"src/app.ts": `import { FirebaseAnalytics } from '@capacitor-firebase/analytics';
FirebaseAnalytics.logEvent({ name: 'cap_event' });
`,
	}
	for _, out := range []string{"www/browser/main.js", "out/app/main.js", "dist/main.js", "www/main.js"} {
		files[out] = files["src/app.ts"]
	}
	p := analyze(t, files)
	wantLines(t, "built", events(p), "Firebase Analytics name=cap_event @src/app.ts:2 x1")

	p = analyze(t, testproject.Files{
		"package.json":             `{"dependencies": {"@capacitor/core": "^8.0.0"}}`,
		"capacitor.config.json":    `{"appId": "com.example.cap", "webDir": "www"}`,
		"android/app/build.gradle": "apply plugin: 'com.android.application'\nandroid { defaultConfig { applicationId \"com.example.cap\" } }\n",
		"www/app.js": `import { FirebaseAnalytics } from '@capacitor-firebase/analytics';
FirebaseAnalytics.logEvent({ name: 'written_in_www' });
`,
	})
	wantLines(t, "not built", events(p), "Firebase Analytics name=written_in_www @www/app.js:2 x1")
}

func TestAnalyticsLeaveOutMocksAndEndToEndTests(t *testing.T) {
	call := "import analytics from '@react-native-firebase/analytics';\nanalytics().logEvent('in_a_test');\n"
	p := analyze(t, testproject.Files{
		"package.json":             `{"dependencies": {"react-native": "0.79.2"}}`,
		"android/app/build.gradle": "apply plugin: \"com.android.application\"\nandroid { defaultConfig { applicationId \"com.example.rn\" } }\n",
		"__mocks__/analytics.js":   call,
		"e2e/login.e2e.js":         call,
		"src/flows/login.e2e.ts":   call,
		"src/app.ts":               "import analytics from '@react-native-firebase/analytics';\nanalytics().logEvent('real');\n",
	})
	wantLines(t, "events", events(p), "Firebase Analytics name=real @src/app.ts:2 x1")
}

func TestAnalyticsSDKsAndShapesFoundSince(t *testing.T) {
	p := analyze(t, testproject.Files{
		"package.json":             `{"dependencies": {"react-native": "0.79.2"}}`,
		"android/app/build.gradle": "apply plugin: \"com.android.application\"\nandroid { defaultConfig { applicationId \"com.example.rn\" } }\n",
		"src/legacy.js": `import amplitude from 'amplitude-js';

amplitude.getInstance().logEvent('legacy_event');
amplitude.getInstance().setUserId(user.id);
`,
		"src/meta.js": `import { AppEventsLogger } from 'react-native-fbsdk-next';

AppEventsLogger.logEvent('meta_event');
`,
	})
	wantLines(t, "amplitude-js", events(p), "Amplitude name=legacy_event @src/legacy.js:3 x1", "Facebook App Events name=meta_event @src/meta.js:3 x1")
	wantLines(t, "amplitude-js user IDs", userIDs(p), "Amplitude user.id @src/legacy.js:4")

	p = analyze(t, testproject.IOSNative().With(testproject.Files{
		"Sample/Flurry.swift": `import Flurry_iOS_SDK

func report(account: Account) {
    Flurry.log(eventName: "flurry_event")
    Flurry.log(errorId: "not_an_event", message: "", error: nil)
    Flurry.set(userId: account.id)
    Flurry.set(age: 30)
}
`,
		"Sample/Adjust.m": `#import "Adjust.h"
@import AdjustSdk;

@implementation Buy
- (void)buy {
    ADJEvent *event = [[ADJEvent alloc] initWithEventToken:@"abc123"];
    [Adjust trackEvent:event];
}
@end
`,
		"Sample/Feature/Home/Src/Main/Ui/Screen/Deep/Down/Below/The/Old/Limit/Deep.swift": `import FirebaseAnalytics
func deep() { Analytics.logEvent("deep_event", parameters: nil) }
`,
	}))
	wantLines(t, "iOS", events(p),
		"Adjust token=abc123 @Sample/Adjust.m:6 x1",
		"Firebase Analytics name=deep_event @Sample/Feature/Home/Src/Main/Ui/Screen/Deep/Down/Below/The/Old/Limit/Deep.swift:2 x1",
		"Flurry name=flurry_event @Sample/Flurry.swift:4 x1",
	)
	wantLines(t, "iOS user IDs", userIDs(p), "Flurry account.id @Sample/Flurry.swift:6")

	unity := `using FlurrySDK;
using Firebase.Analytics;

public class Report
{
    void Send(string playerId)
    {
        Flurry.LogEvent("unity_flurry");
        Flurry.SetUserId(playerId);
        FirebaseAnalytics.LogEvent("utf16_event");
    }
}
`
	units := utf16.Encode([]rune(unity))
	raw := []byte{0xFF, 0xFE}
	for _, u := range units {
		raw = append(raw, byte(u), byte(u>>8))
	}
	p = analyze(t, testproject.Unity().With(testproject.Files{"Assets/Scripts/Report.cs": string(raw)}))
	wantLines(t, "Unity", events(p),
		"Flurry name=unity_flurry @Assets/Scripts/Report.cs:8 x1",
		"Firebase Analytics name=utf16_event @Assets/Scripts/Report.cs:10 x1",
	)
	wantLines(t, "Unity user IDs", userIDs(p), "Flurry playerId @Assets/Scripts/Report.cs:9")
}

// The JSON of what is found is a contract, as project detect prints it.
func TestAnalyticsJSON(t *testing.T) {
	p := analyze(t, testproject.Files{
		"package.json":             `{"dependencies": {"react-native": "0.79.2"}}`,
		"android/app/build.gradle": "apply plugin: \"com.android.application\"\nandroid { defaultConfig { applicationId \"com.example.rn\" } }\n",
		"src/analytics.ts": `import analytics from '@react-native-firebase/analytics';
import { Adjust, AdjustEvent } from 'react-native-adjust';

export function track(name: string) {
  analytics().logEvent(name);
}

export const LEVEL = 'level_done';

export function play(level: number, user: User) {
  track('started');
  track('started');
  analytics().logEvent(LEVEL);
  analytics().logEvent('level_' + level);
  Adjust.trackEvent(new AdjustEvent('abc123'));
  analytics().setUserId(user.id);
}
`,
	})
	got, err := json.MarshalIndent(p.Analytics, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "events": [
    {
      "name": "started",
      "sdk": "Firebase Analytics",
      "file": "src/analytics.ts",
      "line": 11,
      "through": "track (src/analytics.ts:4)",
      "count": 2
    },
    {
      "name": "level_done",
      "expression": "LEVEL",
      "sdk": "Firebase Analytics",
      "file": "src/analytics.ts",
      "line": 13,
      "count": 1
    },
    {
      "expression": "'level_' + level",
      "sdk": "Firebase Analytics",
      "file": "src/analytics.ts",
      "line": 14,
      "count": 1
    },
    {
      "token": "abc123",
      "sdk": "Adjust",
      "file": "src/analytics.ts",
      "line": 15,
      "count": 1
    }
  ],
  "user_ids": [
    {
      "expression": "user.id",
      "sdk": "Firebase Analytics",
      "file": "src/analytics.ts",
      "line": 16,
      "count": 1
    }
  ]
}`
	if string(got) != want {
		t.Errorf("JSON:\n%s\nwant:\n%s", got, want)
	}
}
