# Architecture

`mailtui` is deliberately process-local. An interactive run or script command owns an SMTP listener, an in-memory store, and one consumer of that store.

```text
SMTP
  ↓
Capture / MIME parsing
  ↓
In-memory Store
  ↓
├── TUI
└── script-mode matcher / wait / extract commands
```

## Components

- `internal/smtp` owns the SMTP conversation and envelope. It accepts only a validated loopback address and hands each exact DATA payload to the message parser.
- `internal/message` separates the SMTP envelope from RFC 5322 headers, parses MIME bodies and attachment metadata, and preserves the original payload.
- `internal/store` owns the captured messages and exposes concurrency-safe snapshots.
- `internal/tui` renders the live inbox and its Body, Headers, Raw, and Attachments views. `internal/interactive` coordinates the listener and terminal lifecycle.
- `internal/match`, `internal/wait`, and `internal/extract` implement deterministic script-mode selection and extraction. `internal/cli` owns arguments, streams, signals, and exit codes.

## Design decisions

### The Store owns snapshots

The Store clones messages when they enter and when they leave. SMTP sessions, the TUI, and script commands therefore cannot share mutable slices or raw byte buffers accidentally. The additional copies are an intentional tradeoff for clear ownership in a developer-sized, in-memory inbox.

`List` returns newest-first snapshots for the TUI. Waiters scan the same complete snapshot in arrival order, so there is one authoritative representation rather than separate consumer caches.

### Malformed mail is captured, not rejected

The SMTP transaction and the message syntax answer different questions: what bytes were delivered, and whether those bytes could be interpreted as RFC 5322/MIME. A parsing failure is often the artifact a developer most needs to inspect. `Capture` therefore retains the envelope, receipt time, exact raw payload, and parse error even when structured fields are incomplete.

### Script mode uses a one-shot catcher

There is no daemon, IPC protocol, control socket, or persistent database. Each `wait`, `assert`, or `extract` invocation binds its own listener, observes mail sent during that run, prints its result, and exits. This keeps deployment and lifecycle to one process and one binary. The consequence is explicit: a script command cannot inspect an already-running interactive instance and cannot share its port.

### Raw inspection is windowed

Raw payloads can include large encoded attachments. The Raw view indexes line checkpoints once, caps pathological unbroken source-line chunks, and renders only the visible terminal rows. It does not materialize a second full line-by-line copy of the payload. The complete original bytes remain preserved in the message.

### Notifications are wake-ups

Store notifications are buffered and best-effort so a slow UI or waiter cannot block an SMTP delivery. Consumers treat a notification as “the store changed” and refresh from `List`; they do not treat the notification ID as authoritative event history. Subscribing before the first scan and rescanning complete snapshots closes the arrival race without requiring a durable queue.

## Lifecycle and boundaries

Interactive mode binds the SMTP port before entering the alternate screen, then lets Bubble Tea own terminal signal handling. Script mode owns its cancellation context and keeps stdout reserved for machine-readable results. Both modes stop the listener gracefully so a message already accepted can receive its SMTP acknowledgement before the process exits.
