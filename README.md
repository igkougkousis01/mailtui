# mailtui

[![CI](https://github.com/igkougkousis01/mailtui/actions/workflows/ci.yml/badge.svg)](https://github.com/igkougkousis01/mailtui/actions/workflows/ci.yml)
[![Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> A terminal-first local SMTP catcher and email testing tool for developers.

`mailtui` captures emails from local applications and lets you inspect them without opening a browser. Its interactive inbox is useful while developing; its `wait`, `assert`, and `extract` commands make the same SMTP catcher useful from shell scripts and CI.

- Capture local development email on a loopback-only SMTP listener
- Inspect bodies, headers, exact raw payloads, and attachment metadata
- Keep malformed messages available for debugging
- Wait for mail, assert content, and extract OTPs or HTTP(S) links
- Run as a single binary with deterministic exit codes and script-safe output

## Quick start

Start the interactive inbox:

```bash
mailtui
```

Configure your application under test to send mail to:

```text
SMTP_HOST=127.0.0.1
SMTP_PORT=1025
```

Those variable names are a common application convention; `mailtui` listens on `127.0.0.1:1025` by default and can be moved with `--smtp-addr`.

In the inbox, use `j`/`k` to move, `Tab` to change pane focus, and `b`, `h`, `r`, or `a` to open the Body, Headers, Raw, or Attachments view. Press `q` to quit.

## Installation

### From source

With Go 1.27 or newer:

```bash
go install github.com/igkougkousis01/mailtui/cmd/mailtui@latest
```

### Release binaries

Download the binary for your operating system and architecture from [GitHub Releases](https://github.com/igkougkousis01/mailtui/releases). On macOS and Linux, make it executable before placing it on your `PATH`:

```bash
chmod +x mailtui_Darwin_arm64
```

The release workflow publishes SHA-256 checksums alongside the binaries. Package-manager installation is not available yet.

## Commands

| Command | Purpose |
| --- | --- |
| `mailtui` | Interactive inbox |
| `mailtui wait` | Wait for a matching email |
| `mailtui assert` | Assert that an email arrives |
| `mailtui extract otp` | Print an OTP |
| `mailtui extract link` | Print the first HTTP(S) link |
| `mailtui version` | Show the version |

Wait for a reset email:

```bash
mailtui wait --to user@example.test --subject "Reset"
```

Capture an OTP:

```bash
OTP=$(mailtui extract otp --to user@example.test --timeout 5s)
```

Assert that a welcome email arrives with the expected text:

```bash
mailtui assert \
  --to user@example.test \
  --subject "Welcome" \
  --contains "Verify your account" \
  --timeout 5s
```

Every script command starts its own one-shot SMTP catcher. Start the command before triggering the email, and do not run it on the same port as the interactive inbox. Text filters are case-insensitive substring matches and are combined with AND. Run `mailtui help` for flags, output contracts, and exit codes.

## What mailtui is optimized for

`mailtui` is a development-only SMTP catcher for a zero-browser workflow. It combines interactive terminal inspection and script automation in one binary, while keeping captured messages in memory for the lifetime of that process. It does not provide persistence, accounts, a daemon, or a publicly reachable mail service.

Attachment inspection reports metadata; attachment payloads remain available only within the preserved raw message.

## Demo

> **Demo asset pending.** No synthetic product image is included. Follow the [20–30 second recording plan](docs/demo.md) to capture the real TUI, then replace this note with `docs/assets/mailtui-demo.gif` or a real screenshot.

## Documentation

- [Architecture](docs/architecture.md) — components and the reasoning behind the main design choices
- [Development](docs/development.md) — local workflow, validation, and versioned builds
- [Releasing](docs/releasing.md) — the v0.1.0 tag and artifact procedure
- [Contributing](CONTRIBUTING.md) — focused contribution guidelines
- [Security](SECURITY.md) — safe local use and vulnerability reporting

## License

`mailtui` is available under the [MIT License](LICENSE).
