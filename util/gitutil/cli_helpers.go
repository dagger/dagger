package gitutil

import (
	"bytes"
	"context"
	"slices"
)

func (cli *GitCLI) Dir() string {
	if cli.dir != "" {
		return cli.dir
	}
	return cli.workTree
}

func (cli *GitCLI) WorkTree(ctx context.Context) (string, error) {
	if cli.workTree != "" {
		return cli.workTree, nil
	}
	out, err := cli.Run(ctx, "rev-parse", "--is-inside-work-tree", "--show-toplevel")
	out = bytes.TrimSpace(out)
	if err != nil {
		if string(out) == "false" {
			return "", nil
		}
		return "", err
	}
	lines := slices.Collect(bytes.Lines(out))
	return string(lines[len(lines)-1]), nil
}

func (cli *GitCLI) GitDir(ctx context.Context) (string, error) {
	if cli.gitDir != "" {
		return cli.gitDir, nil
	}
	out, err := cli.Run(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(out)), err
}

func (cli *GitCLI) URL(ctx context.Context) (string, error) {
	gitDir, err := cli.GitDir(ctx)
	if err != nil {
		return "", err
	}
	return "file://" + gitDir, nil
}
