# Development

## Prerequisites

- Go 1.27 or newer, matching the `go` directive in `go.mod`
- Git
- An SMTP-capable application or client for manual end-to-end testing

No database, browser, daemon, or external service is required.

## Run

Start the interactive inbox from the repository root:

```bash
go run ./cmd/mailtui
```

Run a script command the same way:

```bash
go run ./cmd/mailtui wait --to user@example.test --subject "Reset" --timeout 5s
```

Use a different loopback port when needed:

```bash
go run ./cmd/mailtui --smtp-addr 127.0.0.1:2525
```

## Format, test, and build

Format source files:

```bash
go fmt ./...
```

Check formatting without changing files:

```bash
test -z "$(gofmt -l .)"
```

Run the validation used by CI:

```bash
go vet ./...
go test ./...
go test -race ./...
go build ./...
```

The race test runs on Linux and macOS in CI. Windows runs formatting, vet, unit tests, and builds.

After changing module requirements, normalize them with:

```bash
go mod tidy
```

## Release builds and version injection

Development builds report `mailtui dev`:

```bash
go build -o mailtui ./cmd/mailtui
./mailtui --version
```

Release builds set `internal/version.Version` through the linker. The release workflow removes the leading `v` from the Git tag, so tag `v0.1.0` produces `mailtui 0.1.0`:

```bash
go build \
  -trimpath \
  -ldflags "-s -w -X github.com/igkougkousis01/mailtui/internal/version.Version=0.1.0" \
  -o mailtui \
  ./cmd/mailtui
```

Native Go cross-compilation is sufficient because release builds disable CGO. For example:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/mailtui
```

Do not commit generated binaries.
