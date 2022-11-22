package engine

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/pkg/errors"
)

const (
	DockerImageProvider     = "docker-image"
	DockerContainerProvider = "docker-container"

	// trim image digests to 16 characters to makeoutput more readable
	hashLen             = 16
	containerNamePrefix = "dagger-engine-"
)

// Pull the image and run it with a unique name tied to the pinned
// sha of the image. Remove any other containers leftover from
// previous executions of the engine at different versions (which
// are identified by looking for containers with the prefix
// "dagger-engine-").
func dockerImageProvider(ctx context.Context, remote *url.URL) (string, error) {
	imageRef := remote.Host + remote.Path

	// NOTE: this isn't as robust as using the official docker parser, but
	// our other SDKs don't have access to that, so this is simpler to
	// replicate and keep consistent.
	var id string
	_, dgst, ok := strings.Cut(imageRef, ":")
	if !ok {
		return "", errors.Errorf("invalid image reference %q", imageRef)
	}
	id = dgst
	id = id[:hashLen]

	containerName := containerNamePrefix + id

	if output, err := exec.CommandContext(ctx,
		"docker", "run",
		"--name", containerName,
		"-d",
		"--restart", "always",
		"--privileged",
		imageRef,
		"--debug",
	).CombinedOutput(); err != nil {
		if !strings.Contains(
			string(output),
			fmt.Sprintf(`Conflict. The container name "/%s" is already in use by container`, containerName),
		) {
			return "", errors.Wrapf(err, "failed to run container: %s", output)
		}
	}
	if output, err := exec.CommandContext(ctx,
		"docker", "ps",
		"-a",
		"--no-trunc",
		"--filter", "name=^/"+containerNamePrefix,
		"--format", "{{.Names}}",
	).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to list containers: %s", output)
	} else {
		for _, line := range strings.Split(string(output), "\n") {
			if line == "" {
				continue
			}
			if line == containerName {
				continue
			}
			if output, err := exec.CommandContext(ctx,
				"docker", "rm", "-fv", line,
			).CombinedOutput(); err != nil {
				if !strings.Contains(string(output), fmt.Sprintf("removal of container %s is already in progress", line)) {
					fmt.Fprintf(os.Stderr, "failed to remove old container %s: %s", line, output)
				}
			}
		}
	}
	return "docker-container://" + containerName, nil
}

// Just connect to the container as provided, nothing fancy
func dockerContainerProvider(ctx context.Context, remote *url.URL) (string, error) {
	return "docker-container://" + remote.Host + remote.Path, nil
}
