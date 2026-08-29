# Security policy

## Intended use

`mailtui` is an unauthenticated SMTP catcher intended only for local development and testing. Its default listener is loopback-only (`127.0.0.1:1025`), and the CLI rejects non-loopback listener addresses.

Do not expose `mailtui` to a public network or use it to receive production email. Captured messages can contain credentials, one-time codes, links, and other sensitive development data; terminal output and CI logs should be handled accordingly.

## Reporting a vulnerability

Please report security issues privately through this repository's GitHub Security Advisories **Report a vulnerability** flow, if available. Do not open a public issue containing exploit details, credentials, or captured messages. If private advisories are unavailable, open a minimal public issue asking the maintainer for a private contact channel without disclosing the vulnerability.

No independent security audit or formal security guarantee is claimed.
