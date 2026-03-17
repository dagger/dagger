//
// Docusaurus Dagger Module
//
// This module allows you to run docusaurus sites locally with Dagger
// without needing to install any additional dependencies.
//
// Example Usage:
//
// `dagger call -m github.com/levlaz/daggerverse/docusaurus --dir "/src/docs" --src https://github.com/kpenfound/dagger#kyle/docs-239-convert-secrets` serve up
//
// The example above shows how to grab a remote git branch, the basic
// structure is https://github.com/$USER/$REPO#$BRANCH. The `src` argument can
// take a local directory, but this module becomes especially
// useful when you pass in remote git repos. In particular, imagine you are trying
// to preview a PR for a docs change. You can simply pass in the git branch from
// your fork and preview the docs without needing to install any local dependencies
// or have to remember how to fetch remote branches locally.
//

package main

import (
	"dagger/docusaurus/internal/dagger"
	"path/filepath"
)

func New(
	// The source directory of your docusaurus site, this can be a local directory or a remote git repo
	src *dagger.Directory,
	// Optional working directory if you need to execute docusaurus commands outside of your root
	// +optional
	// +default="."
	dir string,
	// Optional flag to disable cache
	// +optional
	// +default=false
	disableCache bool,
	// Optional cache volume name; this is useful if you work with multiple projects
	// or have node_dependencies that are rapidly changing to avoid issues with
	// npm having failures.
	// +optional
	// +default="node-docusaurus-docs"
	cacheVolumeName string,
	// Optional flag to use yarn instead of npm
	// +optional
	// +default=false
	yarn bool,
) *Docusaurus {
	return &Docusaurus{
		Src:             src,
		Dir:             dir,
		DisableCache:    disableCache,
		CacheVolumeName: cacheVolumeName,
		Yarn:            yarn,
	}
}

// Docusaurus
type Docusaurus struct {
	Src             *dagger.Directory
	Dir             string
	DisableCache    bool
	CacheVolumeName string
	Yarn            bool
}

// Return base container for running docusaurus with docs mounted and docusaurus
// dependencies installed.
func (m *Docusaurus) Base() *dagger.Container {
	ctr := dag.Container().
		From("node:lts-alpine").
		WithoutEntrypoint().
		WithMountedDirectory("/src", m.Src).
		WithWorkdir(filepath.Join("/src", m.Dir))

	if !m.DisableCache {
		ctr = ctr.
			WithMountedCache(
				"./node_modules/.cache",
				dag.CacheVolume(m.CacheVolumeName+"-cache"),
				dagger.ContainerWithMountedCacheOpts{
					Sharing: dagger.CacheSharingModePrivate,
				},
			).
			WithMountedCache(
				"/root/.npm/_cacache",
				dag.CacheVolume("npm-cache"),
			).
			WithMountedCache(
				"/usr/local/share/.cache/yarn",
				dag.CacheVolume("yarn-cache"),
			)
	}

	return ctr.
		WithExposedPort(3000).
		With(m.packageManagerInstall)
}

// Build production docs
func (m *Docusaurus) Build() *dagger.Directory {
	return m.Base().
		WithExec([]string{m.packageManager(), "run", "build"}).
		Directory("build")
}

// Serve production docs locally as a service
func (m *Docusaurus) Serve() *dagger.Service {
	return m.Base().
		WithExec([]string{m.packageManager(), "run", "build"}).
		WithExec([]string{m.packageManager(), "run", "serve", "--build"}).
		AsService()
}

// Build and serve development docs as a service
func (m *Docusaurus) ServeDev() *dagger.Service {
	return m.Base().
		WithExec([]string{m.packageManager(), "start", "--", "--host", "0.0.0.0"}).
		AsService()
}

func (m *Docusaurus) packageManager() string {
	if m.Yarn {
		return "yarn"
	}
	return "npm"
}

func (m *Docusaurus) packageManagerInstall(ctr *dagger.Container) *dagger.Container {
	if m.Yarn {
		return ctr.WithExec([]string{"yarn", "install", "--frozen-lockfile"})
	}
	return ctr.WithExec([]string{"npm", "ci"})
}
