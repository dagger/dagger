//go:build darwin || windows

package buildkit

import (
	"context"
	"sync"
	"time"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sourcegraph/conc/pool"
)

const (
	idleTimeout    = 1 * time.Second
	workerPoolSize = 5
)

// NamespaceJob represents a job to be executed in a specific namespace context
type NamespaceJob struct {
	PID        int                    // PID whose namespaces to enter
	Namespaces []specs.LinuxNamespace // Namespaces to enter (e.g., network, mount, etc.)
	Fn         func() error           // Function to execute
	ResultCh   chan error             // Channel to send result back
}

// GlobalNamespaceWorkerPool manages a global pool of workers that can enter
// different namespace contexts based on PID and namespace type
type GlobalNamespaceWorkerPool struct {
	jobs    chan *NamespaceJob
	ctx     context.Context
	cancel  context.CancelFunc
	pool    *pool.ContextPool
	mu      sync.RWMutex
	started bool
}

// GetGlobalNamespaceWorkerPool returns the singleton global namespace worker pool
func GetGlobalNamespaceWorkerPool() *GlobalNamespaceWorkerPool {
	panic("implemented only on linux")
}

func (gwp *GlobalNamespaceWorkerPool) Start() error {
	panic("implemented only on linux")
}

func (gwp *GlobalNamespaceWorkerPool) Stop() error {
	panic("implemented only on linux")
}

func (gwp *GlobalNamespaceWorkerPool) RunInNamespaces(ctx context.Context, pid int, namespaces []specs.LinuxNamespace, fn func() error) error {
	panic("implemented only on linux")
}

func runInNetNS[T any](
	ctx context.Context,
	state *execState,
	fn func() (T, error),
) (T, error) {
	panic("implemented only on linux")
}

// ShutdownGlobalNamespaceWorkerPool gracefully shuts down the global namespace worker pool
// This should be called during application shutdown
func ShutdownGlobalNamespaceWorkerPool() error {
	// No-op on non-Linux platforms
	return nil
}

// getContainerPID retrieves the PID of a container using libcontainer
func getContainerPID(containerID string) (int, error) {
	panic("implemented only on linux")
}
