# Contributing

Thanks for helping improve the Tenjin CLI. Bug reports, fixes and new checks are all welcome.

## Before you start

- **Questions about your Tenjin account or integration** go to [support@tenjin.com](mailto:support@tenjin.com), not to an issue here.
- **Security issues** are reported privately: see [SECURITY.md](SECURITY.md).
- **Larger changes**, such as a new command or a check for a new framework, are worth an issue first, so we can agree on the approach before you write it.

## Developing

Requires Go 1.27 (see `go.mod`).

```sh
make build              # bin/tenjin, with the version stamped in
make test               # go test -race ./...
make lint               # gofmt check and golangci-lint
make generate           # regenerate the API clients from internal/tenjinapi/openapi
make coverage           # coverage of all the code and of what changed since main
make lint-scripts       # actionlint and shellcheck (needs shellcheck installed)
make release-snapshot   # every release archive and the Homebrew cask, into dist/; publishes nothing
```

The Markdown files are linted and their links checked in CI too. To run the lint locally: `npx markdownlint-cli2`.

The tests need no Tenjin account and make no network requests: Tenjin's services are faked (`internal/cli/fake_test.go`), and devices are faked through `adb`, `simctl` and `devicectl` stand-ins. Project fixtures come from `internal/testproject`: start from a correct integration and change one thing.

`CLAUDE.md` describes how the code is laid out and the rules it follows. The ones that most often matter in a review:

- Every command supports `--json`, writes only its result to stdout, and never prompts when stdin is not a terminal.
- A check that cannot tell must not report a pass.
- A fix names the file, the line and the version, so it can be applied as written.
- Credentials and whole SDK keys are never printed, and SDK keys are never given to a coding agent.
- The JSON of `project detect` and `doctor` is a contract: add fields, do not rename or remove them. Accept a deliberate change to the golden files with `go test ./internal/cli -run JSONOutputIsStable -update`.

## Layout

```text
cmd/tenjin            entry point; only calls cli.Execute
internal/cli          command tree; commands parse flags, call one operation and render
internal/project      detects what a project is; reads files only
internal/verify       the doctor checks
internal/agent        hands a task to an installed coding agent
internal/proc         starts programs in their own process group, so Ctrl-C stops them
internal/guides       the SDK integration guides, embedded
internal/skill        the instructions for coding agents, and their installer
internal/keyfill      finds the SDK key placeholders agents leave, and fills them
internal/devicelog    reads the SDK's lines from a device log
internal/track        asks Tenjin's tracking service about an SDK key or a device
internal/transport    HTTP retries and the --verbose log
internal/tenjinapi    operations on the Tenjin API, over clients generated from its specs (make generate)
internal/oauth        browser sign-in
internal/session      stored credentials and token refresh
internal/config       service URLs
internal/render       output: table, JSON and colour
internal/sdkrelease   the newest release of each SDK, once a day
internal/update       the once-a-day check for a newer CLI release
internal/buildinfo    the version, commit and date stamped in at build time
internal/clierr       error kinds and exit codes
internal/testproject  sample projects for tests
docs/adr              architecture decisions
```

The reasoning behind the language, the repository and the scope is in [docs/adr/0001-language-repository-and-scope.md](docs/adr/0001-language-repository-and-scope.md).

## Releases

Releases are made with [release-please](https://github.com/googleapis/release-please), from the titles of the commits on `main`. It keeps a pull request open with the next version and its changelog: a `feat:` raises the minor version, a `fix:` the patch version, and a breaking change (`feat!:`) the minor version until 1.0. `chore:`, `ci:`, `docs:`, `test:` and `refactor:` make no release. Dependabot titles Go module updates `fix(deps):`, so they ship in a patch release, and action updates `ci(deps):`, which ship nothing.

Merging that pull request tags the release as a draft. The release workflow then runs the tests on the tagged commit, builds the archives for macOS, Linux and Windows with GoReleaser (`.goreleaser.yaml`), uploads them with their checksums, attests their build provenance, and publishes the release. Only then does it open a pull request with the new cask in [tenjin/homebrew-tap](https://github.com/tenjin/homebrew-tap); Homebrew users get the release once that is merged. Pre-releases (`v0.2.0-rc.1`) get no cask. The job that publishes runs in the repository's `release` environment, so that environment's protection rules decide who may approve a release.

One commit always builds the same archives, byte for byte, so running the release again replaces them with identical files. If the release workflow fails before the release is published, run it again from the Actions tab, from `main`, with the draft's tag: it refuses any tag that is not a draft release of a commit on `main`. If only the cask's pull request failed, re-run that run's failed jobs. If the tests fail on the tagged commit, delete the draft release but keep its tag: release-please then proposes the fix as the next patch version. Deleting the tag too would make it start over and repeat every change in the next changelog.

## Pull requests

- Keep each pull request to one change, with tests.
- `make test` and `make lint` pass, on macOS, Linux and Windows (CI runs all three). CI also lints the workflows and shell scripts, and comments the test coverage on the pull request: at least 80% of the changed statements must be tested.
- Title the pull request as a [conventional commit](https://www.conventionalcommits.org/) (`feat: …`, `fix: …`, `docs: …`): squashed, it becomes the commit's title, and CI checks it. Its description says what changed and why.
- Device log captures added as test fixtures must have every identifier, receipt, signature, key and location replaced first.

By contributing, you agree that your contribution is licensed under the [MIT License](LICENSE).
