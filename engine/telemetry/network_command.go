package telemetry

import (
	"context"
	"os/exec"
	"sync/atomic"

	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
)

type commandNetworkHook func(context.Context, *exec.Cmd) func()

var commandNetwork atomic.Pointer[commandNetworkHook]

// ExecMountResources owns filesystem helpers for one execution. The executor
// samples the parent cgroup and adds these network counters to its veth sample.
type ExecMountResources interface {
	Prepare(*exec.Cmd) (func() error, error)
	Active() bool
	Available() bool
	Sample() (*resourcestypes.NetworkSample, error)
	Close() error
}

type execMountResourcesHook func(string) ExecMountResources

var execMountResources atomic.Pointer[execMountResourcesHook]

type execMountResourcesKey struct{}

func SetExecMountResourcesHook(hook func(string) ExecMountResources) {
	if hook == nil {
		execMountResources.Store(nil)
		return
	}
	fn := execMountResourcesHook(hook)
	execMountResources.Store(&fn)
}

func NewExecMountResources(path string) ExecMountResources {
	if hook := execMountResources.Load(); hook != nil {
		return (*hook)(path)
	}
	return nil
}

func WithExecMountResources(ctx context.Context, resources ExecMountResources) context.Context {
	return context.WithValue(ctx, execMountResourcesKey{}, resources)
}

func PrepareExecMountCommand(ctx context.Context, cmd *exec.Cmd) (func() error, error) {
	if resources, ok := ctx.Value(execMountResourcesKey{}).(ExecMountResources); ok {
		return resources.Prepare(cmd)
	}
	return func() error { return nil }, nil
}

// SetCommandNetworkHook installs engine-side subprocess accounting. Shared
// command helpers must not import the engine's kernel instrumentation. A nil
// hook disables accounting; the CLI leaves it unset.
func SetCommandNetworkHook(hook func(context.Context, *exec.Cmd) func()) {
	if hook == nil {
		commandNetwork.Store(nil)
		return
	}
	fn := commandNetworkHook(hook)
	commandNetwork.Store(&fn)
}

// PrepareCommandNetwork prepares accounting before cmd.Start. Call the returned
// function after Wait, or after releasing a daemon. Without an engine-installed
// hook this does nothing, including emitting no availability metrics.
func PrepareCommandNetwork(ctx context.Context, cmd *exec.Cmd) func() {
	if hook := commandNetwork.Load(); hook != nil {
		return (*hook)(ctx, cmd)
	}
	return func() {}
}
