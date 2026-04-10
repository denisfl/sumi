// internal/updater/version.go
package updater

import (
	"fmt"
	"strconv"
	"strings"
)

// semver holds major.minor.patch components parsed from a vX.Y.Z string.
type semver struct {
	major, minor, patch int
}

// parseSemver parses a version string like "v1.2.3" or "1.2.3".
// Pre-release and build-metadata suffixes on the patch segment are stripped.
func parseSemver(v string) (semver, error) {
	s := strings.TrimPrefix(v, "v")
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("invalid semver: %q", v)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return semver{}, fmt.Errorf("invalid semver major in %q: %w", v, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return semver{}, fmt.Errorf("invalid semver minor in %q: %w", v, err)
	}
	// Strip pre-release / build metadata from the patch segment.
	patchStr := parts[2]
	if idx := strings.IndexAny(patchStr, "-+"); idx >= 0 {
		patchStr = patchStr[:idx]
	}
	patch, err := strconv.Atoi(patchStr)
	if err != nil {
		return semver{}, fmt.Errorf("invalid semver patch in %q: %w", v, err)
	}
	return semver{major: major, minor: minor, patch: patch}, nil
}

// isNewerThan returns true when v is strictly greater than other.
func (v semver) isNewerThan(other semver) bool {
	if v.major != other.major {
		return v.major > other.major
	}
	if v.minor != other.minor {
		return v.minor > other.minor
	}
	return v.patch > other.patch
}
