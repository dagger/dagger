package buildkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func runInNamespace[T any](
	ctx context.Context,
	runState *runningState,
	fn func() (T, error),
) (T, error) {
	var zero T
	type result struct {
		value T
		err   error
	}
	resultCh := make(chan result)

	select {
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	case <-runState.done:
		return zero, fmt.Errorf("container exited")
	case runState.nsJobs <- func() {
		v, err := fn()
		resultCh <- result{value: v, err: err}
	}:
	}

	select {
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	case <-runState.done:
		return zero, fmt.Errorf("container exited")
	case result := <-resultCh:
		return result.value, result.err
	}
}

func (w *Worker) runNamespaceWorkers(
	ctx context.Context,
	runState *runningState,
	cleanup *cleanups,
) error {
	var namespaces []*namespaceFiles
	// TODO: switch this to iterate over runState.namespaces instead
	// TODO: switch this to iterate over runState.namespaces instead
	// TODO: switch this to iterate over runState.namespaces instead
	for _, nsType := range []namespaceType{
		/* TODO: for later
		{
			specType:   specs.MountNamespace,
			procFSName: "mnt",
			setNSArg:   unix.CLONE_NEWNS,
		},
		*/
		{
			specType:   specs.NetworkNamespace,
			procFSName: "net",
			setNSArg:   unix.CLONE_NEWNET,
		},
	} {
		hostFile, err := os.OpenFile(filepath.Join("/proc/self/ns", nsType.procFSName), os.O_RDONLY, 0)
		if err != nil {
			return fmt.Errorf("failed to open host namespace file %s: %w", nsType.procFSName, err)
		}
		cleanup.add("close host netns file", hostFile.Close)

		var ctrFile *os.File
		for _, ns := range runState.namespaces {
			if ns.Type != nsType.specType {
				continue
			}
			if ns.Path == "" {
				continue
			}
			ctrFile, err = os.OpenFile(ns.Path, os.O_RDONLY, 0)
			if err != nil {
				return fmt.Errorf("failed to open container namespace file %s: %w", ns.Path, err)
			}
			cleanup.add("close container netns file", ctrFile.Close)
		}
		if ctrFile == nil {
			return fmt.Errorf("container namespace file not found for %s", nsType.specType)
		}

		namespaces = append(namespaces, &namespaceFiles{
			hostFile: hostFile,
			ctrFile:  ctrFile,
			setNSArg: nsType.setNSArg,
		})
	}

	ctx, cancel := context.WithCancel(ctx)
	p := pool.New().WithContext(ctx)
	cleanup.add("stopping namespace workers", p.Wait)
	cleanup.addNoErr("canceling namespace workers", cancel)

	for i := 0; i < workerPoolSize; i++ {
		p.Go(func(ctx context.Context) (rerr error) {
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-runState.done:
					return nil
				default:
				}

				nsw := &namespaceWorker{namespaces: namespaces}

				// must run in it's own isolated goroutine since it will lock to threads
				errCh := make(chan error)
				go func() {
					errCh <- nsw.run(ctx, runState.nsJobs)
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

type namespaceType struct {
	specType   specs.LinuxNamespaceType
	procFSName string
	setNSArg   int
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
