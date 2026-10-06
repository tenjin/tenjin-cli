# Tenjin CLI

Developer tools for integrating and verifying the [Tenjin](https://tenjin.com) SDK from the terminal.

**Status: proof of concept.** The commands below work; nothing is released yet, and registering test devices and tailing live events are not here yet.

## Install

Not released yet. Once it is:

```
brew install tenjin/tap/tenjin                                                    # macOS and Linux
curl -fsSL https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.sh | sh   # macOS and Linux
go install github.com/tenjin/tenjin-cli/cmd/tenjin@latest                          # anywhere with Go
```

On Windows, download the zip from the releases page. The install script checks each download against the release's `checksums.txt` and never uses sudo; `TENJIN_VERSION` picks a version and `TENJIN_INSTALL_DIR` the folder.

Homebrew installs shell completions for bash, zsh and fish. Otherwise, `tenjin completion <bash|zsh|fish|powershell> --help` says how to load them; the release archives also carry them, under `completions/`.

## What it does

The parts of SDK onboarding that happen on your machine:

- work out what your project is, and its bundle IDs;
- find or create the Tenjin app for each platform;
- check the integration and explain why it would send nothing;
- read what the SDK sent from a device, and what Tenjin answered.

tenjin does not write your integration. The code changes are made by you, or by your own coding agent: if Claude Code or Codex is installed, `tenjin setup` offers to hand it the integration, and `tenjin agent install` teaches any agent to use the CLI on its own. The CLI handles the Tenjin side and the checking. The one change it makes to your files is to write an SDK key where a coding agent left a placeholder for it, so that the key never passes through the agent.

## Commands

| Command | What it does |
| --- | --- |
| `tenjin setup [path]` | The guided path, from sign-in to a launch Tenjin has processed: runs the steps below in order, offers to integrate using your coding agent, and re-checks after each change |
| `tenjin project detect [path]` | Framework, platforms, bundle IDs, where the app starts, ad SDKs, any Tenjin SDK already declared |
| `tenjin doctor [path]` | Check the integration on disk and the SDK key against Tenjin |
| `tenjin doctor --device` | Also read a connected Android device or an iOS simulator: what the SDK sent and what Tenjin answered |
| `tenjin login`, `logout`, `auth status` | Sign in through the browser; session kept in the system keychain |
| `tenjin apps list` | The apps in your organization; `--platform` and `--bundle-id` narrow it |
| `tenjin apps get <id>` | One app, and whether Tenjin is receiving its events |
| `tenjin apps create` | Create an app from `--name`, `--bundle-id` and `--platform`, or from `--store-url` |
| `tenjin apps ensure --bundle-id … --platform …` | Find the app, or create it. Safe to repeat |
| `tenjin apps keys list --app <id>` | The app's SDK keys, by their last four characters, and how to copy one from the dashboard |
| `tenjin project set-keys [path]` | Write SDK keys where a coding agent left placeholders, fetching them from Tenjin where it can. `setup` does this itself; the command is for scripts and agents |
| `tenjin sdk guide [platform]` | The SDK integration guide for the project, or a named platform, as tested with this version; works offline |
| `tenjin agent install [path]` | Give your coding agent the instructions for this CLI |
| `tenjin version` | Version, commit and build date |

`setup` is for people. Every other command accepts `--json`, never prompts when stdin is not a terminal, and exits with a stable code, so scripts and coding agents can drive them:

| Exit | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Unexpected error |
| 2 | Wrong usage |
| 3 | Not signed in |
| 4 | Your role does not allow it |
| 5 | Not found |
| 6 | Rate limited |
| 7 | Tenjin is unavailable |
| 9 | `doctor` found problems |
| 10 | Already exists |

With `--json`, a failure is written to stderr as `{"error": {"code", "message", "hint"}}`.

`--verbose` (`-v`) writes a line to stderr for each request to Tenjin and each program run on your behalf, with the address, the answer and the time taken. It never shows a credential or a request body, so the output can be pasted into a support ticket. A request that fails in transit, or meets a gateway error, is sent again up to twice when that is safe: reads, and the SDK key check.

### The steps of setup

1. **Your project.** What `project detect` reports, and which Tenjin features to integrate. App opens are always included; purchases, subscriptions, ad revenue, consent, deep links, SKAdNetwork conversion values, custom events and the customer user ID are offered, and the ones the project shows a reason for are suggested: Google Play Billing or StoreKit for purchases, an ad SDK for ad revenue, a consent SDK for consent. Enter accepts the suggestion; `--features` chooses without asking.
2. **Sign in.** Through the browser, if there is no session.
3. **Tenjin apps.** Finds the app for each platform, or creates it after asking.
4. **SDK keys.** Where Tenjin's API serves SDK keys, `setup` fetches each app's key and checks it, and there is nothing to copy. Otherwise it says where to copy each one from: the app's page in the dashboard, in the "SDK Keys" card. It asks Tenjin about a key as soon as you paste it, so a wrong one, or the other platform's, is caught here and not after a build. A key that is already in the project and accepted is left alone. If a coding agent has already left a placeholder for it, the key is written there.
5. **Add the SDK.** With your coding agent, or by hand: `setup` lists what is still to add and the file the app starts in. An agent writes a placeholder where the key belongs, and `setup` writes the key there when the agent is done.
6. **Check.** Runs `doctor`, and again after each change until it is clean.
7. **Check a real launch.** Asks you to launch a debug build on an Android device or emulator, or on an iOS simulator, reads the device's log (through adb, or Xcode's `simctl`), and says what Tenjin answered. It ends when Tenjin has processed the device's install, which shows the integration works end to end; if the launch was refused, it says why and reads the next one. A project for both platforms is checked on each. Without adb or Xcode it says how to check later.

### SDK keys

A Tenjin app is one bundle ID on one platform, and each app has its own SDK key. A project that builds for Android and iOS therefore has two. An Android build for another store is an app of its own too: Tenjin files a build under `amazon` or `android_other` when it declares `TENJIN_APP_STORE`, so an app shipped to Google Play and the Amazon Appstore has two Android apps and two keys, even with one bundle ID. The same goes for product flavors with their own application ID.

`project detect` lists these builds, each with an ID (`android`, `ios`, or `android:<variant>`) and the placeholder for its key. `setup` asks which of them ship (or takes `--build`), creates an app for each, and checks each key against that build's own app, so the Google Play key in the Amazon build is caught before anything is built. `doctor` checks each build's key the same way, including keys kept in a flavor's `buildConfigField`, and warns when a flavor named for another store does not declare it.

A key can be passed to `setup` and `doctor` instead of pasted:

```
tenjin doctor --sdk-key <key>                               # one platform, or a key that serves all
tenjin doctor --sdk-key android=<key> --sdk-key ios=<key>   # one per platform
tenjin doctor --sdk-key android:amazon=<key>                # one per build, as project detect lists them
tenjin doctor --build android:amazon                        # check one build only
```

`doctor` then says whether Tenjin accepts each key for its platform's bundle ID. Once a key is a string literal in the source, `doctor` reads it from there and the flag is not needed; `setup` checks the one in the source in preference to one passed in, since that is what the app will send.

A coding agent never gets the key. Where it belongs, the agent writes `__TENJIN_ANDROID_SDK_KEY__` or `__TENJIN_IOS_SDK_KEY__`, and `doctor` reports each one still in place as `sdk.key.unfilled`. `setup` writes the keys over them. So can `project set-keys`, with each key checked against Tenjin before it is written and nothing else in the project changed:

```
tenjin project set-keys                                         # fetches the keys from Tenjin, when signed in
tenjin project set-keys --sdk-key android=<key> --sdk-key ios=<key>
```

Fetching is what lets an agent finish the job without seeing a key: it runs `tenjin project set-keys` and is shown only the last four characters. Doing the integration by hand works the same way: write the placeholder where the guide says `<SDK_KEY>`, and `setup` fills it.

### What project detect reports

For each platform: the bundle ID and where it was read, plus every other ID a build of the app can have (a debug suffix, a product flavor), since Tenjin treats each as a different app. For Android: the Gradle DSL, the version catalog, the launcher Activity and its file, code shrinking. For iOS: CocoaPods or Swift packages, the entry point (SwiftUI `App` or app delegate), a generated Info.plist. For the project: the JavaScript package manager, the ad SDKs among the dependencies, and where the app already asks for tracking permission.

It says so when a project is not one to integrate into: a build exported from Unity, a Cordova app, a Kotlin Multiplatform app, a .NET app. Run above the project, as in a monorepo, it names the projects it found up to two levels down.

### What doctor checks

Native Android, native iOS, Flutter, React Native, Capacitor and Unity projects: the SDK dependency and whether it is an old release, the initialise and connect calls, a placeholder key, Android permissions and Play Services libraries, ProGuard rules, the iOS tracking and SKAdNetwork plist keys, and whether Tenjin accepts the SDK key for each platform's bundle ID.

Each problem comes with a fix that can be applied as written: where a dependency is missing, the fix names the version to add. The versions are the newest releases at the time the CLI was built.

For Unity it also checks that the External Dependency Manager is there to add Tenjin's Android libraries, finds the `TenjinObject` component in scenes as well as calls in code, reads a key kept in a C# constant, and checks that a build script points SKAdNetwork postbacks at Tenjin, which the Unity package does not do.

The key check sends one request to Tenjin's collector and no event; `--offline` skips it. Unreal projects are detected but not checked yet.


It also reports, for each feature beyond app opens, whether the project sends it to Tenjin: ad revenue per ad network, purchases, subscriptions and the rest, by the SDK's own calls. With `--features`, a chosen feature that nothing sends is a warning; without it, a feature the project suggests is a suggestion, and one it does not suggest is not mentioned.
### Checking a real launch

The checks above read the project. `--device` reads what happened when the app ran:

```
tenjin doctor --device            # the connected Android device or emulator (adb), or the running iOS simulator (simctl)
tenjin doctor --logs logcat.txt   # a saved log, from logcat or a simulator; - reads standard input
```

Launch a debug build first. `doctor` then reports what Tenjin answered to the SDK's connect request and why a refusal happened, whether the build that ran has the application ID the project declares, the device's advertising ID, and the other requests the SDK sent. When the connect was accepted it asks Tenjin whether the install was processed, and to what it was attributed. `--serial` picks a device when several are connected: an adb serial, or a simulator's UDID. A project for both platforms is read from an Android device when one is connected, and from a simulator otherwise.

On iOS the SDK logs its requests only when the build calls `TenjinSDK.debugLogs()`; `doctor` checks that debug builds do, and that release builds do not. An iPhone connected by cable is read by launching the installed app with its console attached (`xcrun devicectl`); the app is left running. A running simulator is read from its log. Tenjin accepts a simulator's requests, but its installs have not been seen to be processed, so an iPhone is what confirms the install.

A device that cannot be read is a warning in the report, not a failure of the command.

### Integrating with a coding agent

At the "Add the SDK" step, `setup` looks for Claude Code and Codex. With one installed it asks:

```
Integrate the Tenjin SDK using Claude Code? [Y/n]
```

With both, it asks which. On yes it starts that agent in the project with the open findings as its task. The agent runs under your own account, and each is kept to the task in the way that agent supports:

| | How it is confined | What it runs to check itself |
| --- | --- | --- |
| Claude Code | May read and edit files and run `tenjin doctor` and `tenjin sdk guide`. No other command | `tenjin doctor` |
| Codex | Its own sandbox: it can change files in this project only, and has no network access | `tenjin doctor --offline` |

Neither is given your connectors. Claude Code cannot commit; Codex is told not to.

`setup` then lists the files that changed and runs the checks itself, including the SDK key check against Tenjin; the agent's word is not taken for it. It also prints the command that reopens the conversation (`claude --resume <id>` or `codex resume <id>`): the agent keeps it on your machine, so you can open it in another terminal to ask why something changed, or to carry on from there. That resumed session is an ordinary one, under your own settings, not the confined run `setup` started.

Pass `--agent claude` or `--agent codex` to skip the question, or `--agent none` to do the integration by hand. Without a terminal, an agent is started only when `--agent` names one. If `tenjin` is itself being run by a coding agent, no second one is started.

The task does not include the SDK key. The agent writes a placeholder where the key belongs, and `setup` writes the key there once the agent has finished, so the key never enters the agent's context, its transcript or its provider's logs.

### Teaching your agent the CLI

`setup` starts the agent for you. The other way round also works: an agent you are already using can drive the CLI itself, once it knows how.

```
tenjin agent install          # lists the files it would write, and asks
tenjin agent status
tenjin agent uninstall
```

For Claude Code this writes a skill to `.claude/skills/tenjin-cli/` in the project, or to your home directory with `--global`. For Codex, Cursor and other agents that read `AGENTS.md`, it adds a marked section to that file and leaves the rest as it is. `--agent claude` or `--agent agents-md` picks one; by default it uses the ones it finds. `tenjin agent skill` prints the instructions for anything else.

The instructions are part of the binary, so they describe the commands your version has. Run `tenjin agent install` again after updating.

### Environment

| Variable | Use |
| --- | --- |
| `TENJIN_API_TOKEN` | An access token from the dashboard, used instead of a browser sign-in. For CI |
| `TENJIN_SDK_KEY` | The SDK key, when the project does not hold it as a string literal |
| `TENJIN_SDK_KEY_ANDROID`, `TENJIN_SDK_KEY_IOS` | The same, per platform |
| `TENJIN_SDK_KEY_ANDROID_<VARIANT>` | The same, for another build, such as `TENJIN_SDK_KEY_ANDROID_AMAZON` |
| `TENJIN_DASHBOARD_URL`, `TENJIN_API_URL`, `TENJIN_COLLECTOR_URL`, `TENJIN_RESOURCE_URL` | Replace one of Tenjin's addresses, for a local or test copy of a service. The session for them is kept apart from production's |
| `NO_COLOR` | Disable colour and the wordmark |
| `CLICOLOR_FORCE` | Keep colour when the output is piped, for example into `less -R` |

## Build

Requires Go 1.27.

```
make build              # bin/tenjin
make test
make lint
make release-snapshot   # every release archive and the Homebrew cask, into dist/; publishes nothing
```

A release is made by pushing a tag such as `v0.1.0`: the release workflow tests, builds the archives for macOS, Linux and Windows with GoReleaser (`.goreleaser.yaml`), publishes them with their checksums on GitHub, and updates the cask in `tenjin/homebrew-tap`.

## Layout

```
cmd/tenjin          entry point
internal/cli        command tree, one file per noun
internal/project    detects what a project is; reads files only
internal/verify     the doctor checks
internal/agent      hands a task to an installed coding agent
internal/skill      the instructions for coding agents, and their installer
internal/keyfill    finds the SDK key placeholders agents leave, and fills them
internal/devicelog  reads the SDK's lines from a device log
internal/collector  asks Tenjin's collector about an SDK key or a device
internal/transport  HTTP retries and the --verbose log
internal/tenjinapi  operations on the Tenjin API
internal/oauth      browser sign-in
internal/session    stored credentials and token refresh
internal/config     service URLs per environment
internal/render     output: table and JSON
internal/clierr     error kinds and exit codes
docs/adr            architecture decisions
```

Commands stay thin: parse flags, call one operation, render the result. See [`docs/adr/0001-language-repository-and-scope.md`](docs/adr/0001-language-repository-and-scope.md).

## License

MIT. See [LICENSE](LICENSE). The SDK integration guides embedded under `internal/guides/data` come from [tenjin/sdk-llm-guides](https://github.com/tenjin/sdk-llm-guides), under the same licence.
