package drivers

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strings"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/pkg/errors"
	"github.com/vito/progrock"

	connh "github.com/moby/buildkit/client/connhelper"
	connhDocker "github.com/moby/buildkit/client/connhelper/dockercontainer"
)

func init() {
	register("docker-image", &dockerDriver{})
}

// dockerDriver creates and manages a container, then connects to it
type dockerDriver struct{}

func (d *dockerDriver) Provision(ctx context.Context, rec *progrock.VertexRecorder, target *url.URL, opts *DriverOpts) (Connector, error) {
	helper, err := d.create(ctx, rec, target.Host+target.Path, opts)
	if err != nil {
		return nil, err
	}
	return dockerConnector{helper: helper, target: target}, nil
}

type dockerConnector struct {
	helper *connh.ConnectionHelper
	target *url.URL
}

func (d dockerConnector) Connect(ctx context.Context) (net.Conn, error) {
	return d.helper.ContextDialer(ctx, d.target.String())
}

const (
	// trim image digests to 16 characters to makeoutput more readable
	hashLen             = 16
	containerNamePrefix = "dagger-engine-"
)

// Pull the image and run it with a unique name tied to the pinned
// sha of the image. Remove any other containers leftover from
// previous executions of the engine at different versions (which
// are identified by looking for containers with the prefix
// "dagger-engine-").
func (d *dockerDriver) create(ctx context.Context, vtx *progrock.VertexRecorder, imageRef string, opts *DriverOpts) (helper *connh.ConnectionHelper, rerr error) {
	// Get the SHA digest of the image to use as an ID for the container we'll create
	var id string
	fallbackToLeftoverEngine := false
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, errors.Wrap(err, "parsing image reference")
	}
	if digest, ok := ref.(name.Digest); ok {
		// We already have the digest as part of the image ref
		id = digest.DigestStr()
	} else {
		// We only have a tag in the image ref, so resolve it to a digest. The default
		// auth keychain parses the same docker credentials as used by the buildkit
		// session attachable.
		if img, err := remote.Get(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithUserAgent(opts.UserAgent)); err != nil {
			vtx.Recorder.Warn("failed to resolve image; falling back to leftover engine", progrock.ErrorLabel(err))
			if strings.Contains(err.Error(), "DENIED") {
				vtx.Recorder.Warn("check your docker registry auth; it might be incorrect or expired")
			}
			fallbackToLeftoverEngine = true
		} else {
			id = img.Digest.String()
		}
	}

	// We collect leftover engine anyway since we garbage collect them at the end
	// And check if we are in a fallback case then perform fallback to most recent engine
	leftoverEngines, err := collectLeftoverEngines(ctx)
	if err != nil {
		vtx.Recorder.Warn("failed to list containers", progrock.ErrorLabel(err))
		leftoverEngines = []string{}
	}
	if fallbackToLeftoverEngine {
		if len(leftoverEngines) == 0 {
			return nil, errors.Errorf("no fallback container found")
		}

		startTask := vtx.Task("starting engine")
		defer startTask.Done(rerr)

		// the first leftover engine may not be running, so make sure to start it
		firstEngine := leftoverEngines[0]
		cmd := exec.CommandContext(ctx, "docker", "start", firstEngine)
		if output, err := cmd.CombinedOutput(); err != nil {
			return nil, errors.Wrapf(err, "failed to start container: %s", output)
		}

		garbageCollectEngines(ctx, vtx, leftoverEngines[1:])

		return connhDocker.Helper(&url.URL{
			Scheme: "docker-container",
			Host:   firstEngine,
		})
	}

	_, id, ok := strings.Cut(id, "sha256:")
	if !ok {
		return nil, errors.Errorf("invalid image reference %q", imageRef)
	}
	id = id[:hashLen]

	// run the container using that id in the name
	containerName := containerNamePrefix + id

	for i, leftoverEngine := range leftoverEngines {
		// if we already have a container with that name, attempt to start it
		if leftoverEngine == containerName {
			startTask := vtx.Task("starting engine")
			defer startTask.Done(rerr)

			cmd := exec.CommandContext(ctx, "docker", "start", leftoverEngine)
			if output, err := cmd.CombinedOutput(); err != nil {
				return nil, errors.Wrapf(err, "failed to start container: %s", output)
			}
			garbageCollectEngines(ctx, vtx, append(leftoverEngines[:i], leftoverEngines[i+1:]...))
			return connhDocker.Helper(&url.URL{
				Scheme: "docker-container",
				Host:   containerName,
			})
		}
	}

	// ensure the image is pulled
	if err := exec.CommandContext(ctx, "docker", "inspect", "--type=image", imageRef).Run(); err != nil {
		pullCmd := exec.CommandContext(ctx, "docker", "pull", imageRef)
		pullCmd.Stdout = vtx.Stdout()
		pullCmd.Stderr = vtx.Stderr()
		pullTask := vtx.Task("pulling %s", imageRef)
		if err := pullCmd.Run(); err != nil {
			pullTask.Done(err)
			return nil, errors.Wrapf(err, "failed to pull image")
		}
		pullTask.Done(nil)
	}

	cmd := exec.CommandContext(ctx,
		"docker",
		"run",
		"--name", containerName,
		"-d",
		"--restart", "always",
		"-v", distconsts.EngineDefaultStateDir,
		"--privileged",
	)
	if opts.DaggerCloudToken != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", EnvDaggerCloudToken, opts.DaggerCloudToken))
		cmd.Args = append(cmd.Args, "-e", EnvDaggerCloudToken)
	}
	if opts.GPUSupport != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", EnvGPUSupport, opts.GPUSupport))
		cmd.Args = append(cmd.Args, "-e", EnvGPUSupport, "--gpus", "all")
	}

	cmd.Args = append(cmd.Args, imageRef, "--debug")

	startTask := vtx.Task("starting engine")
	defer startTask.Done(rerr)
	if output, err := cmd.CombinedOutput(); err != nil {
		if !isContainerAlreadyInUseOutput(string(output)) {
			return nil, errors.Wrapf(err, "failed to run container: %s", output)
		}
	}

	// garbage collect any other containers with the same name pattern, which
	// we assume to be leftover from previous runs of the engine using an older
	// version
	garbageCollectEngines(ctx, vtx, leftoverEngines)

	return connhDocker.Helper(&url.URL{
		Scheme: "docker-container",
		Host:   containerName,
	})
}

func garbageCollectEngines(ctx context.Context, rec *progrock.VertexRecorder, engines []string) {
	for _, engine := range engines {
		if engine == "" {
			continue
		}
		if output, err := exec.CommandContext(ctx,
			"docker", "rm", "-fv", engine,
		).CombinedOutput(); err != nil {
			if !strings.Contains(string(output), "already in progress") {
				rec.Recorder.Warn("failed to remove old container", progrock.ErrorLabel(err), progrock.Labelf("container", engine))
			}
		}
	}
}

func collectLeftoverEngines(ctx context.Context) ([]string, error) {
	output, err := exec.CommandContext(ctx,
		"docker", "ps",
		"-a",
		"--no-trunc",
		"--filter", "name=^/"+containerNamePrefix,
		"--format", "{{.Names}}",
	).CombinedOutput()
	output = bytes.TrimSpace(output)

	if len(output) == 0 {
		return nil, err
	}

	engineNames := strings.Split(string(output), "\n")
	return engineNames, err
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
