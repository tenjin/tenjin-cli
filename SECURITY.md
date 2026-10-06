# Security

## Reporting a vulnerability

Please do not open a public issue for a security problem. Email [support@tenjin.com](mailto:support@tenjin.com) with "Security: tenjin-cli" in the subject, and include:

- what the problem is, and what an attacker could do with it;
- the steps to reproduce it, and the version (`tenjin version`);
- your name or handle, if you would like to be credited.

We will acknowledge your report, keep you informed while we work on a fix, and credit you in the release notes if you wish.

## Supported versions

Fixes are made to the latest release. Update with `brew upgrade tenjin`, or by installing the latest release again.

## What the CLI protects

The areas where a vulnerability matters most:

- **Credentials.** The sign-in is kept in the operating system's keychain, or in a file only the user can read when there is no keychain. No credential is printed or logged with `--verbose`, and none is given to a coding agent: `TENJIN_API_TOKEN` and the SDK key variables are removed from the environment an agent is started with.
- **SDK keys.** A whole SDK key is never printed, and keys are never given to a coding agent: the agent writes a placeholder and the CLI fills it.
- **Coding agents.** An agent started by `tenjin setup` is confined: Claude Code to file edits and two read-only `tenjin` commands, Codex to its own sandbox.
- **Your project.** The CLI changes nothing in it except to write an SDK key where a placeholder stands for it.
