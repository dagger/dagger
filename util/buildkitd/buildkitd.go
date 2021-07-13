package buildkitd

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/docker/distribution/reference"
	bk "github.com/moby/buildkit/client"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the container connection driver
	"github.com/rs/zerolog/log"
)

const (
	image         = "moby/buildkit"
	version       = "v0.9.0-rc1"
	imageVersion  = image + ":" + version
	containerName = "dagger-buildkitd"
	volumeName    = "dagger-buildkitd"
)

func Start(ctx context.Context) (string, error) {
	lg := log.Ctx(ctx)

	// Attempt to detect the current buildkit version
	currentVersion, err := getBuildkitVersion(ctx)
	if err != nil {
		// If that failed, it might either be because buildkitd is not running
		// or because the docker CLI is out of service.
		if err := checkDocker(ctx); err != nil {
			return "", err
		}

		currentVersion = ""
		lg.Debug().Msg("no buildkit daemon detected")
	} else {
		lg.Debug().Str("version", currentVersion).Msg("detected buildkit version")
	}

	if currentVersion != version {
		if currentVersion != "" {
			lg.
				Info().
				Str("version", version).
				Msg("upgrading buildkit")
			if err := remvoveBuildkit(ctx); err != nil {
				return "", err
			}
		} else {
			lg.
				Info().
				Str("version", version).
				Msg("starting buildkit")
		}
		if err := startBuildkit(ctx); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("docker-container://%s", containerName), nil
}

// ensure the docker CLI is available and properly set up (e.g. permissions to
// communicate with the daemon, etc)
func checkDocker(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "info")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.
			Ctx(ctx).
			Error().
			Err(err).
			Bytes("output", output).
			Msg("failed to run docker")
		return err
	}

	return nil
}

func startBuildkit(ctx context.Context) error {
	lg := log.
		Ctx(ctx).
		With().
		Str("version", version).
		Logger()

	lg.Debug().Msg("pulling buildkit image")
	cmd := exec.CommandContext(ctx,
		"docker",
		"pull",
		imageVersion,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		lg.
			Error().
			Err(err).
			Bytes("output", output).
			Msg("failed to pull buildkit image")
		return err
	}

	// FIXME: buildkitd currently runs without network isolation (--net=host)
	// in order for containers to be able to reach localhost.
	// This is required for things such as kubectl being able to
	// reach a KinD/minikube cluster locally
	cmd = exec.CommandContext(ctx,
		"docker",
		"run",
		"--net=host",
		"-d",
		"--restart", "always",
		"-v", volumeName+":/var/lib/buildkit",
		"--name", containerName,
		"--privileged",
		imageVersion,
	)
	output, err = cmd.CombinedOutput()
	if err != nil {
		// If the daemon failed to start because it's already running,
		// chances are another dagger instance started it. We can just ignore
		// the error.
		if !strings.Contains(string(output), "Error response from daemon: Conflict.") {
			log.
				Ctx(ctx).
				Error().
				Err(err).
				Bytes("output", output).
				Msg("unable to start buildkitd")
			return err
		}
	}
	return waitBuildkit(ctx)
}

// waitBuildkit waits for the buildkit daemon to be responsive.
func waitBuildkit(ctx context.Context) error {
	c, err := bk.New(ctx, "docker-container://"+containerName)
	if err != nil {
		return err
	}
	defer c.Close()

	// Try to connect every 100ms up to 50 times (5 seconds total)
	const (
		retryPeriod   = 100 * time.Millisecond
		retryAttempts = 50
	)

	for retry := 0; retry < retryAttempts; retry++ {
		_, err = c.ListWorkers(ctx)
		if err == nil {
			return nil
		}
		time.Sleep(retryPeriod)
	}
	return errors.New("buildkit failed to respond")
}

func remvoveBuildkit(ctx context.Context) error {
	lg := log.
		Ctx(ctx)

	cmd := exec.CommandContext(ctx,
		"docker",
		"rm",
		"-fv",
		containerName,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		lg.
			Error().
			Err(err).
			Bytes("output", output).
			Msg("failed to stop buildkit")
		return err
	}

	return nil
}

func getBuildkitVersion(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx,
		"docker",
		"inspect",
		"--format",
		"{{.Config.Image}}",
		containerName,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	ref, err := reference.ParseNormalizedNamed(strings.TrimSpace(string(output)))
	if err != nil {
		return "", err
	}
	tag, ok := ref.(reference.Tagged)
	if !ok {
		return "", fmt.Errorf("failed to parse image: %s", output)
	}
	return tag.Tag(), nil
}
