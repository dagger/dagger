package main

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dagger/dagger/modules/dirdiff/internal/dagger"
)

type Dirdiff struct{}

// Return an error if two directories are not identical at the given paths.
// Paths not specified in the arguments are not compared.
func (dd *Dirdiff) AssertEqual(
	ctx context.Context,
	// The first directory to compare
	a *dagger.Directory,
	// The second directory to compare
	b *dagger.Directory,
	// The paths to include in the comparison.
	paths []string,
) error {
	ctr := dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{Packages: []string{
			// install diffutils, since busybox diff -r sometimes doesn't output anything
			"diffutils",
		}}).
		WithMountedDirectory("/mnt/a", a).
		WithMountedDirectory("/mnt/b", b).
		WithWorkdir("/mnt")
	for _, path := range paths {
		_, err := ctr.
			WithExec([]string{"diff", "-r", filepath.Join("a", path), filepath.Join("b", path)}).
			Sync(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

// Search for a filename in a directory and return paths of matching parent directories
func (dd *Dirdiff) Find(
	ctx context.Context,
	dir *dagger.Directory,
	filename string,
	// +optional
	exclude []string,
) ([]string, error) {
	excludeRE := make([]*regexp.Regexp, 0, len(exclude))
	for _, pattern := range exclude {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		excludeRE = append(excludeRE, re)
	}
	entries, err := dir.Glob(ctx, "**/"+filename)
	if err != nil {
		return nil, err
	}
	var parents []string
	for _, entry := range entries {
		entry = filepath.Clean(entry)
		parent := strings.TrimSuffix(entry, filename)
		if parent == "" {
			parent = "."
		}
		if matchAny(parent, excludeRE) {
			continue
		}
		parents = append(parents, parent)
	}

	return parents, nil
}

func matchAny(s string, regexps []*regexp.Regexp) bool {
	for _, re := range regexps {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
