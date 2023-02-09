package util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"dagger.io/dagger"
)

const (
	EngineContainerName = "dagger-engine.dev"
)

// Repository with common set of exclude filters to speed up upload
func Repository(c *dagger.Client) *dagger.Directory {
	return c.Host().Directory(".", dagger.HostDirectoryOpts{
		Exclude: []string{
			".git",
			"bin",

			// node
			"**/node_modules",

			// python
			"**/__pycache__",
			"**/.venv",
			"**/.mypy_cache",
			"**/.pytest_cache",
		},
	})
}

// RepositoryGoCodeOnly is Repository, filtered to only contain Go code.
//
// NOTE: this function is a shared util ONLY because it's used both by the Engine
// and the Go SDK. Other languages shouldn't have a common helper.
func RepositoryGoCodeOnly(c *dagger.Client) *dagger.Directory {
	return c.Directory().WithDirectory("/", Repository(c), dagger.DirectoryWithDirectoryOpts{
		Include: []string{
			// go source
			"**/*.go",

			// git since we need the vcs buildinfo
			".git",

			// modules
			"**/go.mod",
			"**/go.sum",
			"**/go.work",
			"**/go.work.sum",

			// embedded files
			"**/*.go.tmpl",
			"**/*.ts.tmpl",
			"**/*.graphqls",
			"**/*.graphql",

			// misc
			".golangci.yml",
			"**/README.md", // needed for examples test
		},
	})
}

func AdvertiseDevEngine(c *dagger.Client, ctr *dagger.Container) *dagger.Container {
	// the cli bin is statically linked, can just mount it in anywhere
	dockerCli := c.Container().From("docker:cli").File("/usr/local/bin/docker")

	cliBinPath := "/.dagger-cli"
	return ctr.
		// Mount in the docker cli + socket, this will be used to connect to the dev engine
		// container
		WithUnixSocket("/var/run/docker.sock", c.Host().UnixSocket("/var/run/docker.sock")).
		WithMountedFile("/usr/bin/docker", dockerCli).
		// Also mount in the engine session binary.
		// FIXME: this shouldn't be necessary, but provisioning the engine session binary
		// with a mounted in docker socket doesn't work (always results in an empty file
		// even though the docker run command succeeds). This will be fixed by switching
		// to provisioning via downloading the CLI.
		WithMountedFile(cliBinPath, DaggerBinary(c)).
		// Point the SDKs to use the dev engine via these env vars
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-container://"+EngineContainerName)
}

func goBase(c *dagger.Client) *dagger.Container {
	repo := RepositoryGoCodeOnly(c)

	// Create a directory containing only `go.{mod,sum}` files.
	goMods := c.Directory()
	for _, f := range []string{"go.mod", "go.sum", "sdk/go/go.mod", "sdk/go/go.sum"} {
		goMods = goMods.WithFile(f, repo.File(f))
	}

	return c.Container().
		From("golang:1.20.0-alpine").
		// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
		Exec(dagger.ContainerExecOpts{Args: []string{"apk", "add", "build-base"}}).
		WithEnvVariable("CGO_ENABLED", "0").
		// adding the git CLI to inject vcs info
		// into the go binaries
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithMountedDirectory("/app", goMods).
		WithExec([]string{"go", "mod", "download"}).
		// run `go build` with all source
		WithMountedDirectory("/app", repo).
		// include a cache for go build
		WithMountedCache("/root/.cache/go-build", c.CacheVolume("go-build"))
}

// GoBase is a standardized base image for running Go, cache optimized for the layout
// of this repository
//
// NOTE: this function is a shared util ONLY because it's used both by the Engine
// and the Go SDK. Other languages shouldn't have a common helper.
func GoBase(c *dagger.Client) *dagger.Container {
	return AdvertiseDevEngine(c, goBase(c))
}

func PlatformDaggerBinary(c *dagger.Client, goos, goarch, goarm string) *dagger.File {
	base := goBase(c)
	if goos != "" {
		base = base.WithEnvVariable("GOOS", goos)
	}
	if goarch != "" {
		base = base.WithEnvVariable("GOARCH", goarch)
	}
	if goarm != "" {
		base = base.WithEnvVariable("GOARM", goarm)
	}
	return base.
		WithExec([]string{"go", "build", "-o", "./bin/dagger", "-ldflags", "-s -w", "./cmd/dagger"}).
		File("./bin/dagger")
}

// DaggerBinary returns a compiled dagger binary
func DaggerBinary(c *dagger.Client) *dagger.File {
	return PlatformDaggerBinary(c, "", "", "")
}

// HostDaggerBinary returns a dagger binary compiled to target the host's OS+arch
func HostDaggerBinary(c *dagger.Client) *dagger.File {
	var goarm string
	if runtime.GOARCH == "arm" {
		goarm = "7" // not always correct but not sure of better way right now
	}
	return PlatformDaggerBinary(c, runtime.GOOS, runtime.GOARCH, goarm)
}

// ClientGenBinary returns a compiled dagger binary
func ClientGenBinary(c *dagger.Client) *dagger.File {
	return goBase(c).
		WithExec([]string{"go", "build", "-o", "./bin/client-gen", "-ldflags", "-s -w", "./cmd/client-gen"}).
		File("./bin/client-gen")
}

// HostDockerCredentials returns the host's ~/.docker dir if it exists, otherwise just an empty dir
func HostDockerDir(c *dagger.Client) *dagger.Directory {
	if runtime.GOOS != "linux" {
		// doesn't work on darwin, untested on windows
		return c.Directory()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return c.Directory()
	}
	path := filepath.Join(home, ".docker")
	if _, err := os.Stat(path); err != nil {
		return c.Directory()
	}
	return c.Host().Directory(path)
}

func WithSetHostVar(ctx context.Context, h *dagger.Host, varName string) *dagger.HostVariable {
	hv := h.EnvVariable(varName)
	if val, _ := hv.Secret().Plaintext(ctx); val == "" {
		fmt.Fprintf(os.Stderr, "env var %s must be set", varName)
		os.Exit(1)
	}
	return hv
}
