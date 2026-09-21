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
	"sync"

	"github.com/adrg/xdg"
	telemetry "github.com/dagger/otel-go"
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
	ContainerLs(ctx context.Context) ([]string, error)
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
		env:           target.Query()["env"],
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
		warm:    newWarmTunnels(),
	}, nil
}

func (d *imageDriver) ImageLoader(ctx context.Context) imageload.Backend {
	return d.backend.ImageLoader(ctx)
}

type containerConnector struct {
	host    string
	values  url.Values
	backend containerBackend

	// warm holds tunnels dialed ahead of the clients that will ask for
	// them; see Connect.
	warm *warmTunnels
}

// A command opens a few connections to the engine, one per client library
// (gRPC control, session, telemetry, API), and each one dials when its
// library is created, one after the other. Through the exec tunnel a dial
// is a runc exec, ~40ms. So once the engine has answered on the first
// tunnel, the connector dials this many more at once and hands them out
// as clients ask: a later client waits for its own dial only, not for
// the ones before it. Anything past them dials on demand. Tunnels no
// client asked for end with the process.
const warmTunnelCount = 3

// warmTunnels holds one channel per warm dial; a dial that failed delivers
// nil. A client takes a channel to wait on, and gives it back if it stops
// waiting, so the tunnel goes to the next client instead of leaking.
type warmTunnels struct {
	once    sync.Once
	pending chan chan net.Conn
}

func newWarmTunnels() *warmTunnels {
	return &warmTunnels{pending: make(chan chan net.Conn, warmTunnelCount)}
}

// start dials the warm tunnels in the background, the first time only.
func (w *warmTunnels) start(dial func() net.Conn) {
	w.once.Do(func() {
		for range warmTunnelCount {
			ch := make(chan net.Conn, 1)
			w.pending <- ch
			go func() { ch <- dial() }()
		}
	})
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
		warm:    newWarmTunnels(),
	}, nil
}

func (d *containerDriver) ImageLoader(ctx context.Context) imageload.Backend {
	return d.backend.ImageLoader(ctx)
}

func (d containerConnector) Connect(ctx context.Context) (net.Conn, error) {
	select {
	case ch := <-d.warm.pending:
		select {
		case conn := <-ch:
			if conn != nil {
				return conn, nil
			}
			// the warm dial failed: dial fresh below, which reports its own error
		case <-ctx.Done():
			d.warm.pending <- ch
			return nil, ctx.Err()
		}
	default:
	}
	conn, err := d.dial(ctx)
	if err != nil {
		return nil, err
	}
	// A dial succeeds as soon as the exec process starts, whether or not
	// the engine behind it answers: into a still-booting or paused engine
	// the tunnel opens and then reads EOF. So the warm tunnels are dialed
	// only once this one has delivered a byte, which is the engine talking.
	return &answeredConn{Conn: conn, answered: func() {
		d.warm.start(func() net.Conn {
			conn, _ := d.dial(ctx)
			return conn
		})
	}}, nil
}

// answeredConn calls answered the first time a Read returns data.
type answeredConn struct {
	net.Conn
	once     sync.Once
	answered func()
}

func (c *answeredConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.once.Do(c.answered)
	}
	return n, err
}

func (d containerConnector) dial(ctx context.Context) (net.Conn, error) {
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
	hashLen                     = 16
	containerNamePrefix         = "dagger-engine-"
	defaultDebugListenerAddress = "127.0.0.1:6060"
)

const InstrumentationLibrary = "dagger.io/client.drivers"

type containerCreateOpts struct {
	imageRef string

	containerName string
	volumeName    string

	cleanup bool

	port int

	env []string

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

	// The common case is an engine that already exists: look it up by name
	// and start it (a no-op when it already runs), instead of listing every
	// container on the host first. The listing grows with the host and was
	// most of a command's connect time. Leftovers from older versions are
	// still swept, in the background. Only when the engine is missing does
	// the command list before running a new one.
	if exists, err := d.backend.ContainerExists(ctx, containerName); err == nil && exists {
		if err := d.backend.ContainerStart(ctx, containerName); err != nil {
			return nil, fmt.Errorf("failed to start container: %w", err)
		}
		go d.sweepLeftoverEngines(context.WithoutCancel(ctx), opts.cleanup, containerName)
		return &url.URL{Host: containerName}, nil
	} else if errors.Is(err, context.Canceled) {
		return nil, err
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
			if err := d.backend.ContainerStart(ctx, leftoverEngine); err != nil {
				return nil, fmt.Errorf("failed to start container: %w", err)
			}
			d.garbageCollectEngines(ctx, opts.cleanup, nil, slices.Delete(leftoverEngines, i, i+1))
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
		args:       []string{"--debug", "--debugaddr", defaultDebugListenerAddress},
		env:        opts.env,
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
	d.garbageCollectEngines(ctx, opts.cleanup, nil, leftoverEngines)

	return &url.URL{Host: containerName}, nil
}

// sweepLeftoverEngines removes engines of other versions, sparing current.
// It runs in the background once the engine is known to exist, so a command
// does not wait for the listing; a sweep cut short by the process exiting
// is finished by a later command.
func (d *imageDriver) sweepLeftoverEngines(ctx context.Context, cleanup bool, current string) {
	if !cleanup {
		return
	}
	leftoverEngines, err := d.collectLeftoverEngines(ctx)
	if err != nil {
		slog.SpanLogger(ctx, InstrumentationLibrary).Warn("failed to list containers", "error", err)
		return
	}
	d.garbageCollectEngines(ctx, cleanup, []string{current}, leftoverEngines)
}

func (d *imageDriver) garbageCollectEngines(ctx context.Context, cleanup bool, preserveNames, engines []string) {
	if !cleanup {
		return
	}
	for _, engineName := range engines {
		if engineName == "" || slices.Contains(preserveNames, engineName) {
			continue
		}
		if err := d.backend.ContainerRemove(ctx, engineName); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
		}
	}
}

func engineNames(versions []string) []string {
	names := make([]string, 0, len(versions))
	for _, version := range versions {
		if version != "" {
			names = append(names, containerNamePrefix+version)
		}
	}
	return names
}

// CleanupOldEngines removes local engine containers while preserving the
// versions that are expected to be used by the current command.
func CleanupOldEngines(ctx context.Context, preserveVersions []string) error {
	driver, err := GetDriver(ctx, "image")
	if err != nil {
		return err
	}
	imageDriver, ok := driver.(*imageDriver)
	if !ok {
		return nil
	}
	leftoverEngines, err := imageDriver.collectLeftoverEngines(ctx)
	if err != nil {
		return err
	}
	imageDriver.garbageCollectEngines(ctx, true, engineNames(preserveVersions), leftoverEngines)
	return nil
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
