package mage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
)

type Engine mg.Namespace

// Build builds the engine binary
func (t Engine) Build(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	build := util.GoBase(c).
		WithEnvVariable("GOOS", runtime.GOOS).
		WithEnvVariable("GOARCH", runtime.GOARCH).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "build", "-o", "./bin/cloak", "-ldflags", "-s -w", "/app/cmd/cloak"},
		})

	_, err = build.Directory("./bin").Export(ctx, "./bin")
	return err
}

// Lint lints the engine
func (t Engine) Lint(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	_, err = c.Container().
		From("golangci/golangci-lint:v1.48").
		WithMountedDirectory("/app", util.RepositoryGoCodeOnly(c)).
		WithWorkdir("/app").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"golangci-lint", "run", "-v", "--timeout", "5m"},
		}).ExitCode(ctx)
	return err
}

const (
	sdkHelper      = "dagger-sdk-helper"
	buildkitRepo   = "github.com/moby/buildkit"
	buildkitBranch = "v0.10.5"
	// TODO: placeholder until real one exists
	engineImageRef = "eriksipsma/dagger-test:bootstrap"
)

func engineContainer(c *dagger.Client, arches, oses []string) []*dagger.Container {
	buildkitRepo := c.Git(buildkitRepo).Branch(buildkitBranch).Tree()

	platformVariants := make([]*dagger.Container, 0, len(arches))
	for _, arch := range arches {
		buildkitBase := c.Container(dagger.ContainerOpts{
			Platform: dagger.Platform("linux/" + arch),
		}).Build(buildkitRepo)
		for _, os := range oses {
			// include each helper for each arch too in case there is a
			// client/server mismatch
			for _, arch := range arches {
				helperBin := util.GoBase(c).
					WithEnvVariable("GOOS", os).
					WithEnvVariable("GOARCH", arch).
					Exec(dagger.ContainerExecOpts{
						Args: []string{"go", "build", "-o", "./bin/" + sdkHelper, "-ldflags", "-s -w", "/app/cmd/sdk-helper"},
					}).
					File("./bin/" + sdkHelper)
				buildkitBase = buildkitBase.WithFS(
					buildkitBase.FS().WithFile("/usr/bin/"+sdkHelper+"-"+os+"-"+arch, helperBin),
				)
			}
		}
		platformVariants = append(platformVariants, buildkitBase)
	}

	return platformVariants
}

func (t Engine) Publish(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	arches := []string{"amd64", "arm64"}
	oses := []string{"linux", "darwin"}

	imageRef, err := c.Container().Publish(ctx, engineImageRef, dagger.ContainerPublishOpts{
		PlatformVariants: engineContainer(c, arches, oses),
	})
	if err != nil {
		return err
	}
	fmt.Println("Image published:", imageRef)

	return nil
}

func localEngine(ctx context.Context, c *dagger.Client) (string, error) {
	tmpfile, err := os.CreateTemp("", "dagger-engine-export")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpfile.Name())

	_, err = c.Container().Export(ctx, tmpfile.Name(), dagger.ContainerExportOpts{
		PlatformVariants: engineContainer(c, []string{runtime.GOARCH}, []string{runtime.GOOS}),
	})
	if err != nil {
		return "", err
	}

	containerName := "test-dagger-engine"
	volumeName := "test-dagger-engine"
	imageName := "localhost/test-dagger-engine:latest"

	// #nosec
	loadCmd := exec.CommandContext(ctx, "docker", "load", "-i", tmpfile.Name())
	output, err := loadCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker load failed: %w: %s", err, output)
	}
	_, imageID, ok := strings.Cut(string(output), "sha256:")
	if !ok {
		return "", fmt.Errorf("unexpected output from docker load: %s", output)
	}
	imageID = strings.TrimSpace(imageID)

	if output, err := exec.CommandContext(ctx, "docker",
		"tag",
		imageID,
		imageName,
	).CombinedOutput(); err != nil {
		return "", fmt.Errorf("docker tag: %w: %s", err, output)
	}

	if output, err := exec.CommandContext(ctx, "docker",
		"rm",
		"-fv",
		containerName,
	).CombinedOutput(); err != nil {
		return "", fmt.Errorf("docker rm: %w: %s", err, output)
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
		return "", fmt.Errorf("docker run: %w: %s", err, output)
	}

	return containerName, nil
}

func (t Engine) Dev(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	containerName, err := localEngine(ctx, c)
	if err != nil {
		return err
	}

	fmt.Println("export DAGGER_HOST=docker-container://" + containerName)
	return nil
}

func (t Engine) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	containerName, err := localEngine(ctx, c)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "go", "test", "-v", "-count=1", "./...")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "DAGGER_HOST=docker-container://"+containerName)
	return cmd.Run()
}
