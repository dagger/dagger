package util

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"dagger.io/dagger"
)

// Repository with common set of exclude filters to speed up upload
func Repository(c *dagger.Client) *dagger.Directory {
	return c.Host().Workdir(dagger.HostWorkdirOpts{
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

	return c.Container().
		From("golang:1.19-alpine").
		WithEnvVariable("CGO_ENABLED", "0").
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithMountedDirectory("/app", goMods).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "mod", "download"},
		}).
		// run `go build` with all source
		WithMountedDirectory("/app", repo)
}

// DaggerBinary returns a compiled dagger binary
func DaggerBinary(c *dagger.Client) *dagger.File {
	return GoBase(c).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "build", "-o", "./bin/cloak", "-ldflags", "-s -w", "./cmd/cloak"},
		}).
		File("./bin/cloak")
}

const (
	engineSessionBin = "dagger-engine-session"
	buildkitRepo     = "github.com/moby/buildkit"
	buildkitBranch   = "v0.10.5"
)

func DevEngineContainer(c *dagger.Client, arches, oses []string) []*dagger.Container {
	buildkitRepo := c.Git(buildkitRepo).Branch(buildkitBranch).Tree()

	platformVariants := make([]*dagger.Container, 0, len(arches))
	for _, arch := range arches {
		buildkitBase := c.Container(dagger.ContainerOpts{
			Platform: dagger.Platform("linux/" + arch),
		}).Build(buildkitRepo)
		for _, os := range oses {
			// include each engine-session bin for each arch too in case there is a
			// client/server mismatch
			for _, arch := range arches {
				builtBin := GoBase(c).
					WithEnvVariable("GOOS", os).
					WithEnvVariable("GOARCH", arch).
					Exec(dagger.ContainerExecOpts{
						Args: []string{"go", "build", "-o", "./bin/" + engineSessionBin, "-ldflags", "-s -w", "/app/cmd/engine-session"},
					}).
					File("./bin/" + engineSessionBin)
				buildkitBase = buildkitBase.WithFS(
					buildkitBase.FS().WithFile("/usr/bin/"+engineSessionBin+"-"+os+"-"+arch, builtBin),
				)
			}
		}
		platformVariants = append(platformVariants, buildkitBase)
	}

	return platformVariants
}

var devEngineOnce sync.Once
var devEngineContainerName string
var devEngineErr error

func DevEngine(ctx context.Context, c *dagger.Client) (string, error) {
	devEngineOnce.Do(func() {
		tmpfile, err := os.CreateTemp("", "dagger-engine-export")
		if err != nil {
			devEngineErr = err
			return
		}
		defer os.Remove(tmpfile.Name())

		_, err = c.Container().Export(ctx, tmpfile.Name(), dagger.ContainerExportOpts{
			PlatformVariants: DevEngineContainer(c, []string{runtime.GOARCH}, []string{runtime.GOOS}),
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

	otherClient, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	return cb(ctx, otherClient)
}
