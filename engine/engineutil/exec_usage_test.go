package engineutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/wcprof"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
)

func TestExecUsageCgroup(t *testing.T) {
	for _, tc := range []struct {
		path, scope, want string
	}{
		{"/dagger/exec-1", wcprof.UsageScopeMountHelpers, cgroupMountpoint + "/dagger/exec-1/sshfs"},
		{"/dagger/exec-1", wcprof.UsageScopeExecCgroup, cgroupMountpoint + "/dagger/exec-1"},
		{"system.slice:dagger:exec-1", wcprof.UsageScopeMountHelpers, ""}, // a systemd scope: no path
		{"", wcprof.UsageScopeExecCgroup, ""},
	} {
		if got := execUsageCgroup(tc.path, tc.scope); got != tc.want {
			t.Errorf("execUsageCgroup(%q, %q) = %q, want %q", tc.path, tc.scope, got, tc.want)
		}
	}
}

// usageFixture lays out an exec's resource cgroup and its mount helpers'
// sshfs child under a temporary root, with the given helpers' peak (none if
// "").
func usageFixture(t *testing.T, helpersPeak string) string {
	t.Helper()
	root := t.TempDir()
	prev := execUsageRoot
	execUsageRoot = root
	t.Cleanup(func() { execUsageRoot = prev })
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("dagger/x/cpu.stat", "usage_usec 1600\nuser_usec 1250\nsystem_usec 350\n")
	write("dagger/x/memory.peak", "8192\n")
	write("dagger/x/sshfs/cpu.stat", "usage_usec 100\nuser_usec 50\nsystem_usec 50\n")
	if helpersPeak != "" {
		write("dagger/x/sshfs/memory.peak", helpersPeak)
	}
	return "/dagger/x"
}

// usageMountResources is an exec's mount helpers as recordExecUsage sees them:
// whether one ran (active), and whether all joined the exec's cgroup.
type usageMountResources struct{ active, available bool }

func (usageMountResources) Prepare(*exec.Cmd) (func() error, error) {
	return func() error { return nil }, nil
}
func (r usageMountResources) Active() bool    { return r.active }
func (r usageMountResources) Available() bool { return r.available }
func (usageMountResources) Sample() (*resourcestypes.NetworkSample, error) {
	return &resourcestypes.NetworkSample{}, nil
}
func (usageMountResources) Close() error { return nil }

func TestRecordExecUsage(t *testing.T) {
	path := usageFixture(t, "4096\n")
	read := func(mr enginetel.ExecMountResources, profiled bool) map[string]wcprof.Usage {
		t.Helper()
		state := &execState{id: "x", resourceCgroupPath: path, profUsageWanted: profiled, mountResources: mr}
		recordExecUsage(t.Context(), state, wcprof.UsageScopeExecCgroup)
		recordExecUsage(t.Context(), state, wcprof.UsageScopeMountHelpers)
		return state.profUsage
	}
	wantExec := wcprof.Usage{CPUUserUS: 1250, CPUSystemUS: 350, MemPeakBytes: 8192}
	wantHelpers := wcprof.Usage{CPUUserUS: 50, CPUSystemUS: 50, MemPeakBytes: 4096}

	// Not profiled: nothing is read, though the cgroups are readable.
	if got := read(usageMountResources{active: true, available: true}, false); got != nil {
		t.Errorf("unprofiled exec read usage: %v", got)
	}
	// No mount helper ran: the exec cgroup alone, the workload's own usage.
	for _, mr := range []enginetel.ExecMountResources{nil, usageMountResources{available: true}} {
		got := read(mr, true)
		if len(got) != 1 || got[wcprof.UsageScopeExecCgroup] != wantExec {
			t.Errorf("without a helper (%v): %+v", mr, got)
		}
	}
	// A helper ran: both, so the workload's usage is the difference.
	got := read(usageMountResources{active: true, available: true}, true)
	if len(got) != 2 || got[wcprof.UsageScopeExecCgroup] != wantExec || got[wcprof.UsageScopeMountHelpers] != wantHelpers {
		t.Errorf("with a helper: %+v", got)
	}
	// A helper outside the exec's cgroup: neither reading would hold it.
	if got := read(usageMountResources{active: true, available: false}, true); got != nil {
		t.Errorf("with a helper outside the exec's cgroup: %+v", got)
	}
}

func TestRecordExecUsageWithoutPeak(t *testing.T) {
	path := usageFixture(t, "")
	state := &execState{id: "x", resourceCgroupPath: path, profUsageWanted: true, mountResources: usageMountResources{active: true, available: true}}
	recordExecUsage(t.Context(), state, wcprof.UsageScopeMountHelpers)
	if got := state.profUsage[wcprof.UsageScopeMountHelpers]; got != (wcprof.Usage{CPUUserUS: 50, CPUSystemUS: 50}) {
		t.Errorf("mount helpers' usage without memory.peak %+v", got)
	}
}

func TestSetExecUsageAttrsOmitsUnknownPeak(t *testing.T) {
	sr, _, span := newRecordingRoot("exec.run")
	state := &execState{profUsage: map[string]wcprof.Usage{
		wcprof.UsageScopeMountHelpers: {CPUUserUS: 50, CPUSystemUS: 50},
		wcprof.UsageScopeExecCgroup:   {CPUUserUS: 1250, CPUSystemUS: 350, MemPeakBytes: 8192},
	}}
	setExecUsageAttrs(span, state)
	span.End()
	got := map[string]int64{}
	for _, kv := range sr.Ended()[0].Attributes() {
		got[string(kv.Key)] = kv.Value.AsInt64()
	}
	want := map[string]int64{
		"wcprof.usage.mount_helpers.cpu_user_us":   50,
		"wcprof.usage.mount_helpers.cpu_system_us": 50,
		"wcprof.usage.exec_cgroup.cpu_user_us":     1250,
		"wcprof.usage.exec_cgroup.cpu_system_us":   350,
		"wcprof.usage.exec_cgroup.mem_peak_bytes":  8192,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
	if _, ok := got["wcprof.usage.mount_helpers.mem_peak_bytes"]; ok {
		t.Error("an unknown mount helpers' peak was reported")
	}
}
