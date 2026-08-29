// Package version carries the build identity of the mailtui binary.
//
// It is its own package so that a release build can set the version with a
// linker flag without the value being buried in a package that does anything
// else:
//
//	go build -ldflags "-X github.com/igkougkousis01/mailtui/internal/version.Version=0.1.0" ./cmd/mailtui
package version

// Version is the release this binary was built as.
//
// The fallback is "dev" rather than a number, because a number is a claim: a
// binary built from a working tree is not the release it happens to sit next
// to, and a version string that says otherwise is worse than one that admits
// it does not know.
var Version = "dev"

// String is the single line `mailtui version` prints. One line, no
// decoration, so a script can read it with cut -d' ' -f2.
func String() string { return "mailtui " + Version }
