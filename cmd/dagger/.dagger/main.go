package main

import (
	"context"
	"dagger/dagger/internal/dagger"
)

func New(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	// +ignore=["*", ".*", "!/cmd/dagger/*", "!**/go.sum", "!**/go.mod", "!**/*.go", "!**.graphql"]
	source *dagger.Directory,
	// Base image for go build environment
	// +optional
	base *dagger.Container,
) (*DaggerCli, error) {
	// FIXME: this go builder config is duplicated with engine build
	// move into a shared engine/builder module
	version, err := dag.Version().Version(ctx)
	if err != nil {
		return nil, err
	}
	imageTag, err := dag.Version().ImageTag(ctx)
	if err != nil {
		return nil, err
	}
	return &DaggerCli{
		Gomod: dag.Go(source, dagger.GoOpts{
			Base: base,
			Values: []string{
				// FIXME: how to avoid duplication with engine module?
				"github.com/dagger/dagger/engine.Version=" + version,
				"github.com/dagger/dagger/engine.Tag=" + imageTag,
			},
		}),
	}, nil
}

type DaggerCli struct {
	Gomod *dagger.Go // +private
}

// Build the dagger CLI binary for a single platform
func (cli DaggerCli) Binary(
	// +optional
	platform dagger.Platform,
) *dagger.File {
	return cli.Gomod.Binary("./cmd/dagger", dagger.GoBinaryOpts{
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
	return cli.Gomod.
		Env().
		WithExec(cmd).
		File("cli.mdx")
}
