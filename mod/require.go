package mod

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type Require struct {
	repo string
	path string

	cloneRepo string
	clonePath string

	version           string
	versionConstraint string
	checksum          string
}

func newRequire(repoName, versionConstraint string) (*Require, error) {
	switch {
	case strings.HasPrefix(repoName, "github.com"):
		return parseGithubRepoName(repoName, versionConstraint)
	case strings.HasPrefix(repoName, "alpha.dagger.io"):
		return parseDaggerRepoName(repoName, versionConstraint)
	default:
		return nil, fmt.Errorf("repo name does not match suported providers")
	}
}

var githubRepoNameRegex = regexp.MustCompile(`(github.com/[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+)([a-zA-Z0-9/_.-]*)@?([0-9a-zA-Z.-]*)`)

func parseGithubRepoName(repoName, versionConstraint string) (*Require, error) {
	repoMatches := githubRepoNameRegex.FindStringSubmatch(repoName)

	if len(repoMatches) < 4 {
		return nil, fmt.Errorf("issue when parsing github repo")
	}

	return &Require{
		repo:              repoMatches[1],
		path:              repoMatches[2],
		version:           repoMatches[3],
		versionConstraint: versionConstraint,

		cloneRepo: repoMatches[1],
		clonePath: repoMatches[2],
	}, nil
}

var daggerRepoNameRegex = regexp.MustCompile(`alpha.dagger.io([a-zA-Z0-9/_.-]*)@?([0-9a-zA-Z.-]*)`)

func parseDaggerRepoName(repoName, versionConstraint string) (*Require, error) {
	repoMatches := daggerRepoNameRegex.FindStringSubmatch(repoName)

	if len(repoMatches) < 3 {
		return nil, fmt.Errorf("issue when parsing dagger repo")
	}

	return &Require{
		repo:              "alpha.dagger.io",
		path:              repoMatches[1],
		version:           repoMatches[2],
		versionConstraint: versionConstraint,

		cloneRepo: "github.com/dagger/universe",
		clonePath: path.Join("/stdlib", repoMatches[1]),
	}, nil
}

func (r *Require) String() string {
	return fmt.Sprintf("%s@%s", r.fullPath(), r.version)
}

func (r *Require) fullPath() string {
	return path.Join(r.repo, r.path)
}

func replace(r *Require, sourceRepoPath, destPath string) error {
	// remove previous package directory
	if err := os.RemoveAll(destPath); err != nil {
		return err
	}

	// Make sure the destination exists
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	if err := os.Rename(path.Join(sourceRepoPath, r.clonePath), destPath); err != nil {
		return err
	}

	return nil
}
