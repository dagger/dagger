package main

import (
	"context"

	"github.com/dagger/dagger/cmd/dagger/.dagger/internal/dagger"
)

func New(
	ctx context.Context,

	// +optional
	runnerHost string,

	// +optional
	// +defaultPath="/"
	// +ignore=["*", ".*", "!cmd/dagger/*", "!**/go.sum", "!**/go.mod", "!**/*.go", "!vendor/**/*", "!**.graphql", "!.goreleaser*.yml", "!.changes", "!LICENSE", "!install.sh", "!install.ps1", "!**/*.sql"]
	source *dagger.Directory,

	// Base image for go build environment
	// +optional
	base *dagger.Container,
) (*DaggerCli, error) {
	// FIXME: this go builder config is duplicated with engine build
	// move into a shared engine/builder module
	v := dag.Version()
	version, err := v.Version(ctx)
	if err != nil {
		return nil, err
	}
	imageTag, err := v.ImageTag(ctx)
	if err != nil {
		return nil, err
	}
	values := []string{
		// FIXME: how to avoid duplication with engine module?
		"github.com/dagger/dagger/engine.Version=" + version,
		"github.com/dagger/dagger/engine.Tag=" + imageTag,
	}
	if runnerHost != "" {
		values = append(values, "main.RunnerHost="+runnerHost)
	}

	return &DaggerCli{
		Version: version,
		Tag:     version,
		Go: dag.Go(dagger.GoOpts{
			Source: source,
			Base:   base,
			Values: values,
		}),
	}, nil
}

type DaggerCli struct {
	Version string
	Tag     string

	Go *dagger.Go // +private
}

// Build the dagger CLI binary for a single platform
func (cli DaggerCli) Binary(
	// +optional
	platform dagger.Platform,
) *dagger.File {
	return cli.Go.Binary("./cmd/dagger", dagger.GoBinaryOpts{
		Platform:  platform,
		NoSymbols: true,
		NoDwarf:   true,
	})
}

// Generate a markdown CLI reference doc
func (cli DaggerCli) Reference(
	// +optional
	frontmatter string,
	// +optional
	// Include experimental commands
	includeExperimental bool,
) *dagger.File {
	cmd := []string{"go", "run", "./cmd/dagger", "gen", "--output", "cli.mdx"}
	if includeExperimental {
		cmd = append(cmd, "--include-experimental")
	}
	if frontmatter != "" {
		cmd = append(cmd, "--frontmatter="+frontmatter)
	}
	return cli.Go.
		Env().
		WithExec(cmd).
		File("cli.mdx")
}
