package buildkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sourcegraph/conc/pool"
	"golang.org/x/sys/unix"
)

const (
	idleTimeout    = 1 * time.Second
	workerPoolSize = 5
)

func runInNetNS[T any](
	ctx context.Context,
	state *execState,
	fn func() (T, error),
) (T, error) {
	var zero T
	type result struct {
		value T
		err   error
	}
	resultCh := make(chan result, 1)

	select {
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	case <-state.done:
		return zero, fmt.Errorf("container exited")
	case state.netNSJobs <- func() {
		defer close(resultCh)
		v, err := fn()
		resultCh <- result{value: v, err: err}
	}:
	}

	select {
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	case <-state.done:
		return zero, fmt.Errorf("container exited")
	case result := <-resultCh:
		return result.value, result.err
	}
}

func (w *Worker) runNetNSWorkers(ctx context.Context, state *execState) error {
	if state.networkNamespace == nil {
		return fmt.Errorf("network namespace not found")
	}

	// need this to extract the namespace file
	var tmpSpec specs.Spec
	if err := state.networkNamespace.Set(&tmpSpec); err != nil {
		return fmt.Errorf("failed to set network namespace: %w", err)
	}
	var ctrNetNSPath string
	for _, ns := range tmpSpec.Linux.Namespaces {
		if ns.Type == specs.NetworkNamespace {
			ctrNetNSPath = ns.Path
			break
		}
	}
	if ctrNetNSPath == "" {
		return fmt.Errorf("network namespace path not found")
	}

	hostFile, err := os.OpenFile("/proc/self/ns/net", os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to open host netns file: %w", err)
	}
	state.cleanups.Add("close host netns file", hostFile.Close)

	ctrFile, err := os.OpenFile(ctrNetNSPath, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to open container netns file: %w", err)
	}
	state.cleanups.Add("close container netns file", ctrFile.Close)

	ctx, cancel := context.WithCancel(ctx)
	p := pool.New().WithContext(ctx)
	state.cleanups.Add("stopping namespace workers", p.Wait)
	state.cleanups.Add("canceling namespace workers", Infallible(cancel))

	for i := 0; i < workerPoolSize; i++ {
		p.Go(func(ctx context.Context) (rerr error) {
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-state.done:
					return nil
				default:
				}

				nsw := &namespaceWorker{namespaces: []*namespaceFiles{{
					hostFile: hostFile,
					ctrFile:  ctrFile,
					setNSArg: unix.CLONE_NEWNET,
				}}}

				// must run in it's own isolated goroutine since it will lock to threads
				errCh := make(chan error, 1)
				go func() {
					defer close(errCh)
					errCh <- nsw.run(ctx, state.netNSJobs)
				}()
				err := <-errCh
				if err != nil && !errors.Is(err, context.Canceled) {
					bklog.G(ctx).WithError(err).Error("namespace worker failed")
				}
			}
		})
	}

	return nil
}

type namespaceWorker struct {
	namespaces []*namespaceFiles

	inContainer   bool
	idleTimerCh   <-chan time.Time
	stopIdleTimer func() bool
}

type namespaceFiles struct {
	hostFile *os.File
	ctrFile  *os.File

	setNSArg int
}

func (nsw *namespaceWorker) enterContainer() error {
	nsw.stopIdleTimer()
	timer := time.NewTimer(idleTimeout)
	nsw.idleTimerCh = timer.C
	nsw.stopIdleTimer = timer.Stop

	if nsw.inContainer {
		return nil
	}

	runtime.LockOSThread()
	for _, ns := range nsw.namespaces {
		if err := unix.Setns(int(ns.ctrFile.Fd()), ns.setNSArg); err != nil {
			return fmt.Errorf("failed to enter container namespace: %w", err)
		}
	}
	nsw.inContainer = true

	return nil
}

func (nsw *namespaceWorker) leaveContainer() error {
	if !nsw.inContainer {
		return nil
	}

	for _, ns := range nsw.namespaces {
		if err := unix.Setns(int(ns.hostFile.Fd()), ns.setNSArg); err != nil {
			return fmt.Errorf("failed to leave container namespace: %w", err)
		}
	}
	runtime.UnlockOSThread()
	nsw.inContainer = false

	nsw.stopIdleTimer()
	nsw.idleTimerCh = nil
	nsw.stopIdleTimer = func() bool { return false }

	return nil
}

func (nsw *namespaceWorker) run(ctx context.Context, jobQueue <-chan func()) error {
	nsw.stopIdleTimer = func() bool { return false }
	defer nsw.leaveContainer()
	defer nsw.stopIdleTimer()

	for {
		select {
		case j := <-jobQueue:
			if j == nil {
				continue
			}
			if err := nsw.enterContainer(); err != nil {
				return err
			}
			j()
		case <-nsw.idleTimerCh:
			if err := nsw.leaveContainer(); err != nil {
				return err
			}
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}
