package mage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/containerd/containerd/platforms"
	"github.com/magefile/mage/mg"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/ci/mage/util"
	"github.com/dagger/dagger/engine/distconsts"
)

type Engine mg.Namespace

var (
	publishedEnginePlatforms    = []string{"linux/amd64", "linux/arm64"}
	publishedGPUEnginePlatforms = []string{"linux/amd64"}

	publishedSDKs = []string{
		"go",
		"python",
		"typescript",
		"elixir",
		"rust",
		"php",
	}
)

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

	args := []string{"--version=" + version, "engine", "publish"}
	args = append(args, commonArgs...)
	for _, p := range publishedEnginePlatforms {
		args = append(args, "--platform="+p)
	}
	err := util.DaggerCall(ctx, args...)
	if err != nil {
		return err
	}

	args = []string{"--version=" + version, "engine", "with-base", "--image=ubuntu", "--gpu-support=true", "publish"}
	args = append(args, commonArgs...)
	for _, p := range publishedGPUEnginePlatforms {
		args = append(args, "--platform="+p)
	}
	err = util.DaggerCall(ctx, args...)
	if err != nil {
		return err
	}

	if semver.IsValid(version) {
		eg, gctx := errgroup.WithContext(ctx)
		for _, sdk := range publishedSDKs {
			sdk := sdk
			eg.Go(func() error {
				return util.DaggerCall(gctx, "sdk", sdk, "bump", "--version="+version, "export", "--path=.")
			})
		}
		if err := eg.Wait(); err != nil {
			return err
		}
	} else {
		fmt.Println("skipping image bump in SDKs")
	}

	return nil
}

// Dev builds and starts an Engine & CLI from local source code
func (t Engine) Dev(ctx context.Context) error {
	gpuSupport := os.Getenv(util.GPUSupportEnvName) != ""
	trace := os.Getenv(util.TraceEnvName) != ""
	race := os.Getenv(util.RaceEnvName) != ""

	args := []string{"engine"}
	if gpuSupport {
		args = append(args, "with-base", "--image=ubuntu", "--gpu-support=true")
	}
	if trace {
		args = append(args, "with-trace")
	}
	if race {
		args = append(args, "with-race")
	}
	tarPath := "./bin/engine.tar"
	args = append(args, "container", "export", "--path="+tarPath)
	args = append(args, "--forced-compression=Gzip") // use gzip to avoid incompatibility w/ older docker versions
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

	runArgs = append(runArgs, imageName, "--extra-debug", "--debugaddr=0.0.0.0:6060")

	if output, err := exec.CommandContext(ctx, "docker", runArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("docker run: %w: %s", err, output)
	}

	// build the CLI and export locally so it can be used to connect to the Engine
	binDest := filepath.Join(os.Getenv("DAGGER_SRC_ROOT"), "bin", "dagger")
	if runtime.GOOS == "windows" {
		binDest += ".exe"
	}
	_ = os.Remove(binDest) // HACK(vito): avoid 'text file busy'.

	err = util.DaggerCall(ctx, "cli", "file", "--platform="+platforms.DefaultString(), "export", "--path="+binDest)
	if err != nil {
		return err
	}

	fmt.Println("export _EXPERIMENTAL_DAGGER_CLI_BIN=" + binDest)
	fmt.Println("export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://" + util.EngineContainerName)
	return nil
}
