# Releasing

Releases are built from Git tags by `.github/workflows/release.yml`. The workflow cross-compiles native binaries, injects the tag version, writes SHA-256 checksums, and attaches the artifacts to a GitHub Release.

## Procedure

1. Ensure `main` is checked out, up to date, and clean:

   ```bash
   git switch main
   git pull --ff-only
   git status --short
   ```

2. Move completed entries out of `[Unreleased]` in `CHANGELOG.md`, confirm the version and date, and merge that focused change through a pull request.
3. Run the release validation from a clean checkout:

   ```bash
   go mod tidy
   go fmt ./...
   go vet ./...
   go test ./...
   go test -race ./...
   go build ./...
   ```

4. Create the release tag:

   ```bash
   git tag v0.1.0
   ```

5. Push the tag:

   ```bash
   git push origin v0.1.0
   ```

6. Confirm the Release workflow succeeds and publishes all expected artifacts.
7. Download representative binaries, verify `checksums.txt`, and run `mailtui --version`. Tag `v0.1.0` must report `mailtui 0.1.0`.

Do not move or recreate a published tag. Correct a released defect with a new version.

## Versioning

`mailtui` follows semantic versioning:

- Patch releases contain backwards-compatible bug fixes.
- Minor releases add backwards-compatible features.
- Major releases may contain breaking changes.

While the project is below 1.0.0, document command or output contract changes prominently even when semantic versioning permits them in a minor release.
