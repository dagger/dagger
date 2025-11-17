// Build container images from apk packages.
package main

import (
	"context"
	"dagger/apko/internal/dagger"
	"strings"
	"time"
)

// defaultImageRepository is used when no container is specified.
const defaultImageRepository = "cgr.dev/chainguard/apko"

const cachePath = "/work/cache"

type Apko struct {
	// +private
	Container *dagger.Container

	// +private
	RegistryConfig *dagger.RegistryConfig
}

func New(
	// Custom container to use as a base container.
	//
	// +optional
	container *dagger.Container,

	// Disable mounting a default cache volume.
	//
	// +optional
	withoutCache bool,
) *Apko {
	if container == nil {
		container = dag.Container().From(defaultImageRepository)
	}

	if !withoutCache {
		container = container.WithMountedCache(cachePath, dag.CacheVolume("apko"))
	}

	container = container.WithWorkdir("/work/output")

	return &Apko{
		Container:      container,
		RegistryConfig: dag.RegistryConfig(),
	}
}

// use container for actions that need registry credentials
func (m *Apko) container() *dagger.Container {
	return m.Container.With(func(c *dagger.Container) *dagger.Container {
		return c.WithMountedSecret("/root/.docker/config.json", m.RegistryConfig.Secret())
	})

	// return m.Container.With(m.RegistryConfig.SecretMount("/root/.docker/config.json").Mount)
}

// Mount a cache volume for apk cache.
func (m *Apko) WithCache(
	cache *dagger.CacheVolume,

	// Identifier of the directory to use as the cache volume's root.
	//
	// +optional
	source *dagger.Directory,

	// Sharing mode of the cache volume.
	//
	// +optional
	sharing dagger.CacheSharingMode,
) *Apko {
	return &Apko{
		Container: m.Container.WithMountedCache(
			cachePath,
			cache,
			dagger.ContainerWithMountedCacheOpts{
				Source:  source,
				Sharing: sharing,
			},
		),
	}
}

// Add credentials for a registry.
func (m *Apko) WithRegistryAuth(address string, username string, secret *dagger.Secret) *Apko {
	m.RegistryConfig = m.RegistryConfig.WithRegistryAuth(address, username, secret)

	return m
}

// Removes credentials for a registry.
func (m *Apko) WithoutRegistryAuth(address string) *Apko {
	m.RegistryConfig = m.RegistryConfig.WithoutRegistryAuth(address)

	return m
}

type buildAndPublishArgs struct {
	// OCI annotations to add. Separate with colon (key:value).
	annotations []string

	// Architectures to build for (e.g., x86_64,ppc64le,arm64) -- default is all, unless specified in config. Can also use 'host' to indicate arch of host this is running on.
	arch []string

	// Date used for the timestamps of the files inside the image in RFC3339 format.
	buildDate string

	// Path to extra keys to include in the keyring.
	keyringAppend []string

	// Do not use network to fetch packages (cache must be pre-populated).
	offline bool

	// Extra packages to include.
	packageAppend []string

	// Path to extra repositories to include.
	repositoryAppend []string

	// Detect and embed VCS URLs.
	vcs bool
}

func (o buildAndPublishArgs) Process(args []string) []string {
	if len(o.annotations) > 0 {
		args = append(args, "--annotations", strings.Join(o.annotations, ","))
	}

	if len(o.arch) > 0 {
		args = append(args, "--arch", strings.Join(o.arch, ","))
	}

	if o.buildDate != "" {
		args = append(args, "--build-date", o.buildDate)
	}

	if len(o.keyringAppend) > 0 {
		args = append(args, "--keyring-append", strings.Join(o.keyringAppend, ","))
	}

	if o.offline {
		args = append(args, "--offline")
	}

	if len(o.repositoryAppend) > 0 {
		args = append(args, "--repository-append", strings.Join(o.repositoryAppend, ","))
	}

	if len(o.packageAppend) > 0 {
		args = append(args, "--package-append", strings.Join(o.packageAppend, ","))
	}

	if !o.vcs {
		args = append(args, "--vcs=false")
	}
	return args
}

// Build an image from a YAML configuration file.
func (m *Apko) Build(
	// Configuration file.
	config *dagger.File,

	// Image tag.
	tag string,

	// A .lock.json file (e.g. produced by apko lock) that constraints versions of packages to the listed ones.
	//
	// +optional
	lockfile *dagger.File,

	// OCI annotations to add. Separate with colon (key:value).
	//
	// +optional
	annotations []string,

	// Architectures to build for (e.g., x86_64,ppc64le,arm64) -- default is all, unless specified in config. Can also use 'host' to indicate arch of host this is running on.
	//
	// +optional
	arch []string,

	// Date used for the timestamps of the files inside the image in RFC3339 format.
	//
	// +optional
	buildDate string,

	// Path to extra keys to include in the keyring.
	//
	// +optional
	keyringAppend []string,

	// Do not use network to fetch packages (cache must be pre-populated).
	//
	// +optional
	offline bool,

	// Extra packages to include.
	//
	// +optional
	packageAppend []string,

	// Path to extra repositories to include.
	//
	// +optional
	repositoryAppend []string,

	// TODO: add sbom options

	// Detect and embed VCS URLs.
	//
	// +optional
	// +default=true
	vcs bool,
) *BuildResult {
	args := []string{
		"apko",
		"build",
		"/work/config.yaml", tag, "image.tar",

		"--cache-dir", cachePath,
	}

	container := m.Container.WithMountedFile("/work/config.yaml", config)

	if lockfile != nil {
		container = container.WithMountedFile("/work/config.lock.json", lockfile)
		args = append(args, "--lockfile", "/work/config.lock.json")
	}

	commonArgs := buildAndPublishArgs{
		annotations:      annotations,
		arch:             arch,
		buildDate:        buildDate,
		keyringAppend:    keyringAppend,
		offline:          offline,
		packageAppend:    packageAppend,
		repositoryAppend: repositoryAppend,
		vcs:              vcs,
	}

	args = commonArgs.Process(args)

	return &BuildResult{
		File: container.WithExec(args).File("/work/output/image.tar"),
		Tag:  tag,
	}
}

type BuildResult struct {
	File *dagger.File
	Tag  string
}

// Import the image into a container.
func (m *BuildResult) AsContainer() *dagger.Container {
	return dag.Container().Import(m.File)
}

// Publish a built image from a YAML configuration file.
func (m *Apko) Publish(
	ctx context.Context,

	// Configuration file.
	config *dagger.File,

	// Image tag.
	tag string,

	// OCI annotations to add. Separate with colon (key:value).
	//
	// +optional
	annotations []string,

	// Architectures to build for (e.g., x86_64,ppc64le,arm64) -- default is all, unless specified in config. Can also use 'host' to indicate arch of host this is running on.
	//
	// +optional
	arch []string,

	// Date used for the timestamps of the files inside the image in RFC3339 format.
	//
	// +optional
	buildDate string,

	// Path to extra keys to include in the keyring.
	//
	// +optional
	keyringAppend []string,

	// Do not use network to fetch packages (cache must be pre-populated).
	//
	// +optional
	offline bool,

	// Extra packages to include.
	//
	// +optional
	packageAppend []string,

	// Path to extra repositories to include.
	//
	// +optional
	repositoryAppend []string,

	// TODO: add sbom options

	// Detect and embed VCS URLs.
	//
	// +optional
	// +default=true
	vcs bool,
) error {
	args := []string{
		"apko",
		"publish",
		"/work/config.yaml", tag,

		"--cache-dir", cachePath,
	}

	container := m.container().
		WithMountedFile("/work/config.yaml", config)

	commonArgs := buildAndPublishArgs{
		annotations:      annotations,
		arch:             arch,
		buildDate:        buildDate,
		keyringAppend:    keyringAppend,
		offline:          offline,
		packageAppend:    packageAppend,
		repositoryAppend: repositoryAppend,
		vcs:              vcs,
	}

	args = commonArgs.Process(args)

	_, err := container.
		WithEnvVariable("CACHE_BUSTER", time.Now().Format(time.RFC3339Nano)).
		WithExec(args).
		Sync(ctx)

	return err
}
