# Tenjin CLI

A command-line tool for the parts of Tenjin SDK onboarding that happen on the developer's machine: the app and its SDK key, a launch on a test device, and diagnosing an integration that sends nothing. Driven by a person (`tenjin setup`) or by a coding agent (the individual commands with `--json`).

This repository is meant to be public. Keep internal details (private repos, internal hosts, ticket links, customer data) out of anything committed. Internal notes live in `CLAUDE.local.md` and the gitignored files under `docs/` and `demo/`.

## Commands

```sh
make build      # bin/tenjin, with version stamped in
make test       # go test -race ./...
make lint       # gofmt check + golangci-lint
make generate   # the API clients, from internal/tenjinapi/openapi (oapi-codegen v2.7.1)
make release-snapshot   # GoReleaser, locally: every archive (with THIRD_PARTY_NOTICES.md) and the cask into dist/, nothing published
go test -tags keychain ./internal/session   # opt-in: writes to the real system keychain
go test -tags realagent -run RealClaudeCode -v ./internal/cli   # opt-in: starts the real Claude Code (setup, then the skill); needs `make build`
go test -tags realagent -run RealCodex -v ./internal/cli        # opt-in: starts the real Codex
go test ./internal/cli -run JSONOutputIsStable -update          # accept a deliberate change to the golden JSON
TENJIN_API_TOKEN=<token> go test -tags liveapi -run Live -v ./internal/tenjinapi   # opt-in: every API operation against a real API; reads only unless TENJIN_LIVE_WRITE=1, which creates, changes and deletes a throwaway app's data
```

Go 1.27 (see `go.mod`).

## Layout

```text
cmd/tenjin            entry point; only calls cli.Execute
internal/cli          command tree; root.go holds shared flags and the environment
internal/project      detection: framework, platforms, bundle IDs. Reads files only, no network
internal/verify       doctor checks; source.go holds the per-framework call patterns
internal/proc         starts programs in their own process group, so Ctrl-C reaches them
internal/agent        starts an installed coding agent on a task and follows its output; one driver each for Claude Code and Codex
internal/guides       the SDK integration guides from sdk-llm-guides, embedded at a pinned revision (scripts/sync-guides.sh)
internal/skill        tenjin-cli.md, the instructions for agents (embedded), and the installer behind `tenjin agent`
internal/keyfill      finds and fills the SDK key placeholders agents leave
internal/devicelog    parses the SDK's log lines (Android logcat, iOS log and console); reads them through adb, simctl, or devicectl on an iPhone
internal/track        the SDK key check and the device lookup against Tenjin's tracking service (track.tenjin.com)
internal/transport    every HTTP client's transport: retries where safe, and the --verbose log
internal/tenjinapi    API operations (apps, SDK keys) over the clients generated from the API's specs (openapi/, cma/, rma/: the same specs and edits as the MCP server's); the seed of the shared module
internal/oauth        browser sign-in: PKCE on a loopback port, then token exchange
internal/session      credential store (keychain, file fallback) and token refresh
internal/config       service URLs per environment
internal/render       table and JSON output, terminal colour (a muted palette, 24-bit where the terminal says it can)
internal/sdkrelease   the newest release of each SDK, from Maven Central, pub.dev, npm and GitHub, once a day
internal/update       the once-a-day check for a newer release, told on stderr to a person at a terminal
internal/buildinfo    version, commit and date, stamped in by GoReleaser and make build
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
- **The CLI does not edit the user's project**, except to write an SDK key over its own placeholder (`internal/keyfill`). `setup` may start the user's own coding agent to do it, and so may `doctor` (its question defaults to no, since doctor is first a check), only after a yes or `--agent`, and never from inside another agent. Each agent is confined by the strongest means it has: Claude Code by `--restricted` (no settings or hooks from the project or the user, file tools kept in the project; a version without it is refused) and an allow-list (file edits, `tenjin doctor` and `tenjin sdk guide`), Codex by its sandbox, pinned to no network and no extra folders (project files only, so it runs the doctor with `--offline`). Keys `setup` wrote go back to their placeholders before an agent runs again. Never pass a flag that bypasses an agent's sandbox or approvals. The task goes in on stdin; `setup` re-runs the checks itself afterwards. The conversation is kept by the agent, and `setup` prints the command to resume it after a run that finished.
- **SDK keys go through `internal/cli/keys.go`.** A key is already in the project, passed in (`--sdk-key`, per platform), fetched from the API (`keyFromTenjin`, the first key Tenjin accepts), or copied from the dashboard by the developer following `keyInstructions`, in that order. Where the API does not serve keys (`tenjinapi.ErrKeysNotServed`), say nothing about it and fall back to the dashboard. Check a key against Tenjin as soon as it is known. A key written in the project wins over one passed in when `setup` checks, because it is what the app sends.
- **A Tenjin app is a build, not a platform.** `project.Build` is one bundle ID and Tenjin platform (`android`, `amazon`, `android_other`, `ios`); an Android module's flavors can make several. Keys, placeholders, apps and key checks are per build ID (`android`, `ios`, `android:<variant>`), and a key for one build never stands in for another's. A single-build project must read exactly as before.
- **The feature choice in `setup` adds and removes:** numbers toggle, `all` and `none` set it, the list is shown again after each change, Enter takes it, and the choice is always said back. Enter must never be the only way in and out of a choice.
- **A no is not a failure:** a declined confirmation exits 0 with "Nothing was changed."; input that ends at a prompt is a usage error that says so.
- **Features are chosen, app opens are not.** `project.Feature` lists what an integration can include, suggested from files only (dependencies, a few distinctive APIs in code, manifests). Detection never asks an agent. `verify.Options.Features` is the choice: unsent and chosen is a warning, unsent and suggested is `info`, neither is silence. A generic call name (`transaction`) counts only on a Tenjin receiver.
- **SDK keys never go to an agent.** Not in its task, its arguments or its environment (`TENJIN_SDK_KEY*` and `TENJIN_API_TOKEN` are removed from it). The agent writes its build's `SDKKeyPlaceholder`; `setup` fills it after the agent finishes (`project set-keys` is the scripted form), and `doctor` reports a placeholder left as `sdk.key.unfilled`. The skill says the same.
- **`setup` ends on a real launch.** Its last step reads the device and waits, a bounded number of polls through `env.wait`, for Tenjin to process the install.
- **What is said about an agent must be what is enforced.** The sentence shown before the yes comes from the driver (`limits`). If a limit is only an instruction to the agent, say "it is told not to", not "it cannot".
- **All HTTP goes through `internal/transport`** (`env.transport()`). It retries a request only when sending it again cannot do anything twice: GET, or a request whose context is marked `transport.Retryable`. Never mark a request that creates or changes something, or an OAuth token request (refresh tokens rotate). The `--verbose` log is method, address, status and time; never add headers or bodies to it.
- **Never print a credential or a whole SDK key.** Findings show the last four characters (`mask`), and an agent's report is masked the same way before it is shown. `tenjinapi` builds requests with the generated client but decodes responses itself, into types holding only what the CLI shows: the API returns shared secrets on an app, and an unexpected value in a field nobody shows must not fail a whole list. Never render a generated response type directly.
- **A check that cannot tell must not say pass.** Use an `info` finding.
- **Read the project as its tools do.** Comments are blanked by a lexer that knows strings (`project.BlankComments`); calls count on the SDK's receiver under the project's own names for it (`verify/receivers.go`); test sources say nothing about the app; `TENJIN_APP_STORE` is read as the Android SDK reads it; what a library brings (permissions, Play Services) counts.
- **Text from outside is cleaned for people.** Project content and agent output reach the terminal through `render.Clean` / `forPeople`; JSON escapes on its own.
- **Ctrl-C stops everything.** Prompts and waits end on it (exit 130, `interrupted`), and programs the CLI starts run in their own process group (`internal/proc`).
- **A fix must be usable as written.** Name the file, the line and the version; never "the latest". The SDK versions fixes name come from `internal/verify/versions.go`, which is also the offline answer; online, `internal/sdkrelease` finds newer releases once a day, and `verify` takes one only within the major version the table knows. A new major SDK version is a CLI change: read its changelog, adjust the checks, then move the table.
- **No dead flags or code.** Add a flag in the change that gives it behaviour. The generated clients are the exception: they cover the whole spec, used or not. Change the specs in `internal/tenjinapi/openapi` (and its README's list of edits), then `make generate`; never edit `client.gen.go`, and CI fails if it differs from what the specs generate.
- **The README describes this binary too.** `TestTheReadmeOnlyNamesCommandsThatExist` checks every command and flag it names, and `TestTheReadmeListsEveryExitCode` its exit codes against `clierr`. The Markdown is linted and its links checked in CI (`.github/workflows/docs.yaml`, `.markdownlint-cli2.yaml`).
- **The skill describes this binary.** `internal/skill/tenjin-cli.md` is what agents are told. When a command or flag it mentions changes, change it too; `TestTheSkillOnlyNamesCommandsThatExist` fails if it names something that is not there.
- **Data commands that write** (`internal/cli/write.go`, over `tenjinapi/write.go`) send only the fields given as flags, ask first at a terminal (deletes default to no), need `--yes` otherwise, and print the body with `--dry-run`. Writes are never retried (`internal/transport` retries only safe requests). Fields the dashboard shows, such as callback URLs and parameters, are printed as the API returns them (Diego, 16 October 2026).
- **What the MCP server offers, the CLI offers.** Besides the API's reads and writes: `site-ids` (Site ID Optimization, Connect JSON at `connect.tenjin.com` with the API's token, `tenjinapi/siteids.go`), `docs search` (the public docs site, no token), `reports metrics` and `apps s2s-callbacks macros` (`internal/catalog`, the MCP server's catalogs as JSON; change both together until the shared module holds them), and `apps callback-groups inspect` (its `inspect_app_integration`). A new MCP tool needs a CLI command.
- **Data commands are one API read each** (`internal/cli/data.go`, over `tenjinapi/data.go`): its parameters as flags, a table at a terminal (a view's columns, or every attribute in the API's order for reports), and with `--json` the API's document as it is, so the API's docs describe it. Lists show one page with a hint on stderr; `--all` merges every page; reports read every page unless `--page`/`--per-page` is given. IDs are checked before any request. `apps list|get|create|ensure` and `apps keys list` keep the CLI's own JSON, which leaves out the shared secrets an app carries.
- **JSON output is a contract.** `project detect --json` and `doctor --json` are pinned by golden files in `internal/cli/testdata`. Add fields; do not rename or remove them.
- **`agent install` writes only what it lists.** It owns one file (the Claude Code skill) and one marked section (in `AGENTS.md`), and removes exactly those.

## Releases

- release-please makes releases from the conventional titles of squashed pull requests (`.github/workflows/release-please.yaml`, `release-please-config.json`; `pr-title.yaml` checks titles). Merging its pull request makes a draft release and the `v` tag; `release.yaml` tests that commit, GoReleaser fills the draft (`use_existing_draft`, matched by the name `v<version>`) and opens the cask, and the draft is published last, so `releases/latest` never lacks archives. Tags keep the `v`: `install.sh`, `install.ps1`, the update notice and Go need it. `CHANGELOG.md` is release-please's; don't edit it by hand.
- `.goreleaser.yaml` builds six targets (macOS, Linux and Windows, each on amd64 and arm64), archives named `tenjin_<version>_<os>_<arch>`, `checksums.txt`, and the Homebrew cask. `install.sh` and `install.ps1` build the same archive names; change all three together. CI builds a snapshot on every pull request and installs it with both scripts and the cask, on each OS.
- A release proposes its cask to `tenjin/homebrew-tap` as a pull request, with a token from the organization's Code Ops GitHub App limited to the tap; someone merges it. Pin every action by commit SHA: the release job holds that token.
- The cask removes macOS's quarantine mark after install, because the binaries are not notarized yet. Drop that step once they are. It is a `postflight_steps` block in `custom_block` (Homebrew deprecated `postflight`); move it to `hooks.post.install_steps` with GoReleaser v2.19.
- The README's "Pre-release" note goes in a `docs:` pull request merged just before the first release's.
- A local snapshot shows `commit none` and a `v0.0.0` download address while the repository has no tag. A release in CI fills both in.

## Tests

- Tests go through `execute` with a `harness` (`internal/cli/fake_test.go`) and assert on exit code, stdout and stderr separately.
- `fakeTenjin` stands in for the authorization server, the API and Tenjin, and answers as they do, quirks included (see `fake_test.go`).
- Project fixtures come from `internal/testproject`: start from a correct integration and change one thing.
- `internal/devicelog/testdata/android-accepted.log` and `ios-accepted.log` are real captures with every identifier, receipt, signature, key and location replaced. Scrub any new capture the same way before committing it.
- A booted iOS simulator is faked with `harness.bootSimulator(log)`, which stands in for `xcrun simctl`, and a connected iPhone with `harness.connectIPhone(console)`, for `xcrun devicectl`. `ios-device-accepted.log` is a real iPhone capture, scrubbed.
- A device is faked with `harness.connectDevice(log)`, which stands in for `adb`.
- A real-agent test must call `isolate(t, h)`. It points every `tenjin` an agent starts at the fake, through `TENJIN_*_URL`, so an agent can never act on production or on the session of whoever runs the test.

Scope and reasoning: `docs/adr/0001-language-repository-and-scope.md`.
