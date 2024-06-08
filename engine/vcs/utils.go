package vcs

import (
	"fmt"
	"strings"

	"github.com/moby/buildkit/util/gitutil"
)

// Extracts the root of the repo and the subdir from the user ref and the computed root of repo
func ExtractRootAndSubdirFromRef(userRefWithoutVersion, parsedRepoRoot string) (string, string, error) {
	// resolve Host and Path from refs
	userRef, err := resolveGitURL(userRefWithoutVersion)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve git ref %s: %w", userRefWithoutVersion, err)
	}
	repoRoot, err := resolveGitURL(parsedRepoRoot)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve git ref %s: %w", parsedRepoRoot, err)
	}

	// extract repo path and subdir
	repoPath, subdir := splitPathAndSubdir(cleanPath(userRef.Path), cleanPath(repoRoot.Path))
	repoPath = normalizeGitSuffix(userRefWithoutVersion, repoPath)

	rootURL := repoRoot.Host + "/" + repoPath
	return rootURL, subdir, nil
}

// extract path to root of repo and subdir
func splitPathAndSubdir(refPath, rootPath string) (string, string) {
	// Vanity URL case
	if !strings.HasPrefix(refPath, rootPath) {
		return extractSubdirVanityURL(rootPath, refPath)
	}

	subdir := strings.TrimPrefix(refPath, rootPath)
	subdir = strings.TrimPrefix(subdir, "/")

	return rootPath, subdir
}

// normalizes ".git" suffix according to user ref
func normalizeGitSuffix(ref, root string) string {
	refContainsGit := strings.Contains(ref, ".git")

	if refContainsGit {
		return root + ".git"
	}
	return root
}

// cleanPath removes leading slashes and ".git" from the path.
func cleanPath(p string) string {
	return strings.TrimPrefix(strings.ReplaceAll(p, ".git", ""), "/")
}

// resolve the ref as a Git URL
func resolveGitURL(ref string) (*gitutil.GitURL, error) {
	u, err := gitutil.ParseURL(ref)
	if err != nil {
		if err == gitutil.ErrUnknownProtocol {
			u, err = gitutil.ParseURL("https://" + ref)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to parse URL: %w", err)
		}
	}
	return u, nil
}

// extract root and subdir for vanity URLs when one element of the root URL path is a prefix of the module path
// Problem solved: vanity URLs generally do not have the same root URL structure as the userRefPath
// We then need to find a heuristic to isolate, from the vanity URL, the root and the path
func extractSubdirVanityURL(rootURLPath, userRefPath string) (string, string) {
	rootComponents := strings.Split(strings.Trim(rootURLPath, "/"), "/")
	modulePathComponents := strings.Split(strings.Trim(userRefPath, "/"), "/")

	modIndexMap := make(map[string]int)
	for i, component := range modulePathComponents {
		modIndexMap[component] = i
	}

	// Iterate over the root components in reverse order to find the deepest match first,
	// ensuring we get the most specific subdirectory.
	for i := len(rootComponents) - 1; i >= 0; i-- {
		if j, found := modIndexMap[rootComponents[i]]; found {
			subdir := strings.Join(modulePathComponents[j+1:], "/")
			return rootURLPath, subdir
		}
	}

	return strings.TrimSuffix(rootURLPath, "/"), ""
}
