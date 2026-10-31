package project_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// analyze detects the project and reads its analytics.
func analyze(t *testing.T, files testproject.Files) *project.Project {
	t.Helper()
	p := detect(t, files)
	p.ReadAnalytics()
	return p
}

// events lists what detection found, one line each: SDK, then the name,
// token or expression, then where, and through what.
func events(p *project.Project) string {
	var out []string
	for _, e := range p.Analytics.Events {
		what := "name=" + e.Name
		switch {
		case e.Token != "":
			what = "token=" + e.Token
		case e.Name == "":
			what = "expr=" + e.Expression
		case e.Expression != "":
			what += " (" + e.Expression + ")"
		}
		line := fmt.Sprintf("%s %s @%s:%d x%d", e.SDK, what, e.File, e.Line, e.Count)
		if e.Through != "" {
			line += " via " + e.Through
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func userIDs(p *project.Project) string {
	var out []string
	for _, u := range p.Analytics.UserIDs {
		line := fmt.Sprintf("%s %s @%s:%d", u.SDK, u.Expression, u.File, u.Line)
		if u.Through != "" {
			line += " via " + u.Through
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func wantLines(t *testing.T, what, got string, want ...string) {
	t.Helper()
	if strings.Join(want, "\n") != got {
		t.Errorf("%s:\n%s\nwant:\n%s", what, got, strings.Join(want, "\n"))
	}
}

func TestAnalyticsOnAndroid(t *testing.T) {
	const dir = "app/src/main/java/com/example/sample/"
	p := analyze(t, testproject.AndroidNative().With(testproject.Files{
		dir + "Game.kt": `package com.example.sample

import android.content.Context
import com.appsflyer.AFInAppEventType
import com.appsflyer.AppsFlyerLib
import com.google.firebase.Firebase
import com.google.firebase.analytics.FirebaseAnalytics
import com.google.firebase.analytics.analytics

class Game(private val context: Context, private val tracker: Tracker) {
    private val analytics = Firebase.analytics

    fun finishLevel(level: Int) {
        analytics.logEvent("level_complete") { param("level", level.toLong()) }
        analytics.logEvent(FirebaseAnalytics.Event.LEVEL_UP, null)
        AppsFlyerLib.getInstance().logEvent(context, AFInAppEventType.LEVEL_ACHIEVED, mapOf())
        // analytics.logEvent("commented_out", null)
        /* analytics.logEvent("blocked_out", null) */
        val label = "level_" + level
        analytics.logEvent(label, null)
        analytics.logEvent("level_$level", null)
        analytics.logEvent("level_complete", null)
        tracker.track("tutorial_done", null)
        tracker.track(Events.SHOP_OPENED, null)
    }

    fun signIn(user: User) {
        analytics.setUserId(user.id)
        AppsFlyerLib.getInstance().setCustomerUserId(user.id)
        analytics.setUserId(null)
        logger.logEvent("not_firebase", null)
        logger.setUserId(user.id)
    }
}

object Events {
    const val SHOP_OPENED = "shop_opened"
}
`,
		dir + "Tracker.java": `package com.example.sample;

import android.os.Bundle;
import com.google.firebase.analytics.FirebaseAnalytics;

public class Tracker {
    private FirebaseAnalytics firebaseAnalytics;

    public void track(String eventName, Bundle params) {
        firebaseAnalytics.logEvent(eventName, params);
    }
}
`,
		// No analytics import: these are the app's own objects.
		dir + "Player.kt": `package com.example.sample

class Player(private val analytics: Analytics, private val media: MediaSession) {
    fun play() {
        analytics.logEvent("own_event", null)
        media.track("not_an_event")
        Analytics.setUserId("nobody")
    }
}
`,
		// Tenjin's own calls are not another SDK's.
		dir + "MainActivity.kt": strings.Replace(testproject.AndroidNative()[dir+"MainActivity.kt"], "tenjin.connect()", `tenjin.connect()
        tenjin.eventWithName("tenjin_event")
        tenjin.setCustomerUserId("tenjin_user")`, 1),
		"app/src/test/java/com/example/sample/GameTest.kt": `package com.example.sample

import com.google.firebase.analytics.FirebaseAnalytics

class GameTest {
    fun test(analytics: FirebaseAnalytics) {
        analytics.logEvent("in_a_test", null)
    }
}
`,
	}))
	wantLines(t, "events", events(p),
		"Firebase Analytics name=level_complete @"+dir+"Game.kt:14 x2",
		"Firebase Analytics name=level_up (FirebaseAnalytics.Event.LEVEL_UP) @"+dir+"Game.kt:15 x1",
		"AppsFlyer name=af_level_achieved (AFInAppEventType.LEVEL_ACHIEVED) @"+dir+"Game.kt:16 x1",
		"Firebase Analytics expr=label @"+dir+"Game.kt:20 x1",
		`Firebase Analytics expr="level_$level" @`+dir+"Game.kt:21 x1",
		"Firebase Analytics name=tutorial_done @"+dir+"Game.kt:23 x1 via track ("+dir+"Tracker.java:9)",
		"Firebase Analytics name=shop_opened (Events.SHOP_OPENED) @"+dir+"Game.kt:24 x1 via track ("+dir+"Tracker.java:9)",
	)
	wantLines(t, "user IDs", userIDs(p),
		"Firebase Analytics user.id @"+dir+"Game.kt:28",
		"AppsFlyer user.id @"+dir+"Game.kt:29",
	)
	if got := p.Analytics.Names(); strings.Join(got, ",") != "level_complete,level_up,af_level_achieved,tutorial_done,shop_opened" {
		t.Errorf("names = %q", got)
	}
}

func TestAnalyticsOnIOS(t *testing.T) {
	p := analyze(t, testproject.IOSNative().With(testproject.Files{
		"Sample/Events.swift": `import FirebaseAnalytics
import Mixpanel
import AppsFlyerLib

final class Events {
    func levelUp() {
        Analytics.logEvent(AnalyticsEventLevelUp, parameters: nil)
        Analytics.logEvent("boss_defeated", parameters: ["boss": "dragon"])
        Mixpanel.mainInstance().track(event: "boss_defeated")
        AppsFlyerLib.shared().logEvent("af_custom", withValues: [:])
        Analytics.logEvent("level_\(level)", parameters: nil)
    }

    func login(account: Account) {
        Analytics.setUserID(account.uid)
        Mixpanel.mainInstance().identify(distinctId: account.uid)
        AppsFlyerLib.shared().customerUserID = account.uid
    }
}
`,
		"Sample/Store.m": `#import "Store.h"
#import "Flurry.h"
@import FirebaseAnalytics;

@implementation Store
- (void)buy {
    [FIRAnalytics logEventWithName:@"store_opened" parameters:nil];
    [Flurry logEvent:@"STORE: buy" withParameters:nil];
    [FIRAnalytics setUserID:self.user.identifier];
}
@end
`,
		// An Analytics class of the app's own, with no Firebase import.
		"Sample/Own.swift": `final class Own {
    func go() {
        Analytics.logEvent("own_analytics", parameters: nil)
    }
}
`,
		// The SDK's own source, copied into the project.
		"Vendor/Mixpanel/Mixpanel.swift": `import Mixpanel
open class Mixpanel {
    func flush() { Mixpanel.mainInstance().track(event: "vendored") }
}
`,
		"SampleTests/EventsTests.swift": `import FirebaseAnalytics
func testIt() { Analytics.logEvent("in_a_test", parameters: nil) }
`,
	}))
	wantLines(t, "events", events(p),
		"Firebase Analytics name=level_up (AnalyticsEventLevelUp) @Sample/Events.swift:7 x1",
		"Firebase Analytics name=boss_defeated @Sample/Events.swift:8 x1",
		"Mixpanel name=boss_defeated @Sample/Events.swift:9 x1",
		"AppsFlyer name=af_custom @Sample/Events.swift:10 x1",
		`Firebase Analytics expr="level_\(level)" @Sample/Events.swift:11 x1`,
		"Firebase Analytics name=store_opened @Sample/Store.m:7 x1",
		"Flurry name=STORE: buy @Sample/Store.m:8 x1",
	)
	wantLines(t, "user IDs", userIDs(p),
		"Firebase Analytics account.uid @Sample/Events.swift:15",
		"Mixpanel account.uid @Sample/Events.swift:16",
		"AppsFlyer account.uid @Sample/Events.swift:17",
		"Firebase Analytics self.user.identifier @Sample/Store.m:9",
	)
}

func TestAnalyticsOnFlutter(t *testing.T) {
	p := analyze(t, testproject.Flutter().With(testproject.Files{
		"lib/analytics.dart": `import 'package:firebase_analytics/firebase_analytics.dart';
import 'package:gameanalytics_sdk/gameanalytics.dart';

class AnalyticsService {
  static FirebaseAnalytics? _analytics = FirebaseAnalytics.instance;

  static Future<void> logEvent({required String name, Map<String, Object>? parameters}) async {
    await _analytics!.logEvent(name: name, parameters: parameters);
  }

  static Future<void> identify(String uid) async {
    await _analytics?.setUserId(id: uid);
  }

  static void levelUp() {
    FirebaseAnalytics.instance.logLevelUp(level: 2);
    GameAnalytics.addDesignEvent({"eventId": "ga:design"});
  }
}

class AppConstants {
  static const String memoryCreated = 'memory_created';
}
`,
		"lib/screens.dart": `import 'analytics.dart';

void open(User user) {
  AnalyticsService.logEvent(name: 'screen_opened');
  AnalyticsService.logEvent(name: AppConstants.memoryCreated, parameters: {});
  AnalyticsService.logEvent(name: 'screen_opened');
  AnalyticsService.identify(user.uid);
  logger.logEvent(name: 'not_through_the_service');
}
`,
		"test/analytics_test.dart": `import 'package:firebase_analytics/firebase_analytics.dart';
void main() { FirebaseAnalytics.instance.logEvent(name: 'in_a_test'); }
`,
	}))
	wantLines(t, "events", events(p),
		"Firebase Analytics name=screen_opened @lib/screens.dart:4 x2 via logEvent (lib/analytics.dart:7)",
		"Firebase Analytics name=level_up (logLevelUp) @lib/analytics.dart:16 x1",
		"GameAnalytics name=ga:design @lib/analytics.dart:17 x1",
		"Firebase Analytics name=memory_created (AppConstants.memoryCreated) @lib/screens.dart:5 x1 via logEvent (lib/analytics.dart:7)",
	)
	wantLines(t, "user IDs", userIDs(p),
		"Firebase Analytics user.uid @lib/screens.dart:7 via identify (lib/analytics.dart:11)",
	)
}

func TestAnalyticsOnReactNative(t *testing.T) {
	p := analyze(t, testproject.Files{
		"package.json":             `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "^1.7.0"}}`,
		"android/app/build.gradle": "apply plugin: \"com.android.application\"\nandroid { defaultConfig { applicationId \"com.example.rn\" } }\n",
		"src/analytics.ts": `import analytics from '@react-native-firebase/analytics';
import appsFlyer from 'react-native-appsflyer';
import { Mixpanel } from 'mixpanel-react-native';
import { Adjust, AdjustEvent } from 'react-native-adjust';
import { getAnalytics, logEvent } from '@react-native-firebase/analytics';
import Tenjin from 'react-native-tenjin';

const mixpanel = new Mixpanel('token', true);
export const Events = { PURCHASE_DONE: 'purchase_done' };

export async function onPurchase() {
  await analytics().logEvent(Events.PURCHASE_DONE, {});
  appsFlyer.logEvent('af_purchase', {});
  mixpanel.track('Purchase');
  Adjust.trackEvent(new AdjustEvent('abc123'));
  logEvent(getAnalytics(), 'modular_event');
  Tenjin.sendEvent('tenjin_event');
}

export function login(user) {
  analytics().setUserId(user.id);
  mixpanel.identify(user.id);
}

const player = { track(x) {} };
player.track('not_an_event');
`,
		"src/__tests__/analytics.test.ts": `import analytics from '@react-native-firebase/analytics';
analytics().logEvent('in_a_test');
`,
	})
	wantLines(t, "events", events(p),
		"Firebase Analytics name=purchase_done (Events.PURCHASE_DONE) @src/analytics.ts:12 x1",
		"AppsFlyer name=af_purchase @src/analytics.ts:13 x1",
		"Mixpanel name=Purchase @src/analytics.ts:14 x1",
		"Adjust token=abc123 @src/analytics.ts:15 x1",
		"Firebase Analytics name=modular_event @src/analytics.ts:16 x1",
	)
	wantLines(t, "user IDs", userIDs(p),
		"Firebase Analytics user.id @src/analytics.ts:21",
		"Mixpanel user.id @src/analytics.ts:22",
	)
}

func TestAnalyticsOnCapacitor(t *testing.T) {
	p := analyze(t, testproject.Files{
		"package.json":             `{"dependencies": {"@capacitor/core": "^8.0.0"}}`,
		"capacitor.config.ts":      `const config = { appId: 'com.example.cap', appName: 'Cap' };`,
		"android/app/build.gradle": "apply plugin: 'com.android.application'\nandroid { defaultConfig { applicationId \"com.example.cap\" } }\n",
		"src/app.ts": `import { FirebaseAnalytics } from '@capacitor-firebase/analytics';

export async function start(session: Session) {
  await FirebaseAnalytics.logEvent({ name: 'cap_event', params: {} });
  await FirebaseAnalytics.setUserId({ userId: session.userId });
}
`,
	})
	wantLines(t, "events", events(p), "Firebase Analytics name=cap_event @src/app.ts:4 x1")
	wantLines(t, "user IDs", userIDs(p), "Firebase Analytics session.userId @src/app.ts:5")
}

func TestAnalyticsOnUnity(t *testing.T) {
	p := analyze(t, testproject.Unity().With(testproject.Files{
		"Assets/Scripts/Analytics.cs": `using System.Collections.Generic;
using Firebase.Analytics;
using GameAnalyticsSDK;
using AppsFlyerSDK;
using UnityEngine;

public class Analytics : MonoBehaviour
{
    void OnLevel(string playerId)
    {
        FirebaseAnalytics.LogEvent(FirebaseAnalytics.EventLevelUp);
        GameAnalytics.NewDesignEvent("level:complete");
        AppsFlyer.sendEvent("af_level", new Dictionary<string, string>());
        FirebaseAnalytics.SetUserId(SystemInfo.deviceUniqueIdentifier);
        GameAnalytics.SetCustomId(playerId);
        Tenjin.getInstance("key").SendEvent("tenjin_event");
    }
}
`,
		"Assets/GameAnalytics/Plugins/Scripts/Example.cs": `using GameAnalyticsSDK;
public class Example { void Go() { GameAnalytics.NewDesignEvent("vendor_example"); } }
`,
	}))
	wantLines(t, "events", events(p),
		"Firebase Analytics name=level_up (FirebaseAnalytics.EventLevelUp) @Assets/Scripts/Analytics.cs:11 x1",
		"GameAnalytics name=level:complete @Assets/Scripts/Analytics.cs:12 x1",
		"AppsFlyer name=af_level @Assets/Scripts/Analytics.cs:13 x1",
	)
	// SetCustomId(playerId) passes a parameter, but nothing in the project
	// calls OnLevel: the call inside it is what there is to report.
	wantLines(t, "user IDs", userIDs(p),
		"Firebase Analytics SystemInfo.deviceUniqueIdentifier @Assets/Scripts/Analytics.cs:14",
		"GameAnalytics playerId @Assets/Scripts/Analytics.cs:15",
	)
}

// Apps often send through a function of their own. Its callers give the
// names, counted only when the call is to that function: on its class, on
// a variable of that class, or, for a function of its own, on nothing.
func TestAnalyticsThroughTheAppsOwnFunctions(t *testing.T) {
	p := analyze(t, testproject.IOSNative().With(testproject.Files{
		"Sample/AnalyticsService.swift": `import FirebaseAnalytics

final class AnalyticsService {
    static let shared = AnalyticsService()

    func track(_ name: String) {
        Analytics.logEvent(name, parameters: nil)
    }

    func identify(as id: String?) {
        Analytics.setUserID(id)
    }
}
`,
		"Sample/Game.swift": `final class Game {
    let service = AnalyticsService()

    func win() {
        AnalyticsService.shared.track("swift_wrapped")
        service.track("swift_wrapped")
        player.track("not_through_the_service")
        AnalyticsService.shared.identify(as: nil)
    }
}
`,
		"Sample/Tracker.m": `#import "Tracker.h"
@import FirebaseAnalytics;

@implementation Tracker
+ (void)logEvent:(NSString *)name {
    [FIRAnalytics logEventWithName:name parameters:nil];
}
@end
`,
		"Sample/Shop.m": `#import "Tracker.h"
@implementation Shop
- (void)buy {
    [Tracker logEvent:@"objc_wrapped"];
    [self.other logEvent:@"not_the_tracker"];
}
@end
`,
	}))
	wantLines(t, "events", events(p),
		"Firebase Analytics name=swift_wrapped @Sample/Game.swift:5 x2 via track (Sample/AnalyticsService.swift:6)",
		"Firebase Analytics name=objc_wrapped @Sample/Shop.m:4 x1 via logEvent (Sample/Tracker.m:5)",
	)
	// The only call to identify gives nil: the call inside it is what there
	// is to report.
	wantLines(t, "user IDs", userIDs(p), "Firebase Analytics id @Sample/AnalyticsService.swift:11")

	p = analyze(t, testproject.Files{
		"package.json":             `{"dependencies": {"react-native": "0.79.2"}}`,
		"android/app/build.gradle": "apply plugin: \"com.android.application\"\nandroid { defaultConfig { applicationId \"com.example.rn\" } }\n",
		"src/analytics.ts": `import analytics from '@react-native-firebase/analytics';
import { Mixpanel } from 'mixpanel-react-native';

const mixpanel = new Mixpanel('token', true);

export function trackEvent(name: string, params?: object) {
  analytics().logEvent(name, params);
}

export const Tracking = {
  track(name) {
    mixpanel.track(name);
  },
};
`,
		"src/screen.tsx": `import { trackEvent, Tracking } from './analytics';

export function Screen() {
  trackEvent('js_wrapped', {});
  Tracking.track('object_wrapped');
  audio.track('not_tracking');
}
`,
	})
	wantLines(t, "events", events(p),
		"Firebase Analytics name=js_wrapped @src/screen.tsx:4 x1 via trackEvent (src/analytics.ts:6)",
		"Mixpanel name=object_wrapped @src/screen.tsx:5 x1 via track (src/analytics.ts:11)",
	)
}

func TestNoAnalytics(t *testing.T) {
	p := analyze(t, testproject.AndroidNative())
	if len(p.Analytics.Events) != 0 || len(p.Analytics.UserIDs) != 0 || p.Analytics.Events == nil || p.Analytics.UserIDs == nil {
		t.Errorf("analytics = %+v", p.Analytics)
	}
}

// sentToTenjin lists what the app sends Tenjin's own SDK, as events and
// userIDs list the other SDKs'.
func sentToTenjin(t *testing.T, files testproject.Files) (string, string) {
	t.Helper()
	sent := &project.Project{Analytics: detect(t, files).SentToTenjin()}
	return events(sent), userIDs(sent)
}

// The custom events and the user ID the app sends Tenjin are read by the
// same rules as other SDKs': on Tenjin's receivers, names from literals and
// constants, and through one of the app's own functions.
func TestWhatTheAppSendsTenjin(t *testing.T) {
	const dir = "app/src/main/java/com/example/sample/"
	android := testproject.AndroidNative()
	got, ids := sentToTenjin(t, android.With(testproject.Files{
		dir + "MainActivity.kt": strings.Replace(android[dir+"MainActivity.kt"], "tenjin.connect()", `tenjin.connect()
        tenjin.eventWithName("existing_event")
        tenjin.eventWithNameAndValue(Events.LEVEL, 5)
        tenjin.customerUserId = user.id
        track("through_a_wrapper")
        player.eventWithName("not_tenjin")`, 1),
		dir + "Events.kt": `package com.example.sample

import com.tenjin.android.TenjinSDK

object Events {
    const val LEVEL = "level_up"
}

fun track(name: String) {
    TenjinSDK.getInstance(null, "").eventWithName(name)
}
`,
	}))
	wantLines(t, "Android events", got,
		"Tenjin name=existing_event @app/src/main/java/com/example/sample/MainActivity.kt:11 x1",
		"Tenjin name=level_up (Events.LEVEL) @app/src/main/java/com/example/sample/MainActivity.kt:12 x1",
		"Tenjin name=through_a_wrapper @app/src/main/java/com/example/sample/MainActivity.kt:14 x1 via track (app/src/main/java/com/example/sample/Events.kt:9)",
	)
	wantLines(t, "Android user IDs", ids, "Tenjin user.id @app/src/main/java/com/example/sample/MainActivity.kt:13")

	ios := testproject.IOSNative()
	got, ids = sentToTenjin(t, ios.With(testproject.Files{
		"Sample/Events.swift": "import TenjinSDK\n\nfunc won() {\n    TenjinSDK.sendEvent(withName: \"swift_event\")\n    TenjinSDK.setCustomerUserId(account.id)\n}\n",
		"Sample/Legacy.m":     "#import <TenjinSDK/TenjinSDK.h>\n\nvoid won(void) {\n    [TenjinSDK sendEventWithName:@\"objc_event\" andValue:5];\n}\n",
	}))
	if !strings.Contains(got, "name=swift_event") || !strings.Contains(got, "name=objc_event") || !strings.Contains(ids, "account.id") {
		t.Errorf("iOS: events\n%s\nuser IDs\n%s", got, ids)
	}

	flutter := testproject.Flutter()
	got, _ = sentToTenjin(t, flutter.With(testproject.Files{
		"lib/events.dart": "import 'package:tenjin_plugin/tenjin_sdk.dart';\n\nvoid won() {\n  TenjinSDK.instance.eventWithName('dart_event');\n}\n",
	}))
	wantLines(t, "Flutter events", got, "Tenjin name=dart_event @lib/events.dart:4 x1")

	unity := testproject.Unity()
	got, _ = sentToTenjin(t, unity.With(testproject.Files{
		"Assets/Scripts/TenjinBootstrap.cs": strings.Replace(unity["Assets/Scripts/TenjinBootstrap.cs"], "instance.SetAppStoreType(AppStoreType.googleplay);",
			"instance.SetAppStoreType(AppStoreType.googleplay);\n        instance.SendEvent(\"unity_event\");", 1),
	}))
	if !strings.Contains(got, "name=unity_event") {
		t.Errorf("Unity events:\n%s", got)
	}

	got, ids = sentToTenjin(t, testproject.Files{
		"package.json":             `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "^1.7.0"}}`,
		"android/app/build.gradle": "apply plugin: \"com.android.application\"\nandroid { defaultConfig { applicationId \"com.example.rn\" } }\n",
		"src/events.ts":            "import Tenjin from 'react-native-tenjin';\n\nexport function won(user) {\n  Tenjin.eventWithName('rn_event');\n  Tenjin.setCustomerUserId(user.id);\n}\n",
	})
	wantLines(t, "React Native events", got, "Tenjin name=rn_event @src/events.ts:4 x1")
	wantLines(t, "React Native user IDs", ids, "Tenjin user.id @src/events.ts:5")

	got, _ = sentToTenjin(t, testproject.Files{
		"package.json":             `{"dependencies": {"@capacitor/core": "^8.0.0"}}`,
		"capacitor.config.ts":      `const config = { appId: 'com.example.cap', appName: 'Cap' };`,
		"android/app/build.gradle": "apply plugin: 'com.android.application'\nandroid { defaultConfig { applicationId \"com.example.cap\" } }\n",
		"src/app.ts":               "import { Tenjin } from '@tenjin/capacitor';\n\nexport async function won() {\n  await Tenjin.eventWithName({ name: 'cap_event' });\n}\n",
	})
	wantLines(t, "Capacitor events", got, "Tenjin name=cap_event @src/app.ts:4 x1")

	// A shorthand property is the variable of that name: { userId } is
	// { userId: userId }, and { name } in the app's own function is its
	// parameter, followed to the calls that give it.
	got, ids = sentToTenjin(t, testproject.Files{
		"package.json":             `{"dependencies": {"@capacitor/core": "^8.0.0"}}`,
		"capacitor.config.ts":      `const config = { appId: 'com.example.cap', appName: 'Cap' };`,
		"android/app/build.gradle": "apply plugin: 'com.android.application'\nandroid { defaultConfig { applicationId \"com.example.cap\" } }\n",
		"src/app.ts": `import { Tenjin } from '@tenjin/capacitor';

export async function signedIn(user: User) {
  const userId = user.id;
  await Tenjin.setCustomerUserId({ userId });
}

function track(name: string) {
  return Tenjin.eventWithName({ name });
}

export function won() {
  track('level_up');
}
`,
	})
	wantLines(t, "Capacitor shorthand events", got, "Tenjin name=level_up @src/app.ts:13 x1 via track (src/app.ts:8)")
	wantLines(t, "Capacitor shorthand user IDs", ids, "Tenjin userId @src/app.ts:5")

	// The receiver may be set in another file, under one of the names doctor
	// takes for it.
	got, ids = sentToTenjin(t, testproject.Flutter().With(testproject.Files{
		"lib/analytics.dart": "import 'package:tenjin_plugin/tenjin_sdk.dart';\n\nfinal tenjin = TenjinSDK.instance;\n",
		"lib/game.dart":      "import 'package:tenjin_plugin/tenjin_sdk.dart';\nimport 'analytics.dart';\n\nvoid levelUp(User user) {\n  tenjin.eventWithName('level_up');\n  tenjin.setCustomerUserId(user.id);\n}\n",
	}))
	wantLines(t, "Flutter events on another file's receiver", got, "Tenjin name=level_up @lib/game.dart:5 x1")
	wantLines(t, "Flutter user IDs on another file's receiver", ids, "Tenjin user.id @lib/game.dart:6")

	// Another SDK's instance, or its getInstance(), is not Tenjin's.
	_, ids = sentToTenjin(t, testproject.AndroidNative().With(testproject.Files{
		"app/src/main/java/com/example/sample/Login.kt": "package com.example.sample\n\nimport com.appsflyer.AppsFlyerLib\nimport com.tenjin.android.TenjinSDK\n\nfun login(user: User) {\n    AppsFlyerLib.getInstance().setCustomerUserId(user.id)\n    Other.instance.setCustomerUserId(user.id)\n}\n",
	}))
	wantLines(t, "Android user IDs on other SDKs", ids)

	// Nor is a variable named instance that the file sets from another SDK.
	_, ids = sentToTenjin(t, testproject.AndroidNative().With(testproject.Files{
		"app/src/main/java/com/example/sample/Login.kt": "package com.example.sample\n\nimport com.appsflyer.AppsFlyerLib\nimport com.tenjin.android.TenjinSDK\n\nfun login(user: User) {\n    val instance = AppsFlyerLib.getInstance()\n    instance.setCustomerUserId(user.id)\n}\n",
	}))
	wantLines(t, "Android user IDs on another SDK's instance", ids)
	got, _ = sentToTenjin(t, testproject.Flutter().With(testproject.Files{
		"lib/game.dart": "import 'package:tenjin_plugin/tenjin_sdk.dart';\n\nvoid levelUp() {\n  final instance = Other.instance;\n  instance.sendEvent('level_up');\n}\n",
	}))
	wantLines(t, "Flutter events on another SDK's instance", got)
	got, _ = sentToTenjin(t, unity.With(testproject.Files{
		"Assets/Scripts/Store.cs": "using AppsFlyerSDK;\n\npublic class Store {\n    void Start() {\n        Tenjin.getInstance(Keys.Tenjin).Connect();\n    }\n\n    void Bought() {\n        var instance = AppsFlyer.instance;\n        instance.SendEvent(\"purchase\");\n    }\n}\n",
	}))
	wantLines(t, "Unity events on another SDK's instance", got)

	// An instance the file does not set may be Tenjin's, set in another file.
	got, ids = sentToTenjin(t, unity.With(testproject.Files{
		"Assets/Scripts/Level.cs": "public partial class TenjinManager {\n    void Won(User user) {\n        instance.SendEvent(\"level_up\");\n        instance.SetCustomerUserId(user.id);\n    }\n}\n",
	}))
	wantLines(t, "Unity events on another file's instance", got, "Tenjin name=level_up @Assets/Scripts/Level.cs:3 x1")
	wantLines(t, "Unity user IDs on another file's instance", ids, "Tenjin user.id @Assets/Scripts/Level.cs:4")
}
