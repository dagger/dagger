package buildkitd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/mitchellh/go-homedir"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/tracing/detect"
	"go.dagger.io/dagger/internal/version"
	"go.opentelemetry.io/otel"

	_ "github.com/moby/buildkit/client/connhelper/kubepod"              // import the kubernetes connection driver
	_ "github.com/moby/buildkit/client/connhelper/podmancontainer"      // import the podman connection driver
	_ "go.dagger.io/dagger/internal/buildkitd/bundling/dockercontainer" // import the docker connection driver, tweaked
)

const (
	image         = "dagger-buildkitd"
	containerName = "dagger-buildkitd"
	volumeName    = "dagger-buildkitd"

	daggerBuildkitdLockPath = "~/.config/dagger/.dagger-buildkitd.lock"
	// Long timeout to allow for slow image build of
	// dagger-buildkitd while not blocking for infinity
	lockTimeout = 10 * time.Minute
)

func Client(ctx context.Context) (*bkclient.Client, error) {
	host := os.Getenv("DAGGER_BUILDKITD_HOST")
	if host == "" {
		h, err := startDaggerBuildkitd(ctx)
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
		return nil, fmt.Errorf("dagger-buildkitd client: %w", err)
	}
	return c, nil
}

func startDaggerBuildkitd(ctx context.Context) (string, error) {
	revisionedVersion, err := version.GetBuildInfo()
	if err != nil {
		return revisionedVersion, err
	}
	if revisionedVersion == "" {
		revisionedVersion, err = version.GetCommitHash()
		if err != nil {
			return revisionedVersion, err
		}
	}
	return startDaggerBuildkitdVersion(ctx, revisionedVersion)
}

func startDaggerBuildkitdVersion(ctx context.Context, version string) (string, error) {
	if version == "" {
		return "", errors.New("dagger-buildkitd version is empty")
	}

	containerName, err := checkDaggerBuildkitd(ctx, version)
	if err != nil {
		return "", err
	}

	return containerName, nil
}

// ensure that dagger-buildkitd is built, active and properly set up (e.g. connected to host)
func checkDaggerBuildkitd(ctx context.Context, version string) (string, error) {
	// acquire a file-based lock to ensure parallel dagger clients
	// don't interfere with checking+creating the dagger-buildkitd container
	lockFilePath, err := homedir.Expand(daggerBuildkitdLockPath)
	if err != nil {
		return "", fmt.Errorf("unable to expand dagger-buildkitd lock path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(lockFilePath), 0755); err != nil {
		return "", fmt.Errorf("unable to create dagger-buildkitd lock path parent dir: %w", err)
	}
	lock := flock.New(lockFilePath)
	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	locked, err := lock.TryLockContext(lockCtx, 100*time.Millisecond)
	if err != nil {
		return "", fmt.Errorf("failed to lock dagger-buildkitd lock file: %w", err)
	}
	if !locked {
		return "", fmt.Errorf("failed to acquire dagger-buildkitd lock file")
	}
	defer lock.Unlock()

	// Check available provisioner
	provisioner, err := initProvisioner(ctx)
	if err != nil {
		return "", err
	}

	// check status of dagger-buildkitd
	host, config, err := provisioner.DaggerBuildkitdState(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "No buildkitd container found, creating one...")

		// provisioner.RemoveDaggerBuildkitd(ctx)

		if err := provisioner.InstallDaggerBuildkitd(ctx, version); err != nil {
			return "", err
		}
		return host, nil
	}

	if config.Version != version {
		fmt.Fprintln(os.Stderr, "Buildkitd container is out of date, updating it...")

		if err := provisioner.RemoveDaggerBuildkitd(ctx); err != nil {
			return "", err
		}
		if err := provisioner.InstallDaggerBuildkitd(ctx, version); err != nil {
			return "", err
		}
	}
	if !config.IsActive {
		fmt.Println("dagger-buildkitd container is not running, starting it...")

		if err := provisioner.StartDaggerBuildkitd(ctx); err != nil {
			return "", err
		}
	}
	return host, nil
}

func initProvisioner(ctx context.Context) (Provisioner, error) {
	// If that failed, it might be because the docker CLI is out of service.
	if err := checkDocker(ctx); err == nil {
		return Docker{
			host: fmt.Sprintf("docker-container://%s", containerName),
		}, nil
	}
	return nil, fmt.Errorf("no provisioner available")
}

type Provisioner interface {
	RemoveDaggerBuildkitd(ctx context.Context) error
	InstallDaggerBuildkitd(ctx context.Context, version string) error
	StartDaggerBuildkitd(ctx context.Context) error
	DaggerBuildkitdState(ctx context.Context) (string, *daggerBuildkitdInfo, error)
}

type daggerBuildkitdInfo struct {
	Version  string
	IsActive bool
}
