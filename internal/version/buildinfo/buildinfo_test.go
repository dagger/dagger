package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestReadBuildInfo_NoOverrides(t *testing.T) {
	resetInjected(t)

	got, ok := ReadBuildInfo()
	want, wantOK := debug.ReadBuildInfo()

	if ok != wantOK {
		t.Fatalf("ok = %v, want %v", ok, wantOK)
	}
	if (got == nil) != (want == nil) {
		t.Fatalf("nil-ness mismatch: got=%v want=%v", got, want)
	}
	if got == nil {
		return
	}
	if len(got.Settings) != len(want.Settings) {
		t.Fatalf("len(Settings) = %d, want %d", len(got.Settings), len(want.Settings))
	}
}

func TestReadBuildInfo_OverridesAddMissingSettings(t *testing.T) {
	resetInjected(t)
	InjectedVCS = "git"
	InjectedVCSRevision = "abc123def456"
	InjectedVCSModified = "false"
	InjectedVCSTime = "2026-05-29T12:00:00Z"

	info, ok := ReadBuildInfo()
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if info == nil {
		t.Fatalf("info = nil, want non-nil")
	}

	want := map[string]string{
		"vcs":          "git",
		"vcs.revision": "abc123def456",
		"vcs.modified": "false",
		"vcs.time":     "2026-05-29T12:00:00Z",
	}
	got := settingsMap(info.Settings)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Settings[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestReadBuildInfo_PartialOverride(t *testing.T) {
	resetInjected(t)
	InjectedVCSRevision = "deadbeef"

	info, ok := ReadBuildInfo()
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	got := settingsMap(info.Settings)
	if got["vcs.revision"] != "deadbeef" {
		t.Errorf("vcs.revision = %q, want %q", got["vcs.revision"], "deadbeef")
	}
	if _, present := got["vcs.modified"]; present {
		// Standard debug.ReadBuildInfo from `go test` may set vcs.modified
		// itself; that's fine. Just shouldn't be overridden to "".
		if got["vcs.modified"] == "" {
			t.Errorf("vcs.modified is empty after partial override")
		}
	}
}

func TestReadBuildInfo_OverwritesExistingSetting(t *testing.T) {
	resetInjected(t)

	// First read baseline to see what the toolchain stamps (if anything).
	baseline, _ := ReadBuildInfo()
	hadKey := false
	for _, s := range baseline.Settings {
		if s.Key == "vcs.revision" {
			hadKey = true
			break
		}
	}

	InjectedVCSRevision = "overridden"
	info, _ := ReadBuildInfo()
	got := settingsMap(info.Settings)
	if got["vcs.revision"] != "overridden" {
		t.Fatalf("vcs.revision = %q, want %q (had pre-existing: %v)",
			got["vcs.revision"], "overridden", hadKey)
	}

	// And the slice didn't gain a duplicate entry.
	count := 0
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("vcs.revision appears %d times in Settings, want 1", count)
	}
}

func settingsMap(s []debug.BuildSetting) map[string]string {
	m := make(map[string]string, len(s))
	for _, kv := range s {
		m[kv.Key] = kv.Value
	}
	return m
}

func resetInjected(t *testing.T) {
	t.Helper()
	prev := struct{ vcs, rev, mod, ts string }{
		InjectedVCS, InjectedVCSRevision, InjectedVCSModified, InjectedVCSTime,
	}
	InjectedVCS = ""
	InjectedVCSRevision = ""
	InjectedVCSModified = ""
	InjectedVCSTime = ""
	t.Cleanup(func() {
		InjectedVCS = prev.vcs
		InjectedVCSRevision = prev.rev
		InjectedVCSModified = prev.mod
		InjectedVCSTime = prev.ts
	})
}
