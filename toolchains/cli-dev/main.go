// Develop the Dagger CLI
package main

import (
	"context"

	"dagger/cli-dev/internal/dagger"

	"github.com/containerd/platforms"
)

func New(
	ctx context.Context,

	// +optional
	runnerHost string,

	// +optional
	// +defaultPath="/"
	// +ignore=[
	//   "*",
	//   ".*",
	//   "!cmd/dagger/*",
	//   "!**/go.sum",
	//   "!**/go.mod",
	//   "!**/*.go",
	//   "!vendor/**/*",
	//   "!**.graphql",
	//   "!.goreleaser*.yml",
	//   "!.changes",
	//   "!LICENSE",
	//   "!install.sh",
	//   "!install.ps1",
	//   "!**/*.sql"
	// ]
	source *dagger.Directory,

	// Base image for go build environment
	// +optional
	base *dagger.Container,
) (*CliDev, error) {
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

	return &CliDev{
		Version: version,
		Tag:     version,
		Go: dag.Go(dagger.GoOpts{
			Source: source,
			Base:   base,
			Values: values,
		}),
	}, nil
}

type CliDev struct {
	Version string
	Tag     string

	Go *dagger.Go // +private
}

// Build the dagger CLI binary for a single platform
func (cli CliDev) Binary(
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
func (cli CliDev) Reference(
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

// Build dev CLI binaries
// TODO: remove this
func (cli *CliDev) DevBinaries(
	// +optional
	platform dagger.Platform,
) *dagger.Directory {
	p := platforms.MustParse(string(platform))
	bin := cli.Binary(platform)
	binName := "dagger"
	if p.OS == "windows" {
		binName += ".exe"
	}
	dir := dag.Directory().WithFile(binName, bin)
	if p.OS != "linux" {
		p2 := p
		p2.OS = "linux"
		p2.OSFeatures = nil
		p2.OSVersion = ""
		dir = dir.WithFile("dagger-linux", cli.Binary(dagger.Platform(platforms.Format(p2))))
	}
	return dir
}
