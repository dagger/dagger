package engineutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/engine/wcprof"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
)

func TestExecUsageCgroup(t *testing.T) {
	for _, tc := range []struct {
		path, scope, want string
	}{
		{"/dagger/exec-1", wcprof.UsageScopeContainer, cgroupMountpoint + "/dagger/exec-1/container"},
		{"/dagger/exec-1", wcprof.UsageScopeExecCgroup, cgroupMountpoint + "/dagger/exec-1"},
		{"system.slice:dagger:exec-1", wcprof.UsageScopeContainer, ""}, // a systemd scope: no path
		{"", wcprof.UsageScopeExecCgroup, ""},
	} {
		if got := execUsageCgroup(tc.path, tc.scope); got != tc.want {
			t.Errorf("execUsageCgroup(%q, %q) = %q, want %q", tc.path, tc.scope, got, tc.want)
		}
	}
}

// usageFixture lays out an exec's resource cgroup and its container child
// under a temporary root, with the given container peak (none if "").
func usageFixture(t *testing.T, containerPeak string) string {
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
	write("dagger/x/container/cpu.stat", "usage_usec 1500\nuser_usec 1200\nsystem_usec 300\n")
	if containerPeak != "" {
		write("dagger/x/container/memory.peak", containerPeak)
	}
	return "/dagger/x"
}

type unavailableMountResources struct{}

func (unavailableMountResources) Prepare(*exec.Cmd) (func() error, error) {
	return func() error { return nil }, nil
}
func (unavailableMountResources) Active() bool    { return true }
func (unavailableMountResources) Available() bool { return false }
func (unavailableMountResources) Sample() (*resourcestypes.NetworkSample, error) {
	return &resourcestypes.NetworkSample{}, nil
}
func (unavailableMountResources) Close() error { return nil }

func TestRecordExecUsage(t *testing.T) {
	path := usageFixture(t, "4096\n")

	// Not profiled: nothing is read, though the cgroups are readable.
	state := &execState{id: "x", resourceCgroupPath: path}
	recordExecUsage(t.Context(), state, wcprof.UsageScopeContainer)
	if state.profUsage != nil {
		t.Fatalf("unprofiled exec read usage: %v", state.profUsage)
	}

	state.profUsageWanted = true
	recordExecUsage(t.Context(), state, wcprof.UsageScopeContainer)
	recordExecUsage(t.Context(), state, wcprof.UsageScopeExecCgroup)
	if got := state.profUsage[wcprof.UsageScopeContainer]; got != (wcprof.Usage{CPUUserUS: 1200, CPUSystemUS: 300, MemPeakBytes: 4096}) {
		t.Errorf("container usage %+v", got)
	}
	if got := state.profUsage[wcprof.UsageScopeExecCgroup]; got != (wcprof.Usage{CPUUserUS: 1250, CPUSystemUS: 350, MemPeakBytes: 8192}) {
		t.Errorf("exec cgroup usage %+v", got)
	}

	// A mount helper outside the exec's cgroup: no inclusive reading, but
	// the container's own still counts.
	state = &execState{id: "x", resourceCgroupPath: path, profUsageWanted: true, mountResources: unavailableMountResources{}}
	recordExecUsage(t.Context(), state, wcprof.UsageScopeContainer)
	recordExecUsage(t.Context(), state, wcprof.UsageScopeExecCgroup)
	if _, ok := state.profUsage[wcprof.UsageScopeExecCgroup]; ok {
		t.Error("exec cgroup usage recorded though a mount helper was outside it")
	}
	if _, ok := state.profUsage[wcprof.UsageScopeContainer]; !ok {
		t.Error("container usage missing")
	}
}

func TestRecordExecUsageWithoutPeak(t *testing.T) {
	path := usageFixture(t, "")
	state := &execState{id: "x", resourceCgroupPath: path, profUsageWanted: true}
	recordExecUsage(t.Context(), state, wcprof.UsageScopeContainer)
	if got := state.profUsage[wcprof.UsageScopeContainer]; got != (wcprof.Usage{CPUUserUS: 1200, CPUSystemUS: 300}) {
		t.Errorf("container usage without memory.peak %+v", got)
	}
}

func TestSetExecUsageAttrsOmitsUnknownPeak(t *testing.T) {
	sr, _, span := newRecordingRoot("exec.run")
	state := &execState{profUsage: map[string]wcprof.Usage{
		wcprof.UsageScopeContainer:  {CPUUserUS: 1200, CPUSystemUS: 300},
		wcprof.UsageScopeExecCgroup: {CPUUserUS: 1250, CPUSystemUS: 350, MemPeakBytes: 8192},
	}}
	setExecUsageAttrs(span, state)
	span.End()
	got := map[string]int64{}
	for _, kv := range sr.Ended()[0].Attributes() {
		got[string(kv.Key)] = kv.Value.AsInt64()
	}
	want := map[string]int64{
		"wcprof.usage.container.cpu_user_us":      1200,
		"wcprof.usage.container.cpu_system_us":    300,
		"wcprof.usage.exec_cgroup.cpu_user_us":    1250,
		"wcprof.usage.exec_cgroup.cpu_system_us":  350,
		"wcprof.usage.exec_cgroup.mem_peak_bytes": 8192,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
	if _, ok := got["wcprof.usage.container.mem_peak_bytes"]; ok {
		t.Error("an unknown container peak was reported")
	}
}
