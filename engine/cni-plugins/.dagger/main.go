package main

import (
	"dagger/cni-plugins/internal/dagger"
)

func New(
	// Base build environment
	// +optional
	base *dagger.Container,

	// Version of CNI to build plugins from
	// +optional
	// +default="v1.5.0"
	version string,
) CniPlugins {
	return CniPlugins{
		Base:    base,
		Version: version,
	}
}

type CniPlugins struct {
	Base    *dagger.Container // +private
	Version string            // +private
}

// Build the CNI plugins needed by the Dagger Engine
// We build the plugins from source to enable upgrades to Go and other dependencies that
// can contain CVEs in the builds on github releases
func (cni CniPlugins) Build() *dagger.Directory {
	src := dag.Git("github.com/containernetworking/plugins").Tag(cni.Version).Tree()
	return dag.Go(src, dagger.GoOpts{
		Base: cni.Base,
	}).
		Build(dagger.GoBuildOpts{
			Pkgs: []string{
				"./plugins/main/bridge",
				"./plugins/main/loopback",
				"./plugins/meta/firewall",
				"./plugins/ipam/host-local",
			},
			NoSymbols: true,
			NoDwarf:   true,
		}).
		Directory("./bin")
}
