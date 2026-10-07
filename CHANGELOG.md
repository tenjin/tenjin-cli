# Changelog

Notable changes to the Tenjin CLI. Release notes with every change are on the [releases page](https://github.com/tenjin/tenjin-cli/releases).

## Unreleased

The first release:

- `tenjin setup`: a guided integration, from sign-in to a launch Tenjin has processed, optionally done by your own coding agent.
- `tenjin project detect`, `tenjin doctor`, `tenjin sdk guide`, `tenjin login`, `tenjin apps`, `tenjin project set-keys` and `tenjin agent`.
- Native Android and iOS, Flutter, React Native, Capacitor and Unity projects; builds per app store; device checks on Android devices, iPhones and iOS simulators.
- Installs with Homebrew (macOS and Linux), a shell script (macOS and Linux) or a PowerShell script (Windows), each checked against the release checksums.
- Says when a newer release is out, once a day, with the command that updates it.
- `doctor` names the newest SDK releases, looked up once a day, and knows that the Android SDK brings its own keep rules from 2.0.0.
- Release archives carry the licences of the software compiled into the binary (`THIRD_PARTY_NOTICES.md`).
- Hardened after an adversarial review: the coding agent is confined more tightly and never sees a key; doctor reads projects, device logs and Tenjin's answers more carefully; Ctrl-C stops everything; installs and releases cover more environments.
