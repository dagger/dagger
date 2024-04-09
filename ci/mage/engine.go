package mage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/magefile/mage/mg"

	"github.com/dagger/dagger/ci/mage/util"
	"github.com/dagger/dagger/engine/distconsts"
)

type Engine mg.Namespace

var (
	publishedEnginePlatforms    = []string{"linux/amd64", "linux/arm64"}
	publishedGPUEnginePlatforms = []string{"linux/amd64"}
)

// Connect tests a connection to a Dagger Engine
func (t Engine) Connect(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	return c.Close()
}

// Lint lints the engine
func (t Engine) Lint(ctx context.Context) error {
	return util.DaggerCall(ctx, "engine", "lint")
}

// Lint lints the engine
func (t Engine) Scan(ctx context.Context) error {
	return util.DaggerCall(ctx, "engine", "scan")
}

// Publish builds and pushes Engine OCI image to a container registry
func (t Engine) Publish(ctx context.Context, version string) error {
	var commonArgs []string
	if v, ok := os.LookupEnv("DAGGER_ENGINE_IMAGE"); ok {
		commonArgs = append(commonArgs, "--image="+v)
	}
	if v, ok := os.LookupEnv("DAGGER_ENGINE_IMAGE_REGISTRY"); ok {
		commonArgs = append(commonArgs, "--registry="+v)
	}
	if v, ok := os.LookupEnv("DAGGER_ENGINE_IMAGE_USERNAME"); ok {
		commonArgs = append(commonArgs, "--registry-username="+v)
	}
	if _, ok := os.LookupEnv("DAGGER_ENGINE_IMAGE_PASSWORD"); ok {
		commonArgs = append(commonArgs, "--registry-password=env:DAGGER_ENGINE_IMAGE_PASSWORD")
	}

	args := []string{"engine", "publish", "--version=" + version}
	args = append(args, commonArgs...)
	for _, p := range publishedEnginePlatforms {
		args = append(args, "--platform="+p)
	}
	err := util.DaggerCall(ctx, args...)
	if err != nil {
		return err
	}

	args = []string{"engine", "with-gpusupport", "publish", "--version=" + version}
	args = append(args, commonArgs...)
	for _, p := range publishedGPUEnginePlatforms {
		args = append(args, "--platform="+p)
	}
	err = util.DaggerCall(ctx, args...)
	if err != nil {
		return err
	}

	return nil
}

// Verify that all arches for the Engine can be built. Just do a local export to avoid setting up
// a registry
func (t Engine) TestPublish(ctx context.Context) error {
	err := util.DaggerCall(ctx, "engine", "test-publish")
	if err != nil {
		return err
	}
	err = util.DaggerCall(ctx, "engine", "with-gpusupport", "test-publish")
	if err != nil {
		return err
	}
	return nil
}

// Test runs Engine tests
func (t Engine) Test(ctx context.Context) error {
	return t.test(ctx, "all")
}

// TestRace runs Engine tests with go race detector enabled
func (t Engine) TestRace(ctx context.Context) error {
	return t.test(ctx, "all", "--race=true")
}

// TestImportant runs Engine Container+Module tests, which give good basic coverage
// of functionality w/out having to run everything
func (t Engine) TestImportant(ctx context.Context) error {
	return t.test(ctx, "important", "--race=true")
}

func (t Engine) test(ctx context.Context, additional ...string) error {
	args := []string{"test"}
	if cfg, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_CACHE_CONFIG"); ok {
		args = append(args, "with-cache", "--config="+cfg)
	}
	args = append(args, additional...)
	return util.DaggerCall(ctx, args...)
}

// Dev builds and starts an Engine & CLI from local source code
func (t Engine) Dev(ctx context.Context) error {
	gpuSupport := false
	if v := os.Getenv(util.GPUSupportEnvName); v != "" {
		gpuSupport = true
	}

	args := []string{"engine"}
	if gpuSupport {
		args = append(args, "with-gpusupport")
	}
	tarPath := "./bin/engine.tar"
	args = append(args, "container", "export", "--path="+tarPath)
	args = append(args, "--forced-compression="+string(dagger.Gzip)) // use gzip to avoid incompatibility w/ older docker versions
	err := util.DaggerCall(ctx, args...)
	if err != nil {
		return err
	}

	volumeName := util.EngineContainerName
	imageName := fmt.Sprintf("localhost/%s:latest", util.EngineContainerName)

	// #nosec
	loadCmd := exec.CommandContext(ctx, "docker", "load", "-i", tarPath)
	output, err := loadCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker load failed: %w: %s", err, output)
	}
	_, imageID, ok := strings.Cut(string(output), "Loaded image ID: sha256:")
	if !ok {
		_, imageID, ok = strings.Cut(string(output), "Loaded image: sha256:") // podman
		if !ok {
			return fmt.Errorf("unexpected output from docker load: %s", output)
		}
	}
	imageID = strings.TrimSpace(imageID)

	if output, err := exec.CommandContext(ctx, "docker",
		"tag",
		imageID,
		imageName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("docker tag %s %s: %w: %s", imageID, imageName, err, output)
	}

	//nolint:gosec
	if output, err := exec.CommandContext(ctx, "docker",
		"rm",
		"-fv",
		util.EngineContainerName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("docker rm: %w: %s", err, output)
	}

	runArgs := []string{
		"run",
		"-d",
	}

	// Make all GPUs visible to the engine container if the GPU support flag is set:
	if gpuSupport {
		runArgs = append(runArgs, []string{"--gpus", "all"}...)
	}
	runArgs = append(runArgs, []string{
		"-e", util.CacheConfigEnvName,
		"-e", "DAGGER_CLOUD_TOKEN",
		"-e", "DAGGER_CLOUD_URL",
		"-e", util.GPUSupportEnvName,
		"-v", volumeName + ":" + distconsts.EngineDefaultStateDir,
		"-p", "6060:6060",
		"--name", util.EngineContainerName,
		"--privileged",
	}...)

	runArgs = append(runArgs, imageName, "--debug", "--debugaddr=0.0.0.0:6060")

	if output, err := exec.CommandContext(ctx, "docker", runArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("docker run: %w: %s", err, output)
	}

	// build the CLI and export locally so it can be used to connect to the Engine
	binDest := filepath.Join(os.Getenv("DAGGER_SRC_ROOT"), "bin", "dagger")
	_ = os.Remove(binDest) // HACK(vito): avoid 'text file busy'.

	err = util.DaggerCall(ctx, "cli", "file", "export", "--path="+binDest)
	if err != nil {
		return err
	}

	fmt.Println("export _EXPERIMENTAL_DAGGER_CLI_BIN=" + binDest)
	fmt.Println("export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://" + util.EngineContainerName)
	return nil
}
