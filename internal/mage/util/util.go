package util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"dagger.io/dagger"
)

// Repository with common set of exclude filters to speed up upload
func Repository(c *dagger.Client) *dagger.Directory {
	return c.Host().Directory(".", dagger.HostDirectoryOpts{
		Exclude: []string{
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
			"**/Dockerfile", // needed for shim TODO: just build shim directly
			"**/README.md",  // needed for examples test
		},
	})
}

// GoBase is a standardized base image for running Go, cache optimized for the layout
// of this repository
//
// NOTE: this function is a shared util ONLY because it's used both by the Engine
// and the Go SDK. Other languages shouldn't have a common helper.
func GoBase(c *dagger.Client) *dagger.Container {
	repo := RepositoryGoCodeOnly(c)

	// Create a directory containing only `go.{mod,sum}` files.
	goMods := c.Directory()
	for _, f := range []string{"go.mod", "go.sum", "sdk/go/go.mod", "sdk/go/go.sum"} {
		goMods = goMods.WithFile(f, repo.File(f))
	}

	// FIXME: bootstrap API doesn't support `WithExec`
	//nolint
	return c.Container().
		From("golang:1.19-alpine").
		// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
		Exec(dagger.ContainerExecOpts{Args: []string{"apk", "add", "build-base"}}).
		WithEnvVariable("CGO_ENABLED", "0").
		// adding the git CLI to inject vcs info
		// into the go binaries
		Exec(dagger.ContainerExecOpts{
			Args: []string{"apk", "add", "git"},
		}).
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithMountedDirectory("/app", goMods).
		Exec(dagger.ContainerExecOpts{Args: []string{"go", "mod", "download"}}).
		// run `go build` with all source
		WithMountedDirectory("/app", repo)
}

// DaggerBinary returns a compiled dagger binary
func DaggerBinary(c *dagger.Client) *dagger.File {
	return GoBase(c).
		WithExec([]string{"go", "build", "-o", "./bin/dagger", "-ldflags", "-s -w", "./cmd/dagger"}).
		File("./bin/dagger")
}

// ClientGenBinary returns a compiled dagger binary
func ClientGenBinary(c *dagger.Client) *dagger.File {
	return GoBase(c).
		WithExec([]string{"go", "build", "-o", "./bin/client-gen", "-ldflags", "-s -w", "./cmd/client-gen"}).
		File("./bin/client-gen")
}

func EngineSessionBinary(c *dagger.Client) *dagger.File {
	return GoBase(c).
		WithExec([]string{"go", "build", "-o", "./bin/dagger-engine-session", "-ldflags", "-s -w", "./cmd/engine-session"}).
		File("./bin/dagger-engine-session")
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
	if val, err := hv.Secret().Plaintext(ctx); err != nil || val == "" {
		fmt.Fprintf(os.Stderr, "env var %s is empty", varName)
		os.Exit(1)
	}
	return hv
}
