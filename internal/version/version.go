// Package version holds the sf release version so both the CLI (`sf
// --version`) and `sf doctor` report the same string without an import
// cycle between internal/cli and internal/common/doctor.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is the sf release version. A release build overrides it via
//
//	-ldflags "-X github.com/sofia-ctx/sofia/internal/version.Version=..."
//
// (see scripts/build.sh and .goreleaser.yml). When it isn't set that way —
// most importantly a `go install github.com/sofia-ctx/sofia/cmd/sf@vX.Y.Z`,
// the primary way a newcomer installs — init() below recovers the version
// from the module's build info. A plain `go build`/`go run` of a checkout has
// no module version, so it stays "dev".
var Version = "dev"

func init() {
	if Version != "dev" {
		return // an ldflag-stamped release build wins.
	}
	if v, ok := moduleVersion(); ok {
		Version = v
	}
}

// moduleVersion reads the main module's version from the binary's build info
// and normalizes it (strips the leading "v"). ok is false for a plain
// `go build`/`go run` of a checkout, whose build info reports "(devel)" or an
// empty version — there's no release version to report there.
func moduleVersion() (string, bool) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	return normalizeVersion(bi.Main.Version)
}

// normalizeVersion turns a build-info module version into a display version:
// a semver tag like "v0.17.0" becomes "0.17.0"; the "(devel)" / empty cases a
// non-module build reports have no release version and return ok=false.
func normalizeVersion(v string) (string, bool) {
	if v == "" || v == "(devel)" {
		return "", false
	}
	return strings.TrimPrefix(v, "v"), true
}
