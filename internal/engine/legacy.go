package engine

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

const (
	LegacyBuildkitdProvider = "legacy-buildkitd"

	mobyBuildkitImage      = "moby/buildkit"
	buildkitdContainerName = "dagger-buildkitd"
	buildkitdVolumeName    = "dagger-buildkitd"

	buildkitdLockPath = "~/.config/dagger/.buildkitd.lock"
	// Long timeout to allow for slow image pulls of
	// buildkitd while not blocking for infinity
	lockTimeout = 10 * time.Minute
)

// The old implementation of buildkitd that is still needed by cloak dev
// for now
func legacyBuildkitdProvider(ctx context.Context, u *url.URL) (string, error) {
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
	var ref string
	customImage, found := modVersionToImage[version]
	if found {
		ref = customImage
	} else {
		ref = mobyBuildkitImage + ":" + version
	}
	return startBuildkitdVersion(ctx, ref)
}

// NB: normally we take the version of Buildkit from our go.mod, e.g. v0.10.5,
// and use the same version for the moby/buildkit Docker tag.
//
// this isn't possible when we're using an unreleased version of Buildkit. in
// this scenario a new buildkit image will eventually be built + pushed to
// moby/buildkit:master by their own CI, but if we were to use just "master" we
// wouldn't know when the image needs to be bumped.
//
// so instead we'll manually map the go.mod version to the the image that
// corresponds to it. note that this go.mod version doesn't care what repo it's
// from; the sha should be enough.
//
// you can find this digest by pulling moby/buildkit:master like so:
//
//		$ docker pull moby/buildkit:master
//
//	  # check that it matches
//		$ docker run moby/buildkit:master --version
//
//	  # get the exact digest
//		$ docker images --digests | grep moby/buildkit:master
//
// (unfortunately this relies on timing/chance/spying on their CI)
//
// alternatively you can build your own image and push it somewhere
var modVersionToImage = map[string]string{
	"v0.10.1-0.20221027014600-b78713cdd127": "moby/buildkit@sha256:4984ac6da1898a9a06c4c3f7da5eaabe8a09ec56f5054b0a911ab0f9df6a092c",
	"(devel)":                               "moby/buildkit:v0.10.5",
}

func getBuildInfoVersion() (string, error) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("unable to read build info")
	}

	for _, d := range bi.Deps {
		if d.Path != "github.com/moby/buildkit" {
			continue
		}

		if d.Replace != nil {
			return d.Replace.Version, nil
		}

		return d.Version, nil
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
	//   github.com/moby/buildkit v0.10.5
	//
	// if it's replaced it'll be:
	//
	//   github.com/moby/buildkit v0.10.5 => github.com/vito/buildkit v0.10.5
	_, replace, replaced := strings.Cut(trimmed, " => ")
	if replaced {
		trimmed = strings.TrimSpace(replace)
	}

	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return "", fmt.Errorf("unexpected go list output: %s", trimmed)
	}

	version := fields[1]
	return version, nil
}

func startBuildkitdVersion(ctx context.Context, imageRef string) (string, error) {
	if imageRef == "" {
		return "", errors.New("buildkitd image ref is empty")
	}

	if err := checkBuildkit(ctx, imageRef); err != nil {
		return "", err
	}

	return fmt.Sprintf("docker-container://%s", buildkitdContainerName), nil
}

// ensure the buildkit is active and properly set up (e.g. connected to host and last version with moby/buildkit)
func checkBuildkit(ctx context.Context, imageRef string) error {
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
		if err := installBuildkit(ctx, imageRef); err != nil {
			return err
		}
		return nil
	}

	if config.Image != imageRef {
		fmt.Fprintln(os.Stderr, "Buildkitd container is out of date, updating it...")

		if err := removeBuildkit(ctx); err != nil {
			return err
		}
		if err := installBuildkit(ctx, imageRef); err != nil {
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
		buildkitdContainerName,
	)
	_, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}

	return nil
}

// Pull and run the buildkit daemon with a proper configuration
// If the buildkit daemon is already configured, use startBuildkit
func installBuildkit(ctx context.Context, ref string) error {
	// #nosec
	cmd := exec.CommandContext(ctx, "docker", "pull", ref)
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
		"-v", buildkitdVolumeName+":/var/lib/buildkit",
		"--name", buildkitdContainerName,
		"--privileged",
		ref,
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
	return nil
}

func removeBuildkit(ctx context.Context) error {
	cmd := exec.CommandContext(ctx,
		"docker",
		"rm",
		"-fv",
		buildkitdContainerName,
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
		buildkitdContainerName,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}

	s := strings.Split(string(output), ";")

	// Retrieve the image name
	imageRef := strings.TrimSpace(s[0])

	// Retrieve the state
	isActive, err := strconv.ParseBool(strings.TrimSpace(s[1]))
	if err != nil {
		return nil, err
	}

	return &buildkitInformation{
		Image:    imageRef,
		IsActive: isActive,
	}, nil
}

type buildkitInformation struct {
	Image    string
	IsActive bool
}
