package mod

import (
	"fmt"
	"net/url"
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
	case strings.HasPrefix(repoName, "https://") || strings.HasPrefix(repoName, "http://"):
		return parseHTTPRepoName(repoName, versionConstraint)
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

		cloneRepo: "github.com/dagger/dagger",
		clonePath: path.Join("/stdlib", repoMatches[1]),
	}, nil
}

func parseHTTPRepoName(repoName, versionConstraint string) (*Require, error) {
	u, err := url.Parse(repoName)

	if err != nil {
		return nil, fmt.Errorf("issue when parsing HTTP repo")
	}

	clone, _ := url.Parse(repoName)
	clone.Path = path.Join(clone.Path, strings.TrimPrefix(clone.Fragment, "/"))
	clone.Fragment = ""

	return &Require{
		repo:              u.Host + u.Path,
		path:              "",
		version:           "",
		versionConstraint: versionConstraint,

		cloneRepo: clone.String(),
		clonePath: "",
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
