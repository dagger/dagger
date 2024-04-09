package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/ci/consts"
	"github.com/dagger/dagger/ci/internal/dagger"
)

type VersionInfo struct {
	Tag      string
	Commit   string
	TreeHash string
}

func newVersionFromGit(ctx context.Context, dir *dagger.Directory) (*VersionInfo, error) {
	base := dag.Container().
		From(consts.AlpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithMountedDirectory("/app/.git", dir).
		WithWorkdir("/app")

	info := &VersionInfo{}

	// use git write-tree to get a content hash of the current state of the repo
	var err error
	info.TreeHash, err = base.
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "write-tree"}).
		Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tree hash: %w", err)
	}
	info.TreeHash = strings.TrimSpace(info.TreeHash)

	return info, nil
}

func (info *VersionInfo) String() string {
	if info.Tag != "" {
		return info.Tag
	}
	if info.Commit != "" {
		return info.Commit
	}
	return info.TreeHash
}
