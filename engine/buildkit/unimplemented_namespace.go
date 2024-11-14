//go:build darwin || windows

package buildkit

import (
	"context"
	"os"
	"time"
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
	panic("implemented only on linux")
}

func (w *Worker) runNetNSWorkers(ctx context.Context, state *execState) error {
	panic("implemented only on linux")
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
	panic("implemented only on linux")
}

func (nsw *namespaceWorker) leaveContainer() error {
	panic("implemented only on linux")
}

func (nsw *namespaceWorker) run(ctx context.Context, jobQueue <-chan func()) error {
	panic("implemented only on linux")
}
