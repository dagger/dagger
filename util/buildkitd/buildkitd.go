package buildkitd

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	bk "github.com/moby/buildkit/client"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the container connection driver
	"github.com/rs/zerolog/log"
)

var (
	// vendoredVersion is filled in by init()
	vendoredVersion string
)

const (
	image         = "moby/buildkit"
	containerName = "dagger-buildkitd"
	volumeName    = "dagger-buildkitd"
)

func init() {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	for _, d := range bi.Deps {
		if d.Path == "github.com/moby/buildkit" {
			vendoredVersion = d.Version
			break
		}
	}
}

func Start(ctx context.Context) (string, error) {
	if vendoredVersion == "" {
		return "", fmt.Errorf("vendored version is empty")
	}

	if err := checkBuildkit(ctx); err != nil {
		return "", err
	}

	return fmt.Sprintf("docker-container://%s", containerName), nil
}

// ensure the buildkit is active and properly set up (e.g. connected to host and last version with moby/buildkit)
func checkBuildkit(ctx context.Context) error {
	lg := log.Ctx(ctx)

	config, err := getBuildkitInformation(ctx)
	if err != nil {
		// If that failed, it might be because the docker CLI is out of service.
		if err := checkDocker(ctx); err != nil {
			return err
		}

		lg.Debug().Msg("no buildkit daemon detected")

		if err := removeBuildkit(ctx); err != nil {
			lg.Debug().Err(err).Msg("error while removing buildkit")
		}

		if err := installBuildkit(ctx); err != nil {
			return err
		}
	} else {
		lg.
			Debug().
			Str("version", config.Version).
			Bool("isActive", config.IsActive).
			Bool("haveHostNetwork", config.HaveHostNetwork).
			Msg("detected buildkit config")

		if config.Version != vendoredVersion || !config.HaveHostNetwork {
			lg.
				Info().
				Str("version", vendoredVersion).
				Bool("have host network", config.HaveHostNetwork).
				Msg("upgrading buildkit")

			if err := removeBuildkit(ctx); err != nil {
				return err
			}
			if err := installBuildkit(ctx); err != nil {
				return err
			}
		}
		if !config.IsActive {
			lg.
				Info().
				Str("version", vendoredVersion).
				Msg("starting buildkit")

			if err := startBuildkit(ctx); err != nil {
				return err
			}
		}
	}

	return nil
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

// Start the buildkit daemon
func startBuildkit(ctx context.Context) error {
	lg := log.
		Ctx(ctx).
		With().
		Str("version", vendoredVersion).
		Logger()

	lg.Debug().Msg("starting buildkit image")

	cmd := exec.CommandContext(ctx,
		"docker",
		"start",
		containerName,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		lg.
			Error().
			Err(err).
			Bytes("output", output).
			Msg("failed to start buildkit container")
		return err
	}

	return waitBuildkit(ctx)
}

// Pull and run the buildkit daemon with a proper configuration
// If the buildkit daemon is already configured, use startBuildkit
func installBuildkit(ctx context.Context) error {
	lg := log.
		Ctx(ctx).
		With().
		Str("version", vendoredVersion).
		Logger()

	lg.Debug().Msg("pulling buildkit image")
	// #nosec
	cmd := exec.CommandContext(ctx,
		"docker",
		"pull",
		image+":"+vendoredVersion,
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
	// #nosec
	cmd = exec.CommandContext(ctx,
		"docker",
		"run",
		"--net=host",
		"-d",
		"--restart", "always",
		"-v", volumeName+":/var/lib/buildkit",
		"--name", containerName,
		"--privileged",
		image+":"+vendoredVersion,
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

	// FIXME Does output "failed to wait: signal: broken pipe"
	defer c.Close()

	// Try to connect every 100ms up to 100 times (10 seconds total)
	const (
		retryPeriod   = 100 * time.Millisecond
		retryAttempts = 100
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

func removeBuildkit(ctx context.Context) error {
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
