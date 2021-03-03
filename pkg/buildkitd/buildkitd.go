package buildkitd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/docker/distribution/reference"
	"github.com/rs/zerolog/log"
)

const (
	image         = "moby/buildkit"
	version       = "v0.8.2"
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

	cmd = exec.CommandContext(ctx,
		"docker",
		"run",
		"-d",
		"--restart", "always",
		"-v", volumeName+":/var/lib/buildkit",
		"--name", containerName,
		"--privileged",
		imageVersion,
	)
	output, err = cmd.CombinedOutput()
	if err != nil {
		log.
			Ctx(ctx).
			Error().
			Err(err).
			Bytes("output", output).
			Msg("unable to start buildkitd")
		return err
	}
	return nil
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
