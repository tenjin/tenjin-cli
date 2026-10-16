# Tenjin CLI

[![Test](https://github.com/tenjin/tenjin-cli/actions/workflows/test.yaml/badge.svg?branch=main)](https://github.com/tenjin/tenjin-cli/actions/workflows/test.yaml)
[![Lint](https://github.com/tenjin/tenjin-cli/actions/workflows/linter.yaml/badge.svg?branch=main)](https://github.com/tenjin/tenjin-cli/actions/workflows/linter.yaml)
[![Docs](https://github.com/tenjin/tenjin-cli/actions/workflows/docs.yaml/badge.svg?branch=main)](https://github.com/tenjin/tenjin-cli/actions/workflows/docs.yaml)
[![Release](https://img.shields.io/github/v/release/tenjin/tenjin-cli?sort=semver)](https://github.com/tenjin/tenjin-cli/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Integrate the [Tenjin](https://tenjin.com) SDK, and work with your Tenjin account, from the terminal.

| You are | Start with |
| --- | --- |
| An app developer adding the Tenjin SDK to an app | `tenjin setup` in the project: see [Quick start](#quick-start) |
| Anyone with a Tenjin account, UA teams included, or a script working for them | `tenjin login`, then `tenjin campaigns list` or `tenjin reports spend`: see [Your Tenjin data](#your-tenjin-data) |

For app developers, the Tenjin CLI covers the parts of SDK onboarding that happen on your machine:

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

Archives for macOS, Linux and Windows (Intel and ARM) are also on the [releases page](https://github.com/tenjin/tenjin-cli/releases), with a `checksums.txt`. Both install scripts check each download against it, and neither needs sudo or administrator rights.

<details>
<summary>What the install scripts do, and how to change it</summary>

| Variable | Effect |
| --- | --- |
| `TENJIN_VERSION` | Install this version (for example `v0.1.0`) instead of the latest |
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

- A Tenjin account, for anything that signs in: apps and SDK keys. Detecting and checking a project work without one.
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

For app developers, in the app's project.

| Command | What it does |
| --- | --- |
| `tenjin setup [path]` | The guided path, from sign-in to a launch Tenjin has processed |
| `tenjin project detect [path]` | Framework, platforms, bundle IDs, where the app starts, ad SDKs, any Tenjin SDK already declared |
| `tenjin doctor [path]` | Check the integration on disk, and each SDK key against Tenjin |
| `tenjin doctor --device` | Also read a connected device: what the SDK sent and what Tenjin answered |
| `tenjin project set-keys [path]` | Write SDK keys where a coding agent left placeholders |
| `tenjin sdk guide [platform]` | The SDK integration guide, as tested with this version; works offline |
| `tenjin agent install [path]` | Teach your coding agent to use this CLI |

Two of setup's steps are under [Your Tenjin data](#your-tenjin-data): `tenjin apps ensure` finds or creates the app, and `tenjin apps keys list` reads its SDK keys.

#### Your Tenjin data

For anyone with a Tenjin account, from any directory: none of these reads a project. Each reads one thing from the [Tenjin API](https://tenjin.com/docs/automation-apis/), with its parameters as flags. At a terminal it prints a table; with `--json`, the API's answer as it is, so the API's documentation describes it. Lists show one page of 25: `--page` and `--per-page` (up to 1000) pick another, `--all` reads them all. Reports show every row unless a page is asked for; `apps list` always shows every app.

| Command | What it does |
| --- | --- |
| `tenjin apps list` | The apps in your organization; `--platform` and `--bundle-id` narrow it |
| `tenjin apps get <id>` | One app, and whether Tenjin is receiving its events |
| `tenjin apps create` | Create an app from `--name`, `--bundle-id` and `--platform`, or from `--store-url` |
| `tenjin apps ensure --bundle-id <id> --platform <platform>` | Find the app, or create it. Safe to repeat |
| `tenjin apps keys list --app <id>` | The app's SDK keys, by their last four characters |
| `tenjin apps events list --app <id>` | The events the app has sent |
| `tenjin apps callbacks list --app <id>`, `get <id>` | The app's callbacks to marketing channels; `--channel` narrows the list |
| `tenjin apps s2s-callbacks list --app <id>`, `get <id>` | The app's server-to-server callbacks |
| `tenjin apps callback-templates list --app <id>`, `get <id>` | The callback templates the app can use |
| `tenjin apps callback-groups list --app <id> --channel <id>`, `get <id>` | The app's callback groups for one channel |
| `tenjin apps callback-settings get --app <id> --channel <id>` | The app's callback settings for one channel |
| `tenjin campaigns list`, `get <id>` | Campaigns and their tracking links; `--app`, `--channel`, `--platform` and `--query` narrow the list |
| `tenjin channels list`, `get <id>` | Marketing channels, by ID |
| `tenjin channels callback-group-templates <id>` | A channel's callback group templates |
| `tenjin ad-accounts list`, `get <id>` | The ad network accounts Tenjin reads spend from |
| `tenjin skadnetworks list`, `get <id>` | SKAdNetwork IDs |
| `tenjin reports spend --from <date> --to <date>` | User acquisition: spend, installs and what they returned |
| `tenjin reports ad-revenue --from <date> --to <date>` | Ad revenue, by ad network |
| `tenjin reports skadnetwork --from <date> --to <date>`, `skan` | SKAdNetwork postbacks, as Tenjin attributes them, or as Apple sends them |
| `tenjin saved-reports list`, `get <id>`, `run <id>` | Reports saved in the dashboard, and their data |
| `tenjin reports metrics [words]` | The metrics each report can show, for `--metrics`; `roi_7d` finds `roi_Nd` |
| `tenjin apps s2s-callbacks macros [words]` | The macros a callback URL can use, such as `{{country}}` |
| `tenjin apps callback-groups inspect --app <id> --channel <id>` | An app's callbacks to one channel in one view: templates, groups with the settings each still lacks, shared settings, events |
| `tenjin site-ids list` | Site ID Optimization: the site IDs whose installs are not attributed |
| `tenjin docs search <words>` | Tenjin's documentation, with links |

Changes are commands too. Each sends only the fields given as flags, asks before it changes anything (deletes default to no), and needs `--yes` when nothing can ask. `--dry-run` prints the request body and sends nothing.

| Command | What it does |
| --- | --- |
| `tenjin apps update <id>`, `delete <id>` | Change an app's name or settings; delete an app |
| `tenjin campaigns create --app <id> --name <name> --channel <id>` | Create a campaign and its tracking links; `--ad-account` instead of `--channel` |
| `tenjin campaigns update <id> --attribution-window <seconds>`, `delete <id>` | Change a campaign's attribution window; delete one that has not tracked installs |
| `tenjin apps callbacks create --app <id> --template <id>`, `update <id>` | Callbacks from a channel's templates; `--param key=value` for its parameters |
| `tenjin apps s2s-callbacks create --app <id> --url <url> --event <event> --name <name> --action ping`, `update <id>` | Server-to-server callbacks |
| `tenjin apps callback-groups create --app <id> --channel <id> --template <id>`, `update <id>` | Callback groups; `--setting key=value` for their settings |
| `tenjin apps callback-settings update --app <id> --channel <id> --setting <key=value>` | An app's callback settings for one channel |
| `tenjin saved-reports create --type <type> --name <name> --granularity <g> --group-by <g>`, `update <id>`, `delete <id>` | Saved reports |
| `tenjin site-ids block <site-id>...`, `reinstate <site-id>...`, `clear` | Block site IDs, everywhere or for one `--app`, `--campaign` or `--channel`; `--replace` makes them the only rules |

Reports take the API's own values: `--granularity`, `--group-by`, and `--app`, `--channel` and `--metrics`, each repeated or comma-separated. `tenjin reports metrics` lists the metric names. For example:

```sh
tenjin reports spend --from 2026-10-01 --to 2026-10-07 --group-by channel,app --granularity totals-daily
tenjin campaigns list --app <app-id> --all --json | jq '.data[].attributes.name'
tenjin campaigns create --app <app-id> --name "Summer UA" --channel <channel-id> --yes
```

#### Your account

| Command | What it does |
| --- | --- |
| `tenjin login`, `tenjin logout`, `tenjin auth status` | Sign in through the browser (`--no-browser` over SSH), sign out, see who is signed in |

`tenjin version` prints the version, commit and build date.

`tenjin <command> --help` describes every flag.

### The steps of setup

1. **Your project.** What `project detect` reports, and which Tenjin features to integrate. App opens are always included. Purchases, subscriptions, ad revenue, consent, deep links, SKAdNetwork conversion values, custom events and the customer user ID are offered, and the ones the project shows a reason for are suggested. `--features` chooses without asking.
2. **Sign in**, through the browser, if there is no session.
3. **Tenjin apps.** Finds the app for each build, or creates it after asking (`--yes` to skip the question).
4. **SDK keys.** Fetches each app's key from Tenjin, or says where to copy it in the dashboard, and checks it at once, so a wrong key, or the other platform's, is caught before a build.
5. **Add the SDK**, with your coding agent or by hand: `setup` lists what is left to add and where.
6. **Check.** Runs `doctor`, and again after each change, until it is clean.
7. **Verify a launch.** Asks you to launch a debug build, reads the device's log, and ends when Tenjin has processed the install. Without `adb` or Xcode, it says how to check later.

### Checking a project

`tenjin doctor` checks native Android, native iOS, Flutter, React Native, Capacitor and Unity projects:

- the SDK dependency, and whether it is an old release;
- the initialise and connect calls, and the features you chose (purchases, ad revenue per ad network, and so on);
- Android permissions, Play Services libraries and ProGuard rules;
- the iOS tracking and SKAdNetwork `Info.plist` keys;
- for Unity, the External Dependency Manager, the `TenjinObject` component and SKAdNetwork postbacks;
- whether Tenjin accepts each SDK key for its build's bundle ID.

Every problem comes with a fix that names the file, the line and the version. The key check sends one request to Tenjin and no event; `--offline` skips it, along with the once-a-day lookup of the newest SDK releases. `--strict` also fails on warnings.

`doctor` never changes the project itself. At a terminal, when Claude Code or Codex is installed and there is something to fix in code, it offers to have your agent fix it (no by default), then checks again. `--agent claude` or `--agent codex` fixes without asking; `--agent none` never offers. The agent works under the same limits as in `setup` and never sees an SDK key.

### Verifying a launch

```sh
tenjin doctor --device            # a connected Android device or emulator, an iPhone on a cable, or a running iOS simulator
tenjin doctor --logs logcat.txt   # a saved log; - reads standard input
```

Launch a debug build first. `doctor` reports what Tenjin answered to the SDK, why a request was refused, whether the build that ran has the application ID the project declares, and whether Tenjin has processed the install. `--serial` picks a device when several are connected.

On iOS, the SDK logs its requests only when the build calls `TenjinSDK.debugLogs()`; `doctor` checks that debug builds do and release builds don't. Tenjin accepts a simulator's requests but leaves its installs out of attribution by design: the SDK marks them, and Android emulators it recognises too. A physical device is what confirms the install.

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

On yes, it starts the agent in your project, under your own account, with the open findings as its task. Each agent is confined as tightly as it allows:

| Agent | Confined to | Checks its work with |
| --- | --- | --- |
| Claude Code | Reading and editing files in the project, and running `tenjin doctor` and `tenjin sdk guide`. No other command, and no settings or hooks from the project or your own configuration | `tenjin doctor` |
| Codex | Its sandbox: files in this project only, no network | `tenjin doctor --offline` |

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

For Claude Code, this writes a skill to `.claude/skills/tenjin-cli/` in the project (`--global` for your home folder). For Codex, Cursor and other agents that read `AGENTS.md`, it adds a marked section to that file. `tenjin agent skill` prints the instructions for anything else. They describe the commands of the installed version, so run `tenjin agent install` again after updating.

## Scripts, CI and coding agents

Every command except `setup` is built to be driven by a program:

- `--json` (or `-o json`) writes only the result to stdout; everything else goes to stderr. The data commands also take `-o csv`, for a spreadsheet.
- A command never prompts when stdin is not a terminal. Anything that creates something needs `--yes` or a flag.
- A failure with `--json` is written to stderr as `{"error": {"code", "message", "hint"}}`.
- The JSON of `project detect` and `doctor` is stable: fields are added, never renamed or removed.
- The data commands (campaigns, channels, reports and the others in [Your Tenjin data](#your-tenjin-data)) print the API's answer as it is.
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
| 130 | Stopped with Ctrl-C |

`--verbose` (`-v`) writes a line to stderr for each request to Tenjin and each program run, with the address, the answer and the time taken. It never shows a credential or a request body, so it is safe to paste into a support request. Requests that fail in transit are retried up to twice when that is safe.

## Configuration

### Environment variables

| Variable | Use |
| --- | --- |
| `TENJIN_API_TOKEN` | An access token from the dashboard, used instead of a browser sign-in |
| `TENJIN_SDK_KEY` | The SDK key, when the project does not contain it |
| `TENJIN_SDK_KEY_ANDROID`, `TENJIN_SDK_KEY_IOS` | The same, per platform |
| `TENJIN_SDK_KEY_ANDROID_<VARIANT>` | The same, for another build, such as `TENJIN_SDK_KEY_ANDROID_AMAZON` |
| `TENJIN_NO_UPDATE_CHECK` | `1` turns off the new-release notice |
| `TENJIN_DASHBOARD_URL`, `TENJIN_API_URL`, `TENJIN_COLLECTOR_URL`, `TENJIN_RESOURCE_URL`, `TENJIN_SITE_IDS_URL`, `TENJIN_DOCS_URL` | Point at another copy of a Tenjin service. Each must be `https`, except on this machine |
| `NO_COLOR` | Turn off colour |
| `CLICOLOR_FORCE` | Keep colour when output is piped, for example into `less -R` |

### Files

The CLI keeps a few small files in a `tenjin` folder in your user configuration directory:

| System | Folder |
| --- | --- |
| macOS | `~/Library/Application Support/tenjin` |
| Linux | `$XDG_CONFIG_HOME/tenjin`, or `~/.config/tenjin` |
| Windows | `%AppData%\tenjin` |

It holds the time of the last update check and the newest SDK releases it found. Your session is kept in the system keychain (macOS Keychain, Windows Credential Manager, the Secret Service on Linux), or in this folder, readable only by you, where there is no keychain.

## Privacy and security

- **No analytics of its own.** The CLI sends no telemetry. Tenjin's API, like any server, records the requests it gets, with the CLI's version in the User-Agent.
- **What it contacts:** Tenjin's dashboard, API, collector and Site ID Optimization service for the commands you run; Tenjin's documentation site for `docs search`; GitHub, once a day, for the newest release of the CLI; and, once a day for `doctor`, the public registries of the Tenjin SDKs (Maven Central, pub.dev, npm, GitHub). `--offline` keeps `doctor` from contacting Tenjin and the registries.
- **Credentials and SDK keys are never printed.** Keys are shown by their last four characters, in tables, JSON, `--verbose` and an agent's report. The data commands show what the dashboard shows you, callback URLs and parameters included.
- **Downloads can be verified.** Each release's archives carry build provenance: `gh attestation verify tenjin_<version>_<os>_<arch>.tar.gz --repo tenjin/tenjin-cli` shows they were built by this repository's release workflow, from the tagged commit. The install scripts and Homebrew check each archive against the release's checksums.
- **It changes your project only to write an SDK key** where a placeholder was left for it. Code changes are yours or your agent's.

Report a vulnerability privately: see [SECURITY.md](SECURITY.md).

## Troubleshooting

- **Anything that talks to Tenjin:** run it again with `--verbose` to see each request and its answer.
- **macOS says the binary cannot be opened** after you downloaded an archive in a browser: run `xattr -d com.apple.quarantine /path/to/tenjin`. Homebrew and `install.sh` do not need this.
- **Windows PowerShell reports a TLS error** when installing: run `[Net.ServicePointManager]::SecurityProtocol = 'Tls12'` first.
- **`tenjin login` on a remote machine** (SSH): use `tenjin login --no-browser`, open the address it prints in your local browser, and paste back the address you land on.
- **`doctor --device` finds no device:** check that `adb devices` lists it, or that the simulator is booted, and pass `--serial` when there are several.

Questions about your Tenjin account or integration: [support@tenjin.com](mailto:support@tenjin.com). Bugs and ideas: [open an issue](https://github.com/tenjin/tenjin-cli/issues/new/choose).

## Contributing

Contributions are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) covers building, testing and the project's conventions. Each release's changes are on the [releases page](https://github.com/tenjin/tenjin-cli/releases), and everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).

```sh
make build   # bin/tenjin
make test
make lint
```

## License

[MIT](LICENSE). The SDK integration guides embedded under `internal/guides/data` come from [tenjin/sdk-llm-guides](https://github.com/tenjin/sdk-llm-guides), under the same license. Release archives include the licenses of the software compiled into the binary, in `THIRD_PARTY_NOTICES.md`.
