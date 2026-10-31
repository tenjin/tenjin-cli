# Tenjin CLI

[![Test](https://github.com/tenjin/tenjin-cli/actions/workflows/test.yaml/badge.svg?branch=main)](https://github.com/tenjin/tenjin-cli/actions/workflows/test.yaml)
[![Lint](https://github.com/tenjin/tenjin-cli/actions/workflows/linter.yaml/badge.svg?branch=main)](https://github.com/tenjin/tenjin-cli/actions/workflows/linter.yaml)
[![Docs](https://github.com/tenjin/tenjin-cli/actions/workflows/docs.yaml/badge.svg?branch=main)](https://github.com/tenjin/tenjin-cli/actions/workflows/docs.yaml)
[![Release](https://img.shields.io/github/v/release/tenjin/tenjin-cli?sort=semver)](https://github.com/tenjin/tenjin-cli/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Integrate the [Tenjin](https://tenjin.com) SDK into your app, from the terminal.

The Tenjin CLI covers the parts of SDK onboarding that happen on your machine:

- **Detect** what your project is: framework, platforms, bundle IDs, the file the app starts in, ad SDKs.
- **Set up** the Tenjin app for each build, and its SDK key.
- **Check** the integration and explain, with a fix you can apply as written, why it would send nothing.
- **Verify** a real launch: read what the SDK sent from a device, and confirm Tenjin processed the install.

It does not write your integration. You do, or your own coding agent does: `tenjin setup` can hand the work to Claude Code or Codex, and checks the result itself.

> **Pre-release.** The first release is in preparation. Until it is published, [build from source](CONTRIBUTING.md#developing).

## Contents

- [Quick start](#quick-start)
- [Install](#install)
- [Requirements](#requirements)
- [Usage](#usage)
- [Scripts, CI and coding agents](#scripts-ci-and-coding-agents)
- [Configuration](#configuration)
- [Privacy and security](#privacy-and-security)
- [Troubleshooting](#troubleshooting)
- [Contributing](#contributing)
- [License](#license)

## Quick start

```sh
brew install tenjin/tap/tenjin
cd path/to/your/app
tenjin setup
```

`setup` walks you from sign-in to a launch Tenjin has processed. Every step is also a command of its own; see [Commands](#commands).

## Install

| Platform | Command |
| --- | --- |
| macOS, Linux | `brew install tenjin/tap/tenjin` |
| macOS, Linux | `curl -fsSL https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.sh \| sh` |
| Windows (PowerShell) | `irm https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.ps1 \| iex` |
| Anywhere with Go | `go install github.com/tenjin/tenjin-cli/cmd/tenjin@latest` |

Archives for macOS, Linux and Windows (Intel and ARM) are also on the [releases page](https://github.com/tenjin/tenjin-cli/releases), with a `checksums.txt`. Both install scripts check each download against it, which catches a damaged download; to check that an archive was built by this repository's release workflow, see [Privacy and security](#privacy-and-security). Neither script needs sudo or administrator rights.

<details>
<summary>What the install scripts do, and how to change it</summary>

| Variable | Effect |
| --- | --- |
| `TENJIN_VERSION` | Install this version (for example `v1.0.0`) instead of the latest |
| `TENJIN_INSTALL_DIR` | Install into this folder |
| `TENJIN_NO_MODIFY_PATH=1` | Never edit shell startup files (`install.sh`) |

**`install.sh`** installs where `tenjin` already is, or in a folder already on your PATH (`~/.local/bin`, `~/bin`, or `/usr/local/bin` when it can be written to). Otherwise it uses `~/.local/bin` and adds it to PATH for zsh (`.zshenv`), bash (`.bashrc` and the login profile), fish (`conf.d/tenjin.fish`) and `.profile`, each line marked `# Added by the Tenjin CLI installer`. bash reads those files only for login and interactive shells, so a script run with `bash -c` needs the full path. If the CLI was installed with Homebrew, the script stops and points to `brew upgrade tenjin`.

**`install.ps1`** installs `tenjin.exe` in `%LOCALAPPDATA%\Programs\tenjin` and adds that folder to your user PATH.

</details>

### Update

The CLI tells you when a newer release is out, with the command that updates it the way you installed it:

```sh
brew upgrade tenjin                    # Homebrew
curl -fsSL https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.sh | sh   # install script
```

It checks at most once a day, and only when a person is at the terminal: never with `--json`, in CI, or when stderr is not a terminal. `TENJIN_NO_UPDATE_CHECK=1` turns it off.

### Uninstall

```sh
tenjin logout                          # forget the session and revoke it
brew uninstall tenjin                  # Homebrew; or delete the tenjin binary
```

Then remove the CLI's folder (see [Files](#files)) and, for `install.sh`, the lines marked `# Added by the Tenjin CLI installer` in your shell startup files. If you ran `tenjin agent install`, run `tenjin agent uninstall` first.

### Shell completions

Homebrew installs completions for bash, zsh and fish. Otherwise, `tenjin completion <bash|zsh|fish|powershell> --help` says how to load them, and the release archives carry them under `completions/`.

## Requirements

- A Tenjin account, for the Tenjin apps and their SDK keys. Detecting and checking a project work without one, and so does `docs search`.
- To verify a launch: `adb` for an Android device or emulator, or Xcode for an iPhone or an iOS simulator.
- Optional: [Claude Code](https://claude.com/claude-code) or [Codex](https://github.com/openai/codex), for `setup` to integrate with.

| Project | Detect | Check | Verify a launch |
| --- | --- | --- | --- |
| Native Android (Kotlin, Java) | ✓ | ✓ | ✓ |
| Native iOS (Swift, Objective-C) | ✓ | ✓ | ✓ |
| Flutter | ✓ | ✓ | ✓ |
| React Native | ✓ | ✓ | ✓ |
| Capacitor | ✓ | ✓ | ✓ |
| Unity | ✓ | ✓ | ✓ |
| Unreal | ✓ | | |

## Usage

### Commands

#### Integrating the SDK

In the app's project.

| Command | What it does |
| --- | --- |
| `tenjin setup [path]` | The guided path, from sign-in to a launch Tenjin has processed |
| `tenjin project detect [path]` | Framework, platforms, bundle IDs, where the app starts, ad SDKs, any Tenjin SDK already declared, and the events and user ID the app gives other analytics SDKs |
| `tenjin doctor [path]` | Check the integration on disk, and each SDK key against Tenjin |
| `tenjin doctor --device` | Also read a connected device: what the SDK sent and what Tenjin answered |
| `tenjin project set-keys [path]` | Write SDK keys where a coding agent left placeholders. Run it yourself: it does not run inside a coding agent, which would then read the keys |
| `tenjin sdk guide [platform]` | The SDK integration guide, as tested with this version; works offline |
| `tenjin sdk upgrade [path]` | Plan the upgrade to the SDK's newest release, with what a new major version changes, and have your coding agent make the changes the files need |
| `tenjin agent install [path]` | Teach your coding agent to integrate Tenjin with this CLI |

The Tenjin apps, their SDK keys and the documentation are commands of their own too, for scripts and agents; setup's steps 3 and 4 use the first two. None of these reads a project.

| Command | What it does |
| --- | --- |
| `tenjin apps ensure --bundle-id <id> --platform <platform>` | Find the Tenjin app for a build, or create it. Safe to repeat; with `--dry-run`, an app not there yet is not created, and the request that would create it is printed |
| `tenjin apps keys list --app <id>` | The app's SDK keys, by their last four characters, and where to copy one from |
| `tenjin apps list`, `tenjin apps get <id>` | The apps in your organization; one app, and whether Tenjin is receiving its events |
| `tenjin apps create` | Create an app from `--name`, `--bundle-id` and `--platform`, or from `--store-url`; `--dry-run` prints the request and sends nothing |
| `tenjin docs search <words>` | Tenjin's documentation, with links |

#### Your account

| Command | What it does |
| --- | --- |
| `tenjin login`, `tenjin logout`, `tenjin auth status` | Sign in through the browser (`--no-browser` over SSH); sign out, which ends the session's API access at once, and that of your other Tenjin CLI sessions still open in the same organization, on any machine; see who is signed in |

`tenjin version` prints the version, commit and build date. `tenjin telemetry off` stops the usage statistics on this machine (see [Privacy and security](#privacy-and-security)); `tenjin telemetry on` and `tenjin telemetry status` turn them back on and say whether they are sent.

`tenjin <command> --help` describes every flag, with examples. `tenjin help integration` and `tenjin help scripting` walk through the commands, step by step.

### The steps of setup

1. **Your project.** What `project detect` reports, and which Tenjin features to integrate. App opens are always included. Purchases, subscriptions, ad revenue, consent, deep links, SKAdNetwork conversion values, custom events and the customer user ID are offered, and the ones the project shows a reason for are suggested. Move through the list with the arrow keys, tick or untick with space, and press Enter. `--features` chooses without asking.

   Only you know which events matter and which value identifies a user, so for those two features `setup` asks, and shows what the app already gives its other analytics SDKs (Firebase Analytics, Amplitude, Mixpanel, AppsFlyer, GameAnalytics, Facebook App Events, Flurry; Adjust's event tokens too, which are not names): Enter takes those. `--events <name,name>` and `--user-id <value>` answer without asking, and choose their features (at a terminal, they start ticked); the value can be code, such as `user.id`, or words, such as "the account ID from the login response". Without a terminal and without the flags, `setup` takes what the app sends already. Where nothing was named or found, none are added: a coding agent doing the rest of the integration proposes events or a user ID in its report, and is never left to invent them. None is started only to propose. Each event named stays to do until the app sends it to Tenjin by that name, and a user ID named as code until it is what the app passes Tenjin, even where the app already sends Tenjin others.
2. **Sign in**, through the browser, if there is no session.
3. **Tenjin apps.** Finds the app for each build, or creates it after asking (`--yes` to skip the question), named after the project as its own files name it (Gradle's `rootProject.name`, the Xcode project, a Flutter app's label or display name or else `pubspec.yaml`, Expo's app config or `app.json`, the Capacitor `appName`, Unity's product name) and the platform.
4. **SDK keys.** Fetches each app's key from Tenjin, or says where to copy it in the dashboard, and checks it at once, so a wrong key, or the other platform's, is caught before a build.
5. **Add the SDK**, with your coding agent or by hand: `setup` lists each problem left, with its file, then offers the agent or says what to add and where.
6. **Check.** Runs `doctor`, and again after each change, until it is clean.
7. **Verify a launch.** Asks you to launch a debug build, reads the device's log, and ends when Tenjin has processed the install. On a simulator or emulator it ends when Tenjin has accepted the install, since their installs are left out by design. Without `adb` or Xcode, it says how to check later.

### Checking a project

`tenjin doctor` checks native Android, native iOS, Flutter, React Native, Capacitor and Unity projects:

- the SDK dependency, and whether it is an old release;
- the initialize and connect calls, and the features you chose (purchases, ad revenue per ad network, and so on);
- Android permissions, Play Services libraries and ProGuard rules;
- the iOS tracking and SKAdNetwork `Info.plist` keys;
- for Unity, the External Dependency Manager, the `TenjinObject` component and SKAdNetwork postbacks;
- whether Tenjin accepts each SDK key for its build's bundle ID.

Every problem comes with a fix that names the file, the line and the version. The key check sends one request to Tenjin and no event; `--offline` skips it, along with the once-a-day lookup of the newest SDK releases. `--strict` also fails on warnings.

`doctor` does not edit the project itself. At a terminal, when Claude Code or Codex is installed and there is something to fix in code, it offers to have your agent fix it (no by default), then checks again. `--agent claude` or `--agent codex` fixes without asking; `--agent none` never offers. The agent works under the same limits as in `setup`, never sees an SDK key, and leaves a key already in the project as it is. Where it writes a placeholder for a key, `doctor` writes the key given with `--sdk-key` (or the environment) in its place once Tenjin accepts it for that build; a key given without a build only for a platform with one build, and none with `--offline`. `--events` and `--user-id` work as in `setup`, and choose their features when `--features` is not given; `doctor` then says which of the events named the app does not send to Tenjin.

### Verifying a launch

```sh
tenjin doctor --device            # a connected Android device or emulator, an iPhone on a cable, or a running iOS simulator
tenjin doctor --logs logcat.txt   # a saved log; - reads standard input
```

Launch a debug build first. `doctor` reports what Tenjin answered to the SDK, why a request was refused, whether the build that ran has the application ID the project declares, and whether Tenjin has processed the install. Other apps on the device with the Tenjin SDK log the same way; their requests are left out. `--serial` picks a device when several are connected.

On iOS, the SDK logs its requests only when the build calls `TenjinSDK.debugLogs()`; `doctor` checks that debug builds do and release builds don't. It also warns when release builds would log: from Unity's `DebugLogs()` outside a development build or the TenjinObject's Debug Logs box, and from a native call in a Flutter, React Native or Capacitor app's `ios/` folder. The Android SDK has no such switch. Tenjin accepts a simulator's requests but leaves its installs out of attribution by design: the SDK marks them, and Android emulators it recognizes too. A physical device is what confirms the install.

### Builds and SDK keys

A Tenjin app is one bundle ID on one platform, with its own SDK key. A project for Android and iOS has two. An Android build for another store, such as the Amazon Appstore, is an app of its own too, even with the same bundle ID, and so is a product flavor with its own application ID.

`project detect` lists these builds, each with an ID (`android`, `ios`, `android:<variant>`). `setup` and `doctor` check each build's key against its own app, so the Google Play key in the Amazon build is caught. A key can be passed instead of pasted:

```sh
tenjin doctor --sdk-key <key>                              # one build
tenjin doctor --sdk-key android=<key> --sdk-key ios=<key>  # one per build
tenjin doctor --sdk-key android:amazon=<key> --build android:amazon
```

Once the key is in the source, `doctor` reads it from there.

### Integrating with a coding agent

At the "Add the SDK" step, `setup` looks for Claude Code and Codex, and asks:

```text
Integrate the Tenjin SDK using Claude Code? [Y/n]
```

On yes, it starts the agent in your project, under your own account, with the open findings as its task. The run uses tokens from that account and edits your files, so commit or stash your work first. Each agent is confined as tightly as it allows:

| Agent | Confined to | Checks its work with |
| --- | --- | --- |
| Claude Code | Reading and editing files in the project, and running `tenjin doctor` and `tenjin sdk guide`. No other command, and no settings or hooks from the project or your own configuration | `tenjin doctor`, with `--offline` when the run that started it has it |
| Codex | Its sandbox: changes to files in this project only (not even `/tmp`), and no network. Your MCP servers, connectors and plugins are turned off for the run, and Codex is not started if one stays on. It can still read files elsewhere on your machine, so it is neither offered nor started where your Tenjin sign-in is kept in a file rather than a system keychain, or when an SDK key or `TENJIN_API_TOKEN` was given to that run (`setup`, `doctor` and `sdk upgrade` alike) | `tenjin doctor --offline` |

`setup` then lists the files that changed and runs the checks itself; the agent's word is not taken for it. It prints the command that reopens the conversation, so you can ask the agent why something changed.

**The agent never sees an SDK key.** It writes a placeholder (`__TENJIN_ANDROID_SDK_KEY__`, `__TENJIN_IOS_SDK_KEY__`) where the key belongs, and `setup` writes the key over it afterwards.

`--agent claude`, `--agent codex` or `--agent none` skips the question. Without a terminal, an agent starts only when `--agent` names one, and never when `tenjin` is itself run by an agent.

### Teaching your agent the CLI

An agent you already use can drive the CLI itself, once it knows how:

```sh
tenjin agent install     # lists the files it would write, and asks
tenjin agent status
tenjin agent uninstall
```

For Claude Code, this writes a skill to `.claude/skills/tenjin-cli/` in the project (`--global` for your home folder); `uninstall` removes that file and its `tenjin-cli` folder, and leaves the folders above it, such as `.claude`, as they are. For Codex, Cursor and other agents that read `AGENTS.md`, it adds a marked section to that file, which `uninstall` takes out again. `tenjin agent skill` prints the instructions for anything else. They describe the commands of the installed version, so run `tenjin agent install` again after updating.

## Scripts, CI and coding agents

Every command except `setup` is built to be driven by a program:

- `--json` (or `-o json`) writes only the result to stdout; everything else goes to stderr.
- A command never prompts when stdin is not a terminal. Anything that creates something needs `--yes` or a flag.
- A failure with `--json` is written to stderr as `{"error": {"code", "message", "hint"}}`.
- The JSON of `project detect` and `doctor` is stable: fields are added, never renamed or removed.
- The `apps` commands print the CLI's own JSON, which leaves out the shared secrets the API returns with an app.
- `TENJIN_API_TOKEN`, an access token from the dashboard, signs in without a browser.

| Exit code | Meaning |
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
| 11 | Tenjin refused the request, such as a value it does not accept; its message says why |
| 12 | Another `tenjin` command was using the sign-in for too long, and this one stopped before changing anything: run it again once that one has finished. A command that had already changed something exits 7 instead, and says what it changed |
| 130 | Stopped: Ctrl-C, or SIGTERM or SIGHUP (a closed terminal) |

`--verbose` (`-v`) writes a line to stderr for each request to Tenjin and each program run, with the address, the answer and the time taken. It never shows a credential or a request body, so it is safe to paste into a support request. Requests that fail in transit are retried up to twice when that is safe. When Tenjin refuses a sign-in that looked current, the CLI renews it and sends the request once more.

## Configuration

### Environment variables

| Variable | Use |
| --- | --- |
| `TENJIN_API_TOKEN` | An access token from the dashboard, used instead of a browser sign-in |
| `TENJIN_SDK_KEY` | The SDK key, when the project does not contain it |
| `TENJIN_SDK_KEY_ANDROID`, `TENJIN_SDK_KEY_IOS` | The same, per platform |
| `TENJIN_SDK_KEY_ANDROID_<VARIANT>` | The same, for another build, such as `TENJIN_SDK_KEY_ANDROID_AMAZON` |
| `TENJIN_NO_UPDATE_CHECK` | `1` turns off the new-release notice |
| `TENJIN_NO_TELEMETRY`, `DO_NOT_TRACK` | `1` turns off usage statistics (see [Privacy and security](#privacy-and-security)) |
| `TENJIN_DASHBOARD_URL`, `TENJIN_API_URL`, `TENJIN_TRACK_URL`, `TENJIN_RESOURCE_URL`, `TENJIN_SITE_IDS_URL`, `TENJIN_DOCS_URL` | Point at another copy of a Tenjin service. Each must be `https`, except on this machine |
| `NO_COLOR` | Turn off color |
| `CLICOLOR_FORCE` | Keep color when output is piped, for example into `less -R` |

### Files

The CLI keeps a few small files in a `tenjin` folder in your user configuration directory:

| System | Folder |
| --- | --- |
| macOS | `~/Library/Application Support/tenjin` |
| Linux | `$XDG_CONFIG_HOME/tenjin`, or `~/.config/tenjin` |
| Windows | `%AppData%\tenjin` |

It holds the time of the last update check and the newest SDK releases it found, whether you turned the usage statistics off and when one last failed to send (no identifier), and a lock file that keeps two `tenjin` commands from renewing the sign-in at once. Your session is kept in the system keychain (macOS Keychain, Windows Credential Manager, the Secret Service on Linux), or in this folder, readable only by you, where there is no keychain.

## Privacy and security

- **Usage statistics.** After each command, a release of the CLI sends Tenjin one event, through Amplitude: the command, the names of the flags given (never their values), its exit code, error kind and duration, whether a question was answered no, the CLI's version, the operating system and architecture, whether you were at a terminal, and which coding agent started it, if one did. Commands that read a project add its framework, platforms, ad SDKs, whether it has the Tenjin SDK, and the IDs of the checks that failed or warned; `setup` adds the last step reached, the coding agent it ran, and how the launch check ended. Never a path, a bundle ID, an app or event name, a key, a token, or anything from your code. No identifier is kept on this machine: each event carries a random ID of its own, made for it and never stored, so no two events can be tied together, to this machine, or to your Tenjin account. Amplitude, like any server, sees the IP address the event comes from. Nothing is sent in CI, against another copy of Tenjin, with `--offline`, with `TENJIN_NO_TELEMETRY=1` or `DO_NOT_TRACK=1`, or where there is no configuration folder (see [Files](#files)) to keep `tenjin telemetry off` in. `tenjin telemetry off` stops it on this machine for good; `tenjin telemetry status` says whether events are sent, and if not, why. `--verbose` shows the request. Tenjin's API, like any server, also records the requests it gets, with the CLI's version in the User-Agent.
- **What it contacts:** Tenjin's dashboard, API and tracking service (where the SDK sends) for the commands you run; Amplitude, for the usage statistics; Tenjin's documentation site for `docs search`; GitHub, once a day, for the newest release of the CLI; and, once a day for `doctor`, the public registries of the Tenjin SDKs (Maven Central, pub.dev, npm, GitHub). `--offline` keeps `doctor` from contacting Tenjin, the registries and Amplitude.
- **Credentials and SDK keys are never printed.** Keys are shown by their last four characters, in tables, JSON, `--verbose` and an agent's report.
- **Downloads can be verified.** Each release's archives carry build provenance, signed by GitHub: `gh attestation verify tenjin_<version>_<os>_<arch>.tar.gz --repo tenjin/tenjin-cli --signer-workflow tenjin/tenjin-cli/.github/workflows/release.yaml` shows an archive was built by this repository's release workflow, and names the commit. The install scripts and Homebrew check each archive against the `checksums.txt` of the same release, so they catch a damaged download, not a release replaced as a whole.
- **It changes your project only to write an SDK key** where a placeholder was left for it. Code changes are yours or your agent's.

Report a vulnerability privately: see [SECURITY.md](SECURITY.md).

## Troubleshooting

- **Anything that talks to Tenjin:** run it again with `--verbose` to see each request and its answer.
- **macOS says the binary cannot be opened** after you downloaded an archive in a browser: run `xattr -d com.apple.quarantine /path/to/tenjin`. Homebrew and `install.sh` do not need this.
- **Windows PowerShell reports a TLS error** when installing: run `[Net.ServicePointManager]::SecurityProtocol = 'Tls12'` first.
- **`tenjin login` on a remote machine** (SSH): use `tenjin login --no-browser`, open the address it prints in your local browser, and paste back the address you land on.
- **`doctor --device` finds no device:** check that `adb devices` lists it, or that the simulator is booted, and pass `--serial` when there are several.
- **The SDK skipped its connect:** it sends at most one every 30 seconds, also across launches. Wait 30 seconds, launch the app again, and check again.

Questions about your Tenjin account or integration: [support@tenjin.com](mailto:support@tenjin.com). Bugs and ideas: [open an issue](https://github.com/tenjin/tenjin-cli/issues/new/choose).

## Contributing

Bug reports and ideas are welcome as [issues](https://github.com/tenjin/tenjin-cli/issues); we don't take pull requests from outside Tenjin yet. [CONTRIBUTING.md](CONTRIBUTING.md) covers building and testing the CLI yourself. Each release's changes are on the [releases page](https://github.com/tenjin/tenjin-cli/releases), and everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).

```sh
make build   # bin/tenjin
make test
make lint
```

## License

[MIT](LICENSE). The SDK integration guides embedded under `internal/guides/data` come from [tenjin/sdk-llm-guides](https://github.com/tenjin/sdk-llm-guides), under the same license. Release archives include the licenses of the software compiled into the binary, in `THIRD_PARTY_NOTICES.md`.
