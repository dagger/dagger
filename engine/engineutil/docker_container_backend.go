package engineutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"

	"golang.org/x/sync/errgroup"

	"github.com/containerd/platforms"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/executor"
	gatewayapi "github.com/dagger/dagger/internal/buildkit/frontend/gateway/pb"
	randid "github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/distribution/reference"
	dockerrouter "github.com/docker/docker/api/server/router"
	dockercontainer "github.com/docker/docker/api/server/router/container"
	"github.com/docker/docker/api/types/backend"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/namesgenerator"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/docker/pkg/sysinfo"
	"github.com/docker/docker/runconfig"
	archive "github.com/moby/go-archive"
	"go.opentelemetry.io/otel/trace"
)

func newDockerContainerRouter(ctx context.Context, cli *Client, callerExecID string) dockerrouter.Router {
	sysInfo := sysinfo.New()
	decoder := runconfig.ContainerDecoder{GetSysInfo: func() *sysinfo.SysInfo { return sysInfo }}
	return dockercontainer.NewRouter(newDockerContainerBackend(ctx, cli, callerExecID), decoder, sysInfo.CgroupUnified)
}

func newDockerContainerBackend(ctx context.Context, cli *Client, callerExecID string) dockercontainer.Backend {
	return &dockerContainerBackend{
		ctx:          ctx,
		cli:          cli,
		callerExecID: callerExecID,
		storage:      new(dockerContainerBackendStorageSyncMap),
	}
}

type dockerContainerBackend struct {
	ctx          context.Context // server-lifetime context
	cli          *Client
	callerExecID string // exec state ID of the container that invoked docker

	storage dockerContainerBackendStorage
}

var _ dockercontainer.Backend = (*dockerContainerBackend)(nil)

// ContainerCreate stores the container config in memory and returns a new ID.
func (b *dockerContainerBackend) ContainerCreate(ctx context.Context, cfg backend.ContainerCreateConfig) (container.CreateResponse, error) {
	if cfg.Config == nil {
		return container.CreateResponse{}, errors.New("container create: nil config")
	}
	id := randid.NewID()
	name := cfg.Name
	if name == "" {
		name = namesgenerator.GetRandomName(0)
	}
	containerCfg := cfg.Config

	imageRef := containerCfg.Image
	if imageRef != "" {
		if b.cli == nil || b.cli.GetRegistryResolver == nil {
			return container.CreateResponse{}, errors.New("docker: engine client not fully configured")
		}

		if parsed, err := reference.ParseNormalizedNamed(imageRef); err == nil {
			imageRef = reference.TagNameOnly(parsed).String()
		}

		if rslvr, err := b.cli.GetRegistryResolver(b.ctx); err == nil {
			platform := platforms.DefaultSpec()
			if _, _, configBytes, err := rslvr.ResolveImageConfig(b.ctx, imageRef, serverresolver.ResolveImageConfigOpts{
				Platform: &platform,
			}); err == nil {
				var imgSpec struct {
					Config struct {
						Env        []string          `json:"Env"`
						Cmd        []string          `json:"Cmd"`
						Entrypoint []string          `json:"Entrypoint"`
						WorkingDir string            `json:"WorkingDir"`
						User       string            `json:"User"`
						Labels     map[string]string `json:"Labels"`
					} `json:"config"`
				}
				if err := json.Unmarshal(configBytes, &imgSpec); err == nil {
					out := *containerCfg
					// Image env is the base; container env overrides/appends.
					out.Env = append(imgSpec.Config.Env, containerCfg.Env...) //nolint:gocritic
					if len(out.Cmd) == 0 {
						out.Cmd = imgSpec.Config.Cmd
					}
					if len(out.Entrypoint) == 0 {
						out.Entrypoint = imgSpec.Config.Entrypoint
					}
					if out.WorkingDir == "" {
						out.WorkingDir = imgSpec.Config.WorkingDir
					}
					if out.User == "" {
						out.User = imgSpec.Config.User
					}
					containerCfg = &out
				}
			}
		}
	}

	stdinR, stdinW := io.Pipe()
	rec := &dockerContainer{
		id:       id,
		name:     name,
		imageRef: imageRef,
		config:   containerCfg,
		status:   dockerContainerStatusCreated,
		done:     make(chan struct{}),
		stdinR:   stdinR,
		stdinW:   stdinW,
		stdout:   newDockerContainerStream(),
		stderr:   newDockerContainerStream(),
	}
	if cfg.HostConfig != nil {
		rec.hostConfig = cfg.HostConfig
		if err := validateHostConfig(cfg.HostConfig); err != nil {
			return container.CreateResponse{}, err
		}
	}
	if err := b.storage.Create(ctx, rec); err != nil {
		return container.CreateResponse{}, fmt.Errorf("container create: store: %w", err)
	}
	return container.CreateResponse{ID: id, Warnings: []string{"using Docker via Dagger experimentalDockerCompatibility"}}, nil
}

// ContainerStart pulls the image, creates a writable snapshot, and calls cli.Run.
func (b *dockerContainerBackend) ContainerStart(ctx context.Context, id, _, _ string) error {
	rec, ok, err := b.storage.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("container %q not found", id)
	}

	rec.mu.Lock()
	if rec.status == dockerContainerStatusRunning {
		rec.mu.Unlock()
		return fmt.Errorf("container %q already running", id)
	}
	rec.status = dockerContainerStatusRunning
	rec.mu.Unlock()

	if b.cli == nil || b.cli.SnapshotAccessor == nil || b.cli.GetRegistryResolver == nil {
		return errors.New("docker: engine client not fully configured")
	}

	// Use the server-lifetime context (which carries Dagger session metadata)
	// rather than the Docker HTTP request context for engine operations.
	engineCtx := b.ctx

	rslvr, err := b.cli.GetRegistryResolver(engineCtx)
	if err != nil {
		return fmt.Errorf("docker: get registry resolver: %w", err)
	}

	pulled, err := rslvr.Pull(engineCtx, rec.imageRef, serverresolver.PullOpts{
		Platform:    platforms.DefaultSpec(),
		ResolveMode: serverresolver.ResolveModeDefault,
	})
	if err != nil {
		return fmt.Errorf("docker: pull %q: %w", rec.imageRef, err)
	}
	defer pulled.Release(context.WithoutCancel(engineCtx))

	rootfs, err := b.cli.SnapshotAccessor.ImportImage(engineCtx, &bkcache.ImportedImage{
		Ref:          pulled.Ref,
		ManifestDesc: pulled.ManifestDesc,
		ConfigDesc:   pulled.ConfigDesc,
		Layers:       pulled.Layers,
		Nonlayers:    pulled.Nonlayers,
	}, bkcache.ImportImageOpts{
		ImageRef:   pulled.Ref,
		RecordType: bkclient.UsageRecordTypeRegular,
	})
	if err != nil {
		return fmt.Errorf("docker: import image %q: %w", rec.imageRef, err)
	}
	defer rootfs.Release(context.WithoutCancel(engineCtx))

	// Create a writable snapshot on top of the image.
	mutableRoot, err := b.cli.SnapshotAccessor.New(engineCtx, rootfs)
	if err != nil {
		return fmt.Errorf("docker: new mutable snapshot: %w", err)
	}

	cfg := rec.config
	args := append([]string(cfg.Entrypoint), []string(cfg.Cmd)...)
	cwd := cfg.WorkingDir
	if cwd == "" {
		cwd = "/"
	}

	if !cfg.OpenStdin {
		rec.stdinW.Close()
	}

	runCtx, runCancel := context.WithCancelCause(engineCtx)
	rec.mu.Lock()
	rec.cancel = runCancel
	rec.mu.Unlock()

	startedCh := make(chan struct{}, 1)

	go func() {
		defer close(rec.done)
		defer mutableRoot.Release(context.WithoutCancel(context.Background()))
		defer runCancel(nil)

		runErr := b.cli.Run(
			runCtx,
			rec.id,
			executor.Mount{Src: mutableRoot, Readonly: false},
			nil,
			executor.ProcessInfo{
				Meta: executor.Meta{
					Args:     args,
					Env:      cfg.Env,
					Cwd:      cwd,
					User:     cfg.User,
					Hostname: rec.name,
				},
				Stdin:  rec.stdinR,
				Stdout: rec.stdout,
				Stderr: rec.stderr,
			},
			startedCh,
			trace.SpanContext{},
			nil,
			"",
			"",
			nil,
			nil,
			nil,
			nil,
		)

		// Close the streams so any attached readers unblock.
		rec.stdout.Close()
		rec.stderr.Close()

		rec.mu.Lock()
		rec.waitErr = runErr
		if runErr == nil {
			rec.exitCode = 0
		} else {
			var exitErr *gatewayapi.ExitError
			if errors.As(runErr, &exitErr) {
				rec.exitCode = int(exitErr.ExitCode)
				rec.waitErr = nil
			} else {
				rec.exitCode = 1
			}
		}
		rec.status = dockerContainerStatusExited
		rec.mu.Unlock()
	}()

	// Set up port forwarding once the container network is ready.
	if rec.hostConfig != nil && len(rec.hostConfig.PortBindings) > 0 {
		go func() {
			select {
			case <-runCtx.Done():
				return
			case <-startedCh:
			}
			for port, bindings := range rec.hostConfig.PortBindings {
				containerPort := port.Int()
				proto := port.Proto()
				for _, binding := range bindings {
					hostIP := binding.HostIP
					if hostIP == "" {
						hostIP = "0.0.0.0"
					}
					hostAddr := net.JoinHostPort(hostIP, binding.HostPort)
					// Bind inside the caller exec's netns so that localhost:PORT
					// resolves from within the container that ran "docker run -p".
					ln, err := RunInNetNS(runCtx, b.cli, NewDirectNS(b.callerExecID), func() (net.Listener, error) {
						return net.Listen(proto, hostAddr)
					})
					if err != nil {
						bklog.G(runCtx).WithError(err).Warnf("docker: port forward listen %s failed", hostAddr)
						continue
					}
					upstreamAddr := fmt.Sprintf("127.0.0.1:%d", containerPort)
					go proxyPortForward(runCtx, ln, b.cli, NewDirectNS(rec.id), proto, upstreamAddr)
				}
			}
		}()
	}

	return nil
}

// ContainerWait blocks until the container with the given name exits.
func (b *dockerContainerBackend) ContainerWait(ctx context.Context, name string, _ container.WaitCondition) (<-chan container.StateStatus, error) {
	rec, ok, err := b.storage.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("container %q not found", name)
	}
	ch := make(chan container.StateStatus, 1)
	go func() {
		select {
		case <-ctx.Done():
			ch <- container.NewStateStatus(1, ctx.Err())
		case <-rec.done:
			rec.mu.RLock()
			exitCode := rec.exitCode
			waitErr := rec.waitErr
			rec.mu.RUnlock()
			ch <- container.NewStateStatus(exitCode, waitErr)
		}
	}()
	return ch, nil
}

// ContainerKill sends a signal to (i.e., cancels) the container.
func (b *dockerContainerBackend) ContainerKill(name, _ string) error {
	rec, ok, err := b.storage.Get(b.ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("container %q not found", name)
	}
	rec.mu.RLock()
	cancel := rec.cancel
	rec.mu.RUnlock()
	if cancel != nil {
		cancel(errors.New("killed"))
	}
	return nil
}

// ContainerStop cancels the container and waits for it to stop.
func (b *dockerContainerBackend) ContainerStop(ctx context.Context, name string, _ container.StopOptions) error {
	rec, ok, err := b.storage.Get(ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("container %q not found", name)
	}
	rec.mu.RLock()
	cancel := rec.cancel
	rec.mu.RUnlock()
	if cancel != nil {
		cancel(errors.New("stopped"))
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-rec.done:
	}
	return nil
}

// ContainerRm removes a stopped container from the registry.
func (b *dockerContainerBackend) ContainerRm(name string, _ *backend.ContainerRmConfig) error {
	rec, ok, err := b.storage.Get(b.ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("container %q not found", name)
	}
	rec.mu.RLock()
	status := rec.status
	rec.mu.RUnlock()
	if status == dockerContainerStatusRunning {
		return fmt.Errorf("container %q is running; stop it first", name)
	}
	return b.storage.Delete(b.ctx, rec.id)
}

// ContainerInspect returns information about the container.
func (b *dockerContainerBackend) ContainerInspect(_ context.Context, name string, _ backend.ContainerInspectOptions) (*container.InspectResponse, error) {
	rec, ok, err := b.storage.Get(b.ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("container %q not found", name)
	}
	rec.mu.RLock()
	defer rec.mu.RUnlock()
	running := rec.status == dockerContainerStatusRunning
	return &container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:    rec.id,
			Image: rec.imageRef,
			Name:  "/" + rec.name,
			State: &container.State{
				Status:   string(rec.status),
				Running:  running,
				ExitCode: rec.exitCode,
			},
		},
		Config: rec.config,
	}, nil
}

// Containers lists known containers.
func (b *dockerContainerBackend) Containers(ctx context.Context, opts *container.ListOptions) ([]*container.Summary, error) {
	all, err := b.storage.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*container.Summary
	for _, rec := range all {
		rec.mu.RLock()
		status := rec.status
		rec.mu.RUnlock()
		if !opts.All && status != dockerContainerStatusRunning {
			continue
		}
		out = append(out, &container.Summary{
			ID:     rec.id,
			Names:  []string{"/" + rec.name},
			Image:  rec.imageRef,
			Status: string(status),
			State:  string(status),
		})
	}
	return out, nil
}

// ContainerAttach hijacks the HTTP connection and wires client stdio to the container pipes.
func (b *dockerContainerBackend) ContainerAttach(name string, cfg *backend.ContainerAttachConfig) error {
	rec, ok, err := b.storage.Get(b.ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("container %q not found", name)
	}

	ctx, cancel := context.WithCancel(b.ctx)
	defer cancel()

	// Hijack the HTTP connection and obtain the client's stdio streams.
	cStdin, cStdout, cStderr, err := cfg.GetStreams(cfg.MuxStreams, cancel)
	if err != nil {
		return fmt.Errorf("docker attach: get streams: %w", err)
	}

	eg, _ := errgroup.WithContext(ctx)

	// Pipe client stdin → container stdin.
	if cfg.UseStdin && cStdin != nil {
		eg.Go(func() error {
			defer rec.stdinW.Close()
			defer cStdin.Close()
			_, err := io.Copy(rec.stdinW, cStdin)
			return err
		})
	} else {
		rec.stdinW.Close()
	}

	// Pipe container stdout → client stdout.
	if cfg.UseStdout && cStdout != nil {
		eg.Go(func() error {
			w := cStdout
			if cfg.MuxStreams {
				w = stdcopy.NewStdWriter(cStdout, stdcopy.Stdout)
			}
			return drainStream(ctx, rec.stdout, w)
		})
	}

	// Pipe container stderr → client stderr (may be same writer as stdout when muxed).
	if cfg.UseStderr && cStderr != nil {
		eg.Go(func() error {
			w := cStderr
			if cfg.MuxStreams {
				w = stdcopy.NewStdWriter(cStderr, stdcopy.Stderr)
			}
			return drainStream(ctx, rec.stderr, w)
		})
	}

	// Block until the container finishes so the HTTP connection stays open,
	// then close the client stdin so the docker client sees EOF on the attach stream.
	<-rec.done
	if cStdin != nil {
		cStdin.Close()
	}
	return eg.Wait()
}

var (
	errContainerUnimplemented = errors.New("docker container backend: not implemented")
)

func (b *dockerContainerBackend) ContainerArchivePath(name, path string) (io.ReadCloser, *container.PathStat, error) {
	return nil, nil, errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerChanges(ctx context.Context, name string) ([]archive.Change, error) {
	return nil, errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerExecCreate(name string, options *container.ExecOptions) (string, error) {
	return "", errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerExecInspect(id string) (*backend.ExecInspect, error) {
	return nil, errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerExecResize(ctx context.Context, name string, height, width uint32) error {
	return errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerExecStart(ctx context.Context, name string, options backend.ExecStartConfig) error {
	return errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerExport(ctx context.Context, name string, out io.Writer) error {
	return errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerExtractToDir(name, path string, copyUIDGID, noOverwriteDirNonDir bool, content io.Reader) error {
	return errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerLogs(ctx context.Context, name string, config *container.LogsOptions) (<-chan *backend.LogMessage, bool, error) {
	rec, ok, err := b.storage.Get(ctx, name)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, fmt.Errorf("container %q not found", name)
	}

	ch := make(chan *backend.LogMessage, 64)

	send := func(source string, stream *dockerContainerStream) {
		offset := 0
		for {
			data, next, err := stream.ReadFrom(ctx, offset)
			if len(data) > 0 {
				msg := make([]byte, len(data))
				copy(msg, data)
				ch <- &backend.LogMessage{Line: msg, Source: source}
				offset = next
			}
			if err != nil {
				return
			}
			if !config.Follow {
				return
			}
		}
	}

	go func() {
		defer close(ch)
		eg, egCtx := errgroup.WithContext(ctx)
		if config.ShowStdout {
			eg.Go(func() error { send("stdout", rec.stdout); return egCtx.Err() })
		}
		if config.ShowStderr {
			eg.Go(func() error { send("stderr", rec.stderr); return egCtx.Err() })
		}
		eg.Wait() //nolint:errcheck
	}()

	return ch, false, nil
}
func (b *dockerContainerBackend) ContainerPause(name string) error {
	return errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerRename(oldName, newName string) error {
	rec, ok, err := b.storage.Get(b.ctx, oldName)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("container %q not found", oldName)
	}
	rec.mu.Lock()
	rec.name = newName
	rec.mu.Unlock()
	return nil
}
func (b *dockerContainerBackend) ContainerResize(ctx context.Context, name string, height, width uint32) error {
	return errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerRestart(ctx context.Context, name string, options container.StopOptions) error {
	return errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerStatPath(name, path string) (*container.PathStat, error) {
	return nil, errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerStats(ctx context.Context, name string, config *backend.ContainerStatsConfig) error {
	return errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerTop(name, psArgs string) (*container.TopResponse, error) {
	return nil, errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerUnpause(name string) error {
	return errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainerUpdate(name string, hostConfig *container.HostConfig) (container.UpdateResponse, error) {
	return container.UpdateResponse{}, errContainerUnimplemented
}
func (b *dockerContainerBackend) ContainersPrune(ctx context.Context, pruneFilters filters.Args) (*container.PruneReport, error) {
	return nil, errContainerUnimplemented
}
func (b *dockerContainerBackend) CreateImageFromContainer(ctx context.Context, name string, config *backend.CreateImageConfig) (string, error) {
	return "", errContainerUnimplemented
}
func (b *dockerContainerBackend) ExecExists(name string) (bool, error) {
	return false, errContainerUnimplemented
}

// drainStream reads all data from stream and writes it to w until the stream
// is closed or ctx is cancelled.
func drainStream(ctx context.Context, stream *dockerContainerStream, w io.Writer) error {
	offset := 0
	for {
		data, next, err := stream.ReadFrom(ctx, offset)
		if len(data) > 0 {
			if _, werr := w.Write(data); werr != nil {
				return werr
			}
			offset = next
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// validateHostConfig returns an error for HostConfig fields that are not yet supported.
func validateHostConfig(hc *container.HostConfig) error {
	if len(hc.Binds) > 0 {
		return errors.New("docker: volume binds (-v) are not supported")
	}
	if len(hc.Mounts) > 0 {
		return errors.New("docker: mounts (--mount) are not supported")
	}
	if len(hc.VolumesFrom) > 0 {
		return errors.New("docker: --volumes-from is not supported")
	}
	if hc.Privileged {
		return errors.New("docker: --privileged is not supported")
	}
	if len(hc.CapAdd) > 0 {
		return errors.New("docker: --cap-add is not supported")
	}
	if len(hc.CapDrop) > 0 {
		return errors.New("docker: --cap-drop is not supported")
	}
	if len(hc.Devices) > 0 {
		return errors.New("docker: --device is not supported")
	}
	if len(hc.SecurityOpt) > 0 {
		return errors.New("docker: --security-opt is not supported")
	}
	if hc.UsernsMode != "" {
		return errors.New("docker: --userns is not supported")
	}
	if hc.PidMode != "" {
		return errors.New("docker: --pid is not supported")
	}
	if hc.IpcMode != "" && !hc.IpcMode.IsPrivate() && !hc.IpcMode.IsEmpty() {
		return fmt.Errorf("docker: --ipc %q is not supported", hc.IpcMode)
	}
	if len(hc.Tmpfs) > 0 {
		return errors.New("docker: --tmpfs is not supported")
	}
	if len(hc.Sysctls) > 0 {
		return errors.New("docker: --sysctl is not supported")
	}
	if len(hc.Ulimits) > 0 {
		return errors.New("docker: --ulimit is not supported")
	}
	return nil
}

// proxyPortForward accepts connections on ln (bound in the caller exec's netns)
// and proxies each one to upstreamAddr inside the container's netns.
func proxyPortForward(ctx context.Context, ln net.Listener, c *Client, ns Namespaced, proto, upstreamAddr string) {
	defer ln.Close()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		clientConn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer clientConn.Close()
			upstreamConn, err := RunInNetNS(ctx, c, ns, func() (net.Conn, error) {
				return net.Dial(proto, upstreamAddr)
			})
			if err != nil {
				return
			}
			defer upstreamConn.Close()
			done := make(chan struct{}, 2)
			go func() {
				io.Copy(upstreamConn, clientConn) //nolint:errcheck
				done <- struct{}{}
			}()
			go func() {
				io.Copy(clientConn, upstreamConn) //nolint:errcheck
				done <- struct{}{}
			}()
			<-done
		}()
	}
}
