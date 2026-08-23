package archive

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	testTraceA = "11111111111111111111111111111111"
	testTraceB = "22222222222222222222222222222222"
)

func TestManagerStartupIndexesCorruptManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, testTraceA+".json"), []byte(`{"version":`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{Root: root})
	if err != nil {
		t.Fatalf("corrupt archive prevented engine startup: %v", err)
	}
	_, err = manager.Acquire(testTraceA)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != FailureCorrupt {
		t.Fatalf("acquire error = %v", err)
	}
}

func TestManagerStartupMarksInterrupted(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	manager, err := NewManager(Config{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	registered, err := manager.Register(testTraceA, "main")
	if err != nil {
		t.Fatal(err)
	}
	if registered.State != StateActive {
		t.Fatalf("state = %q", registered.State)
	}

	restarted, err := NewManager(Config{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := restarted.Manifest(testTraceA)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.State != StateInterrupted {
		t.Fatalf("state = %q, want interrupted", manifest.State)
	}
	if _, err := restarted.Acquire(testTraceA); err == nil {
		t.Fatal("interrupted archive was resumable")
	} else {
		var failure *Failure
		if !errors.As(err, &failure) || failure.Kind != FailureState || failure.State != StateInterrupted {
			t.Fatalf("unexpected acquire error: %v", err)
		}
	}
}

func TestManagerFinalizationMetadataAndGeneration(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	manager, err := NewManager(Config{Root: root, TTL: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := manager.Register(testTraceA, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateTitle(testTraceA, manifest.Generation, "investigate cache"); err != nil {
		t.Fatal(err)
	}
	if err := manager.BeginFinalizing(testTraceA, manifest.Generation); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateTitle(testTraceA, manifest.Generation, "too late"); err == nil {
		t.Fatal("metadata update during finalization succeeded")
	}
	now = now.Add(time.Minute)
	cut := HighWater{Spans: 3, Logs: 4, Metrics: 5}
	sidecar := testBootstrap(t, manifest, cut, now, 2)
	closed, err := manager.Finalize(testTraceA, manifest.Generation, FinalizeInput{
		HighWater: cut, SealAt: now,
		StoreSizeBytes: 10, BootstrapBytes: sidecar, BootstrapRecords: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if closed.State != StateClosed || closed.Title != "investigate cache" {
		t.Fatalf("unexpected manifest: %+v", closed)
	}
	if closed.ExpiresAt != now.Add(time.Hour) {
		t.Fatalf("expires = %s", closed.ExpiresAt)
	}
	lease, err := manager.Acquire(testTraceA)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if lease.Manifest().Generation != manifest.Generation {
		t.Fatal("generation changed")
	}
	if _, err := os.Stat(lease.BootstrapPath()); err != nil {
		t.Fatal(err)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, ".tmp-*")); len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestManagerQuotaLeaseAndExpiry(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	var removed []string
	manager, err := NewManager(Config{
		Root: root, TTL: time.Hour, QuotaBytes: 20, Now: func() time.Time { return now },
		RemoveStore: func(clientID string) (bool, error) { removed = append(removed, clientID); return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	closeArchive := func(traceID, clientID string) Manifest {
		registered, err := manager.Register(traceID, clientID)
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.BeginFinalizing(traceID, registered.Generation); err != nil {
			t.Fatal(err)
		}
		closed, err := manager.Finalize(traceID, registered.Generation, FinalizeInput{
			SealAt: now, StoreSizeBytes: 15, BootstrapBytes: testBootstrap(t, registered, HighWater{}, now, 0),
		})
		if err != nil {
			t.Fatal(err)
		}
		return closed
	}
	old := closeArchive(testTraceA, "old-client")
	lease, err := manager.Acquire(testTraceA)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	newest := closeArchive(testTraceB, "new-client")
	if newest.ClosedAt.Before(*old.ClosedAt) {
		t.Fatal("bad close ordering")
	}

	overage, err := manager.GC()
	if err != nil {
		t.Fatal(err)
	}
	if overage == 0 {
		t.Fatal("expected newest oversize overage")
	}
	if len(removed) != 0 {
		t.Fatalf("leased store removed early: %v", removed)
	}
	if _, err := manager.Acquire(testTraceA); err == nil {
		t.Fatal("evicted archive remained listed")
	}
	lease.Release()
	if len(removed) != 1 || removed[0] != "old-client" {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := manager.Acquire(testTraceB); err != nil {
		t.Fatalf("newest archive evicted: %v", err)
	}

	now = now.Add(2 * time.Hour)
	if _, err := manager.GC(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(testTraceB); err == nil {
		t.Fatal("expired archive remained")
	}
}

func testBootstrap(t *testing.T, manifest Manifest, cut HighWater, sealAt time.Time, records int64) []byte {
	t.Helper()
	var signals []BootstrapSignal
	if records > 0 {
		signals = []BootstrapSignal{{Kind: BootstrapFrameTraces, Payload: []byte("otlp"), Records: records}}
	}
	data, _, err := BuildBootstrap(BootstrapHeader{
		Generation: manifest.Generation, TraceID: manifest.TraceID,
		SealAt: sealAt.UTC().Format(time.RFC3339Nano), HighWater: cut,
	}, signals, BootstrapExclusions{})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestBootstrapFramingRequiresVerifiedTerminal(t *testing.T) {
	header := BootstrapHeader{Generation: "generation", TraceID: testTraceA, SealAt: time.Now().UTC().Format(time.RFC3339Nano)}
	data, records, err := BuildBootstrap(header, []BootstrapSignal{{Kind: BootstrapFrameTraces, Payload: []byte("otlp"), Records: 7}}, BootstrapExclusions{SpanIDs: []string{"span"}})
	if err != nil {
		t.Fatal(err)
	}
	if records != 7 {
		t.Fatalf("records = %d", records)
	}
	decodedHeader, terminal, err := VerifyBootstrap(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if decodedHeader.Generation != header.Generation || terminal.TraceRecords != 7 {
		t.Fatalf("unexpected decode: %+v %+v", decodedHeader, terminal)
	}
	if _, _, err := VerifyBootstrap(bytes.NewReader(data[:len(data)-1])); err == nil {
		t.Fatal("truncated bootstrap verified")
	}
	corrupt := append([]byte(nil), data...)
	corrupt[12] ^= 1
	if _, _, err := VerifyBootstrap(bytes.NewReader(corrupt)); err == nil {
		t.Fatal("corrupt bootstrap verified")
	}
}
