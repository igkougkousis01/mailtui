# Changelog

Notable changes to `mailtui` are documented here. The format is inspired by [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and version numbers follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-08-29

Initial development release.

### Added

- Loopback-only local SMTP catcher with configurable message and recipient limits
- RFC 5322 and MIME parsing with separate SMTP envelope and message headers
- Exact raw payload preservation and capture of malformed messages for inspection
- Live terminal inbox with Body, Headers, Raw, and Attachments metadata views
- Scriptable `wait` and `assert` commands with deterministic exit codes
- OTP and first HTTP(S) link extraction
- Shared SMTP configuration across interactive and script modes
- Build-time version reporting and graceful interactive/script lifecycle handling

Version 0.1.0 is an initial public release intended for local development workflows; it does not imply production mail-server stability or compatibility guarantees beyond the documented command behavior.
