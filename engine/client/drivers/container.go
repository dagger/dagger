package drivers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/adrg/xdg"
	"github.com/google/go-containerregistry/pkg/name"
	"go.opentelemetry.io/otel"

	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
)

func init() {
	register("docker-image", dockerImageDriver)         // legacy
	register("docker-container", dockerContainerDriver) // legacy

	register("image",
		dockerImageDriver,
		appleImageDriver,
		podmanImageDriver,
		finchImageDriver,
		nerdctlImageDriver,
	)
	register("container",
		dockerContainerDriver,
		appleContainerDriver,
		podmanContainerDriver,
		finchContainerDriver,
		nerdctlContainerDriver,
	)

	register("image+docker", dockerImageDriver)
	register("container+docker", dockerContainerDriver)

	register("image+apple", appleImageDriver)
	register("container+apple", appleContainerDriver)

	register("image+podman", podmanImageDriver)
	register("container+podman", podmanContainerDriver)

	register("image+finch", finchImageDriver)
	register("container+finch", finchContainerDriver)

	register("image+nerdctl", nerdctlImageDriver)
	register("container+nerdctl", nerdctlContainerDriver)
}

var (
	dockerImageDriver     = &imageDriver{docker{cmd: "docker"}}
	dockerContainerDriver = &containerDriver{docker{cmd: "docker"}}

	appleImageDriver     = &imageDriver{apple{}}
	appleContainerDriver = &containerDriver{apple{}}

	podmanImageDriver     = &imageDriver{docker{cmd: "podman"}}
	podmanContainerDriver = &containerDriver{docker{cmd: "podman"}}

	nerdctlContainerDriver = &containerDriver{docker{cmd: "nerdctl"}}
	nerdctlImageDriver     = &imageDriver{docker{cmd: "nerdctl"}}

	finchImageDriver     = &imageDriver{docker{cmd: "finch"}}
	finchContainerDriver = &containerDriver{docker{cmd: "finch"}}
)

var (
	engineConfigPath       = filepath.Join(xdg.ConfigHome, "dagger", "engine.json")
	engineCertificatesPath = filepath.Join(xdg.ConfigHome, "dagger", "ca-certificates")
)

// containerBackend is a generic backend for containers that can be plugged
// into image/container drivers. This allows us to have all the exact same
// logic between different container providers, but have different backend
// commands.
type containerBackend interface {
	Available(ctx context.Context) (bool, error)

	ImagePull(ctx context.Context, image string) error
	ImageExists(ctx context.Context, image string) (bool, error)
	ImageRemove(ctx context.Context, image string) error
	ImageLoader(ctx context.Context) imageload.Backend

	ContainerRun(ctx context.Context, name string, opts runOpts) error
	ContainerExec(ctx context.Context, name string, args []string) (string, string, error)
	ContainerDial(ctx context.Context, name string, args []string) (net.Conn, error)
	ContainerRemove(ctx context.Context, name string) error
	ContainerStart(ctx context.Context, name string) error
	ContainerExists(ctx context.Context, name string) (bool, error)
	ContainerLs(ctx context.Context) ([]container, error)
}

var errContainerAlreadyExists = errors.New("container already exists")

type runOpts struct {
	image string

	volumes []string
	env     []string
	ports   []string

	privileged bool

	cpus   string
	memory string
	gpus   bool

	args []string
}

// imageDriver creates and manages a container, then connects to it
type imageDriver struct {
	backend containerBackend
}

func (d *imageDriver) Available(ctx context.Context) (bool, error) {
	return d.backend.Available(ctx)
}

func (d *imageDriver) Provision(ctx context.Context, target *url.URL, opts *DriverOpts) (Connector, error) {
	cleanup := true
	if val, ok := os.LookupEnv("DAGGER_LEAVE_OLD_ENGINE"); ok {
		b, _ := strconv.ParseBool(val)
		cleanup = !b
	} else if val := target.Query().Get("cleanup"); val != "" {
		cleanup, _ = strconv.ParseBool(val)
	}

	port, _ := strconv.Atoi(target.Query().Get("port"))
	target, err := d.create(ctx, containerCreateOpts{
		imageRef:      target.Host + target.Path,
		containerName: target.Query().Get("container"),
		volumeName:    target.Query().Get("volume"),
		cleanup:       cleanup,
		port:          port,
		cpus:          target.Query().Get("cpus"),
		memory:        target.Query().Get("memory"),
	}, opts)
	if err != nil {
		return nil, err
	}
	return containerConnector{
		backend: d.backend,
		host:    target.Host,
		values:  target.Query(),
	}, nil
}

func (d *imageDriver) ImageLoader(ctx context.Context) imageload.Backend {
	return d.backend.ImageLoader(ctx)
}

type containerConnector struct {
	host    string
	values  url.Values
	backend containerBackend
}

// imageDriver connects to a container directly
type containerDriver struct {
	backend containerBackend
}

func (d *containerDriver) Available(ctx context.Context) (bool, error) {
	return d.backend.Available(ctx)
}

func (d *containerDriver) Provision(ctx context.Context, target *url.URL, opts *DriverOpts) (Connector, error) {
	return containerConnector{
		backend: d.backend,
		host:    target.Host,
		values:  target.Query(),
	}, nil
}

func (d *containerDriver) ImageLoader(ctx context.Context) imageload.Backend {
	return d.backend.ImageLoader(ctx)
}

func (d containerConnector) Connect(ctx context.Context) (net.Conn, error) {
	args := []string{}
	if context := d.values.Get("context"); context != "" {
		args = append(args, "--context="+context)
	}
	args = append(args, "buildctl", "dial-stdio")

	// using uncancelled context because context remains active for the
	// duration of the process, after dial has completed
	return d.backend.ContainerDial(context.WithoutCancel(ctx), d.host, args)
}

func (d containerConnector) EngineID() string {
	// not supported yet
	return ""
}

const (
	// trim image digests to 16 characters to makeoutput more readable
	hashLen             = 16
	containerNamePrefix = "dagger-engine-"
)

const InstrumentationLibrary = "dagger.io/client.drivers"

type containerCreateOpts struct {
	imageRef string

	containerName string
	volumeName    string

	cleanup bool

	port int

	cpus   string
	memory string
}

// Pull the image and run it with a unique name tied to the pinned
// sha of the image. Remove any other containers leftover from
// previous executions of the engine at different versions (which
// are identified by looking for containers with the prefix
// "dagger-engine-").
func (d *imageDriver) create(ctx context.Context, opts containerCreateOpts, dopts *DriverOpts) (target *url.URL, rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, "create container")
	defer telemetry.EndWithCause(span, &rerr)
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	containerName := opts.containerName
	if containerName == "" {
		id, err := resolveImageID(opts.imageRef)
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
		leftoverEngines = nil
	}

	for i, leftoverEngine := range leftoverEngines {
		// if we already have a container with that name, attempt to start it
		if leftoverEngine.name == containerName {
			if leftoverEngine.running {
				break
			}
			if err := d.backend.ContainerStart(ctx, leftoverEngine.name); err != nil {
				return nil, fmt.Errorf("failed to start container: %w", err)
			}
			d.garbageCollectEngines(ctx, opts.cleanup, slices.Delete(leftoverEngines, i, i+1))
			return &url.URL{Host: containerName}, nil
		}
	}

	// ensure the image is pulled
	exists, err := d.backend.ImageExists(ctx, opts.imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect image: %w", err)
	}
	if !exists {
		if err := d.backend.ImagePull(ctx, opts.imageRef); err != nil {
			return nil, fmt.Errorf("failed to pull image: %w", err)
		}
	}

	volume := distconsts.EngineDefaultStateDir
	if opts.volumeName != "" {
		volume = opts.volumeName + ":" + volume
	}

	runOptions := runOpts{
		image:      opts.imageRef,
		volumes:    []string{volume},
		privileged: true,
		args:       []string{"--debug"},
		cpus:       opts.cpus,
		memory:     opts.memory,
	}

	// mount the config path
	if _, err := os.Stat(engineConfigPath); err == nil {
		runOptions.volumes = append(runOptions.volumes, engineConfigPath+":"+config.DefaultConfigPath())
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not stat config", "path", engineConfigPath, "error", err)
	}
	// mount the certificates path
	if _, err := os.Stat(engineCertificatesPath); err == nil {
		runOptions.volumes = append(runOptions.volumes, engineCertificatesPath+":"+distconsts.EngineCustomCACertsDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not stat certificates", "path", engineCertificatesPath, "error", err)
	}

	if dopts.DaggerCloudToken != "" {
		runOptions.env = append(runOptions.env, fmt.Sprintf("%s=%s", EnvDaggerCloudToken, dopts.DaggerCloudToken))
	}
	if dopts.GPUSupport != "" {
		runOptions.gpus = true
		runOptions.env = append(runOptions.env, fmt.Sprintf("%s=%s", EnvGPUSupport, dopts.GPUSupport))
	}
	if opts.port != 0 {
		runOptions.ports = append(runOptions.ports, fmt.Sprintf("%d:%d", opts.port, opts.port))
		runOptions.args = append(runOptions.args, "--addr", fmt.Sprintf("tcp://0.0.0.0:%d", opts.port))
	}

	if err := d.backend.ContainerRun(ctx, containerName, runOptions); err != nil {
		// maybe someone else started the container simultaneously?
		if !errors.Is(err, errContainerAlreadyExists) {
			if exists, _ := d.backend.ContainerExists(ctx, containerName); !exists {
				return nil, fmt.Errorf("failed to run container: %w", err)
			}
		}
	}

	// garbage collect any other containers with the same name pattern, which
	// we assume to be leftover from previous runs of the engine using an older
	// version
	d.garbageCollectEngines(ctx, opts.cleanup, leftoverEngines)

	return &url.URL{Host: containerName}, nil
}

func (d *imageDriver) garbageCollectEngines(ctx context.Context, cleanup bool, engines []container) {
	if !cleanup {
		return
	}
	for _, engine := range engines {
		if engine.name == "" {
			continue
		}
		if err := d.backend.ContainerRemove(ctx, engine.name); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
		}
	}
}

type container struct {
	name    string
	running bool
}

func (d *imageDriver) collectLeftoverEngines(ctx context.Context, additionalNames ...string) ([]container, error) {
	engines, err := d.backend.ContainerLs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	filteredEngines := make([]container, 0, len(engines))
	for _, engine := range engines {
		if strings.HasPrefix(engine.name, containerNamePrefix) || slices.Contains(additionalNames, engine.name) {
			filteredEngines = append(filteredEngines, engine)
		}
	}
	return filteredEngines, nil
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
