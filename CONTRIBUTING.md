# Contributing

Thanks for helping improve `mailtui`. Keep changes small enough to review and verify confidently.

## Workflow

1. Fork the repository and create a branch from `main`.
2. Use a descriptive branch name such as `feat/better-help` or `fix/mime-boundary`.
3. Make one focused change; avoid unrelated cleanup in the same pull request.
4. Format and validate the repository:

   ```bash
   go fmt ./...
   go vet ./...
   go test ./...
   go test -race ./...
   go build ./...
   ```

5. Open a pull request that explains the behavior changed, why it changed, and how it was tested.

Do not commit directly to `main`.

## Style

- Follow standard Go conventions and keep `gofmt` clean.
- Prefer clear package boundaries, explicit ownership, and deterministic behavior over abstraction for its own sake.
- Preserve stdout for script results and stderr for diagnostics.
- Add focused tests for behavior changes, including concurrency coverage where ownership or lifecycle is involved.
- Update user-facing help or documentation when a command contract changes.

## Reporting bugs

Open a GitHub issue with your operating system, Go or binary version, the command you ran, the expected and actual behavior, and a minimal reproduction. Remove real email addresses, message contents, credentials, and tokens before attaching logs or raw messages.

Report security-sensitive issues privately as described in [SECURITY.md](SECURITY.md).
