package releaseversion

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// Parse derives a bare semantic version from a resolved git ref name.
func Parse(ref string) (string, error) {
	name := strings.TrimPrefix(ref, "refs/tags/")
	name = strings.TrimPrefix(name, "refs/heads/")
	version := strings.TrimPrefix(name, "v")
	if !semver.IsValid("v" + version) {
		return "", fmt.Errorf("release ref %q is not a semantic version", ref)
	}
	return version, nil
}
