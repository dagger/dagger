//go:build !darwin && !windows

package buildkit

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/opencontainers/runc/libcontainer"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sourcegraph/conc/pool"
	"golang.org/x/sys/unix"
)

const (
	idleTimeout    = 1 * time.Second
	workerPoolSize = 5
)

// NamespaceJob represents a job to be executed in a specific namespace context
type NamespaceJob struct {
	Container  string                 // Container whose namespaces to enter
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

// globalNSWorkerPool is the singleton global worker pool
var (
	globalNSWorkerPool     *GlobalNamespaceWorkerPool
	globalNSWorkerPoolOnce sync.Once
)

func runInNetNS[T any](
	ctx context.Context,
	state *execState,
	fn func() (T, error),
) (T, error) {
	var zero T
	// Prepare the job result
	type result struct {
		value T
		err   error
	}
	resultCh := make(chan result, 1)

	var nsPath string

	// need this to extract the namespace file
	var tmpSpec specs.Spec
	if state.networkNamespace != nil {
		if err := state.networkNamespace.Set(&tmpSpec); err != nil {
			return zero, fmt.Errorf("failed to set network namespace: %w", err)
		}
		for _, ns := range tmpSpec.Linux.Namespaces {
			if ns.Type == specs.NetworkNamespace {
				nsPath = ns.Path
			}
		}
	}
	namespaces := []specs.LinuxNamespace{
		{
			Type: specs.NetworkNamespace,
			Path: nsPath, // this known (and needed) ahead of time
		},
	}

	// Wrap the function to match the expected signature and capture result
	wrappedFn := func() error {
		value, err := fn()
		resultCh <- result{value: value, err: err}
		return nil // Always return nil from wrappedFn since we handle errors via resultCh
	}

	// Submit job to global pool
	gwp := GetGlobalNamespaceWorkerPool()
	if err := gwp.RunInNamespaces(ctx, state.id, namespaces, wrappedFn); err != nil {
		return zero, err
	}

	// Wait for result
	select {
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	case <-state.done:
		return zero, fmt.Errorf("container exited")
	case res := <-resultCh:
		return res.value, res.err
	}
}

// GetGlobalNamespaceWorkerPool returns the singleton global namespace worker pool
func GetGlobalNamespaceWorkerPool() *GlobalNamespaceWorkerPool {
	globalNSWorkerPoolOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		globalNSWorkerPool = &GlobalNamespaceWorkerPool{
			jobs:   make(chan *NamespaceJob, 100), // buffered channel
			ctx:    ctx,
			cancel: cancel,
			pool:   pool.New().WithContext(ctx),
		}
	})
	return globalNSWorkerPool
}

// Start initializes and starts the global worker pool
func (gwp *GlobalNamespaceWorkerPool) Start() {
	gwp.mu.Lock()
	defer gwp.mu.Unlock()

	if gwp.started {
		return
	}

	for range workerPoolSize {
		gwp.pool.Go(gwp.workerLoop)
	}

	gwp.started = true
}

// Stop gracefully shuts down the global worker pool
func (gwp *GlobalNamespaceWorkerPool) Stop() {
	gwp.mu.Lock()
	defer gwp.mu.Unlock()

	if !gwp.started {
		return
	}

	gwp.cancel()
	gwp.pool.Wait()
	close(gwp.jobs)
	gwp.started = false
}

// SubmitJob submits a job to be executed in the specified namespace context
func (gwp *GlobalNamespaceWorkerPool) SubmitJob(ctx context.Context, job *NamespaceJob) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case gwp.jobs <- job:
		return nil
	}
}

// RunInNamespaces executes a function in the context of specific namespaces for a given PID
func (gwp *GlobalNamespaceWorkerPool) RunInNamespaces(ctx context.Context, containerID string, namespaces []specs.LinuxNamespace, fn func() error) error {
	resultCh := make(chan error, 1)
	job := &NamespaceJob{
		Container:  containerID,
		Namespaces: namespaces,
		Fn:         fn,
		ResultCh:   resultCh,
	}

	if err := gwp.SubmitJob(ctx, job); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case err := <-resultCh:
		return err
	}
}

// workerLoop is the main loop for each worker goroutine
func (gwp *GlobalNamespaceWorkerPool) workerLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case job := <-gwp.jobs:
			if job == nil {
				continue
			}
			gwp.executeJob(ctx, job)
		}
	}
}

// executeJob executes a single job in its own isolated goroutine
func (gwp *GlobalNamespaceWorkerPool) executeJob(ctx context.Context, job *NamespaceJob) {
	// Create a new namespace worker for this job
	nsw, err := gwp.createNamespaceWorker(job.Container, job.Namespaces)
	if err != nil {
		select {
		case job.ResultCh <- fmt.Errorf("failed to create namespace worker: %w", err):
		case <-ctx.Done():
		}
		return
	}
	defer nsw.cleanup()

	// Execute the job in its own goroutine since it will lock to threads
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		errCh <- gwp.executeInNamespace(nsw, job.Fn)
	}()

	select {
	case err := <-errCh:
		select {
		case job.ResultCh <- err:
		case <-ctx.Done():
		}
	case <-ctx.Done():
		select {
		case job.ResultCh <- context.Cause(ctx):
		case <-ctx.Done():
		}
	}
}

// createNamespaceWorker creates a namespace worker for the given PID and namespaces
func (gwp *GlobalNamespaceWorkerPool) createNamespaceWorker(containerID string, namespaces []specs.LinuxNamespace) (*dynamicNamespaceWorker, error) {
	nsFiles := make([]*dynamicNamespaceFiles, 0, len(namespaces))

	for _, ns := range namespaces {
		var hostPath, targetPath string
		var setNSArg int

		// Map namespace types to their paths and setns arguments
		switch ns.Type {
		case specs.NetworkNamespace:
			hostPath = "/proc/self/ns/net"
			setNSArg = unix.CLONE_NEWNET
		case specs.PIDNamespace:
			hostPath = "/proc/self/ns/pid"
			setNSArg = unix.CLONE_NEWPID
		case specs.MountNamespace:
			hostPath = "/proc/self/ns/mnt"
			setNSArg = unix.CLONE_NEWNS
		case specs.UserNamespace:
			hostPath = "/proc/self/ns/user"
			setNSArg = unix.CLONE_NEWUSER
		case specs.UTSNamespace:
			hostPath = "/proc/self/ns/uts"
			setNSArg = unix.CLONE_NEWUTS
		case specs.IPCNamespace:
			hostPath = "/proc/self/ns/ipc"
			setNSArg = unix.CLONE_NEWIPC
		case specs.CgroupNamespace:
			hostPath = "/proc/self/ns/cgroup"
			setNSArg = unix.CLONE_NEWCGROUP
		default:
			return nil, fmt.Errorf("unsupported namespace type: %s", ns.Type)
		}

		// If path is specified in namespace, use it, otherwise construct from PID
		if ns.Path != "" {
			targetPath = ns.Path
		} else {
			// Get the container PID using libcontainer
			pid, err := getContainerPID(containerID)
			if err != nil {
				return nil, fmt.Errorf("failed to get container PID: %w", err)
			}

			targetPath = fmt.Sprintf("/proc/%d/ns/%s", pid, namespaceTypeToString(ns.Type))
		}

		hostFile, err := os.OpenFile(hostPath, os.O_RDONLY, 0)
		if err != nil {
			// Clean up already opened files
			for _, nf := range nsFiles {
				nf.hostFile.Close()
				nf.targetFile.Close()
			}
			return nil, fmt.Errorf("failed to open host namespace %s: %w", hostPath, err)
		}

		targetFile, err := os.OpenFile(targetPath, os.O_RDONLY, 0)
		if err != nil {
			hostFile.Close()
			// Clean up already opened files
			for _, nf := range nsFiles {
				nf.hostFile.Close()
				nf.targetFile.Close()
			}
			return nil, fmt.Errorf("failed to open target namespace %s: %w", targetPath, err)
		}

		nsFiles = append(nsFiles, &dynamicNamespaceFiles{
			hostFile:   hostFile,
			targetFile: targetFile,
			setNSArg:   setNSArg,
		})
	}

	return &dynamicNamespaceWorker{
		namespaces: nsFiles,
	}, nil
}

// namespaceTypeToString converts a namespace type to its string representation
func namespaceTypeToString(nsType specs.LinuxNamespaceType) string {
	switch nsType {
	case specs.NetworkNamespace:
		return "net"
	case specs.PIDNamespace:
		return "pid"
	case specs.MountNamespace:
		return "mnt"
	case specs.UserNamespace:
		return "user"
	case specs.UTSNamespace:
		return "uts"
	case specs.IPCNamespace:
		return "ipc"
	case specs.CgroupNamespace:
		return "cgroup"
	default:
		return string(nsType)
	}
}

// executeInNamespace executes a function in the namespace context
func (gwp *GlobalNamespaceWorkerPool) executeInNamespace(nsw *dynamicNamespaceWorker, fn func() error) error {
	if err := nsw.enterNamespaces(); err != nil {
		return err
	}
	defer nsw.leaveNamespaces()

	return fn()
}

// dynamicNamespaceWorker handles entering/leaving multiple namespaces dynamically
type dynamicNamespaceWorker struct {
	namespaces  []*dynamicNamespaceFiles
	inNamespace bool
}

// dynamicNamespaceFiles includes both host and target files
type dynamicNamespaceFiles struct {
	hostFile   *os.File
	targetFile *os.File
	setNSArg   int
}

// cleanup closes all open files
func (nsw *dynamicNamespaceWorker) cleanup() {
	for _, ns := range nsw.namespaces {
		if ns.hostFile != nil {
			ns.hostFile.Close()
		}
		if ns.targetFile != nil {
			ns.targetFile.Close()
		}
	}
}

// enterNamespaces enters all specified namespaces
func (nsw *dynamicNamespaceWorker) enterNamespaces() error {
	if nsw.inNamespace {
		return nil
	}

	runtime.LockOSThread()
	for _, ns := range nsw.namespaces {
		if err := unix.Setns(int(ns.targetFile.Fd()), ns.setNSArg); err != nil {
			return fmt.Errorf("failed to enter namespace: %w", err)
		}
	}
	nsw.inNamespace = true
	return nil
}

// leaveNamespaces exits all namespaces and returns to host
func (nsw *dynamicNamespaceWorker) leaveNamespaces() error {
	if !nsw.inNamespace {
		return nil
	}

	for _, ns := range nsw.namespaces {
		if err := unix.Setns(int(ns.hostFile.Fd()), ns.setNSArg); err != nil {
			return fmt.Errorf("failed to leave namespace: %w", err)
		}
	}
	runtime.UnlockOSThread()
	nsw.inNamespace = false
	return nil
}

// ShutdownGlobalNamespaceWorkerPool gracefully shuts down the global namespace worker pool
// This should be called during application shutdown
func ShutdownGlobalNamespaceWorkerPool() {
	if globalNSWorkerPool != nil {
		globalNSWorkerPool.Stop()
	}
}

// getContainerPID retrieves the PID of a container using libcontainer
func getContainerPID(containerID string) (int, error) {
	// Load the container using libcontainer
	container, err := libcontainer.Load("/run/runc", containerID)
	if err != nil {
		return 0, fmt.Errorf("failed to create libcontainer factory: %w", err)
	}

	state, err := container.OCIState()
	if err != nil {
		return 0, fmt.Errorf("failed to get OCI state for container %s: %w", containerID, err)
	}

	if state.Pid == 0 {
		return 0, fmt.Errorf("container %s has no running process", containerID)
	}

	return state.Pid, nil
}
