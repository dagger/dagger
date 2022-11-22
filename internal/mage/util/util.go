package util

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

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

const (
	engineSessionBinName = "dagger-engine-session"
	shimBinName          = "dagger-shim"
	buildkitRepo         = "github.com/moby/buildkit"
	buildkitBranch       = "v0.10.5"
)

func DevEngineContainer(c *dagger.Client, arches, oses []string) []*dagger.Container {
	buildkitRepo := c.Git(buildkitRepo).Branch(buildkitBranch).Tree()

	platformVariants := make([]*dagger.Container, 0, len(arches))
	for _, arch := range arches {
		buildkitBase := c.Container(dagger.ContainerOpts{
			Platform: dagger.Platform("linux/" + arch),
		}).Build(buildkitRepo)

		// build engine-session bins
		for _, os := range oses {
			// include each engine-session bin for each arch too in case there is a
			// client/server mismatch
			for _, arch := range arches {
				// FIXME: bootstrap API doesn't support `WithExec`
				//nolint
				builtBin := GoBase(c).
					WithEnvVariable("GOOS", os).
					WithEnvVariable("GOARCH", arch).
					Exec(dagger.ContainerExecOpts{
						Args: []string{"go", "build", "-o", "./bin/" + engineSessionBinName, "-ldflags", "-s -w", "/app/cmd/engine-session"},
					}).
					File("./bin/" + engineSessionBinName)
				// FIXME: the code below is part of "bootstrap" and using the LATEST
				// released engine, which does not contain `WithRootfs`
				//nolint
				buildkitBase = buildkitBase.WithFS(
					buildkitBase.FS().WithFile("/usr/bin/"+engineSessionBinName+"-"+os+"-"+arch, builtBin),
				)
			}
		}

		// build the shim binary
		// FIXME: bootstrap API doesn't support `WithExec`
		//nolint
		shimBin := GoBase(c).
			WithEnvVariable("GOOS", "linux").
			WithEnvVariable("GOARCH", arch).
			Exec(dagger.ContainerExecOpts{
				Args: []string{"go", "build", "-o", "./bin/" + shimBinName, "-ldflags", "-s -w", "/app/cmd/shim"},
			}).
			File("./bin/" + shimBinName)
		//nolint
		buildkitBase = buildkitBase.WithFS(
			buildkitBase.FS().WithFile("/usr/bin/"+shimBinName, shimBin),
		)

		// setup entrypoint
		buildkitBase = buildkitBase.WithEntrypoint([]string{
			"buildkitd",
			"--oci-worker-binary", "/usr/bin/" + shimBinName,
		})

		platformVariants = append(platformVariants, buildkitBase)
	}

	return platformVariants
}

var (
	devEngineOnce          sync.Once
	devEngineContainerName string
	devEngineErr           error
)

func DevEngine(ctx context.Context, c *dagger.Client) (string, error) {
	devEngineOnce.Do(func() {
		tmpfile, err := os.CreateTemp("", "dagger-engine-export")
		if err != nil {
			devEngineErr = err
			return
		}
		defer os.Remove(tmpfile.Name())

		arches := []string{runtime.GOARCH}
		oses := []string{runtime.GOOS}
		if runtime.GOOS != "linux" {
			oses = append(oses, "linux")
		}

		_, err = c.Container().Export(ctx, tmpfile.Name(), dagger.ContainerExportOpts{
			PlatformVariants: DevEngineContainer(c, arches, oses),
		})
		if err != nil {
			devEngineErr = err
			return
		}

		containerName := "test-dagger-engine"
		volumeName := "test-dagger-engine"
		imageName := "localhost/test-dagger-engine:latest"

		// #nosec
		loadCmd := exec.CommandContext(ctx, "docker", "load", "-i", tmpfile.Name())
		output, err := loadCmd.CombinedOutput()
		if err != nil {
			devEngineErr = fmt.Errorf("docker load failed: %w: %s", err, output)
			return
		}
		_, imageID, ok := strings.Cut(string(output), "sha256:")
		if !ok {
			devEngineErr = fmt.Errorf("unexpected output from docker load: %s", output)
			return
		}
		imageID = strings.TrimSpace(imageID)

		if output, err := exec.CommandContext(ctx, "docker",
			"tag",
			imageID,
			imageName,
		).CombinedOutput(); err != nil {
			devEngineErr = fmt.Errorf("docker tag: %w: %s", err, output)
			return
		}

		if output, err := exec.CommandContext(ctx, "docker",
			"rm",
			"-fv",
			containerName,
		).CombinedOutput(); err != nil {
			devEngineErr = fmt.Errorf("docker rm: %w: %s", err, output)
			return
		}

		if output, err := exec.CommandContext(ctx, "docker",
			"run",
			"-d",
			"--rm",
			"-v", volumeName+":/var/lib/buildkit",
			"--name", containerName,
			"--privileged",
			imageName,
			"--debug",
		).CombinedOutput(); err != nil {
			devEngineErr = fmt.Errorf("docker run: %w: %s", err, output)
			return
		}
		devEngineContainerName = containerName
	})
	return devEngineContainerName, devEngineErr
}

func WithDevEngine(ctx context.Context, c *dagger.Client, cb func(context.Context, *dagger.Client) error) error {
	containerName, err := DevEngine(ctx, c)
	if err != nil {
		return err
	}

	// TODO: not thread safe.... only other option is to put dagger host in dagger.Client
	os.Setenv("DAGGER_HOST", "docker-container://"+containerName)
	defer os.Unsetenv("DAGGER_HOST")

	os.Setenv("DAGGER_RUNNER_HOST", "docker-container://"+containerName)
	defer os.Unsetenv("DAGGER_RUNNER_HOST")

	otherClient, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	return cb(ctx, otherClient)
}
