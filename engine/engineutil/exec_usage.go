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
// resource cgroup, or that cgroup's "sshfs" child, where the exec's mount
// helpers run (see networkmetrics' execMountResources). "" when the resource
// cgroup isn't a filesystem path (a systemd scope), where mount helpers get
// no cgroup of their own either.
func execUsageCgroup(resourceCgroupPath, scope string) string {
	if resourceCgroupPath == "" || !filepath.IsAbs(resourceCgroupPath) || strings.Contains(resourceCgroupPath, ":") {
		return ""
	}
	p := filepath.Join(execUsageRoot, resourceCgroupPath)
	if scope == wcprof.UsageScopeMountHelpers {
		p = filepath.Join(p, "sshfs")
	}
	return p
}

// execUsageRoot is where execUsageCgroup finds cgroups: the unified
// hierarchy, or a test's fixture tree.
var execUsageRoot = cgroupMountpoint

// recordExecUsage reads one usage scope of the exec, for profiling: it
// attaches the reading to the exec's wcprof exec.run op, and keeps it for the
// exec.run OTel span (see execUsageAttrs). It does nothing unless the exec
// is profiled (state.profUsageWanted, set at its start).
func recordExecUsage(ctx context.Context, state *execState, scope string) {
	if !state.profUsageWanted {
		return
	}
	// When a mount helper could not join the exec's cgroup, the resource
	// cgroup's total would leave it out, and the helpers' cgroup wouldn't hold
	// it: read neither (the resource sampler skips the exec's samples the
	// same way).
	if state.mountResources != nil && !state.mountResources.Available() {
		return
	}
	// The helpers' cgroup exists only once a helper ran; without one, the
	// exec cgroup's reading is the workload's alone.
	if scope == wcprof.UsageScopeMountHelpers && (state.mountResources == nil || !state.mountResources.Active()) {
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
		)
		// A peak that couldn't be read is left out, not reported as zero.
		if u.MemPeakBytes > 0 {
			span.SetAttributes(attribute.Int64(prefix+"mem_peak_bytes", u.MemPeakBytes))
		}
	}
}
