// Package version holds the sf release version so both the CLI (`sf
// --version`) and `sf doctor` report the same string without an import
// cycle between internal/cli and internal/common/doctor.
package version

// Version is the sf release version. It is overridden at build time via
//
//	-ldflags "-X github.com/sofia-ctx/sofia/internal/version.Version=..."
//
// (see scripts/build.sh and .goreleaser.yml). Left at "dev" for a plain
// `go build`/`go run` without ldflags.
var Version = "dev"
