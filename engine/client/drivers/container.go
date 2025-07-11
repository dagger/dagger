package drivers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/adrg/xdg"
	"github.com/docker/cli/cli/connhelper/commandconn"
	"github.com/google/go-containerregistry/pkg/name"
	"go.opentelemetry.io/otel"

	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/traceexec"
)

var (
	engineConfigPath = filepath.Join(xdg.ConfigHome, "dagger", "engine.json")
)

// XXX: ensure we remove docker refs

// containerBackend is a generic backend for containers that can be plugged
// into image/container drivers. This allows us to have all the exact same
// logic between different container providers, but have different backend
// commands.
//
// TODO: maybe make this more generic, so that we could implement a containerd client on this
//
// TODO: write some tests for impls
type containerBackend interface {
	ImagePull(image string) *exec.Cmd
	ImageLoad(name string, tarball io.Reader) *exec.Cmd
	ImageInspect(image string) *exec.Cmd
	ImageTag(dest string, src string) *exec.Cmd

	ContainerRun(name string, opts runOpts) *exec.Cmd
	ContainerExec(name string, args []string) *exec.Cmd
	ContainerRemove(name string) *exec.Cmd
	ContainerStart(name string) *exec.Cmd
	ContainerInspect(image string) *exec.Cmd
	ContainerLs(ctx context.Context) ([]string, error)
}

type runOpts struct {
	image string

	volumes    []string
	env        []string
	ports      []string
	gpus       bool
	privileged bool

	args []string
}

// imageDriver creates and manages a container, then connects to it
type imageDriver struct {
	backend containerBackend
	loader  imageload.Backend
}

func (d *imageDriver) Provision(ctx context.Context, target *url.URL, opts *DriverOpts) (Connector, error) {
	cleanup := true
	if val, ok := os.LookupEnv("DAGGER_LEAVE_OLD_ENGINE"); ok {
		b, _ := strconv.ParseBool(val)
		cleanup = !b
	} else if val := target.Query().Get("cleanup"); val != "" {
		cleanup, _ = strconv.ParseBool(val)
	}

	containerName := target.Query().Get("container")
	volumeName := target.Query().Get("volume")
	port, _ := strconv.Atoi(target.Query().Get("port"))

	target, err := d.create(ctx, target.Host+target.Path, containerName, volumeName, cleanup, port, opts)
	if err != nil {
		return nil, err
	}
	return containerConnector{target: target, backend: d.backend}, nil
}

func (d *imageDriver) ImageLoader() imageload.Backend {
	return d.loader
}

type containerConnector struct {
	target  *url.URL
	backend containerBackend
}

// imageDriver connects to a container directly
type containerDriver struct {
	backend containerBackend
	loader  imageload.Backend
}

func (d *containerDriver) Provision(ctx context.Context, target *url.URL, opts *DriverOpts) (Connector, error) {
	return containerConnector{target: target, backend: d.backend}, nil
}

func (d *containerDriver) ImageLoader() imageload.Backend {
	return d.loader
}

func (d containerConnector) Connect(ctx context.Context) (net.Conn, error) {
	ctxFlags := []string{}
	if context := d.target.Query().Get("context"); context != "" {
		ctxFlags = append(ctxFlags, "--context="+context)
	}

	args := append(ctxFlags, "buildctl", "dial-stdio")
	cmd := d.backend.ContainerExec(d.target.Hostname(), args)
	cmd.Env = os.Environ()

	// using uncancelled context because context remains active for the
	// duration of the process, after dial has completed
	return commandconn.New(context.WithoutCancel(ctx), cmd.Path, cmd.Args[1:]...)
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
func (d *imageDriver) create(ctx context.Context, imageRef string, containerName string, volumeName string, cleanup bool, port int, opts *DriverOpts) (target *url.URL, rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, "create")
	defer telemetry.End(span, func() error { return rerr })
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	if containerName == "" {
		id, err := resolveImageID(imageRef)
		if err != nil {
			return nil, err
		}
		// run the container using that id in the name
		containerName = containerNamePrefix + id
	}

	leftoverEngines, err := d.collectLeftoverEngines(ctx, containerName)
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
			cmd := d.backend.ContainerStart(leftoverEngine)
			if _, err := traceexec.Exec(ctx, cmd); err != nil {
				// TODO: apple container returns 'running' instead of 'created'
				// return nil, fmt.Errorf("failed to start container %s: %w", output, err)
			}
			d.garbageCollectEngines(ctx, cleanup, slices.Delete(leftoverEngines, i, i+1))
			return &url.URL{
				Scheme: "docker-container",
				Host:   containerName,
			}, nil
		}
	}

	// ensure the image is pulled
	if _, err := traceexec.Exec(ctx, d.backend.ImageInspect(imageRef), telemetry.Encapsulated()); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("failed to inspect image: %w", err)
		}
		if _, err := traceexec.Exec(ctx, d.backend.ImagePull(imageRef)); err != nil {
			return nil, fmt.Errorf("failed to pull image: %w", err)
		}
	}

	volume := distconsts.EngineDefaultStateDir
	if volumeName != "" {
		volume = volumeName + ":" + volume
	}

	runOptions := runOpts{
		image:      imageRef,
		volumes:    []string{volume},
		privileged: true,
		args:       []string{"--debug"},
	}

	// mount the config path
	if _, err := os.Stat(engineConfigPath); err == nil {
		runOptions.volumes = append(runOptions.volumes, engineConfigPath+":"+config.DefaultConfigPath())
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not stat config", "path", engineConfigPath, "error", err)
	}

	// XXX: keep all these secrets? modify the args.
	if opts.DaggerCloudToken != "" {
		runOptions.env = append(runOptions.env, fmt.Sprintf("%s=%s", EnvDaggerCloudToken, opts.DaggerCloudToken))
	}
	if opts.GPUSupport != "" {
		runOptions.gpus = true
		runOptions.env = append(runOptions.env, fmt.Sprintf("%s=%s", EnvGPUSupport, opts.GPUSupport))
	}
	if port != 0 {
		runOptions.ports = append(runOptions.ports, fmt.Sprintf("%d:%d", port, port))
		runOptions.args = append(runOptions.args, "--addr", fmt.Sprintf("tcp://0.0.0.0:%d", port))
	}

	cmd := d.backend.ContainerRun(containerName, runOptions)
	cmd.Env = os.Environ()

	if output, err := traceexec.Exec(ctx, cmd); err != nil {
		// maybe someone else started the container simultaneously?
		if _, err2 := traceexec.Exec(ctx, d.backend.ContainerInspect(containerName), telemetry.Encapsulated()); err2 != nil {
			return nil, fmt.Errorf("failed to run container %s: %w", output, err)
		}
	}

	// garbage collect any other containers with the same name pattern, which
	// we assume to be leftover from previous runs of the engine using an older
	// version
	d.garbageCollectEngines(ctx, cleanup, leftoverEngines)

	return &url.URL{
		Scheme: "docker-container",
		Host:   containerName,
	}, nil
}

func (d *imageDriver) garbageCollectEngines(ctx context.Context, cleanup bool, engines []string) {
	if !cleanup {
		return
	}
	for _, engine := range engines {
		if engine == "" {
			continue
		}
		cmd := d.backend.ContainerRemove(engine)
		if _, err := traceexec.Exec(ctx, cmd); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
		}
	}
}

func (d *imageDriver) collectLeftoverEngines(ctx context.Context, additionalNames ...string) ([]string, error) {
	engines, err := d.backend.ContainerLs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers %s: %w", engines, err)
	}

	var filteredEngines []string
	for _, name := range engines {
		if strings.HasPrefix(name, containerNamePrefix) || slices.Contains(additionalNames, name) {
			filteredEngines = append(filteredEngines, name)
		}
	}
	return engines, nil
}

func resolveImageID(imageRef string) (string, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return "", fmt.Errorf("parsing image reference: %w", err)
	}
	if digest, ok := ref.(name.Digest); ok {
		// We already have the digest as part of the image ref
		_, id, ok := strings.Cut(digest.DigestStr(), "sha256:")
		if !ok {
			return "", fmt.Errorf("invalid image reference %q", imageRef)
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
