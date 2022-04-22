package mod

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"go.dagger.io/dagger/pkg"
)

type Require struct {
	repo string
	path string

	version           string
	versionConstraint string
	checksum          string

	source     RepoSource
	sourcePath string
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

	if err := os.Rename(path.Join(sourceRepoPath, r.sourcePath), destPath); err != nil {
		return err
	}

	return nil
}
