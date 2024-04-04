package drivers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	connh "github.com/moby/buildkit/client/connhelper"
	connhDocker "github.com/moby/buildkit/client/connhelper/dockercontainer"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/distconsts"
	"github.com/dagger/dagger/telemetry"
)

func init() {
	register("docker-image", &dockerDriver{})
}

// dockerDriver creates and manages a container, then connects to it
type dockerDriver struct{}

func (d *dockerDriver) Provision(ctx context.Context, target *url.URL, opts *DriverOpts) (Connector, error) {
	helper, err := d.create(ctx, target.Host+target.Path, opts)
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
func (d *dockerDriver) create(ctx context.Context, imageRef string, opts *DriverOpts) (helper *connh.ConnectionHelper, rerr error) {
	log := telemetry.ContextLogger(ctx, slog.LevelWarn) // TODO

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
			log.Warn("failed to resolve image; falling back to leftover engine", "error", err)
			if strings.Contains(err.Error(), "DENIED") {
				log.Warn("check your docker registry auth; it might be incorrect or expired")
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
		log.Warn("failed to list containers", "error", err)
		leftoverEngines = []string{}
	}
	if fallbackToLeftoverEngine {
		if len(leftoverEngines) == 0 {
			return nil, errors.Errorf("no fallback container found")
		}

		// the first leftover engine may not be running, so make sure to start it
		firstEngine := leftoverEngines[0]
		cmd := exec.CommandContext(ctx, "docker", "start", firstEngine)
		if output, err := traceExec(ctx, cmd); err != nil {
			return nil, errors.Wrapf(err, "failed to start container: %s", output)
		}

		garbageCollectEngines(ctx, log, leftoverEngines[1:])

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
			cmd := exec.CommandContext(ctx, "docker", "start", leftoverEngine)
			if output, err := traceExec(ctx, cmd); err != nil {
				return nil, errors.Wrapf(err, "failed to start container: %s", output)
			}
			garbageCollectEngines(ctx, log, append(leftoverEngines[:i], leftoverEngines[i+1:]...))
			return connhDocker.Helper(&url.URL{
				Scheme: "docker-container",
				Host:   containerName,
			})
		}
	}

	// ensure the image is pulled
	if _, err := traceExec(ctx, exec.CommandContext(ctx, "docker", "inspect", "--type=image", imageRef)); err != nil {
		if _, err := traceExec(ctx, exec.CommandContext(ctx, "docker", "pull", imageRef)); err != nil {
			return nil, errors.Wrapf(err, "failed to pull image")
		}
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
	// explicitly pass current env vars; if we append more below existing ones like DOCKER_HOST
	// won't be passed to the cmd
	cmd.Env = os.Environ()
	if opts.DaggerCloudToken != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", EnvDaggerCloudToken, opts.DaggerCloudToken))
		cmd.Args = append(cmd.Args, "-e", EnvDaggerCloudToken)
	}
	if opts.GPUSupport != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", EnvGPUSupport, opts.GPUSupport))
		cmd.Args = append(cmd.Args, "-e", EnvGPUSupport, "--gpus", "all")
	}

	cmd.Args = append(cmd.Args, imageRef, "--debug")

	if output, err := traceExec(ctx, cmd); err != nil {
		if !isContainerAlreadyInUseOutput(output) {
			return nil, errors.Wrapf(err, "failed to run container: %s", output)
		}
	}

	// garbage collect any other containers with the same name pattern, which
	// we assume to be leftover from previous runs of the engine using an older
	// version
	garbageCollectEngines(ctx, log, leftoverEngines)

	return connhDocker.Helper(&url.URL{
		Scheme: "docker-container",
		Host:   containerName,
	})
}

func garbageCollectEngines(ctx context.Context, log *slog.Logger, engines []string) {
	for _, engine := range engines {
		if engine == "" {
			continue
		}
		if output, err := traceExec(ctx, exec.CommandContext(ctx,
			"docker", "rm", "-fv", engine,
		)); err != nil {
			if !strings.Contains(output, "already in progress") {
				log.Warn("failed to remove old container", "container", engine, "error", err)
			}
		}
	}
}

func traceExec(ctx context.Context, cmd *exec.Cmd) (out string, rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, fmt.Sprintf("exec %s", strings.Join(cmd.Args, " ")))
	defer telemetry.End(span, func() error { return rerr })
	_, stdout, stderr := telemetry.WithStdioToOtel(ctx, "")
	outBuf := new(bytes.Buffer)
	cmd.Stdout = io.MultiWriter(stdout, outBuf)
	cmd.Stderr = io.MultiWriter(stderr, outBuf)
	if err := cmd.Run(); err != nil {
		return outBuf.String(), errors.Wrap(err, "failed to run command")
	}
	return outBuf.String(), nil
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
