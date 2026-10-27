# ADR 0002: Tenjin data as commands, on clients shared with the MCP server

Status: accepted, October 2026. Supersedes the third paragraph of [ADR 0001](0001-language-repository-and-scope.md)'s decision 1, and decision 4's "and the operations built on them".

Update, October 2026: the first releases are for the SDK's integration only. The commands decided here are there only with `TENJIN_DATA_COMMANDS=1`, until they are released.

## Context

ADR 0001 kept the CLI to the local half of SDK onboarding and left the account's data (apps, campaigns, callbacks, reports) to the dashboard and the MCP server. Read that way, the CLI looked like a tool for SDK engineers only, while people who run user acquisition, and the scripts working for them, wanted the same data from a terminal.

Copying the MCP server's tools would not have served them well: each tool shapes its answer for an assistant, and a terminal and a script need different shapes.

## Decisions

### 1. The API's operations are commands, and the MCP server's tools have equivalents

Every read and write of the Tenjin API is a command, one per endpoint, with the endpoint's parameters as flags (`tenjin campaigns list`, `tenjin reports spend`, `tenjin apps callbacks create`). Every tool of the MCP server has a command too, including those not on the API itself: Site ID rules, documentation search, the metric and macro catalogs, and the integration view. A new MCP tool needs a CLI command.

There is no raw `tenjin api <path>` command. Named commands come with help, completions and checks before a request is sent.

### 2. Output: the API's answer, as it is

At a terminal a list is a table and one resource is its fields; `-o csv` is for spreadsheets. With `--json` a command prints the API's document as it is, so the API's own documentation describes it. The exception is the `apps` commands that read or change an app, which print the CLI's own JSON: the API returns an app's shared secrets with it.

### 3. Writes are explicit

A write sends only the fields given as flags, asks before it changes anything (deletes default to no), needs `--yes` when nothing can ask, and shows the exact request with `--dry-run`. Writes are never retried.

### 4. Shared with the MCP server: the specs and the generated clients

The OpenAPI specs, and the clients generated from them, are the same as the MCP server's (`internal/tenjinapi/openapi`, with the edits it lists). That is what moves into the shared public module. The operations do not: each interface shapes its own requests and output. `internal/tenjinapi` builds requests with the generated clients and decodes answers into its own types, leniently, so a value nobody shows cannot fail a whole list.

## Consequences

- ADR 0001's rule still holds: a command parses flags, calls one operation and renders. Each data command is one read or one write.
- The command list is long. Bare `tenjin` shows a short landing for people, with `tenjin --help` behind it.
- The specs and their edits are kept in step with the MCP server's until the shared module holds them.
