package mage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/containerd/platforms"
	"github.com/magefile/mage/mg"

	"github.com/dagger/dagger/.dagger/mage/util"
	"github.com/dagger/dagger/engine/distconsts"
)

var (
	OutputDir           = ""
	EngineContainerName = distconsts.EngineContainerName
)

func shouldLeaveOldEngine() bool {
	val := os.Getenv("DAGGER_LEAVE_OLD_ENGINE")
	return val != "" && val != "0" && strings.ToLower(val) != "false"
}

func init() {
	if v, ok := os.LookupEnv(util.DevContainerEnvName); ok {
		EngineContainerName = v
	}
	if v, ok := os.LookupEnv(util.DevOutputEnvName); ok {
		OutputDir = v
	}
}

type Engine mg.Namespace

// Dev builds and starts an Engine & CLI from local source code
func (t Engine) Dev(ctx context.Context) error {
	binDir := OutputDir
	if binDir == "" {
		binDir = filepath.Join(os.Getenv("DAGGER_SRC_ROOT"), "bin")
	}

	gpuSupport := os.Getenv(util.GPUSupportEnvName) != ""
	trace := os.Getenv(util.TraceEnvName) != ""
	race := os.Getenv(util.RaceEnvName) != ""

	args := []string{"dev-export", "--platform=" + platforms.DefaultString()}
	if gpuSupport {
		args = append(args, "--experimental-gpu-support=true")
	}
	if trace {
		args = append(args, "--trace=true")
	}
	if race {
		args = append(args, "--race=true")
	}
	args = append(args, "export", "--path="+binDir)
	err := util.DaggerCall(ctx, args...)
	if err != nil {
		return err
	}

	containerName := EngineContainerName
	volumeName := EngineContainerName
	imageName := fmt.Sprintf("localhost/%s:latest", EngineContainerName)

	// #nosec
	loadCmd := exec.CommandContext(ctx, "docker", "load", "-i", filepath.Join(binDir, "engine.tar"))
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

	if !shouldLeaveOldEngine() {
		if output, err := exec.CommandContext(ctx, "docker",
			"rm",
			"-fv",
			containerName,
		).CombinedOutput(); err != nil {
			return fmt.Errorf("docker rm: %w: %s", err, output)
		}
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
		// "-p", "6060:6060",
		"--name", containerName,
		"--privileged",
	}...)

	runArgs = append(runArgs, imageName, "--extra-debug", "--debugaddr=0.0.0.0:6060")

	if output, err := exec.CommandContext(ctx, "docker", runArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("docker run: %w: %s", err, output)
	}

	binDest := filepath.Join(binDir, "dagger")
	if runtime.GOOS == "windows" {
		binDest += ".exe"
	}

	fmt.Println("export _EXPERIMENTAL_DAGGER_CLI_BIN=" + binDest)

	if runtime.GOOS != "linux" {
		linuxBinDest := filepath.Join(binDir, "dagger-linux")
		fmt.Println("export _TEST_DAGGER_CLI_LINUX_BIN=" + linuxBinDest)
	}

	fmt.Println("export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://" + containerName)
	fmt.Println("export _DAGGER_TESTS_ENGINE_TAR=" + filepath.Join(binDir, "engine.tar"))
	fmt.Println("export PATH=" + binDir + ":$PATH")

	return nil
}

// Get environment variable updates for running dagger
func (t Engine) DevEnv(ctx context.Context) {
	binDir := OutputDir
	if binDir == "" {
		binDir = filepath.Join(os.Getenv("DAGGER_SRC_ROOT"), "bin")
	}

	binDest := filepath.Join(binDir, "dagger")
	if runtime.GOOS == "windows" {
		binDest += ".exe"
	}

	fmt.Println("export _EXPERIMENTAL_DAGGER_CLI_BIN=" + binDest)

	if runtime.GOOS != "linux" {
		linuxBinDest := filepath.Join(binDir, "dagger-linux")
		fmt.Println("export _TEST_DAGGER_CLI_LINUX_BIN=" + linuxBinDest)
	}

	fmt.Println("export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://" + EngineContainerName)
	fmt.Println("export _DAGGER_TESTS_ENGINE_TAR=" + filepath.Join(binDir, "engine.tar"))
	fmt.Println("export PATH=" + binDir + ":$PATH")
}
