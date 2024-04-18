package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/diff/apply"
	"github.com/containerd/containerd/diff/walking"
	ctdmetadata "github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	containerdoci "github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/platforms"
	ctdsnapshot "github.com/containerd/containerd/snapshots"
	"github.com/containerd/continuity/fs"
	runc "github.com/containerd/go-runc"
	"github.com/dagger/dagger/cmd/engine/cacerts"
	"github.com/docker/docker/pkg/idtools"
	"github.com/moby/buildkit/cache/metadata"
	"github.com/moby/buildkit/executor"
	"github.com/moby/buildkit/executor/oci"
	resourcestypes "github.com/moby/buildkit/executor/resources/types"
	gatewayapi "github.com/moby/buildkit/frontend/gateway/pb"
	randid "github.com/moby/buildkit/identity"
	containerdsnapshot "github.com/moby/buildkit/snapshot/containerd"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/network"
	"github.com/moby/buildkit/util/network/netproviders"
	"github.com/moby/buildkit/util/stack"
	"github.com/moby/buildkit/util/winlayers"
	"github.com/moby/buildkit/worker/base"
	wlabel "github.com/moby/buildkit/worker/label"
	workerrunc "github.com/moby/buildkit/worker/runc"
	"github.com/moby/sys/signal"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	bolt "go.etcd.io/bbolt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func NewWorkerOpt(
	root string,
	snFactory workerrunc.SnapshotterFactory,
	processMode oci.ProcessMode,
	labels map[string]string,
	idmap *idtools.IdentityMapping,
	nopt netproviders.Opt,
	dns *oci.DNSConfig,
	apparmorProfile string,
	selinux bool,
	parallelismSem *semaphore.Weighted,
	traceSocket,
	defaultCgroupParent string,
) (base.WorkerOpt, error) {
	var opt base.WorkerOpt
	name := "runc-" + snFactory.Name
	root = filepath.Join(root, name)
	if err := os.MkdirAll(root, 0700); err != nil {
		return opt, err
	}

	np, npResolvedMode, err := netproviders.Providers(nopt)
	if err != nil {
		return opt, err
	}

	exe, err := New(Opt{
		Root:                filepath.Join(root, "executor"),
		Cmd:                 "/usr/local/bin/dagger-shim",
		ProcessMode:         processMode,
		IdentityMapping:     idmap,
		DNS:                 dns,
		ApparmorProfile:     apparmorProfile,
		SELinux:             selinux,
		TracingSocket:       traceSocket,
		DefaultCgroupParent: defaultCgroupParent,
	}, np)
	if err != nil {
		return opt, err
	}
	s, err := snFactory.New(filepath.Join(root, "snapshots"))
	if err != nil {
		return opt, err
	}

	localstore, err := local.NewStore(filepath.Join(root, "content"))
	if err != nil {
		return opt, err
	}

	db, err := bolt.Open(filepath.Join(root, "containerdmeta.db"), 0644, nil)
	if err != nil {
		return opt, err
	}

	mdb := ctdmetadata.NewDB(db, localstore, map[string]ctdsnapshot.Snapshotter{
		snFactory.Name: s,
	})
	if err := mdb.Init(context.TODO()); err != nil {
		return opt, err
	}

	c := containerdsnapshot.NewContentStore(mdb.ContentStore(), "buildkit")

	id, err := base.ID(root)
	if err != nil {
		return opt, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	xlabels := map[string]string{
		wlabel.Executor:       "oci",
		wlabel.Snapshotter:    snFactory.Name,
		wlabel.Hostname:       hostname,
		wlabel.Network:        npResolvedMode,
		wlabel.OCIProcessMode: processMode.String(),
		wlabel.SELinuxEnabled: strconv.FormatBool(selinux),
	}
	if apparmorProfile != "" {
		xlabels[wlabel.ApparmorProfile] = apparmorProfile
	}

	for k, v := range labels {
		xlabels[k] = v
	}
	lm := leaseutil.WithNamespace(ctdmetadata.NewLeaseManager(mdb), "buildkit")
	snap := containerdsnapshot.NewSnapshotter(snFactory.Name, mdb.Snapshotter(snFactory.Name), "buildkit", idmap)
	md, err := metadata.NewStore(filepath.Join(root, "metadata_v2.db"))
	if err != nil {
		return opt, err
	}

	opt = base.WorkerOpt{
		ID:               id,
		Labels:           xlabels,
		MetadataStore:    md,
		NetworkProviders: np,
		Executor:         exe,
		Snapshotter:      snap,
		ContentStore:     c,
		Applier:          winlayers.NewFileSystemApplierWithWindows(c, apply.NewFileSystemApplier(c)),
		Differ:           winlayers.NewWalkingDiffWithWindows(c, walking.NewWalkingDiff(c)),
		ImageStore:       nil, // explicitly
		Platforms:        []ocispecs.Platform{platforms.Normalize(platforms.DefaultSpec())},
		IdentityMapping:  idmap,
		LeaseManager:     lm,
		GarbageCollect:   mdb.GarbageCollect,
		ParallelismSem:   parallelismSem,
		MountPoolRoot:    filepath.Join(root, "cachemounts"),
	}
	return opt, nil
}

type Opt struct {
	// root directory
	Root string
	Cmd  string
	// DefaultCgroupParent is the cgroup-parent name for executor
	DefaultCgroupParent string
	// ProcessMode
	ProcessMode     oci.ProcessMode
	IdentityMapping *idtools.IdentityMapping
	// runc run --no-pivot (unrecommended)
	NoPivot         bool
	DNS             *oci.DNSConfig
	OOMScoreAdj     *int
	ApparmorProfile string
	SELinux         bool
	TracingSocket   string
}

type runcExecutor struct {
	runc             *runc.Runc
	root             string
	cgroupParent     string
	networkProviders map[pb.NetMode]network.Provider
	processMode      oci.ProcessMode
	idmap            *idtools.IdentityMapping
	noPivot          bool
	dns              *oci.DNSConfig
	oomScoreAdj      *int
	running          map[string]chan error
	mu               sync.Mutex
	apparmorProfile  string
	selinux          bool
	tracingSocket    string
}

func New(opt Opt, networkProviders map[pb.NetMode]network.Provider) (executor.Executor, error) {
	root := opt.Root

	if err := os.MkdirAll(root, 0o711); err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", root, err)
	}

	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}

	// clean up old hosts/resolv.conf file. ignore errors
	os.RemoveAll(filepath.Join(root, "hosts"))
	os.RemoveAll(filepath.Join(root, "resolv.conf"))

	runtime := &runc.Runc{
		Command:   opt.Cmd,
		Log:       filepath.Join(root, "runc-log.json"),
		LogFormat: runc.JSON,
		Setpgid:   true,
	}

	updateRuncFieldsForHostOS(runtime)

	w := &runcExecutor{
		runc:             runtime,
		root:             root,
		cgroupParent:     opt.DefaultCgroupParent,
		networkProviders: networkProviders,
		processMode:      opt.ProcessMode,
		idmap:            opt.IdentityMapping,
		noPivot:          opt.NoPivot,
		dns:              opt.DNS,
		oomScoreAdj:      opt.OOMScoreAdj,
		running:          make(map[string]chan error),
		apparmorProfile:  opt.ApparmorProfile,
		selinux:          opt.SELinux,
		tracingSocket:    opt.TracingSocket,
	}
	return w, nil
}

func (w *runcExecutor) Run(ctx context.Context, id string, root executor.Mount, mounts []executor.Mount, process executor.ProcessInfo, started chan<- struct{}) (_ resourcestypes.Recorder, rerr error) {
	meta := process.Meta
	if meta.NetMode == pb.NetMode_HOST {
		bklog.G(ctx).Info("enabling HostNetworking")
	}

	provider, ok := w.networkProviders[meta.NetMode]
	if !ok {
		return nil, fmt.Errorf("unknown network mode %s", meta.NetMode)
	}
	namespace, err := provider.New(ctx, meta.Hostname)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := namespace.Close(); err != nil {
			bklog.G(ctx).Errorf("failed to close network namespace: %v", err)
		}
	}()

	mountable, err := root.Src.Mount(ctx, false)
	if err != nil {
		return nil, err
	}

	rootMount, release, err := mountable.Mount()
	if err != nil {
		return nil, err
	}
	if release != nil {
		defer release()
	}

	if id == "" {
		id = randid.NewID()
	}

	identity := idtools.Identity{}
	if w.idmap != nil {
		identity = w.idmap.RootPair()
	}

	rootFSPath, err := os.MkdirTemp("", "rootfs")
	if err != nil {
		return nil, err
	}
	if err := idtools.MkdirAllAndChown(rootFSPath, 0o700, identity); err != nil {
		return nil, err
	}
	if err := mount.All(rootMount, rootFSPath); err != nil {
		return nil, err
	}
	defer mount.Unmount(rootFSPath, 0)

	defer executor.MountStubsCleaner(ctx, rootFSPath, mounts, meta.RemoveMountStubsRecursive)()

	setupCACerts := true
	return nil, w.run(
		ctx,
		id,
		root,
		mounts,
		rootFSPath,
		process,
		namespace,
		started,
		setupCACerts,
	)
}

func (w *runcExecutor) run(
	ctx context.Context,
	id string,
	root executor.Mount,
	mounts []executor.Mount,
	rootFSPath string,
	process executor.ProcessInfo,
	namespace network.Namespace,
	started chan<- struct{},
	installCACerts bool,
) (rerr error) {
	startedOnce := sync.Once{}
	done := make(chan error, 1)
	w.mu.Lock()
	w.running[id] = done
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.running, id)
		w.mu.Unlock()
		done <- rerr
		close(done)
		if started != nil {
			startedOnce.Do(func() {
				close(started)
			})
		}
	}()

	bundle := filepath.Join(w.root, id)
	if err := os.Mkdir(bundle, 0o711); err != nil {
		return err
	}
	defer os.RemoveAll(bundle)

	uid, gid, sgids, err := oci.GetUser(rootFSPath, process.Meta.User)
	if err != nil {
		return err
	}

	resolvConf, err := oci.GetResolvConf(ctx, w.root, w.idmap, w.dns, process.Meta.NetMode)
	if err != nil {
		return err
	}

	hostsFile, clean, err := oci.GetHostsFile(ctx, w.root, process.Meta.ExtraHosts, w.idmap, process.Meta.Hostname)
	if err != nil {
		return err
	}
	if clean != nil {
		defer clean()
	}

	configPath := filepath.Join(bundle, "config.json")
	f, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	opts := []containerdoci.SpecOpts{oci.WithUIDGID(uid, gid, sgids)}

	if process.Meta.ReadonlyRootFS {
		opts = append(opts, containerdoci.WithRootFSReadonly())
	}

	identity := idtools.Identity{
		UID: int(uid),
		GID: int(gid),
	}
	if w.idmap != nil {
		identity, err = w.idmap.ToHost(identity)
		if err != nil {
			return err
		}
	}

	spec, cleanup, err := oci.GenerateSpec(
		ctx,
		process.Meta,
		mounts,
		id,
		resolvConf,
		hostsFile,
		namespace,
		w.cgroupParent,
		w.processMode,
		w.idmap,
		w.apparmorProfile,
		w.selinux,
		w.tracingSocket,
		opts...,
	)
	if err != nil {
		return err
	}
	defer cleanup()

	spec.Root.Path = rootFSPath
	if root.Readonly {
		spec.Root.Readonly = true
	}

	newp, err := fs.RootPath(rootFSPath, process.Meta.Cwd)
	if err != nil {
		return fmt.Errorf("working dir %s points to invalid target: %w", newp, err)
	}
	if _, err := os.Stat(newp); err != nil {
		if err := idtools.MkdirAllAndChown(newp, 0o755, identity); err != nil {
			return fmt.Errorf("failed to create working directory %s: %w", newp, err)
		}
	}

	spec.Process.Terminal = process.Meta.Tty
	spec.Process.OOMScoreAdj = w.oomScoreAdj

	if err := json.NewEncoder(f).Encode(spec); err != nil {
		return err
	}
	f.Close()

	bklog.G(ctx).Debugf("> creating %s %v", id, process.Meta.Args)
	defer bklog.G(ctx).Debugf("> container done %s %v", id, process.Meta.Args)

	if installCACerts {
		caInstaller, err := cacerts.NewInstaller(ctx, spec, func(ctx context.Context, args ...string) error {
			id := randid.NewID()
			meta := process.Meta
			meta.Args = args
			meta.User = "0:0"
			meta.Cwd = "/"
			meta.Tty = false
			output := new(bytes.Buffer)
			process := executor.ProcessInfo{
				Stdout: nopCloser{output},
				Stderr: nopCloser{output},
				Meta:   meta,
			}
			started := make(chan struct{}, 1)
			installCACerts := false
			err := w.run(
				ctx,
				id,
				root,
				mounts,
				rootFSPath,
				process,
				namespace,
				started,
				installCACerts,
			)
			return err
		})
		if err == nil {
			err = caInstaller.Install(ctx)
			if errors.As(err, new(cacerts.CleanupErr)) {
				// if install failed and cleanup failed too, we have no choice but to fail this exec; otherwise we're
				// leaving the container in some weird half state
				return fmt.Errorf("failed to install cacerts: %w", err)
			}
			// if install failed but we were able to cleanup, then we should log it but don't need to fail the exec
			if err != nil {
				bklog.G(ctx).Errorf("failed to install cacerts but successfully cleaned up, continuing without CA certs: %v", err)
			} else {
				defer func() {
					if err := caInstaller.Uninstall(ctx); err != nil {
						bklog.G(ctx).Errorf("failed to uninstall cacerts: %v", err)
						rerr = errors.Join(rerr, err)
					}
				}()
			}
		} else {
			bklog.G(ctx).Errorf("failed to create cacerts installer, falling back to not installing CA certs: %v", err)
		}
	}

	trace.SpanFromContext(ctx).AddEvent("Container created")
	killer := newRunProcKiller(w.runc, id)
	startedCallback := func() {
		startedOnce.Do(func() {
			trace.SpanFromContext(ctx).AddEvent("Container started")
			if started != nil {
				close(started)
			}
		})
	}
	runcCall := func(ctx context.Context, started chan<- int, io runc.IO, pidfile string) error {
		_, err := w.runc.Run(ctx, id, bundle, &runc.CreateOpts{
			NoPivot:   w.noPivot,
			Started:   started,
			IO:        io,
			ExtraArgs: []string{"--keep"},
		})
		return err
	}
	err = exitError(ctx, w.callWithIO(ctx, id, bundle, process, startedCallback, killer, runcCall))
	if err != nil {
		w.runc.Delete(context.TODO(), id, &runc.DeleteOpts{})
		return err
	}

	return w.runc.Delete(context.TODO(), id, &runc.DeleteOpts{})
}

func (w *runcExecutor) Exec(ctx context.Context, id string, process executor.ProcessInfo) (err error) {
	// first verify the container is running, if we get an error assume the container
	// is in the process of being created and check again every 100ms or until
	// context is canceled.
	var state *runc.Container
	for {
		w.mu.Lock()
		done, ok := w.running[id]
		w.mu.Unlock()
		if !ok {
			return fmt.Errorf("container %s not found", id)
		}

		state, _ = w.runc.State(ctx, id)
		if state != nil && state.Status == "running" {
			break
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case err, ok := <-done:
			if !ok || err == nil {
				return fmt.Errorf("container %s has stopped", id)
			}
			return fmt.Errorf("container %s has exited with error: %w", id, err)
		case <-time.After(100 * time.Millisecond):
		}
	}

	// load default process spec (for Env, Cwd etc) from bundle
	f, err := os.Open(filepath.Join(state.Bundle, "config.json"))
	if err != nil {
		return err
	}
	defer f.Close()

	spec := &specs.Spec{}
	if err := json.NewDecoder(f).Decode(spec); err != nil {
		return err
	}

	if process.Meta.User != "" {
		uid, gid, sgids, err := oci.GetUser(state.Rootfs, process.Meta.User)
		if err != nil {
			return err
		}
		spec.Process.User = specs.User{
			UID:            uid,
			GID:            gid,
			AdditionalGids: sgids,
		}
	}

	spec.Process.Terminal = process.Meta.Tty
	spec.Process.Args = process.Meta.Args
	if process.Meta.Cwd != "" {
		spec.Process.Cwd = process.Meta.Cwd
	}

	if len(process.Meta.Env) > 0 {
		spec.Process.Env = process.Meta.Env
	}

	err = w.exec(ctx, id, state.Bundle, spec.Process, process, nil)
	return exitError(ctx, err)
}

func (w *runcExecutor) exec(ctx context.Context, id, bundle string, specsProcess *specs.Process, process executor.ProcessInfo, started func()) error {
	killer, err := newExecProcKiller(w.runc, id)
	if err != nil {
		return fmt.Errorf("failed to initialize process killer: %w", err)
	}
	defer killer.Cleanup()

	return w.callWithIO(ctx, id, bundle, process, started, killer, func(ctx context.Context, started chan<- int, io runc.IO, pidfile string) error {
		return w.runc.Exec(ctx, id, *specsProcess, &runc.ExecOpts{
			Started: started,
			IO:      io,
			PidFile: pidfile,
		})
	})
}

func exitError(ctx context.Context, err error) error {
	if err != nil {
		exitErr := &gatewayapi.ExitError{
			ExitCode: gatewayapi.UnknownExitStatus,
			Err:      err,
		}
		var runcExitError *runc.ExitError
		if errors.As(err, &runcExitError) && runcExitError.Status >= 0 {
			exitErr = &gatewayapi.ExitError{
				ExitCode: uint32(runcExitError.Status),
			}
		}
		trace.SpanFromContext(ctx).AddEvent(
			"Container exited",
			trace.WithAttributes(
				attribute.Int("exit.code", int(exitErr.ExitCode)),
			),
		)
		select {
		case <-ctx.Done():
			exitErr.Err = fmt.Errorf(exitErr.Error())
			return exitErr
		default:
			return stack.Enable(exitErr)
		}
	}

	trace.SpanFromContext(ctx).AddEvent(
		"Container exited",
		trace.WithAttributes(attribute.Int("exit.code", 0)),
	)
	return nil
}

type forwardIO struct {
	stdin          io.ReadCloser
	stdout, stderr io.WriteCloser
}

func (s *forwardIO) Close() error {
	return nil
}

func (s *forwardIO) Set(cmd *exec.Cmd) {
	cmd.Stdin = s.stdin
	cmd.Stdout = s.stdout
	cmd.Stderr = s.stderr
}

func (s *forwardIO) Stdin() io.WriteCloser {
	return nil
}

func (s *forwardIO) Stdout() io.ReadCloser {
	return nil
}

func (s *forwardIO) Stderr() io.ReadCloser {
	return nil
}

// newRuncProcKiller returns an abstraction for sending SIGKILL to the
// process inside the container initiated from `runc run`.
func newRunProcKiller(runC *runc.Runc, id string) procKiller {
	return procKiller{runC: runC, id: id}
}

// newExecProcKiller returns an abstraction for sending SIGKILL to the
// process inside the container initiated from `runc exec`.
func newExecProcKiller(runC *runc.Runc, id string) (procKiller, error) {
	// for `runc exec` we need to create a pidfile and read it later to kill
	// the process
	tdir, err := os.MkdirTemp("", "runc")
	if err != nil {
		return procKiller{}, fmt.Errorf("failed to create directory for runc pidfile: %w", err)
	}

	return procKiller{
		runC:    runC,
		id:      id,
		pidfile: filepath.Join(tdir, "pidfile"),
		cleanup: func() {
			os.RemoveAll(tdir)
		},
	}, nil
}

type procKiller struct {
	runC    *runc.Runc
	id      string
	pidfile string
	cleanup func()
}

// Cleanup will delete any tmp files created for the pidfile allocation
// if this killer was for a `runc exec` process.
func (k procKiller) Cleanup() {
	if k.cleanup != nil {
		k.cleanup()
	}
}

// Kill will send SIGKILL to the process running inside the container.
// If the process was created by `runc run` then we will use `runc kill`,
// otherwise for `runc exec` we will read the pid from a pidfile and then
// send the signal directly that process.
func (k procKiller) Kill(ctx context.Context) (err error) {
	bklog.G(ctx).Debugf("sending sigkill to process in container %s", k.id)
	defer func() {
		if err != nil {
			bklog.G(ctx).Errorf("failed to kill process in container id %s: %+v", k.id, err)
		}
	}()

	// this timeout is generally a no-op, the Kill ctx should already have a
	// shorter timeout but here as a fail-safe for future refactoring.
	ctx, cancel := context.WithCancelCause(ctx)
	ctx, _ = context.WithTimeoutCause(ctx, 10*time.Second, context.DeadlineExceeded)
	defer cancel(context.Canceled)

	if k.pidfile == "" {
		// for `runc run` process we use `runc kill` to terminate the process
		return k.runC.Kill(ctx, k.id, int(syscall.SIGKILL), nil)
	}

	// `runc exec` will write the pidfile a few milliseconds after we
	// get the runc pid via the startedCh, so we might need to retry until
	// it appears in the edge case where we want to kill a process
	// immediately after it was created.
	var pidData []byte
	for {
		pidData, err = os.ReadFile(k.pidfile)
		if err != nil {
			if os.IsNotExist(err) {
				select {
				case <-ctx.Done():
					return errors.New("context cancelled before runc wrote pidfile")
				case <-time.After(10 * time.Millisecond):
					continue
				}
			}
			return fmt.Errorf("failed to read pidfile from runc: %w", err)
		}
		break
	}
	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		return fmt.Errorf("read invalid pid from pidfile: %w", err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		// error only possible on non-unix hosts
		return fmt.Errorf("failed to find process for pid %d from pidfile: %w", pid, err)
	}
	defer process.Release()
	return process.Signal(syscall.SIGKILL)
}

// procHandle is to track the process so we can send signals to it
// and handle graceful shutdown.
type procHandle struct {
	// this is for the runc process (not the process in-container)
	monitorProcess *os.Process
	ready          chan struct{}
	ended          chan struct{}
	shutdown       func(error)
	// this this only used when the request context is canceled and we need
	// to kill the in-container process.
	killer procKiller
}

// runcProcessHandle will create a procHandle that will be monitored, where
// on ctx.Done the in-container process will receive a SIGKILL.  The returned
// context should be used for the go-runc.(Run|Exec) invocations.  The returned
// context will only be canceled in the case where the request context is
// canceled and we are unable to send the SIGKILL to the in-container process.
// The goal is to allow for runc to gracefully shutdown when the request context
// is cancelled.
func runcProcessHandle(ctx context.Context, killer procKiller) (*procHandle, context.Context) {
	runcCtx, cancel := context.WithCancelCause(context.Background())
	p := &procHandle{
		ready:    make(chan struct{}),
		ended:    make(chan struct{}),
		shutdown: cancel,
		killer:   killer,
	}
	// preserve the logger on the context used for the runc process handling
	runcCtx = bklog.WithLogger(runcCtx, bklog.G(ctx))

	go func() {
		// Wait for pid
		select {
		case <-ctx.Done():
			return // nothing to kill
		case <-p.ready:
		}

		for {
			select {
			case <-ctx.Done():
				killCtx, timeout := context.WithCancelCause(context.Background())
				killCtx, _ = context.WithTimeoutCause(killCtx, 7*time.Second, context.DeadlineExceeded)
				if err := p.killer.Kill(killCtx); err != nil {
					select {
					case <-killCtx.Done():
						cancel(context.Cause(ctx))
						return
					default:
					}
				}
				timeout(context.Canceled)
				select {
				case <-time.After(50 * time.Millisecond):
				case <-p.ended:
					return
				}
			case <-p.ended:
				return
			}
		}
	}()

	return p, runcCtx
}

// Release will free resources with a procHandle.
func (p *procHandle) Release() {
	close(p.ended)
	if p.monitorProcess != nil {
		p.monitorProcess.Release()
	}
}

// Shutdown should be called after the runc process has exited. This will allow
// the signal handling and tty resize loops to exit, terminating the
// goroutines.
func (p *procHandle) Shutdown() {
	if p.shutdown != nil {
		p.shutdown(context.Canceled)
	}
}

// WaitForReady will wait until we have received the runc pid via the go-runc
// Started channel, or until the request context is canceled.  This should
// return without errors before attempting to send signals to the runc process.
func (p *procHandle) WaitForReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-p.ready:
		return nil
	}
}

// WaitForStart will record the runc pid reported by go-runc via the channel.
// We wait for up to 10s for the runc pid to be reported.  If the started
// callback is non-nil it will be called after receiving the pid.
func (p *procHandle) WaitForStart(ctx context.Context, startedCh <-chan int, started func()) error {
	ctx, cancel := context.WithCancelCause(ctx)
	ctx, _ = context.WithTimeoutCause(ctx, 10*time.Second, context.DeadlineExceeded)
	defer cancel(context.Canceled)
	select {
	case <-ctx.Done():
		return errors.New("go-runc started message never received")
	case runcPid, ok := <-startedCh:
		if !ok {
			return errors.New("go-runc failed to send pid")
		}
		if started != nil {
			started()
		}
		var err error
		p.monitorProcess, err = os.FindProcess(runcPid)
		if err != nil {
			// error only possible on non-unix hosts
			return fmt.Errorf("failed to find runc process %d: %w", runcPid, err)
		}
		close(p.ready)
	}
	return nil
}

func updateRuncFieldsForHostOS(runtime *runc.Runc) {
	// PdeathSignal only supported on unix platforms
	runtime.PdeathSignal = syscall.SIGKILL // this can still leak the process
}

// handleSignals will wait until the procHandle is ready then will
// send each signal received on the channel to the runc process (not directly
// to the in-container process)
func handleSignals(ctx context.Context, runcProcess *procHandle, signals <-chan syscall.Signal) error {
	if signals == nil {
		return nil
	}
	err := runcProcess.WaitForReady(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case sig := <-signals:
			if sig == syscall.SIGKILL {
				// never send SIGKILL directly to runc, it needs to go to the
				// process in-container
				if err := runcProcess.killer.Kill(ctx); err != nil {
					return err
				}
				continue
			}
			if err := runcProcess.monitorProcess.Signal(sig); err != nil {
				bklog.G(ctx).Errorf("failed to signal %s to process: %s", sig, err)
				return err
			}
		}
	}
}

type runcCall func(ctx context.Context, started chan<- int, io runc.IO, pidfile string) error

func (w *runcExecutor) callWithIO(ctx context.Context, id, bundle string, process executor.ProcessInfo, started func(), killer procKiller, call runcCall) error {
	runcProcess, ctx := runcProcessHandle(ctx, killer)
	defer runcProcess.Release()

	eg, ctx := errgroup.WithContext(ctx)
	defer func() {
		if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
			bklog.G(ctx).Errorf("runc process monitoring error: %s", err)
		}
	}()
	defer runcProcess.Shutdown()

	startedCh := make(chan int, 1)
	eg.Go(func() error {
		return runcProcess.WaitForStart(ctx, startedCh, started)
	})

	eg.Go(func() error {
		return handleSignals(ctx, runcProcess, process.Signal)
	})

	if !process.Meta.Tty {
		return call(ctx, startedCh, &forwardIO{stdin: process.Stdin, stdout: process.Stdout, stderr: process.Stderr}, killer.pidfile)
	}

	ptm, ptsName, err := console.NewPty()
	if err != nil {
		return err
	}

	pts, err := os.OpenFile(ptsName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		ptm.Close()
		return err
	}

	defer func() {
		if process.Stdin != nil {
			process.Stdin.Close()
		}
		pts.Close()
		ptm.Close()
		runcProcess.Shutdown()
		err := eg.Wait()
		if err != nil {
			bklog.G(ctx).Warningf("error while shutting down tty io: %s", err)
		}
	}()

	if process.Stdin != nil {
		eg.Go(func() error {
			_, err := io.Copy(ptm, process.Stdin)
			// stdin might be a pipe, so this is like EOF
			if errors.Is(err, io.ErrClosedPipe) {
				return nil
			}
			return err
		})
	}

	if process.Stdout != nil {
		eg.Go(func() error {
			_, err := io.Copy(process.Stdout, ptm)
			// ignore `read /dev/ptmx: input/output error` when ptm is closed
			var ptmClosedError *os.PathError
			if errors.As(err, &ptmClosedError) {
				if ptmClosedError.Op == "read" &&
					ptmClosedError.Path == "/dev/ptmx" &&
					ptmClosedError.Err == syscall.EIO {
					return nil
				}
			}
			return err
		})
	}

	eg.Go(func() error {
		err := runcProcess.WaitForReady(ctx)
		if err != nil {
			return err
		}
		for {
			select {
			case <-ctx.Done():
				return nil
			case resize := <-process.Resize:
				err = ptm.Resize(console.WinSize{
					Height: uint16(resize.Rows),
					Width:  uint16(resize.Cols),
				})
				if err != nil {
					bklog.G(ctx).Errorf("failed to resize ptm: %s", err)
				}
				// SIGWINCH must be sent to the runc monitor process, as
				// terminal resizing is done in runc.
				err = runcProcess.monitorProcess.Signal(signal.SIGWINCH)
				if err != nil {
					bklog.G(ctx).Errorf("failed to send SIGWINCH to process: %s", err)
				}
			}
		}
	})

	runcIO := &forwardIO{}
	if process.Stdin != nil {
		runcIO.stdin = pts
	}
	if process.Stdout != nil {
		runcIO.stdout = pts
	}
	if process.Stderr != nil {
		runcIO.stderr = pts
	}

	return call(ctx, startedCh, runcIO, killer.pidfile)
}

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error { return nil }
