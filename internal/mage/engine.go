package mage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/sdk"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
	"golang.org/x/mod/semver"
)

const (
	daggerCliBinName = "dagger"
	shimBinName      = "dagger-shim"
	buildkitRepo     = "github.com/moby/buildkit"
	buildkitBranch   = "v0.11.0-rc3"

	engineTomlPath = "/etc/dagger/engine.toml"
	// NOTE: this needs to be consistent with DefaultStateDir in internal/engine/docker.go
	engineDefaultStateDir = "/var/lib/dagger"
)

var engineToml = fmt.Sprintf("root = %q\n", engineDefaultStateDir)

func parseRef(tag string) error {
	if tag == "main" {
		return nil
	}
	if ok := semver.IsValid(tag); !ok {
		return fmt.Errorf("invalid semver tag: %s", tag)
	}
	return nil
}

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
		WithExec([]string{"go", "build", "-o", "./bin/dagger", "-ldflags", "-s -w", "/app/cmd/dagger"})

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
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
		ExitCode(ctx)
	return err
}

// Publish builds and pushes Engine OCI image to a container registry
func (t Engine) Publish(ctx context.Context, version string) error {
	if err := parseRef(version); err != nil {
		return err
	}

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	engineImage, err := util.WithSetHostVar(ctx, c.Host(), "DAGGER_ENGINE_IMAGE").Value(ctx)
	if err != nil {
		return err
	}
	ref := fmt.Sprintf("%s:%s", engineImage, version)

	arches := []string{"amd64", "arm64"}

	digest, err := c.Container().Publish(ctx, ref, dagger.ContainerPublishOpts{
		PlatformVariants: devEngineContainer(c, arches),
	})
	if err != nil {
		return err
	}

	if semver.IsValid(version) {
		sdks := sdk.All{}
		if err := sdks.Bump(ctx, version); err != nil {
			return err
		}
	} else {
		fmt.Printf("'%s' is not a semver version, skipping image bump in SDKs", version)
	}

	time.Sleep(3 * time.Second) // allow buildkit logs to flush, to minimize potential confusion with interleaving
	fmt.Println("PUBLISHED IMAGE REF:", digest)

	return nil
}

func (t Engine) test(ctx context.Context, race bool) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	cgoEnabledEnv := "0"
	args := []string{"go", "test", "-p", "16", "-v", "-count=1"}
	if race {
		args = append(args, "-race", "-timeout=1h")
		cgoEnabledEnv = "1"
	}
	args = append(args, "./...")

	output, err := util.GoBase(c).
		WithMountedDirectory("/app", util.Repository(c)). // need all the source for extension tests
		WithWorkdir("/app").
		WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
		WithMountedDirectory("/root/.docker", util.HostDockerDir(c)).
		WithExec(args).
		Stdout(ctx)
	if err != nil {
		return err
	}
	fmt.Println(output)
	return nil
}

// Test runs Engine tests
func (t Engine) Test(ctx context.Context) error {
	return t.test(ctx, false)
}

// TestRace runs Engine tests with go race detector enabled
func (t Engine) TestRace(ctx context.Context) error {
	return t.test(ctx, true)
}

func (t Engine) Dev(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	tmpfile, err := os.CreateTemp("", "dagger-engine-export")
	if err != nil {
		return err
	}
	defer os.Remove(tmpfile.Name())

	arches := []string{runtime.GOARCH}

	_, err = c.Container().Export(ctx, tmpfile.Name(), dagger.ContainerExportOpts{
		PlatformVariants: devEngineContainer(c, arches),
	})
	if err != nil {
		return err
	}

	volumeName := "test-dagger-engine"
	imageName := "localhost/test-dagger-engine:latest"

	// #nosec
	loadCmd := exec.CommandContext(ctx, "docker", "load", "-i", tmpfile.Name())
	output, err := loadCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker load failed: %w: %s", err, output)
	}
	_, imageID, ok := strings.Cut(string(output), "sha256:")
	if !ok {
		return fmt.Errorf("unexpected output from docker load: %s", output)
	}
	imageID = strings.TrimSpace(imageID)

	if output, err := exec.CommandContext(ctx, "docker",
		"tag",
		imageID,
		imageName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("docker tag: %w: %s", err, output)
	}

	if output, err := exec.CommandContext(ctx, "docker",
		"rm",
		"-fv",
		util.TestContainerName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("docker rm: %w: %s", err, output)
	}

	if output, err := exec.CommandContext(ctx, "docker",
		"run",
		"-d",
		"--rm",
		"-v", volumeName+":"+engineDefaultStateDir,
		"--name", util.TestContainerName,
		"--privileged",
		imageName,
		"--debug",
	).CombinedOutput(); err != nil {
		return fmt.Errorf("docker run: %w: %s", err, output)
	}

	// build the CLI and export locally so it can be used to connect to the engine
	binDest := filepath.Join(os.Getenv("DAGGER_SRC_ROOT"), "bin", "dagger")
	_, err = util.HostDaggerBinary(c).Export(ctx, binDest)
	if err != nil {
		return err
	}

	fmt.Println("export _EXPERIMENTAL_DAGGER_CLI_BIN=" + binDest)
	fmt.Println("export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://" + util.TestContainerName)
	return nil
}

func devEngineContainer(c *dagger.Client, arches []string) []*dagger.Container {
	buildkitRepo := c.Git(buildkitRepo, dagger.GitOpts{KeepGitDir: true}).Branch(buildkitBranch).Tree()

	platformVariants := make([]*dagger.Container, 0, len(arches))
	for _, arch := range arches {
		buildkitBase := c.Container(dagger.ContainerOpts{
			Platform: dagger.Platform("linux/" + arch),
		}).Build(buildkitRepo)

		// build the shim binary
		shimBin := util.GoBase(c).
			WithEnvVariable("GOOS", "linux").
			WithEnvVariable("GOARCH", arch).
			WithExec([]string{
				"go", "build",
				"-o", "./bin/" + shimBinName,
				"-ldflags", "-s -w",
				"/app/cmd/shim",
			}).
			File("./bin/" + shimBinName)
		buildkitBase = buildkitBase.WithRootfs(
			buildkitBase.Rootfs().
				WithFile("/usr/bin/"+shimBinName, shimBin).
				WithNewFile(engineTomlPath, engineToml).
				WithNewDirectory(engineDefaultStateDir),
		)

		// setup entrypoint
		buildkitBase = buildkitBase.WithEntrypoint([]string{
			"buildkitd",
			"--config", engineTomlPath,
			"--oci-worker-binary", "/usr/bin/" + shimBinName,
		})

		platformVariants = append(platformVariants, buildkitBase)
	}

	return platformVariants
}
