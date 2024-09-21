package main

import (
	"context"
	"dagger/dagger/internal/dagger"
)

const (
	// https://github.com/goreleaser/goreleaser/releases
	goReleaserVersion = "v2.2.0"
)

func New(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	// +ignore=["*", ".*", "!/cmd/dagger/*", "!**/go.sum", "!**/go.mod", "!**/*.go", "!**.graphql", "!**.graphqls"]
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

func (cli DaggerCli) Build(
	ctx context.Context,
	// Platform to build for
	// +optional
	platform dagger.Platform,
) *dagger.Directory {
	return cli.Gomod.Build(dagger.GoBuildOpts{
		Platform:  platform,
		Pkgs:      []string{"./cmd/dagger"},
		NoSymbols: true,
		NoDwarf:   true,
	})
}

func (cli DaggerCli) Binary(
	ctx context.Context,
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
	ctx context.Context,
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
		cmd = append(cmd, "--frontmatter", frontmatter)
	}
	return cli.Gomod.
		Env().
		WithExec(cmd).
		File("cli.mdx")
}