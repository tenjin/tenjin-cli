# Contributing

Thanks for helping improve the Tenjin CLI. Bug reports, fixes and new checks are all welcome.

## Before you start

- **Questions about your Tenjin account or integration** go to [support@tenjin.com](mailto:support@tenjin.com), not to an issue here.
- **Security issues** are reported privately: see [SECURITY.md](SECURITY.md).
- **Larger changes**, such as a new command or a check for a new framework, are worth an issue first, so we can agree on the approach before you write it.

## Developing

Requires Go 1.27 (see `go.mod`).

```
make build      # bin/tenjin, with the version stamped in
make test       # go test -race ./...
make lint       # gofmt check and golangci-lint
```

The tests need no Tenjin account and make no network requests: Tenjin's services are faked (`internal/cli/fake_test.go`), and devices are faked through `adb`, `simctl` and `devicectl` stand-ins. Project fixtures come from `internal/testproject`: start from a correct integration and change one thing.

`CLAUDE.md` describes how the code is laid out and the rules it follows. The ones that most often matter in a review:

- Every command supports `--json`, writes only its result to stdout, and never prompts when stdin is not a terminal.
- A check that cannot tell must not report a pass.
- A fix names the file, the line and the version, so it can be applied as written.
- Credentials and whole SDK keys are never printed, and SDK keys are never given to a coding agent.
- The JSON of `project detect` and `doctor` is a contract: add fields, do not rename or remove them. Accept a deliberate change to the golden files with `go test ./internal/cli -run JSONOutputIsStable -update`.

## Pull requests

- Keep each pull request to one change, with tests.
- `make test` and `make lint` pass, on macOS, Linux and Windows (CI runs all three).
- Write commit messages that say what changed and why.
- Device log captures added as test fixtures must have every identifier, receipt, signature, key and location replaced first.

By contributing, you agree that your contribution is licensed under the [MIT License](LICENSE).
