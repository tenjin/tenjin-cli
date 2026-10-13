# Integrating and checking the Tenjin SDK with the `tenjin` CLI

The `tenjin` command-line tool does the Tenjin side of an SDK integration and checks the result. You make the code changes. The CLI tells you what the project is, which Tenjin app it belongs to, what is still missing, and what Tenjin answered when the app ran.

Every command below accepts `--json` and never prompts when it is not run at a terminal. Only `tenjin project set-keys` changes the project, and only to write SDK keys over the placeholders you leave for them. A failure prints `{"error": {"code", "message", "hint"}}` on standard error and sets the exit status: 2 usage, 3 not signed in, 4 role not allowed, 5 not found, 9 checks failed, 10 already exists.

Do not run `tenjin setup` yourself. It is the same sequence for a person at a terminal: it asks questions, and it is where the developer's SDK keys go in. Tell the developer to run it when you are done (step 6).

## The sequence

### 1. Find out what the project is

```
tenjin project detect --json
```

Read from the result:

- `supported`. If it is false, stop and tell the developer why; `notes` says why. A build exported from Unity, a Cordova app, a Kotlin Multiplatform app and a .NET app are all reported this way.
- `targets`. One entry per platform, each with its `bundle_id`. `other_bundle_ids` lists the IDs that other builds of the same app use, such as a debug suffix.
- `targets[].builds`. Each entry is a shipped build that is its own app in Tenjin, with its own SDK key: its `id` (`android`, `ios`, or `android:<variant>`), `bundle_id`, `tenjin_platform` (`ios`, `android`, `amazon` or `android_other`), the Android `variants` that produce it, and its `sdk_key_placeholder`. An Android app shipped to Google Play and the Amazon Appstore has two builds with the same bundle ID and different `tenjin_platform`. `tenjin_platform` is Tenjin's name for the app; what a flavor's manifest declares in `TENJIN_APP_STORE` is the SDK's: `googleplay`, `amazon` or `other`, never `android_other`.
- `library`, when present: the project is not an app but a library that wraps the Tenjin SDK (a pod, a Swift package, an Android library, a Flutter plugin or a React Native package). There is no app to set up: `doctor` checks only the SDK version it pins, and `setup` refuses it. Work in the app that uses the library, such as `library.example`. An app that gets the SDK through a library has `tenjin[].through` set, and `local` when the library's code is in the project.
- `targets[].android.launcher_source` and `targets[].ios.entry_point`. These are the files where the SDK is started.
- `features`. The parts of the SDK the integration can include. App opens are required; each other one is `suggested` when the project shows a reason, given in `evidence` (Google Play Billing for purchases, AppLovin MAX for ad revenue, and so on). Ask the developer which to include, with the suggested ones as the default, and integrate only those.
- `tracking_prompt`. If it is present, the app already asks for tracking permission on iOS. Hook into that; never add a second prompt.
- `tenjin`. If it is not empty, the SDK is already a dependency. Check the integration before changing anything.
- `notes`. Read all of them.

### 2. Check the sign-in

```
tenjin auth status --json
```

Exit status 3 means nobody is signed in. Ask the developer to run `tenjin login` themselves: it opens a browser and waits for them. Ask once. Steps 1 and 5 work without it, and without it step 4 leaves the keys to the developer.

### 3. Get the Tenjin app for each build

A Tenjin app is one bundle ID on one platform, and an Android build for another store is an app of its own. Run this once for every entry in `targets[].builds`; ask the developer first which builds ship, if there is more than one per platform:

```
tenjin apps ensure --bundle-id <bundle_id> --platform <tenjin_platform> --json
```

Exit status 5 means there is no such app yet. The bundle ID and platform of an app cannot be changed after it is created, so show the developer what would be created and ask first. Then run the command again with `--yes --name "<app name>"`. Exit status 4 means the developer's role cannot create apps: ask them to pick an existing app from `tenjin apps list --json`, or to ask a manager.

### 4. Leave a placeholder for each SDK key

You are not given the SDK keys, and you do not ask for them: a key that passes through a coding agent ends up in its transcripts. Wherever a key belongs, write the build's `sdk_key_placeholder`, exactly, as a string literal. For a platform's main build that is:

| Platform | Placeholder |
| --- | --- |
| Android | `__TENJIN_ANDROID_SDK_KEY__` |
| iOS | `__TENJIN_IOS_SDK_KEY__` |

In a cross-platform project, choose between them at runtime, as you would between the real keys. Where a platform has several builds, each build must send its own key, and two builds never share a placeholder: for Android product flavors, put each placeholder in its flavor's block of the build file, for example `buildConfigField("String", "TENJIN_SDK_KEY", "\"<placeholder>\"")`, and read `BuildConfig.TENJIN_SDK_KEY` where the SDK starts; in Flutter, choose by `appFlavor`. Keep the word Tenjin on each line that holds a placeholder, so that `doctor` can find the key there later. If the developer gives you a key anyway, still write the placeholder, and do not repeat the key.

Once the placeholders are written, have the CLI fill them:

```
tenjin project set-keys --json
```

Never pass it `--sdk-key`. When the developer is signed in and Tenjin serves the app's keys, it fetches each key, checks it with Tenjin and writes it over the placeholder, changing nothing else; you see only the last four characters. `filled` lists what it wrote. A build in `missing` has no key it could fetch, and the exit status is then 5: leave the placeholder, and say at the end that `tenjin setup` will ask the developer for it. A build in `refused` means Tenjin refused the app's own key: report it as it is. A placeholder in `unknown` names no build: replace it with the right one.

### 5. Integrate, then check

Read the integration guide first:

```
tenjin sdk guide
```

It prints the guide for the project in the current folder (name the platform, `tenjin sdk guide android` or `ios`, where a native project has both), at the revision this version of the CLI was tested with, and works offline. Where it says `<TENJIN_SDK_VERSION>`, use the version its header names; do not look one up. Where it says to ask for the SDK key, write the placeholder instead, as in step 4.

Make the changes, then run, with the features the developer chose:

```
tenjin doctor --json --features <id,id,...>
```

`findings` lists each check with a stable `id`, a `severity` (`error`, `warning`, `info` or `pass`), and for a problem the `file`, `line` and `fix`. Resolve every `error` and `warning`, and on native iOS the `info` finding `ios.debug_logs` as well: the launch check reads the SDK's logs, which only debug builds should turn on. Then run it again. Repeat until there are none, or the only errors left are `sdk.key.unfilled` for placeholders `set-keys` could not fill: those are the developer's step, not yours. The exit status is 0 when nothing would stop events reaching Tenjin and 9 otherwise, so it stays 9 while a placeholder is in place.

When the project already holds a real key, `doctor` checks it against Tenjin by sending one request and no event. A finding whose `id` starts with `key.` is about the key or the app in Tenjin, not about the code: the fix is the right key or the right bundle ID, which is for the developer to sort out in `tenjin setup`. A finding whose `id` starts with `feature.` is about a chosen feature that nothing sends to Tenjin yet: a warning to resolve when it was chosen, a suggestion to pass on to the developer when it was not.

### 6. Hand over to the developer

End by telling the developer to run `tenjin setup` in the project. It writes any SDK key still missing, asking for it if Tenjin cannot serve it; then it re-runs the checks, asks them to launch the app, and reads Tenjin's answer from the device.

If the keys are already in the project and the developer asks you to check a launch, run this after a debug build has been launched on a connected Android device or emulator, or on an iOS simulator, or installed on an iPhone connected by cable:

```
tenjin doctor --device --json
```

When several devices or simulators are running, pass `--serial` with the adb serial or the simulator's UDID that the `device.unavailable` finding lists. `--logs <file>` reads a saved log instead (`adb logcat`, or an iOS simulator's log), and `--logs -` reads standard input. The findings whose `id` starts with `device.` say what happened on the device:

| Finding | Meaning |
| --- | --- |
| `device.connect` is `pass` | Tenjin accepted the app's connect request |
| `device.connect` is `error` | Tenjin refused it; the title says why, and the fix follows from it |
| `device.processed` is `pass` | Tenjin has processed the install and attributed it |
| `device.processed` is `info` | Tenjin has not processed the install yet; run it again in a minute |
| `device.lookup` | Tenjin could not be asked about the install; the detail gives Tenjin's answer |
| `device.processed.unchecked` | The install was not looked up, for the reason in the title (offline, for example) |
| `device.connect` is `warning` | Only other apps' connects are in the log, or Tenjin gave an unusual answer |
| `device.bundle_id` | The build that ran has a different application ID from the project file |
| `device.connect.deduped` | The app was relaunched within 30 seconds, so the SDK sent nothing. Wait and relaunch |
| `device.no_debug_logs` | On iOS, the SDK's requests are logged without their contents: add `TenjinSDK.debugLogs()` to debug builds |
| `device.no_sdk_output` | The SDK never ran, the app was not launched, or, on iOS, the build does not call `TenjinSDK.debugLogs()` |
| `device.advertising_id` | On iOS, `warning` when `connect()` ran before the tracking prompt was answered; `info` when the user declined tracking, which is expected |
| `device.unavailable` | No device could be read; the title says why |

On iOS, `doctor --device` reads an iPhone connected by cable, launching the installed debug build itself, or a running simulator. A simulator's install has not been seen to be processed, so `device.processed` from an iPhone is what confirms an iOS integration; from a simulator, say that `device.connect` passed and the install is unconfirmed.

## Rules

- Call `connect()` on every launch, not only the first. On Android it belongs in the launcher Activity's `onResume()`, not in the `Application` class. On iOS it runs after the user has answered the tracking prompt.
- Use one SDK key per platform, chosen at runtime in a cross-platform project. Write placeholders, never keys.
- Never write an access token or a password into the project, and never repeat a whole SDK key in a summary.
- Make the smallest change that resolves each finding, in the style the project already uses.
- Report the code as done when `doctor` is clean, or when `sdk.key.unfilled` is the only error left. Report that Tenjin receives the app's events only when `device.connect` passed; otherwise say the launch was not checked and that `tenjin setup` checks it.
- Never pass `--yes` to a command that creates something unless the developer agreed to it.
