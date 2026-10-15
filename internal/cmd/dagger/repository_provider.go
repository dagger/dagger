package daggercmd

import (
	"fmt"
	"net/url"
	"strings"

	workspacepkg "github.com/dagger/dagger/core/workspace"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/util/gitutil"
)

// normalizeRepository retains the instance and full namespace. Bare owner/repo
// addresses continue to mean GitHub for compatibility with existing commands.
func normalizeRepository(remote string) string {
	remote = strings.TrimSpace(remote)
	first, _, _ := strings.Cut(remote, "/")
	if strings.Count(remote, "/") == 1 && !strings.ContainsAny(first, ".:") {
		remote = "github.com/" + remote
	}
	// Reuse Git URL parsing/redaction and the workspace's repository key.
	// HTMLURL keeps HTTP(S) ports and drops SSH transport ports/selectors.
	if address, err := gitutil.ParseURL(remote); err == nil {
		remote = address.HTMLURL()
	}
	remote = workspacepkg.NormalizeGitRemote(remote)
	address, _ := url.Parse("https://" + remote)
	if address != nil && address.Hostname() == "github.com" {
		return "github.com/" + normalizeGitHubRepo(remote)
	}
	return remote
}

func repositoryInstance(repository string) string {
	if strings.HasPrefix(normalizeRepository(repository), "github.com/") {
		return "https://github.com"
	}
	if address, err := gitutil.ParseURL(repository); err == nil {
		if browserURL, err := url.Parse(address.HTMLURL()); err == nil {
			return browserURL.Scheme + "://" + strings.ToLower(browserURL.Host)
		}
	}
	host, _, _ := strings.Cut(normalizeRepository(repository), "/")
	return "https://" + host
}

// Canonical stored repositories have no scheme. Resolve their exact authority
// against Cloud's registry so a local HTTP instance is not guessed as HTTPS.
func registeredGitLabInstance(setup *cloudapi.GitLabSetup, repository string) (string, error) {
	host, _, _ := strings.Cut(normalizeRepository(repository), "/")
	var instance string
	for _, origin := range setup.Instances {
		address, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(address.Host, host) {
			continue
		}
		if instance != "" && instance != origin {
			return "", fmt.Errorf("multiple registered GitLab instances match %s; use integration create gitlab --instance with the exact origin", host)
		}
		instance = origin
	}
	if instance == "" {
		return "", fmt.Errorf("GitLab instance for %s is not registered in Dagger Cloud", host)
	}
	return instance, nil
}
