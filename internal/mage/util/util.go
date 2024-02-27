package util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
			"**/.DS_Store",

			// node
			"**/node_modules",

			// python
			"**/__pycache__",
			"**/.venv",
			"**/.mypy_cache",
			"**/.pytest_cache",
			"**/.ruff_cache",
			"sdk/python/dist",

			// go
			// go.work is ignored so that you can use ../foo during local dev and let
			// this exclude rule reflect what the PR would run with, as a reminder to
			// actually bump dependencies
			"go.work",
			"go.work.sum",

			// rust
			"**/target",

			// elixir
			"**/deps",
			"**/cover",
			"**/_build",
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

			// modules
			"**/go.mod",
			"**/go.sum",

			// embedded files
			"**/*.tmpl",
			"**/*.ts.gtpl",
			"**/*.graphqls",
			"**/*.graphql",

			// misc
			".golangci.yml",
			"**/README.md", // needed for examples test
			"**/help.txt",  // needed for linting module bootstrap code
			"sdk/go/codegen/generator/typescript/templates/src/testdata/**/*",
			"core/integration/testdata/**/*",

			// Go SDK runtime codegen
			"**/dagger.json",
		},
		Exclude: []string{
			".git",
		},
	})
}

func goBase(c *dagger.Client) *dagger.Container {
	repo := RepositoryGoCodeOnly(c)

	return c.Container().
		From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion)).
		// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
		WithExec([]string{"apk", "add", "build-base"}).
		WithEnvVariable("CGO_ENABLED", "0").
		// adding the git CLI to inject vcs info
		// into the go binaries
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithDirectory("/app", repo, dagger.ContainerWithDirectoryOpts{
			Include: []string{"**/go.mod", "**/go.sum"},
		}).
		WithMountedCache("/go/pkg/mod", c.CacheVolume("go-mod")).
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
	return goBase(c)
}

func PlatformDaggerBinary(c *dagger.Client, goos, goarch, goarm string, version string) *dagger.File {
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

	ldflags := []string{
		"-s", "-w",
		"-X", "github.com/dagger/dagger/engine.Version=" + version,
	}
	return base.
		WithExec(
			[]string{
				"go", "build",
				"-o", "./bin/dagger",
				"-ldflags", strings.Join(ldflags, " "),
				"./cmd/dagger",
			},
		).
		File("./bin/dagger")
}

// DaggerBinary returns a compiled dagger binary
func DaggerBinary(c *dagger.Client, version string) *dagger.File {
	return PlatformDaggerBinary(c, "", "", "", version)
}

// DevelDaggerBinary returns a compiled dagger binary with the devel version
func DevelDaggerBinary(ctx context.Context, c *dagger.Client) (*dagger.File, error) {
	info, err := DevelVersionInfo(ctx, c)
	if err != nil {
		return nil, err
	}
	return PlatformDaggerBinary(c, "", "", "", info.EngineVersion()), nil
}

// HostDaggerBinary returns a dagger binary compiled to target the host's OS+arch
func HostDaggerBinary(c *dagger.Client, version string) *dagger.File {
	var goarm string
	if runtime.GOARCH == "arm" {
		goarm = "7" // not always correct but not sure of better way right now
	}
	return PlatformDaggerBinary(c, runtime.GOOS, runtime.GOARCH, goarm, version)
}

// CodegenBinary returns a binary for generating the Go and TypeScript SDKs.
func CodegenBinary(c *dagger.Client) *dagger.File {
	return goBase(c).
		WithExec([]string{"go", "build", "-o", "./bin/codegen", "-ldflags", "-s -w", "./cmd/codegen"}).
		File("./bin/codegen")
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

// HostVar is a chainable util for setting an env var from the host in a container.
func HostVar(c *dagger.Client, name string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithEnvVariable(name, GetHostEnv(name))
	}
}

// HostSecretVar is a chainable util for setting a secret env var from the host in a container.
func HostSecretVar(c *dagger.Client, name string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithSecretVariable(name, c.SetSecret(name, GetHostEnv(name)))
	}
}

// GetHostEnv is like os.Getenv but ensures that the env var is set.
func GetHostEnv(name string) string {
	value, ok := os.LookupEnv(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "env var %s must be set\n", name)
		os.Exit(1)
	}
	return value
}

func ShellCmd(cmd string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithExec([]string{"sh", "-c", cmd})
	}
}

func ShellCmds(cmds ...string) dagger.WithContainerFunc {
	return ShellCmd(strings.Join(cmds, " && "))
}

type VersionInfo struct {
	Tag      string
	Commit   string
	TreeHash string
}

func (info VersionInfo) EngineVersion() string {
	if info.Tag != "" {
		return info.Tag
	}
	if info.Commit != "" {
		return info.Commit
	}
	return info.TreeHash
}

func DevelVersionInfo(ctx context.Context, c *dagger.Client) (*VersionInfo, error) {
	base := c.Container().
		From(fmt.Sprintf("alpine:%s", alpineVersion)).
		WithExec([]string{"apk", "add", "git"}).
		WithMountedDirectory("/app/.git", c.Host().Directory(".git")).
		WithWorkdir("/app")

	info := &VersionInfo{}

	// use git write-tree to get a content hash of the current state of the repo
	var err error
	info.TreeHash, err = base.
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "write-tree"}).
		Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tree hash: %w", err)
	}
	info.TreeHash = strings.TrimSpace(info.TreeHash)

	return info, nil
}
