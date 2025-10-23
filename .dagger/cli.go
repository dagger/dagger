package main

import (
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// Develop the Dagger CLI
func (dev *DaggerDev) CLI() *CLI {
	return &CLI{Dagger: dev}
}

type CLI struct {
	Dagger *DaggerDev // +private
}

// Build the CLI binary
func (cli *CLI) Binary(
	// +optional
	runnerHost string,
	// +optional
	platform dagger.Platform,
) *dagger.File {
	return dag.DaggerCli(dagger.DaggerCliOpts{RunnerHost: runnerHost}).
		Binary(dagger.DaggerCliBinaryOpts{Platform: platform})
}

// Build dev CLI binaries
// TODO: remove this
func (cli *CLI) DevBinaries(
	// +optional
	runnerHost string,
	// +optional
	platform dagger.Platform,
) *dagger.Directory {
	p := platforms.MustParse(string(platform))
	c := dag.DaggerCli(dagger.DaggerCliOpts{RunnerHost: runnerHost})

	bin := c.Binary(dagger.DaggerCliBinaryOpts{Platform: platform})
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
		dir = dir.WithFile("dagger-linux", c.Binary(dagger.DaggerCliBinaryOpts{Platform: dagger.Platform(platforms.Format(p2))}))
	}

	return dir
}
