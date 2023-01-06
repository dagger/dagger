package engine

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/pkg/errors"
)

const (
	DockerImageProvider = "docker-image"
	// NOTE: this needs to be consistent with engineDefaultStateDir in internal/mage/engine.go
	DefaultStateDir = "/var/lib/dagger"

	// trim image digests to 16 characters to makeoutput more readable
	hashLen             = 16
	containerNamePrefix = "dagger-engine-"
)

// Pull the image and run it with a unique name tied to the pinned
// sha of the image. Remove any other containers leftover from
// previous executions of the engine at different versions (which
// are identified by looking for containers with the prefix
// "dagger-engine-").
func dockerImageProvider(ctx context.Context, runnerHost *url.URL) (string, error) {
	imageRef := runnerHost.Host + runnerHost.Path

	// Get the SHA digest of the image to use as an ID for the container we'll create
	var id string
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return "", errors.Wrap(err, "parsing image reference")
	}
	if d, ok := ref.(name.Digest); ok {
		// We already have the digest as part of the image ref
		id = d.DigestStr()
	} else {
		// We only have a tag in the image ref, so resolve it to a digest. The default
		// auth keychain parses the same docker credentials as used by the buildkit
		// session attachable.
		img, err := remote.Get(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
		if err != nil {
			return "", errors.Wrap(err, "resolving image digest")
		}
		id = img.Digest.String()
	}
	_, id, ok := strings.Cut(id, "sha256:")
	if !ok {
		return "", errors.Errorf("invalid image reference %q", imageRef)
	}
	id = id[:hashLen]

	// run the container using that id in the name
	containerName := containerNamePrefix + id
	if output, err := exec.CommandContext(ctx,
		"docker", "run",
		"--name", containerName,
		"-d",
		"--restart", "always",
		"-v", DefaultStateDir,
		"--privileged",
		imageRef,
		"--debug",
	).CombinedOutput(); err != nil {
		if !isContainerAlreadyInUseOutput(string(output)) {
			return "", errors.Wrapf(err, "failed to run container: %s", output)
		}
	}

	// garbage collect any other containers with the same name pattern, which
	// we assume to be leftover from previous runs of the engine using an older
	// version
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
				if !strings.Contains(string(output), "already in progress") {
					fmt.Fprintf(os.Stderr, "failed to remove old container %s: %s", line, output)
				}
			}
		}
	}
	return "docker-container://" + containerName, nil
}

func isContainerAlreadyInUseOutput(output string) bool {
	switch {
	// docker cli output
	case strings.Contains(output, "is already in use"):
		return true
	// nerdctl cli output
	case strings.Contains(output, "is already used"):
		return true
	}
	return false
}
