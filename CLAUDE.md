# Tenjin CLI

A command-line tool for the parts of Tenjin SDK onboarding that happen on the developer's machine: the app and its SDK key, test devices, live events, and diagnosing an integration that sends nothing. Driven by a person (`tenjin setup`) or by a coding agent (the individual commands with `--json`).

This repository is meant to be public. Keep internal details (private repos, internal hosts, ticket links, customer data) out of anything committed. Internal notes live in `CLAUDE.local.md` and the gitignored files under `docs/` and `demo/`.

## Commands

```
make build      # bin/tenjin, with version stamped in
make test       # go test -race ./...
make lint       # gofmt check + golangci-lint
make release-snapshot   # GoReleaser, locally: every archive and the cask into dist/, nothing published
go test -tags keychain ./internal/session   # opt-in: writes to the real system keychain
go test -tags realagent -run RealClaudeCode -v ./internal/cli   # opt-in: starts the real Claude Code (setup, then the skill); needs `make build`
go test -tags realagent -run RealCodex -v ./internal/cli        # opt-in: starts the real Codex
go test ./internal/cli -run JSONOutputIsStable -update          # accept a deliberate change to the golden JSON
```

Go 1.27 (see `go.mod`).

## Layout

```
cmd/tenjin            entry point; only calls cli.Execute
internal/cli          command tree; root.go holds shared flags and the environment
internal/project      detection: framework, platforms, bundle IDs. Reads files only, no network
internal/verify       doctor checks; source.go holds the per-framework call patterns
internal/agent        starts an installed coding agent on a task and follows its output; one driver each for Claude Code and Codex
internal/guides       the SDK integration guides from sdk-llm-guides, embedded at a pinned revision (scripts/sync-guides.sh)
internal/skill        tenjin-cli.md, the instructions for agents (embedded), and the installer behind `tenjin agent`
internal/keyfill      finds and fills the SDK key placeholders agents leave
internal/devicelog    parses the SDK's log lines (Android logcat, iOS log and console); reads them through adb, simctl, or devicectl on an iPhone
internal/track        the SDK key check and the device lookup against Tenjin's tracking service (track.tenjin.com)
internal/transport    every HTTP client's transport: retries where safe, and the --verbose log
internal/tenjinapi    API operations (apps); destined for the shared module
internal/oauth        browser sign-in: PKCE on a loopback port, then token exchange
internal/session      credential store (keychain, file fallback) and token refresh
internal/config       service URLs per environment
internal/render       table and JSON output, terminal colour (a muted palette, 24-bit where the terminal says it can)
internal/update       the once-a-day check for a newer release, told on stderr to a person at a terminal
internal/clierr       error kinds and their exit codes
internal/testproject  builds sample projects on disk for tests
docs/adr              architecture decisions
```

## Rules

- **Commands are thin.** Parse flags, call one operation, render. No HTTP in `internal/cli`.
- **One output contract.** Every command honours `--json` / `-o`, writes only the result to stdout, and sends everything else to stderr. `setup` is the exception: it is a guided flow for people and refuses `--json`.
- **Colour and the wordmark are for a terminal only.** Style through `render.Styler`, which is off for pipes, files and `NO_COLOR`; never write an escape code directly. Its palette is muted mid-tones that read on light and dark backgrounds; add a colour there, not at a call site. Styling may add to the text, never change it: piped output must stay what it was. JSON is never styled.
- **The update notice is for people.** It goes to stderr after the command, at most once a day, and never with `--json`, in CI, or when stderr is not a terminal (`TENJIN_NO_UPDATE_CHECK=1` turns it off). Its request goes through `internal/transport` like any other.
- **Never prompt when `env.interactive` is false.** Every prompt needs a flag. Creating anything needs `--yes` or a yes at the prompt.
- **Errors are `*clierr.Error`** with a kind that fixes the exit code. Add kinds; never renumber.
- **The CLI does not edit the user's project**, except to write an SDK key over its own placeholder (`internal/keyfill`). `setup` may start the user's own coding agent to do it, only after a yes or `--agent`, and never from inside another agent. Each agent is confined by the strongest means it has: Claude Code by an allow-list (file edits, `tenjin doctor` and `tenjin sdk guide`), Codex by its sandbox (project files only, no network, so it runs the doctor with `--offline`). Never pass a flag that bypasses an agent's sandbox or approvals. The task goes in on stdin; `setup` re-runs the checks itself afterwards. The conversation is kept by the agent, and `setup` prints the command to resume it after a run that finished.
- **SDK keys go through `internal/cli/keys.go`.** A key is already in the project, passed in (`--sdk-key`, per platform), fetched from the API (`keyFromTenjin`, the first key Tenjin accepts), or copied from the dashboard by the developer following `keyInstructions`, in that order. Where the API does not serve keys (`tenjinapi.ErrKeysNotServed`), say nothing about it and fall back to the dashboard. Check a key against Tenjin as soon as it is known. A key written in the project wins over one passed in when `setup` checks, because it is what the app sends.
- **A Tenjin app is a build, not a platform.** `project.Build` is one bundle ID and Tenjin platform (`android`, `amazon`, `android_other`, `ios`); an Android module's flavors can make several. Keys, placeholders, apps and key checks are per build ID (`android`, `ios`, `android:<variant>`), and a key for one build never stands in for another's. A single-build project must read exactly as before.
- **Features are chosen, app opens are not.** `project.Feature` lists what an integration can include, suggested from files only (dependencies, a few distinctive APIs in code, manifests). Detection never asks an agent. `verify.Options.Features` is the choice: unsent and chosen is a warning, unsent and suggested is `info`, neither is silence. A generic call name (`transaction`) counts only on a Tenjin receiver.
- **SDK keys never go to an agent.** Not in its task, its arguments or its environment (`TENJIN_SDK_KEY*` and `TENJIN_API_TOKEN` are removed from it). The agent writes its build's `SDKKeyPlaceholder`; `setup` fills it after the agent finishes (`project set-keys` is the scripted form), and `doctor` reports a placeholder left as `sdk.key.unfilled`. The skill says the same.
- **`setup` ends on a real launch.** Its last step reads the device and waits, a bounded number of polls through `env.wait`, for Tenjin to process the install.
- **What is said about an agent must be what is enforced.** The sentence shown before the yes comes from the driver (`limits`). If a limit is only an instruction to the agent, say "it is told not to", not "it cannot".
- **All HTTP goes through `internal/transport`** (`env.transport()`). It retries a request only when sending it again cannot do anything twice: GET, or a request whose context is marked `transport.Retryable`. Never mark a request that creates or changes something, or an OAuth token request (refresh tokens rotate). The `--verbose` log is method, address, status and time; never add headers or bodies to it.
- **Never print a credential or a whole SDK key.** Findings show the last four characters (`mask`), and an agent's report is masked the same way before it is shown. The API client decodes only the app attributes it shows, because the API returns shared secrets alongside them.
- **A check that cannot tell must not say pass.** Use an `info` finding.
- **A fix must be usable as written.** Name the file, the line and the version; never "the latest". The SDK versions fixes name live in `internal/verify/versions.go` and are updated with each release.
- **No dead flags or code.** Add a flag in the change that gives it behaviour.
- **The skill describes this binary.** `internal/skill/tenjin-cli.md` is what agents are told. When a command or flag it mentions changes, change it too; `TestTheSkillOnlyNamesCommandsThatExist` fails if it names something that is not there.
- **JSON output is a contract.** `project detect --json` and `doctor --json` are pinned by golden files in `internal/cli/testdata`. Add fields; do not rename or remove them.
- **`agent install` writes only what it lists.** It owns one file (the Claude Code skill) and one marked section (in `AGENTS.md`), and removes exactly those.

## Releases

- `.goreleaser.yaml` builds six targets (macOS, Linux and Windows, each on amd64 and arm64), archives named `tenjin_<version>_<os>_<arch>`, `checksums.txt`, and the Homebrew cask. `install.sh` and `install.ps1` build the same archive names; change all three together. CI builds a snapshot on every pull request and installs it with both scripts and the cask, on each OS.
- The cask removes macOS's quarantine mark after install, because the binaries are not notarized yet. Drop that hook once they are.
- A local snapshot shows `commit none` and a `v0.0.0` download address while the repository has no remote and no tag. A tagged release in CI fills both in.

## Tests

- Tests go through `execute` with a `harness` (`internal/cli/fake_test.go`) and assert on exit code, stdout and stderr separately.
- `fakeTenjin` stands in for the authorization server, the API and Tenjin, and answers as they do, quirks included (see `fake_test.go`).
- Project fixtures come from `internal/testproject`: start from a correct integration and change one thing.
- `internal/devicelog/testdata/android-accepted.log` and `ios-accepted.log` are real captures with every identifier, receipt, signature, key and location replaced. Scrub any new capture the same way before committing it.
- A booted iOS simulator is faked with `harness.bootSimulator(log)`, which stands in for `xcrun simctl`, and a connected iPhone with `harness.connectIPhone(console)`, for `xcrun devicectl`. `ios-device-accepted.log` is a real iPhone capture, scrubbed.
- A device is faked with `harness.connectDevice(log)`, which stands in for `adb`.
- A real-agent test must call `isolate(t, h)`. It points every `tenjin` an agent starts at the fake, through `TENJIN_*_URL`, so an agent can never act on production or on the session of whoever runs the test.

Scope and reasoning: `docs/adr/0001-language-repository-and-scope.md`.
