package engineutil

import (
	"context"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/engineutil/resources"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
)

// execUsageCgroup is the cgroup an exec's usage scope is read from: its
// resource cgroup, or that cgroup's "container" child, the workload's own
// cgroup as generateBaseSpec sets it. "" when the resource cgroup isn't a
// filesystem path (a systemd scope), where generateBaseSpec doesn't nest the
// container either.
func execUsageCgroup(resourceCgroupPath, scope string) string {
	if resourceCgroupPath == "" || !filepath.IsAbs(resourceCgroupPath) || strings.Contains(resourceCgroupPath, ":") {
		return ""
	}
	p := filepath.Join(cgroupMountpoint, resourceCgroupPath)
	if scope == wcprof.UsageScopeContainer {
		p = filepath.Join(p, "container")
	}
	return p
}

// recordExecUsage reads one usage scope of the exec, for profiling: it
// attaches the reading to the exec's wcprof exec.run op, and keeps it for the
// exec.run OTel span (see execUsageAttrs). It does nothing unless the exec
// is profiled (state.profUsageWanted, set at its start).
func recordExecUsage(ctx context.Context, state *execState, scope string) {
	if !state.profUsageWanted {
		return
	}
	dir := execUsageCgroup(state.resourceCgroupPath, scope)
	if dir == "" {
		return
	}
	user, sys, peak, err := resources.ReadUsage(dir)
	if err != nil {
		bklog.G(ctx).Debugf("exec %s: no %s usage: %v", state.id, scope, err)
		return
	}
	u := wcprof.Usage{CPUUserUS: user, CPUSystemUS: sys, MemPeakBytes: peak}
	wcprof.RecordUsage(wcprof.ContextWithOpID(ctx, state.profExecOpID), state.profExecOpID, scope, u)
	state.profUsageMu.Lock()
	defer state.profUsageMu.Unlock()
	if state.profUsage == nil {
		state.profUsage = map[string]wcprof.Usage{}
	}
	state.profUsage[scope] = u
}

// setExecUsageAttrs puts the exec's usage readings on its exec.run span.
func setExecUsageAttrs(span trace.Span, state *execState) {
	state.profUsageMu.Lock()
	defer state.profUsageMu.Unlock()
	for scope, u := range state.profUsage {
		prefix := telemetryattrs.WcprofUsageAttrPrefix + scope + "."
		span.SetAttributes(
			attribute.Int64(prefix+"cpu_user_us", u.CPUUserUS),
			attribute.Int64(prefix+"cpu_system_us", u.CPUSystemUS),
			attribute.Int64(prefix+"mem_peak_bytes", u.MemPeakBytes),
		)
	}
}
