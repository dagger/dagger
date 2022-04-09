package mod

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"go.dagger.io/dagger/pkg"
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
	case strings.HasPrefix(repoName, pkg.UniverseModule):
		return parseDaggerRepoName(repoName, versionConstraint)
	default:
		return parseGitRepoName(repoName, versionConstraint)
	}
}

var gitRepoNameRegex = regexp.MustCompile(`([a-zA-Z0-9_.-]+(?::\d*)?/[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+)([a-zA-Z0-9/_.-]*)@?([0-9a-zA-Z.-]*)`)

func parseGitRepoName(repoName, versionConstraint string) (*Require, error) {
	repoMatches := gitRepoNameRegex.FindStringSubmatch(repoName)

	if len(repoMatches) < 4 {
		return nil, fmt.Errorf("issue when parsing github repo")
	}

	return &Require{
		repo:              strings.TrimSuffix(repoMatches[1], ".git"),
		path:              repoMatches[2],
		version:           repoMatches[3],
		versionConstraint: versionConstraint,

		cloneRepo: repoMatches[1],
		clonePath: repoMatches[2],
	}, nil
}

var daggerRepoNameRegex = regexp.MustCompile(pkg.UniverseModule + `([a-zA-Z0-9/_.-]*)@?([0-9a-zA-Z.-]*)`)

func parseDaggerRepoName(repoName, versionConstraint string) (*Require, error) {
	repoMatches := daggerRepoNameRegex.FindStringSubmatch(repoName)

	if len(repoMatches) < 3 {
		return nil, fmt.Errorf("issue when parsing dagger repo")
	}

	return &Require{
		repo:              pkg.UniverseModule,
		path:              repoMatches[1],
		version:           repoMatches[2],
		versionConstraint: versionConstraint,

		cloneRepo: "github.com/dagger/examples",
		clonePath: path.Join("/helloapp", repoMatches[1]),
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
	if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
		return err
	}

	if err := os.Rename(path.Join(sourceRepoPath, r.clonePath), destPath); err != nil {
		return err
	}

	return nil
}
