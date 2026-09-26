package archive

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
	if err := manager.SetTitle(testTraceA, manifest.Generation, "investigate cache"); err != nil {
		t.Fatal(err)
	}
	if err := manager.BeginFinalizing(testTraceA, manifest.Generation); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetTitle(testTraceA, manifest.Generation, "too late"); err == nil {
		t.Fatal("title update during finalization succeeded")
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
	gapReader, err := manager.Acquire(testTraceA)
	if err != nil {
		t.Fatalf("leased archive unavailable between import requests: %v", err)
	}
	gapReader.Release()
	lease.Release()
	if _, err := manager.GC(); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "old-client" {
		t.Fatalf("removed = %v", removed)
	}
	newReader, err := manager.Acquire(testTraceB)
	if err != nil {
		t.Fatalf("newest archive evicted: %v", err)
	}
	newReader.Release()

	now = now.Add(2 * time.Hour)
	if _, err := manager.GC(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(testTraceB); err == nil {
		t.Fatal("expired archive remained")
	}
}

func TestManagerRetriesPendingStoreDeletion(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	removeReady := false
	removeAttempts := 0
	manager, err := NewManager(Config{
		Root: root, TTL: time.Hour, Now: func() time.Time { return now },
		RemoveStore: func(string) (bool, error) {
			removeAttempts++
			return removeReady, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := manager.Register(testTraceA, "client")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.BeginFinalizing(testTraceA, manifest.Generation); err != nil {
		t.Fatal(err)
	}
	_, err = manager.Finalize(testTraceA, manifest.Generation, FinalizeInput{
		SealAt: now, BootstrapBytes: testBootstrap(t, manifest, HighWater{}, now, 0),
	})
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(2 * time.Hour)
	if _, err := manager.GC(); err != nil {
		t.Fatal(err)
	}
	if removeAttempts != 1 {
		t.Fatalf("remove attempts = %d, want 1", removeAttempts)
	}
	if _, err := os.Stat(filepath.Join(root, manifestKey(manifest)+".json")); err != nil {
		t.Fatalf("pending manifest removed before store: %v", err)
	}
	if _, err := manager.Acquire(testTraceA); err == nil {
		t.Fatal("pending archive remained acquirable")
	}

	removeReady = true
	if _, err := manager.GC(); err != nil {
		t.Fatal(err)
	}
	if removeAttempts != 2 {
		t.Fatalf("remove attempts = %d, want 2", removeAttempts)
	}
	for _, name := range []string{manifestKey(manifest) + ".json", manifestKey(manifest) + ".bootstrap"} {
		if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s remains after retry: %v", name, err)
		}
	}
}

func testBootstrap(t *testing.T, manifest Manifest, cut HighWater, sealAt time.Time, records int64) []byte {
	t.Helper()
	var signals []BootstrapSignal
	if records > 0 {
		signals = []BootstrapSignal{{Kind: BootstrapFrameTraces, Payload: []byte("otlp"), Records: records}}
	}
	data, _, err := BuildBootstrap(BootstrapHeader{
		Generation: manifest.Generation, TraceID: manifest.TraceID, SourceSession: manifest.SourceSession,
		SealAt: sealAt.UTC().Format(time.RFC3339Nano), HighWater: cut,
	}, signals)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestManagerSameTraceIndependentSourceSessions(t *testing.T) {
	root := t.TempDir()
	manager, err := NewManager(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	var manifests []Manifest
	for _, source := range []string{"one", "two"} {
		m, err := manager.RegisterSession(testTraceA, source, "client-"+source)
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.SetTitle(testTraceA, m.Generation, source); err != nil {
			t.Fatal(err)
		}
		if err := manager.BeginFinalizing(testTraceA, m.Generation); err != nil {
			t.Fatal(err)
		}
		seal := time.Now().UTC()
		m, err = manager.Finalize(testTraceA, m.Generation, FinalizeInput{SealAt: seal, BootstrapBytes: testBootstrap(t, m, HighWater{}, seal, 0)})
		if err != nil {
			t.Fatal(err)
		}
		manifests = append(manifests, m)
	}
	manager, err = NewManager(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(testTraceA); err == nil {
		t.Fatal("ambiguous trace selected an arbitrary source")
	} else {
		var failure *Failure
		if !errors.As(err, &failure) || failure.Kind != FailureAmbiguous {
			t.Fatalf("ambiguity: %v", err)
		}
	}
	first := manager.List("", "", 1)
	second := manager.List(first.Next, "", 1)
	if len(first.Archives) != 1 || len(second.Archives) != 1 || first.Next == "" || first.Archives[0].SourceSession == second.Archives[0].SourceSession {
		t.Fatalf("pagination lost a source: %+v %+v", first, second)
	}
	for _, m := range manifests {
		lease, err := manager.AcquireSource(testTraceA, m.Generation, m.SourceSession)
		if err != nil {
			t.Fatal(err)
		}
		if lease.Manifest().Title != m.SourceSession {
			t.Fatal("source metadata mixed")
		}
		lease.Release()
	}
	if _, err := manager.AcquireSource(testTraceA, manifests[0].Generation, manifests[1].SourceSession); err == nil {
		t.Fatal("mismatched selector fell back")
	}
	if _, err := manager.RegisterSession(testTraceB, "other", manifests[0].MainClientID); err == nil {
		t.Fatal("reused source DB accepted")
	}
	if page := manager.List("", manifests[0].Generation, 10); len(page.Archives) != 1 || page.Archives[0].Generation != manifests[1].Generation {
		t.Fatalf("generation exclusion hid sibling source: %+v", page)
	}
	if err := manager.Discard(testTraceA, manifests[0].Generation); err != nil {
		t.Fatal(err)
	}
	lease, err := manager.AcquireSource(testTraceA, manifests[1].Generation, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestManagerTitleSurvivesInterruptionAndSeal(t *testing.T) {
	root := t.TempDir()
	manager, err := NewManager(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	crashed, err := manager.Register(testTraceA, "crashed")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetTitle(testTraceA, crashed.Generation, "first title"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetTitle(testTraceA, crashed.Generation, "  \n"); err != nil {
		t.Fatal(err)
	}
	if page := manager.List("", "", 10); len(page.Archives) != 1 || page.Archives[0].Title != "first title" {
		t.Fatalf("active listing lost the title: %+v", page)
	}
	restarted, err := NewManager(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.Manifest(testTraceA)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != StateInterrupted || recovered.Title != "first title" {
		t.Fatalf("recovered = %+v", recovered)
	}

	sealed, err := restarted.Register(testTraceB, "sealed")
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.SetTitle(testTraceB, sealed.Generation, "final\x1b[31m title"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.BeginFinalizing(testTraceB, sealed.Generation); err != nil {
		t.Fatal(err)
	}
	seal := time.Now().UTC()
	closed, err := restarted.Finalize(testTraceB, sealed.Generation, FinalizeInput{
		SealAt: seal, BootstrapBytes: testBootstrap(t, sealed, HighWater{}, seal, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if closed.Title != "final [31m title" {
		t.Fatalf("closed title = %q", closed.Title)
	}
}

func TestSanitizeTitle(t *testing.T) {
	long := strings.Repeat("word ", 100)
	for _, test := range []struct{ in, want string }{
		{"  plain title ", "plain title"},
		{"line one\nline\ttwo\r\n", "line one line two"},
		{"esc\x1b[2Jape\x07", "esc [2Jape"},
		{"bidi\u202eevil\u200b", "bidievil"},
		{"bad\xffutf8", "badutf8"},
		{"\n\t ", ""},
		{strings.Repeat("\n", 1000), ""},
	} {
		if got := SanitizeTitle(test.in); got != test.want {
			t.Errorf("SanitizeTitle(%q) = %q, want %q", test.in, got, test.want)
		}
	}
	got := SanitizeTitle(long)
	if n := utf8.RuneCountInString(got); n > MaxTitleRunes || !strings.HasSuffix(got, "…") {
		t.Fatalf("long title = %q (%d runes)", got, n)
	}
	huge := SanitizeTitle(strings.Repeat("é", 1<<20))
	if n := utf8.RuneCountInString(huge); n != MaxTitleRunes || !strings.HasSuffix(huge, "…") {
		t.Fatalf("huge title has %d runes", n)
	}
}

func TestBootstrapFramingRequiresVerifiedTerminal(t *testing.T) {
	header := BootstrapHeader{Generation: "generation", TraceID: testTraceA, SealAt: time.Now().UTC().Format(time.RFC3339Nano)}
	data, records, err := BuildBootstrap(header, []BootstrapSignal{{Kind: BootstrapFrameTraces, Payload: []byte("otlp"), Records: 7}})
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
