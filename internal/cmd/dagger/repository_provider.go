package daggercmd

import (
	"strings"

	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/util/gitutil"
)

// normalizeRepository keeps the server and complete namespace. A bare
// owner/repo still means GitHub; SSH transport ports are not web ports.
func normalizeRepository(remote string) string {
	remote = strings.TrimSpace(remote)
	first, _, _ := strings.Cut(remote, "/")
	if strings.Count(remote, "/") == 1 && !strings.ContainsAny(first, ".:") {
		remote = "github.com/" + remote
	}
	if address, err := gitutil.ParseURL(remote); err == nil {
		remote = address.HTMLURL()
	}
	return workspacepkg.NormalizeGitRemote(remote)
}

func sameCloudRepository(a, b string) bool {
	a, b = normalizeRepository(a), normalizeRepository(b)
	if strings.HasPrefix(a, "github.com/") && strings.HasPrefix(b, "github.com/") {
		return strings.EqualFold(a, b)
	}
	return a != "" && a == b
}
