package telemetry

import (
	"context"
	"os/exec"
	"sync/atomic"
)

type commandNetworkHook func(context.Context, *exec.Cmd) func()

var commandNetwork atomic.Pointer[commandNetworkHook]

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
