# ADR 0001: Language, repository and scope

Status: accepted, October 2026.

## Context

Tenjin has a hosted MCP server that lets assistants manage apps, campaigns, callbacks and reports. What it cannot do is anything on the developer's own machine: look at the project, read a device's logs, or give a script a pass-or-fail answer. Those are the parts of SDK onboarding where integrations go wrong.

This ADR records what the CLI is, what it is not, and how it is built.

## Decisions

### 1. The CLI is thin

It covers the local half of SDK onboarding: the app and its SDK key, test devices, live events, and diagnosing an integration that sends nothing.

It does not edit the developer's project itself. The developer makes the code changes, or their own coding agent does: `tenjin setup` can start an installed agent on the open findings, with the developer's consent, confined to file edits and `tenjin doctor`. The CLI then checks the result itself. It embeds no model and calls no model API.

It does not mirror the dashboard. Campaign, callback and reporting commands are out until someone needs them.

People should not have to learn the individual commands. `tenjin setup` runs them in order and repeats the checks after each fix. It composes the same operations the commands expose and adds none of its own, so anything it does, a script or agent can do step by step.

### 2. Usable by agents as well as people

Every command:

- accepts `--json` and writes only the result to stdout;
- never prompts when stdin is not a terminal;
- returns a stable exit code per failure class.

A coding agent should be able to run the whole onboarding sequence with one human action, the login.

### 3. Go, one static binary

A single binary installs on macOS, Linux and Windows with no runtime. That matters for game developers on Windows, who are unlikely to have Node. Go also lets the CLI share its API client with the MCP server.

TypeScript on an agent SDK was the alternative. It would make an embedded coding agent easy, but decision 1 means the CLI does not embed one.

### 4. Public repository, with a shared public module for the API client

A tool that runs next to a customer's source is easier to trust when its own source is readable, and a public repository is what makes `go install` and a Homebrew formula straightforward.

The generated API clients and the operations built on them live in a separate public Go module, used by both this CLI and the MCP server. One copy of the OpenAPI specs, one generated client. The module's name is settled when it is created.

### 5. `cobra` for the command tree

A noun-verb surface with generated help and shell completions is what `cobra` is for. One framework; no mixing.

### 6. Layout and the rule that keeps it extensible

```
cmd/tenjin          entry point
internal/cli        command tree, one file per noun
internal/project    detects what a project is; reads files only
internal/verify     the doctor checks
internal/agent      hands a task to an installed coding agent
internal/track      asks Tenjin's tracking service about an SDK key
internal/tenjinapi  operations on the API
internal/oauth      browser sign-in
internal/session    stored credentials and token refresh
internal/render     output: table and JSON
internal/clierr     error kinds and exit codes
```

The rule: a command parses flags, calls one operation, and renders the result. No HTTP in the command layer. Operations know nothing about terminals. Thin in scope, but laid out so that a later command is a new noun, not a rewrite.

`internal/tenjinapi` is a small hand-written client for now. It is the part that moves into the shared module, on generated clients, when that module exists.

## Not decided here

- The shared module's name.
- macOS notarization and Windows signing. Needed before a public release; unsigned binaries trip Gatekeeper.

## Consequences

- Developers without a coding agent follow the guide by hand for the code changes. If that turns out to be most new customers, decision 1 is the one to revisit.
- Being public, this repository cannot import private Tenjin modules.
- The first commands with value need no backend change at all: detecting the project and checking an integration.
- Test devices and live events wait for the API to expose them.
