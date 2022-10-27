package buildkitd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/docker/distribution/reference"
	"github.com/gofrs/flock"
	"github.com/mitchellh/go-homedir"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/tracing/detect"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the docker connection driver
	_ "github.com/moby/buildkit/client/connhelper/kubepod"         // import the kubernetes connection driver
	_ "github.com/moby/buildkit/client/connhelper/podmancontainer" // import the podman connection driver
)

const (
	image         = "moby/buildkit"
	containerName = "dagger-buildkitd"
	volumeName    = "dagger-buildkitd"

	buildkitdLockPath = "~/.config/dagger/.buildkitd.lock"
	// Long timeout to allow for slow image pulls of
	// buildkitd while not blocking for infinity
	lockTimeout = 10 * time.Minute
)

func Client(ctx context.Context) (*bkclient.Client, error) {
	host := os.Getenv("BUILDKIT_HOST")
	if host == "" {
		h, err := startBuildkitd(ctx)
		if err != nil {
			return nil, err
		}

		host = h
	}
	opts := []bkclient.ClientOpt{
		bkclient.WithFailFast(),
		bkclient.WithTracerProvider(otel.GetTracerProvider()),
	}

	exp, err := detect.Exporter()
	if err != nil {
		return nil, err
	}

	if td, ok := exp.(bkclient.TracerDelegate); ok {
		opts = append(opts, bkclient.WithTracerDelegate(td))
	}

	c, err := bkclient.New(ctx, host, opts...)
	if err != nil {
		return nil, fmt.Errorf("buildkit client: %w", err)
	}
	return c, nil
}

func startBuildkitd(ctx context.Context) (string, error) {
	version, err := getBuildInfoVersion()
	if err != nil {
		return version, err
	}
	if version == "" {
		version, err = getGoModVersion()
		if err != nil {
			return version, err
		}
	}
	return startBuildkitdVersion(ctx, version)
}

func getBuildInfoVersion() (string, error) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("unable to read build info")
	}

	for _, d := range bi.Deps {
		if d.Path == "github.com/moby/buildkit" {
			return d.Version, nil
		}
	}
	return "", nil
}

// Workaround the fact that debug.ReadBuildInfo doesn't work in tests:
// https://github.com/golang/go/issues/33976
func getGoModVersion() (string, error) {
	out, err := exec.Command("go", "list", "-m", "github.com/moby/buildkit").CombinedOutput()
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(string(out))

	// NB: normally this will be:
	//
	//   github.com/moby/buildkit v0.10.5 => github.com/vito/buildkit v0.10.5
	//
	// but if it's replaced for client-only changes in a fork it'll be:
	//
	//   github.com/moby/buildkit v0.10.5 => github.com/vito/buildkit v0.10.5
	//
	// so we always want the second word.
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return "", fmt.Errorf("unexpected go list output: %s", trimmed)
	}

	version := fields[1]
	return version, nil
}

func startBuildkitdVersion(ctx context.Context, version string) (string, error) {
	if version == "" {
		return "", errors.New("buildkitd version is empty")
	}

	if err := checkBuildkit(ctx, version); err != nil {
		return "", err
	}

	return fmt.Sprintf("docker-container://%s", containerName), nil
}

// ensure the buildkit is active and properly set up (e.g. connected to host and last version with moby/buildkit)
func checkBuildkit(ctx context.Context, version string) error {
	// acquire a file-based lock to ensure parallel dagger clients
	// don't interfere with checking+creating the buildkitd container
	lockFilePath, err := homedir.Expand(buildkitdLockPath)
	if err != nil {
		return fmt.Errorf("unable to expand buildkitd lock path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(lockFilePath), 0755); err != nil {
		return fmt.Errorf("unable to create buildkitd lock path parent dir: %w", err)
	}
	lock := flock.New(lockFilePath)
	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	locked, err := lock.TryLockContext(lockCtx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("failed to lock buildkitd lock file: %w", err)
	}
	if !locked {
		return fmt.Errorf("failed to acquire buildkitd lock file")
	}
	defer lock.Unlock()

	// check status of buildkitd container
	config, err := getBuildkitInformation(ctx)
	if err != nil {
		// If that failed, it might be because the docker CLI is out of service.
		if err := checkDocker(ctx); err != nil {
			return err
		}

		fmt.Fprintln(os.Stderr, "No buildkitd container found, creating one...")

		removeBuildkit(ctx)
		if err := installBuildkit(ctx, version); err != nil {
			return err
		}
		return nil
	}

	if config.Version != version {
		fmt.Fprintln(os.Stderr, "Buildkitd container is out of date, updating it...")

		if err := removeBuildkit(ctx); err != nil {
			return err
		}
		if err := installBuildkit(ctx, version); err != nil {
			return err
		}
	}
	if !config.IsActive {
		fmt.Fprintln(os.Stderr, "Buildkitd container is not running, starting it...")

		if err := startBuildkit(ctx); err != nil {
			return err
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
		return fmt.Errorf("%s%s", err, output)
	}

	return nil
}

// Start the buildkit daemon
func startBuildkit(ctx context.Context) error {
	cmd := exec.CommandContext(ctx,
		"docker",
		"start",
		containerName,
	)
	_, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}

	return waitBuildkit(ctx)
}

// Pull and run the buildkit daemon with a proper configuration
// If the buildkit daemon is already configured, use startBuildkit
func installBuildkit(ctx context.Context, version string) error {
	// #nosec
	cmd := exec.CommandContext(ctx,
		"docker",
		"pull",
		image+":"+version,
	)
	_, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}

	// #nosec G204
	cmd = exec.CommandContext(ctx,
		"docker",
		"run",
		"-d",
		"--restart", "always",
		"-v", volumeName+":/var/lib/buildkit",
		"--name", containerName,
		"--privileged",
		image+":"+version,
		"--debug",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If the daemon failed to start because it's already running,
		// chances are another dagger instance started it. We can just ignore
		// the error.
		if !strings.Contains(string(output), "Error response from daemon: Conflict.") {
			return err
		}
	}
	return waitBuildkit(ctx)
}

// waitBuildkit waits for the buildkit daemon to be responsive.
func waitBuildkit(ctx context.Context) error {
	c, err := bkclient.New(ctx, "docker-container://"+containerName)
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
	cmd := exec.CommandContext(ctx,
		"docker",
		"rm",
		"-fv",
		containerName,
	)
	_, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}

	return nil
}

func getBuildkitInformation(ctx context.Context) (*buildkitInformation, error) {
	formatString := "{{.Config.Image}};{{.State.Running}};{{if index .NetworkSettings.Networks \"host\"}}{{\"true\"}}{{else}}{{\"false\"}}{{end}}"
	cmd := exec.CommandContext(ctx,
		"docker",
		"inspect",
		"--format",
		formatString,
		containerName,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}

	s := strings.Split(string(output), ";")

	// Retrieve the tag
	ref, err := reference.ParseNormalizedNamed(strings.TrimSpace(s[0]))
	if err != nil {
		return nil, err
	}
	tag, ok := ref.(reference.Tagged)
	if !ok {
		return nil, fmt.Errorf("failed to parse image: %s", output)
	}

	// Retrieve the state
	isActive, err := strconv.ParseBool(strings.TrimSpace(s[1]))
	if err != nil {
		return nil, err
	}

	return &buildkitInformation{
		Version:  tag.Tag(),
		IsActive: isActive,
	}, nil
}

type buildkitInformation struct {
	Version  string
	IsActive bool
}
