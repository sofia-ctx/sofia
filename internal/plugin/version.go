package plugin

import (
	"fmt"
	"strconv"
	"strings"
)

// semver is a minimal major.minor.patch triple. The protocol only ever needs
// ordering and major-window comparisons, so pre-release / build metadata
// (everything after a `-` or `+`) is parsed off and ignored rather than
// modelled — a plugin declaring "1.2.0-rc1" negotiates as 1.2.0.
type semver struct {
	major, minor, patch int
}

// parseSemver accepts "MAJOR[.MINOR[.PATCH]]" with an optional leading "v" and
// an optional "-prerelease"/"+build" suffix (discarded). Missing minor/patch
// components default to 0, so "2" and "2.0.0" are equal.
func parseSemver(s string) (semver, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return semver{}, fmt.Errorf("empty version")
	}
	raw = strings.TrimPrefix(raw, "v")
	// Drop pre-release / build metadata.
	if i := strings.IndexAny(raw, "-+"); i >= 0 {
		raw = raw[:i]
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 3 {
		return semver{}, fmt.Errorf("invalid version %q", s)
	}
	var out semver
	dst := []*int{&out.major, &out.minor, &out.patch}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, fmt.Errorf("invalid version %q", s)
		}
		*dst[i] = n
	}
	return out, nil
}

// compare returns -1, 0, or 1 for v < o, v == o, v > o.
func (v semver) compare(o semver) int {
	switch {
	case v.major != o.major:
		return sign(v.major - o.major)
	case v.minor != o.minor:
		return sign(v.minor - o.minor)
	default:
		return sign(v.patch - o.patch)
	}
}

func (v semver) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// Compatible decides whether a managed plugin declaring protocol version proto
// and minimum-host version minSF can run against a host speaking hostProto. It
// returns (true, "") when the plugin is usable, or (false, reason) with a
// human-readable reason for why it is disabled — the string `sf plugin
// list`/`info` surfaces verbatim.
//
// The rules, in order:
//
//  1. proto must parse and must be declared — a managed plugin with no protocol
//     is malformed.
//  2. N-1 major window: the plugin's protocol major must be the host's major or
//     exactly one below it. A higher major is "too new"; a lower one outside
//     the window is "too old".
//  3. min_sf, when set, must be ≤ the host protocol — the plugin needs host
//     features from a newer protocol than this host provides.
//
// hostProto is a parameter (not read from the HostProtocol constant) so the
// four compatibility quadrants can be exercised at any host version in tests.
func Compatible(proto, minSF, hostProto string) (bool, string) {
	host, err := parseSemver(hostProto)
	if err != nil {
		// Should never happen for the compiled-in constant; treat as fatal-safe.
		return false, fmt.Sprintf("host protocol %q is invalid", hostProto)
	}
	if strings.TrimSpace(proto) == "" {
		return false, "manifest does not declare a protocol version"
	}
	pv, err := parseSemver(proto)
	if err != nil {
		return false, fmt.Sprintf("invalid protocol version %q", proto)
	}

	low := host.major - 1
	if low < 0 {
		low = 0
	}
	switch {
	case pv.major > host.major:
		return false, fmt.Sprintf("plugin speaks protocol %s, newer than host protocol %s (major %d > %d)",
			pv, host, pv.major, host.major)
	case pv.major < low:
		return false, fmt.Sprintf("plugin speaks protocol %s, older than the host's supported window (host %s supports protocol majors %d–%d)",
			pv, host, low, host.major)
	}

	if strings.TrimSpace(minSF) != "" {
		mv, err := parseSemver(minSF)
		if err != nil {
			return false, fmt.Sprintf("invalid min_sf version %q", minSF)
		}
		if host.compare(mv) < 0 {
			return false, fmt.Sprintf("plugin requires host protocol >= %s, but host speaks %s", mv, host)
		}
	}

	return true, ""
}
