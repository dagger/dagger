package buildkit

/*
The original implementation of this is derived from:
https://github.com/moby/buildkit/blob/08180a774253a8199ebdb629d21cd9f378a14419/executor/runcexecutor/executor.go
*/

import (
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
	runc "github.com/containerd/go-runc"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/executor"
	"github.com/moby/buildkit/executor/oci"
	resourcestypes "github.com/moby/buildkit/executor/resources/types"
	gatewayapi "github.com/moby/buildkit/frontend/gateway/pb"
	randid "github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/moby/buildkit/util/stack"
	"github.com/moby/sys/signal"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/runtime-spec/specs-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

type ExecutionMetadata struct {
	ClientID    string
	SessionID   string
	SecretToken string
	Hostname    string

	// The "stable" ID of the client that is used to identify filesync cache refs
	// across different clients running on the same host.
	// For now, nested execs are just always given a random unique ID each exec (as
	// opposed to clients running on the host which re-use a persisted ID).
	ClientStableID string

	// Unique (random) ID for this execution.
	// This is used to deduplicate the same execution that gets evaluated multiple times.
	ExecID string

	// Internal execution initiated by dagger and not the user.
	// Used when executing the module runtime itself.
	Internal bool

	// TODO: can rm EncodedModuleID now
	CallID              *call.ID
	EncodedModuleID     string
	EncodedFunctionCall json.RawMessage
	CallerClientID      string

	// Client resource IDs passed to this client from parent object fields.
	// Needed to handle finding any secrets, sockets or other client resources
	// that this client should have access to due to being set in the parent
	// object.
	ParentIDs map[digest.Digest]*resource.ID

	CachePerSession bool

	// hostname -> list of aliases
	HostAliases map[string][]string
	// search domains to install prior to the session's domain
	ExtraSearchDomains []string

	RedirectStdoutPath string
	RedirectStderrPath string

	SecretEnvNames  []string
	SecretFilePaths []string

	SystemEnvNames []string

	EnabledGPUs []string

	SpanContext propagation.MapCarrier

	// Path to the SSH auth socket. Used for Dagger-in-Dagger support.
	SSHAuthSocketPath string

	// If true, skip injecting dumb-init into the container.
	NoInit bool
}

const executionMetadataKey = "dagger.executionMetadata"

func executionMetadataFromVtx(vtx solver.Vertex) (*ExecutionMetadata, bool, error) {
	if vtx == nil {
		return nil, false, nil
	}
	return ExecutionMetadataFromDescription(vtx.Options().Description)
}

func ExecutionMetadataFromDescription(desc map[string]string) (*ExecutionMetadata, bool, error) {
	if desc == nil {
		return nil, false, nil
	}

	bs, ok := desc[executionMetadataKey]
	if !ok {
		return nil, false, nil
	}

	md := ExecutionMetadata{}
	if err := json.Unmarshal([]byte(bs), &md); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal execution metadata: %w", err)
	}
	return &md, true, nil
}

func (md ExecutionMetadata) AsConstraintsOpt() (llb.ConstraintsOpt, error) {
	bs, err := json.Marshal(md)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal execution metadata: %w", err)
	}
	return llb.WithDescription(map[string]string{
		executionMetadataKey: string(bs),
	}), nil
}

func (w *Worker) Run(
	ctx context.Context,
	id string,
	rootMount executor.Mount,
	mounts []executor.Mount,
	procInfo executor.ProcessInfo,
	started chan<- struct{},
) (_ resourcestypes.Recorder, rerr error) {
	if id == "" {
		id = randid.NewID()
	}

	if err := w.validateEntitlements(procInfo.Meta); err != nil {
		return nil, err
	}

	state := newExecState(id, &procInfo, rootMount, mounts)
	return nil, w.run(ctx, state, started,
		w.setupNetwork,
		w.injectDumbInit,
		w.generateBaseSpec,
		w.filterEnvs,
		w.setupRootfs,
		w.setUserGroup,
		w.setExitCodePath,
		w.setupStdio,
		w.setupOTel,
		w.setupSecretScrubbing,
		w.setProxyEnvs,
		w.enableGPU,
		w.createCWD,
		w.setupNestedClient,
		w.installCACerts,
	)
}

func (w *Worker) run(
	ctx context.Context,
	state *execState,
	started chan<- struct{},
	setupFuncs ...executorSetupFunc,
) (rerr error) {
	startedOnce := sync.Once{}
	w.mu.Lock()
	w.running[state.id] = state
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.running, state.id)
		w.mu.Unlock()

		close(state.done)
		if err := state.cleanups.Run(); err != nil {
			bklog.G(ctx).Errorf("executor run failed to cleanup: %v", err)
			rerr = errors.Join(rerr, err)
		}
		state.doneErr = rerr

		if started != nil {
			startedOnce.Do(func() {
				close(started)
			})
		}
	}()

	for _, f := range setupFuncs {
		if err := f(ctx, state); err != nil {
			bklog.G(ctx).Errorf("executor setup failed: %v", err)
			return err
		}
	}

	bundle := filepath.Join(w.executorRoot, state.id)
	if err := os.Mkdir(bundle, 0o711); err != nil {
		return err
	}
	defer os.RemoveAll(bundle)

	configPath := filepath.Join(bundle, "config.json")
	f, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(state.spec); err != nil {
		return fmt.Errorf("failed to encode spec: %w", err)
	}
	f.Close()

	lg := bklog.G(ctx).
		WithField("id", state.id).
		WithField("args", state.spec.Process.Args)
	if w.execMD != nil {
		lg = lg.WithField("caller_client_id", w.execMD.CallerClientID)
		if w.execMD.CallID != nil {
			lg = lg.WithField("call_id", w.execMD.CallID.Display())
		}
		if w.execMD.ClientID != "" {
			lg = lg.WithField("nested_client_id", w.execMD.ClientID)
		}
	}
	lg.Debug("starting container")
	defer func() {
		lg.WithError(rerr).Debug("container done")
	}()

	trace.SpanFromContext(ctx).AddEvent("Container created")
	killer := newRunProcKiller(w.runc, state.id)
	startedCallback := func() {
		startedOnce.Do(func() {
			trace.SpanFromContext(ctx).AddEvent("Container started")
			if started != nil {
				close(started)
			}
		})
	}
	runcCall := func(ctx context.Context, started chan<- int, io runc.IO, pidfile string) error {
		_, err := w.runc.Run(ctx, state.id, bundle, &runc.CreateOpts{
			Started:   started,
			IO:        io,
			ExtraArgs: []string{"--keep"},
		})
		return err
	}
	err = exitError(ctx, state.exitCodePath, w.callWithIO(ctx, state.procInfo, startedCallback, killer, runcCall), state.procInfo.Meta.ValidExitCodes)
	if err != nil {
		w.runc.Delete(context.TODO(), state.id, &runc.DeleteOpts{})
		return err
	}

	return w.runc.Delete(context.TODO(), state.id, &runc.DeleteOpts{})
}

// Namespaced is something that has Linux namespaces set up.
// Currently this is either a full-blown container or just a raw
// network namespace that's setns'd into to support service tunneling
// and similar.
type Namespaced interface {
	NamespaceID() string
	Release(context.Context) error
}

type networkNamespace struct {
	id      string
	cleanup *Cleanups
}

var _ Namespaced = (*networkNamespace)(nil)

func (n *networkNamespace) NamespaceID() string {
	return n.id
}

func (n *networkNamespace) Release(_ context.Context) error {
	return n.cleanup.Run()
}

func (w *Worker) newNetNS(ctx context.Context, hostname string) (_ *networkNamespace, rerr error) {
	provider, ok := w.networkProviders[pb.NetMode_UNSET] // get default CNI provider
	if !ok {
		return nil, fmt.Errorf("no default network provider found")
	}

	cleanup := &Cleanups{}
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, cleanup.Run())
		}
	}()

	netNS, err := provider.New(ctx, hostname)
	if err != nil {
		return nil, fmt.Errorf("failed to create network namespace: %w", err)
	}
	cleanup.Add("close netns", netNS.Close)

	state := &execState{
		done:             make(chan struct{}),
		networkNamespace: netNS,
		netNSJobs:        make(chan func()),
		cleanups:         cleanup,
	}
	cleanup.Add("mark run state done", Infallible(func() {
		close(state.done)
	}))

	if err := w.runNetNSWorkers(ctx, state); err != nil {
		return nil, fmt.Errorf("failed to handle namespace jobs: %w", err)
	}

	id := randid.NewID()
	w.mu.Lock()
	w.running[id] = state
	w.mu.Unlock()
	cleanup.Add("delete run state", Infallible(func() {
		w.mu.Lock()
		delete(w.running, id)
		w.mu.Unlock()
	}))

	return &networkNamespace{
		id:      id,
		cleanup: cleanup,
	}, nil
}

func (w *Worker) Exec(ctx context.Context, id string, process executor.ProcessInfo) (err error) {
	if err := w.validateEntitlements(process.Meta); err != nil {
		return err
	}

	// first verify the container is running, if we get an error assume the container
	// is in the process of being created and check again every 100ms or until
	// context is canceled.
	var runcState *runc.Container
	for {
		w.mu.RLock()
		execState, ok := w.running[id]
		w.mu.RUnlock()
		if !ok {
			return fmt.Errorf("container %s not found", id)
		}

		runcState, _ = w.runc.State(ctx, id)
		if runcState != nil && runcState.Status == "running" {
			break
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-execState.done:
			if execState.doneErr == nil {
				return fmt.Errorf("container %s has stopped", id)
			}
			return fmt.Errorf("container %s has exited with error: %w", id, execState.doneErr)
		case <-time.After(100 * time.Millisecond):
		}
	}

	// load default process spec (for Env, Cwd etc) from bundle
	spec := &specs.Spec{}
	f, err := os.Open(filepath.Join(runcState.Bundle, "config.json"))
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(spec); err != nil {
		return err
	}

	spec.Process.Terminal = process.Meta.Tty
	spec.Process.Args = process.Meta.Args
	if process.Meta.Cwd != "" {
		spec.Process.Cwd = process.Meta.Cwd
	}

	if len(process.Meta.Env) > 0 {
		spec.Process.Env = process.Meta.Env
	}

	if process.Meta.User != "" {
		uid, gid, sgids, err := oci.GetUser(runcState.Rootfs, process.Meta.User)
		if err != nil {
			return err
		}
		spec.Process.User = specs.User{
			UID:            uid,
			GID:            gid,
			AdditionalGids: sgids,
		}
	}

	err = w.exec(ctx, id, spec.Process, process, nil)
	return exitError(ctx, "", err, process.Meta.ValidExitCodes)
}

func (w *Worker) exec(ctx context.Context, id string, specsProcess *specs.Process, process executor.ProcessInfo, started func()) error {
	killer, err := newExecProcKiller(w.runc, id)
	if err != nil {
		return fmt.Errorf("failed to initialize process killer: %w", err)
	}
	defer killer.Cleanup()

	return w.callWithIO(ctx, &process, started, killer, func(ctx context.Context, started chan<- int, io runc.IO, pidfile string) error {
		return w.runc.Exec(ctx, id, *specsProcess, &runc.ExecOpts{
			Started: started,
			IO:      io,
			PidFile: pidfile,
		})
	})
}

func (w *Worker) validateEntitlements(meta executor.Meta) error {
	return w.entitlements.Check(entitlements.Values{
		NetworkHost:      meta.NetMode == pb.NetMode_HOST,
		SecurityInsecure: meta.SecurityMode == pb.SecurityMode_INSECURE,
	})
}

func exitError(ctx context.Context, exitCodePath string, err error, validExitCodes []int) error {
	exitErr := &gatewayapi.ExitError{ExitCode: uint32(gatewayapi.UnknownExitStatus), Err: err}

	if err == nil {
		exitErr.ExitCode = 0
	} else {
		var runcExitError *runc.ExitError
		if errors.As(err, &runcExitError) {
			exitErr = &gatewayapi.ExitError{ExitCode: uint32(runcExitError.Status)}
		}
	}

	if exitCodePath != "" {
		if err := os.WriteFile(exitCodePath, []byte(fmt.Sprintf("%d", exitErr.ExitCode)), 0o600); err != nil {
			bklog.G(ctx).Errorf("failed to write exit code %d to %s: %v", exitErr.ExitCode, exitCodePath, err)
		}
	}

	trace.SpanFromContext(ctx).AddEvent(
		"Container exited",
		trace.WithAttributes(attribute.Int("exit.code", int(exitErr.ExitCode))),
	)

	if validExitCodes == nil {
		// no exit codes specified, so only 0 is allowed
		if exitErr.ExitCode == 0 {
			return nil
		}
	} else {
		for _, code := range validExitCodes {
			// exit code in allowed list, so exit cleanly
			if code == int(exitErr.ExitCode) {
				return nil
			}
		}
	}

	select {
	case <-ctx.Done():
		exitErr.Err = fmt.Errorf("%s: %w", exitErr.Error(), context.Cause(ctx))
		return exitErr
	default:
		return stack.Enable(exitErr)
	}
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
	defer cancel(nil)

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

func (w *Worker) callWithIO(ctx context.Context, process *executor.ProcessInfo, started func(), killer procKiller, call runcCall) error {
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
