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
	//   "!internal/cmd/dagger/**",
	//   "!**/go.sum",
	//   "!**/go.mod",
	//   "!**/*.go",
	//   "!**/VERSION",
	//   "!vendor/**/*",
	//   "!**.graphql",
	//   "!.changes",
	//   "!LICENSE",
	//   "!install.sh",
	//   "!install.ps1",
	//   "!**/*.sql",
	//   "!core/prompts/*.md"
	// ]
	source *dagger.Directory,

	// Base image for go build environment
	// +optional
	base *dagger.Container,

	// Version of the Dagger CLI being built. Surfaced as CliDev.Version and
	// consumed by the publish flow (goreleaser ENGINE_VERSION, S3 paths,
	// semver release-gating). The built binary self-reports its own version
	// from the embedded internal/version/VERSION file regardless of what's
	// passed here; this is for publish-time metadata only.
	// +optional
	version string,

	// Workspace forwarded to the go toolchain to stamp the CLI's VCS info.
	// Auto-injected when cli-dev is called directly; when it's a dependency
	// (e.g. of release) the caller must forward it.
	// +optional
	ws *dagger.Workspace,
) (*CliDev, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// FIXME: this go builder config is duplicated with engine build
	// move into a shared engine/builder module
	var values []string
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
			// Dependencies don't inherit the workspace; forward it explicitly.
			Ws: ws,
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
