package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"sync"
	"syscall"

	"github.com/dagger/dagger/dagql"
)

var ErrLLMHarnessProcessClosed = errors.New("LLM harness process is closed")

// LLMHarnessProcess is a session-scoped CLI protocol process. Its stdin stays
// open across turns; callers frame the vendor protocol on Write and Read.
// Process state is deliberately not part of an LLM or Container value.
type LLMHarnessProcess struct {
	command []string
	workdir string

	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	running *RunningService
	sio     *ServiceIO
	release func()

	writeMu sync.Mutex
	stateMu sync.Mutex
	closed  bool
	waitErr error

	closeInputOnce sync.Once
	releaseOnce    sync.Once
	done           chan struct{}
}

// llmHarnessCommand returns the persistent stdio command for a vendor. Neither
// command is a one-shot prompt invocation.
func llmHarnessCommand(kind LLMHarnessKind, nativeSession ...string) ([]string, error) {
	var sessionID string
	if len(nativeSession) > 0 {
		sessionID = nativeSession[0]
	}
	switch kind {
	case LLMHarnessCodex:
		spec := CodexLLMHarnessCommand()
		return append([]string{spec.Path}, spec.Args...), nil
	case LLMHarnessClaude:
		spec := ClaudeLLMHarnessCommand(sessionID)
		return append([]string{spec.Path}, spec.Args...), nil
	default:
		return nil, fmt.Errorf("unsupported LLM harness kind %q", kind)
	}
}

func validateLLMHarnessProcessConfig(harness dagql.ObjectResult[*Container], kind LLMHarnessKind, workspace dagql.ObjectResult[*Directory], nativeSession ...string) (string, []string, error) {
	if harness.Self() == nil {
		return "", nil, fmt.Errorf("harness container is required")
	}
	workdir := harness.Self().Config.WorkingDir
	if workdir == "" || path.Clean(workdir) == "/" {
		return "", nil, fmt.Errorf("harness container working directory must be non-empty and not root (/)")
	}
	if workspace.Self() == nil {
		return "", nil, fmt.Errorf("LLM workspace directory is required")
	}
	command, err := llmHarnessCommand(kind, nativeSession...)
	if err != nil {
		return "", nil, err
	}
	return workdir, command, nil
}

// startLLMHarnessProcess mounts the materialized LLM workspace at the harness
// container's configured working directory and starts one interactive service.
// The returned process is reused across native turns until Close.
func startLLMHarnessProcess(
	ctx context.Context,
	harness dagql.ObjectResult[*Container],
	kind LLMHarnessKind,
	workspace dagql.ObjectResult[*Directory],
	execHTTPHandlerToken string,
	nativeSession string,
) (_ *LLMHarnessProcess, rerr error) {
	workdir, command, err := validateLLMHarnessProcessConfig(harness, kind, workspace, nativeSession)
	if err != nil {
		return nil, err
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	ctr, err := cloneContainerForTerminal(ctx, query, harness.Self())
	if err != nil {
		return nil, fmt.Errorf("clone harness container: %w", err)
	}
	ctr, err = ctr.WithMountedDirectory(ctx, harness, ".", workspace, "", false)
	if err != nil {
		return nil, fmt.Errorf("mount LLM workspace at %q: %w", workdir, err)
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	mounted, err := newSyntheticTerminalContainerResult(srv, ctr, "llm_harness_container")
	if err != nil {
		return nil, fmt.Errorf("create harness container result: %w", err)
	}
	dig, err := mounted.ContentPreferredDigest(ctx)
	if err != nil {
		return nil, fmt.Errorf("harness container digest: %w", err)
	}
	svc, err := ctr.AsService(ctx, mounted, ContainerAsServiceArgs{Args: command})
	if err != nil {
		return nil, fmt.Errorf("create harness service: %w", err)
	}
	svc.ExecHTTPHandlerToken = execHTTPHandlerToken
	svcs, err := query.Services(ctx)
	if err != nil {
		return nil, err
	}

	return startLLMHarnessProcessService(workdir, command, func(sio *ServiceIO) (*RunningService, func(), error) {
		return svcs.StartInteractive(ctx, dig, svc, sio)
	})
}

type llmHarnessServiceStarter func(*ServiceIO) (*RunningService, func(), error)

// llmHarnessStartupWriter buffers output only while StartInteractive runs
// synchronously. Activation flushes those bytes into an ordinary io.Pipe before
// allowing subsequent writes through, restoring normal runtime backpressure.
type llmHarnessStartupWriter struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	pipe    *io.PipeWriter
	buf     bytes.Buffer

	active       bool
	flushing     bool
	writerClosed bool
	flushErr     error
}

func newLLMHarnessOutputPipe() (*io.PipeReader, *llmHarnessStartupWriter) {
	reader, writer := io.Pipe()
	return reader, &llmHarnessStartupWriter{pipe: writer}
}

func (w *llmHarnessStartupWriter) Write(buf []byte) (int, error) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()

	w.mu.Lock()
	if w.writerClosed {
		w.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	if w.flushErr != nil {
		err := w.flushErr
		w.mu.Unlock()
		return 0, err
	}
	if !w.active {
		n, err := w.buf.Write(buf)
		w.mu.Unlock()
		return n, err
	}
	w.mu.Unlock()
	return w.pipe.Write(buf)
}

func (w *llmHarnessStartupWriter) Close() error {
	w.mu.Lock()
	if w.writerClosed {
		w.mu.Unlock()
		return nil
	}
	w.writerClosed = true
	active := w.active
	flushing := w.flushing
	w.mu.Unlock()
	if !active || flushing {
		return nil
	}
	// Do not wait for writeMu: io.PipeWriter.Close must be able to unblock a
	// runtime Write when the adapter has stopped consuming output.
	return w.pipe.Close()
}

func (w *llmHarnessStartupWriter) activate() {
	// Hold writeMu across the asynchronous startup flush. Runtime writes cannot
	// overtake buffered bytes, while Close remains free to unblock the pipe.
	w.writeMu.Lock()
	w.mu.Lock()
	if w.active {
		w.mu.Unlock()
		w.writeMu.Unlock()
		return
	}
	w.active = true
	if w.buf.Len() == 0 {
		closed := w.writerClosed
		w.mu.Unlock()
		w.writeMu.Unlock()
		if closed {
			_ = w.pipe.Close()
		}
		return
	}
	startup := w.buf.Bytes()
	w.buf = bytes.Buffer{}
	w.flushing = true
	w.mu.Unlock()

	go func() {
		_, err := w.pipe.Write(startup)
		w.mu.Lock()
		w.flushErr = err
		w.flushing = false
		closed := w.writerClosed
		w.mu.Unlock()
		w.writeMu.Unlock()
		if closed {
			_ = w.pipe.Close()
		}
	}()
}

func startLLMHarnessProcessService(workdir string, command []string, start llmHarnessServiceStarter) (_ *LLMHarnessProcess, rerr error) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := newLLMHarnessOutputPipe()
	stderrR, stderrW := newLLMHarnessOutputPipe()
	defer func() {
		if rerr != nil {
			_ = errors.Join(
				stdinR.Close(), stdinW.Close(),
				stdoutR.Close(), stdoutW.Close(),
				stderrR.Close(), stderrW.Close(),
			)
		}
	}()

	sio := &ServiceIO{
		Stdin:  stdinR,
		Stdout: stdoutW,
		Stderr: stderrW,
	}
	running, release, err := start(sio)
	if err != nil {
		return nil, fmt.Errorf("start LLM harness process: %w", err)
	}
	if running == nil || running.Wait == nil || running.Stop == nil {
		if release != nil {
			release()
		}
		return nil, fmt.Errorf("start LLM harness process: incomplete running service")
	}

	// StartInteractive is finished, so bound startup buffering to this point.
	// Flushing happens asynchronously into ordinary pipes because readers only
	// become available after this function returns.
	stdoutW.activate()
	stderrW.activate()

	p := &LLMHarnessProcess{
		command: slices.Clone(command),
		workdir: workdir,
		stdin:   stdinW,
		stdout:  stdoutR,
		stderr:  stderrR,
		running: running,
		sio:     sio,
		release: release,
		done:    make(chan struct{}),
	}
	go p.wait()
	return p, nil
}

func (p *LLMHarnessProcess) wait() {
	err := p.running.Wait(context.Background())
	p.stateMu.Lock()
	p.waitErr = err
	p.closed = true
	p.stateMu.Unlock()
	// Service implementations normally close their ServiceIO before Wait
	// returns. Close it here too so every natural-exit path terminates adapter
	// reads, including lightweight/fake service implementations. Publish the
	// exit result first so the unblocked adapter observes the service error
	// rather than the pipe's generic EOF/closed error.
	_ = p.sio.Close()
	p.releaseResources()
	close(p.done)
}

func (p *LLMHarnessProcess) exitError(fallback error) error {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	if p.closed && p.waitErr != nil {
		return p.waitErr
	}
	return fallback
}

func (p *LLMHarnessProcess) releaseResources() {
	p.releaseOnce.Do(func() {
		if p.release != nil {
			p.release()
		}
	})
}

// Write sends protocol bytes to the persistent process. Writes are serialized
// so concurrent dispatchers cannot interleave JSON records.
func (p *LLMHarnessProcess) Write(buf []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.stateMu.Lock()
	closed := p.closed
	p.stateMu.Unlock()
	if closed {
		return 0, p.exitError(ErrLLMHarnessProcessClosed)
	}
	n, err := p.stdin.Write(buf)
	if err != nil {
		err = p.exitError(err)
	}
	return n, err
}

// Read reads protocol stdout. io.Pipe safely supports concurrent calls, though
// protocol adapters should ordinarily have exactly one event-reader goroutine.
func (p *LLMHarnessProcess) Read(buf []byte) (int, error) {
	n, err := p.stdout.Read(buf)
	if err != nil {
		// Container services close their ServiceIO immediately before Wait
		// returns. Await that result instead of racing it and leaking the pipe's
		// generic EOF to the adapter which was using the failed process.
		if waitErr := p.Wait(context.Background()); waitErr != nil {
			err = waitErr
		}
	}
	return n, err
}

// Stderr returns the separate diagnostic stream. Vendor protocol framing must
// only consume Read (stdout).
func (p *LLMHarnessProcess) Stderr() io.Reader {
	return p.stderr
}

// Interrupt sends SIGINT without closing stdio, allowing a CLI which handles
// interruption to remain alive for later turns.
func (p *LLMHarnessProcess) Interrupt(ctx context.Context) error {
	p.stateMu.Lock()
	closed := p.closed
	p.stateMu.Unlock()
	if closed {
		return ErrLLMHarnessProcessClosed
	}
	if p.running.Signal == nil {
		return fmt.Errorf("LLM harness process does not support interrupt")
	}
	return p.running.Signal(ctx, syscall.SIGINT)
}

// Wait waits for process exit. Canceling a waiter does not stop the process.
func (p *LLMHarnessProcess) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-p.done:
		p.stateMu.Lock()
		defer p.stateMu.Unlock()
		return p.waitErr
	}
}

// Stop closes stdin and waits for normal process termination. If the caller's
// context expires, it force-stops the process with a bounded cleanup context.
// Release is idempotent and always performed by Stop or natural exit.
func (p *LLMHarnessProcess) Stop(ctx context.Context) error {
	p.writeMu.Lock()
	var closeErr error
	p.closeInputOnce.Do(func() {
		closeErr = p.stdin.Close()
	})
	p.writeMu.Unlock()

	waitErr := p.Wait(ctx)
	if context.Cause(ctx) != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), TerminateGracePeriod)
		stopErr := p.running.Stop(cleanupCtx, true)
		cancel()
		p.releaseResources()
		return errors.Join(closeErr, waitErr, stopErr)
	}
	p.releaseResources()
	return errors.Join(closeErr, waitErr)
}

// Close implements io.Closer for harness protocol adapters. It delegates to
// Stop with a bounded internal deadline so adapter cleanup cannot wait forever.
func (p *LLMHarnessProcess) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), TerminateGracePeriod)
	defer cancel()
	return p.Stop(ctx)
}

// Command returns a defensive copy of the native command, primarily for
// lifecycle diagnostics and tests.
func (p *LLMHarnessProcess) Command() []string {
	return slices.Clone(p.command)
}

func (p *LLMHarnessProcess) Workdir() string {
	return p.workdir
}

var _ io.ReadWriteCloser = (*LLMHarnessProcess)(nil)
