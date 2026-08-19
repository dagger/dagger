package workspace

import (
	"strings"

	"github.com/dagger/dagger/core/gitref"
)

// IsLocalRef performs a fast heuristic check to determine whether a module
// reference string refers to a local path instead of a git source.
func IsLocalRef(source, pin string) bool {
	switch gitref.FastKindCheck(source, pin) {
	case gitref.KindLocal:
		return true
	case gitref.KindGit:
		return false
	}
	// The ref has a dot but no scheme and no leading path marker. Only the part
	// ahead of the first separator can be a host, so a dot further down the path
	// ("common/.dagger/mymod") still reads as local. A path typed on Windows
	// separates with backslashes, which no git ref ever does; filepath.ToSlash
	// would be a no-op here, since the engine reading this runs on Linux.
	host, _, _ := strings.Cut(strings.ReplaceAll(source, `\`, "/"), "/")
	if _, afterUser, scpLike := strings.Cut(host, "@"); scpLike {
		host = afterUser
	}
	if strings.Contains(host, ".") || strings.Contains(host, ":") {
		return false
	}
	// A dot-free host otherwise has to be spelled with a scheme, except for
	// Azure DevOps Server, whose on-prem refs name themselves with a "_git"
	// path segment (see the matcher in engine/vcs).
	return !strings.Contains(source, "/_git/")
}
