package archive

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testTraceC = "33333333333333333333333333333333"

// sealArchive registers and seals an archive with an empty roster.
func sealArchive(t *testing.T, manager *Manager, traceID, clientID string, telemetryOnly bool, at time.Time, size int64) Manifest {
	t.Helper()
	register := manager.Register
	if telemetryOnly {
		register = manager.RegisterTelemetryOnly
	}
	registered, err := register(traceID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.BeginFinalizing(traceID); err != nil {
		t.Fatal(err)
	}
	closed, err := manager.Finalize(traceID, FinalizeInput{
		SealAt: at, StoreSizeBytes: size, BootstrapBytes: testBootstrap(t, registered, HighWater{}, at, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	return closed
}

func TestManagerTelemetryOnlyArchives(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	manager, err := NewManager(Config{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	sealArchive(t, manager, testTraceA, "agent-client", false, now, 1)
	plain := sealArchive(t, manager, testTraceB, "plain-client", true, now, 1)
	if !plain.TelemetryOnly || plain.State != StateClosed {
		t.Fatalf("plain archive = %+v, want a sealed telemetry-only archive", plain)
	}

	// Retained and addressable by trace ID, across a restart...
	restarted, err := NewManager(Config{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if manifest, err := restarted.Manifest(testTraceB); err != nil || !manifest.TelemetryOnly || manifest.State != StateClosed {
		t.Fatalf("restarted lookup = %+v, %v", manifest, err)
	}
	if !restarted.KeepSet()["plain-client"] {
		t.Fatal("telemetry-only store is not kept from store GC")
	}
	// ...but never listed for agent restore.
	page := restarted.List("", "", 0)
	if len(page.Archives) != 1 || page.Archives[0].TraceID != testTraceA {
		t.Fatalf("listing = %+v, want only the agent archive", page.Archives)
	}

	// A telemetry-only registration never displaces an archive.
	if _, err := restarted.RegisterTelemetryOnly(testTraceA, "late-plain"); err == nil {
		t.Fatal("telemetry-only registration displaced an agent archive")
	}
	if _, err := restarted.RegisterTelemetryOnly(testTraceB, "late-plain"); err == nil {
		t.Fatal("telemetry-only registration displaced a telemetry-only archive")
	}
}

// An agent session sharing a plain session's trace takes over its ended
// telemetry-only archive, so retaining plain sessions never costs a later
// agent session its archive. An active one is still owned by its session.
func TestManagerAgentSupersedesTelemetryOnly(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	manager, err := NewManager(Config{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RegisterTelemetryOnly(testTraceA, "plain-active"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Register(testTraceA, "agent"); err == nil {
		t.Fatal("agent registration took over an active session's archive")
	}

	sealArchive(t, manager, testTraceB, "plain", true, now, 1)
	agent, err := manager.Register(testTraceB, "agent")
	if err != nil {
		t.Fatalf("agent registration over an ended telemetry-only archive: %v", err)
	}
	if agent.TelemetryOnly || agent.State != StateActive || agent.MainClientID != "agent" {
		t.Fatalf("superseding archive = %+v", agent)
	}
	if _, err := os.Stat(manager.BootstrapPath(testTraceB)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("superseded bootstrap remains: %v", err)
	}
	keep := manager.KeepSet()
	if keep["plain"] || !keep["agent"] {
		t.Fatalf("keep set = %v, want the agent store only", keep)
	}
	if manifest, err := manager.Manifest(testTraceB); err != nil || manifest.MainClientID != "agent" {
		t.Fatalf("lookup = %+v, %v", manifest, err)
	}
	restarted, err := NewManager(Config{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if manifest, err := restarted.Manifest(testTraceB); err != nil || manifest.MainClientID != "agent" || manifest.TelemetryOnly {
		t.Fatalf("restarted lookup = %+v, %v", manifest, err)
	}
	// Agent archives still own their trace against each other.
	if _, err := manager.Register(testTraceB, "second-agent"); err == nil {
		t.Fatal("second agent registration succeeded")
	}
}

// GC that chose a telemetry-only archive before an agent session superseded it
// must not delete the agent's archive.
func TestManagerGCSparesSupersedingArchive(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	var manager *Manager
	var registerErr error
	manager, err := NewManager(Config{
		Root: root, TTL: time.Hour, Now: func() time.Time { return now },
		RemoveStore: func(clientID string) (bool, error) {
			if clientID == "plain" {
				_, registerErr = manager.Register(testTraceA, "agent")
			}
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sealArchive(t, manager, testTraceA, "plain", true, now, 1)
	now = now.Add(2 * time.Hour)
	if _, err := manager.GC(); err != nil {
		t.Fatal(err)
	}
	if registerErr != nil {
		t.Fatal(registerErr)
	}
	if manifest, err := manager.Manifest(testTraceA); err != nil || manifest.MainClientID != "agent" {
		t.Fatalf("superseding archive = %+v, %v; want it kept", manifest, err)
	}
	if _, err := os.Stat(filepath.Join(root, testTraceA+".json")); err != nil {
		t.Fatalf("superseding manifest removed: %v", err)
	}
}

// Telemetry-only archives share the quota but are evicted before any agent
// archive, even a newer one.
func TestManagerQuotaEvictsTelemetryOnlyFirst(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	var removed []string
	// Room for two stores (and their small bootstraps), not three.
	manager, err := NewManager(Config{
		Root: root, QuotaBytes: 25000, Now: func() time.Time { return now },
		RemoveStore: func(clientID string) (bool, error) { removed = append(removed, clientID); return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	sealArchive(t, manager, testTraceA, "old-agent", false, now, 10000)
	now = now.Add(time.Minute)
	sealArchive(t, manager, testTraceB, "plain", true, now, 10000)
	now = now.Add(time.Minute)
	sealArchive(t, manager, testTraceC, "new-agent", false, now, 10000)
	overage, err := manager.GC()
	if err != nil {
		t.Fatal(err)
	}
	if overage != 0 {
		t.Fatalf("overage = %d", overage)
	}
	if len(removed) != 1 || removed[0] != "plain" {
		t.Fatalf("removed = %v, want the telemetry-only store", removed)
	}
	for _, traceID := range []string{testTraceA, testTraceC} {
		if _, err := manager.Manifest(traceID); err != nil {
			t.Fatalf("agent archive %s collected: %v", traceID, err)
		}
	}
}
