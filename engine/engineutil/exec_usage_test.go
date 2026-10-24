package engineutil

import (
	"testing"

	"github.com/dagger/dagger/engine/wcprof"
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

func TestRecordExecUsageOnlyWhenProfiled(t *testing.T) {
	state := &execState{id: "x", resourceCgroupPath: "/does/not/exist"}
	recordExecUsage(t.Context(), state, wcprof.UsageScopeContainer)
	if state.profUsage != nil {
		t.Errorf("unprofiled exec read usage: %v", state.profUsage)
	}
	state.profUsageWanted = true
	recordExecUsage(t.Context(), state, wcprof.UsageScopeContainer) // unreadable cgroup: no reading, no error
	if len(state.profUsage) != 0 {
		t.Errorf("unreadable cgroup recorded usage: %v", state.profUsage)
	}
}
