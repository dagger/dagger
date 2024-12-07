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
	"path/filepath"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/adrg/xdg"
	"github.com/google/go-containerregistry/pkg/name"
	connh "github.com/moby/buildkit/client/connhelper"
	connhDocker "github.com/moby/buildkit/client/connhelper/dockercontainer"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
)

var (
	engineConfigPath = filepath.Join(xdg.ConfigHome, "dagger", "engine.json")
)

func init() {
	register("docker-image", &dockerDriver{})
}

// shouldCleanupEngines returns true if old engines should be cleaned up
func shouldCleanupEngines() bool {
	val := os.Getenv("DAGGER_LEAVE_OLD_ENGINE")
	return val == "" || val == "0" || strings.ToLower(val) == "false"
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

const InstrumentationLibrary = "dagger.io/client.drivers"

// Pull the image and run it with a unique name tied to the pinned
// sha of the image. Remove any other containers leftover from
// previous executions of the engine at different versions (which
// are identified by looking for containers with the prefix
// "dagger-engine-").
func (d *dockerDriver) create(ctx context.Context, imageRef string, opts *DriverOpts) (helper *connh.ConnectionHelper, rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, "create")
	defer telemetry.End(span, func() error { return rerr })
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	// Log environment variable state at creation
	slog.Info("checking engine environment",
		"DAGGER_LEAVE_OLD_ENGINE", os.Getenv("DAGGER_LEAVE_OLD_ENGINE"),
		"shouldCleanup", shouldCleanupEngines())

	id, err := resolveImageID(imageRef)
	if err != nil {
		return nil, err
	}

	// run the container using that id in the name
	containerName := containerNamePrefix + id

	leftoverEngines, err := collectLeftoverEngines(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		slog.Warn("failed to list containers", "error", err)
		leftoverEngines = []string{}
	}

	for i, leftoverEngine := range leftoverEngines {
		// if we already have a container with that name, attempt to start it
		if leftoverEngine == containerName {
			slog.Info("found existing container", "name", containerName)
			cmd := exec.CommandContext(ctx, "docker", "start", leftoverEngine)
			if output, err := traceExec(ctx, cmd); err != nil {
				return nil, errors.Wrapf(err, "failed to start container: %s", output)
			}
			slog.Info("cleaning up other engines after starting existing container",
				"current", containerName,
				"leftover_count", len(leftoverEngines)-1)
			if shouldCleanupEngines() {
				garbageCollectEngines(ctx, slog, append(leftoverEngines[:i], leftoverEngines[i+1:]...))
			}
			return connhDocker.Helper(&url.URL{
				Scheme: "docker-container",
				Host:   containerName,
			})
		}
	}

	// ensure the image is pulled
	if _, err := traceExec(ctx, exec.CommandContext(ctx, "docker", "inspect", "--type=image", imageRef), telemetry.Encapsulated()); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, errors.Wrapf(err, "failed to inspect image")
		}
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

	// mount the config path
	if _, err := os.Stat(engineConfigPath); err == nil {
		cmd.Args = append(cmd.Args, "-v", engineConfigPath+":"+config.DefaultConfigPath())
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not stat config", "path", engineConfigPath, "error", err)
	}

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

	if shouldCleanupEngines() {
		slog.Info("cleaning up old engines after creating new container",
			"current", containerName,
			"leftover_count", len(leftoverEngines))
		garbageCollectEngines(ctx, slog, leftoverEngines)
	}

	return connhDocker.Helper(&url.URL{
		Scheme: "docker-container",
		Host:   containerName,
	})
}

func resolveImageID(imageRef string) (string, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return "", errors.Wrap(err, "parsing image reference")
	}
	if digest, ok := ref.(name.Digest); ok {
		// We already have the digest as part of the image ref
		_, id, ok := strings.Cut(digest.DigestStr(), "sha256:")
		if !ok {
			return "", errors.Errorf("invalid image reference %q", imageRef)
		}
		return id[:hashLen], nil
	}
	if tag, ok := ref.(name.Tag); ok {
		// Otherwise, fallback to the image tag
		return tag.TagStr(), nil
	}

	// default to latest
	return "latest", nil
}

func garbageCollectEngines(ctx context.Context, log *slog.Logger, engines []string) {
	val := os.Getenv("DAGGER_LEAVE_OLD_ENGINE")

	// Enhanced logging for debugging
	log.Info("evaluating engine cleanup",
		"raw_env_value", val,
		"engineCount", len(engines))

	// Log each engine being considered
	for i, engine := range engines {
		log.Info("found engine",
			"index", i,
			"name", engine)
	}

	for _, engine := range engines {
		if engine == "" {
			continue
		}
		log.Info("removing engine",
			"name", engine,
			"env_value", val)

		if output, err := traceExec(ctx, exec.CommandContext(ctx,
			"docker", "rm", "-fv", engine,
		)); err != nil {
			log.Warn("failed to remove container",
				"name", engine,
				"error", err,
				"output", output)
		}
	}
}

func traceExec(ctx context.Context, cmd *exec.Cmd, opts ...trace.SpanStartOption) (out string, rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, fmt.Sprintf("exec %s", strings.Join(cmd.Args, " ")), opts...)
	defer telemetry.End(span, func() error { return rerr })
	stdio := telemetry.SpanStdio(ctx, "")
	defer stdio.Close()
	outBuf := new(bytes.Buffer)
	cmd.Stdout = io.MultiWriter(stdio.Stdout, outBuf)
	cmd.Stderr = io.MultiWriter(stdio.Stderr, outBuf)
	if err := cmd.Run(); err != nil {
		return outBuf.String(), errors.Wrap(err, "failed to run command")
	}
	return outBuf.String(), nil
}

func collectLeftoverEngines(ctx context.Context) ([]string, error) {
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)
	slog.Info("collecting leftover engines",
		"DAGGER_LEAVE_OLD_ENGINE", os.Getenv("DAGGER_LEAVE_OLD_ENGINE"))

	cmd := exec.CommandContext(ctx,
		"docker", "ps",
		"-a",
		"--no-trunc",
		"--filter", "name=^/"+containerNamePrefix,
		"--format", "{{.Names}}",
	)
	output, err := traceExec(ctx, cmd)
	if err != nil {
		slog.Error("failed to list containers",
			"error", err,
			"output", output)
		return nil, errors.Wrapf(err, "failed to list containers: %s", output)
	}

	output = strings.TrimSpace(output)
	engineNames := strings.Split(output, "\n")

	slog.Info("found engines",
		"count", len(engineNames),
		"names", engineNames)

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
