package idtui

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"golang.org/x/sync/errgroup"
)

// TestRecordTraceFixture captures a real trace into a TraceFixture JSON for use
// with prettyHarness. It talks to Cloud, so it's opt-in: set the trace ID and
// output path (and DAGGER_CLOUD_URL / auth) to run it.
//
//	RECORD_TRACE_FIXTURE_ID=<traceID> \
//	RECORD_TRACE_FIXTURE_OUT=testdata/traces/<name>.json \
//	DAGGER_CLOUD_URL=http://localhost:8020 \
//	go test ./dagql/idtui/ -run TestRecordTraceFixture -count=1 -v
//
// It loads the priority spans, sweeps the whole tree by listening on every
// known span until no new spans appear, then fetches each span's logs in both
// the own and rolled-up forms -- everything the harness might replay.
func TestRecordTraceFixture(t *testing.T) {
	traceID := os.Getenv("RECORD_TRACE_FIXTURE_ID")
	out := os.Getenv("RECORD_TRACE_FIXTURE_OUT")
	if traceID == "" || out == "" {
		t.Skip("set RECORD_TRACE_FIXTURE_ID and RECORD_TRACE_FIXTURE_OUT to record a fixture")
	}
	ctx := context.Background()

	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil || cloudAuth == nil || cloudAuth.Token == nil {
		t.Fatalf("cloud auth (run 'dagger login' or set DAGGER_CLOUD_TOKEN): %v", err)
	}
	client, err := cloud.NewClient(ctx, cloudAuth)
	if err != nil {
		t.Fatalf("cloud client: %v", err)
	}
	if cloudAuth.Org == nil || cloudAuth.Org.ID == "" {
		t.Fatal("no current org; run 'dagger login' to set a default org")
	}
	orgID := cloudAuth.Org.ID

	var spans []cloud.SpanData
	seen := map[string]bool{}
	var priority []string
	var toListen []string // spans with unloaded children, listened once each
	add := func(batch []cloud.SpanData, isPriority bool) {
		for _, s := range batch {
			if seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			spans = append(spans, s)
			if isPriority {
				priority = append(priority, s.ID)
			}
			if s.ChildCount > 0 {
				toListen = append(toListen, s.ID)
			}
		}
	}

	if err := client.StreamSpansWith(ctx, orgID, traceID,
		cloud.SpanStreamOpts{Root: true, Incremental: true},
		func(b []cloud.SpanData) { add(b, true) }); err != nil {
		t.Fatalf("load priority spans: %v", err)
	}

	// Sweep the tree: listen on each span with children exactly once. New
	// spans with children enqueue more listens; this converges in O(spans),
	// not O(rounds*spans).
	listened := map[string]bool{}
	for len(toListen) > 0 {
		id := toListen[0]
		toListen = toListen[1:]
		if listened[id] {
			continue
		}
		listened[id] = true
		if err := client.StreamSpansWith(ctx, orgID, traceID,
			cloud.SpanStreamOpts{Root: false, Listen: []string{id}, Incremental: true},
			func(b []cloud.SpanData) { add(b, false) }); err != nil {
			t.Fatalf("listen %s: %v", id, err)
		}
	}

	// Fetch logs (both variants) for spans that have them, in parallel.
	logs := map[string]FixtureLogs{}
	var logsMu sync.Mutex
	eg, egctx := errgroup.WithContext(ctx)
	eg.SetLimit(8)
	for _, s := range spans {
		if !s.HasLogs {
			continue
		}
		spanID := s.ID
		eg.Go(func() error {
			var own, roll []cloud.LogMessage
			_ = client.StreamLogs(egctx, orgID, traceID, spanID, false,
				func(m []cloud.LogMessage) { own = append(own, m...) })
			_ = client.StreamLogs(egctx, orgID, traceID, spanID, true,
				func(m []cloud.LogMessage) { roll = append(roll, m...) })
			if len(own) > 0 || len(roll) > 0 {
				logsMu.Lock()
				logs[spanID] = FixtureLogs{Own: own, Roll: roll}
				logsMu.Unlock()
			}
			return nil
		})
	}
	_ = eg.Wait()

	snaps := make([]dagui.SpanSnapshot, len(spans))
	for i, s := range spans {
		snaps[i] = recordSpanDataToSnapshot(s)
	}
	fix := &TraceFixture{TraceID: traceID, Spans: snaps, Priority: priority, Logs: logs}
	// OUT is relative to the package directory (go test's cwd); create the dir.
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatalf("mkdir for fixture: %v", err)
	}
	if err := fix.Save(out); err != nil {
		t.Fatalf("save fixture: %v", err)
	}
	t.Logf("recorded %d spans (%d priority), %d spans with logs -> %s",
		len(snaps), len(priority), len(logs), out)
}

// recordSpanDataToSnapshot mirrors spanDataToSnapshot in
// internal/cmd/dagger/trace.go (which is package main and can't be imported).
// Keep them in sync; ProcessAttribute does the heavy lifting.
func recordSpanDataToSnapshot(s cloud.SpanData) dagui.SpanSnapshot {
	var snapshot dagui.SpanSnapshot
	snapshot.ID.SpanID, _ = trace.SpanIDFromHex(s.ID)
	snapshot.TraceID.TraceID, _ = trace.TraceIDFromHex(s.TraceID)
	snapshot.Name = s.Name
	if s.ParentID != nil {
		snapshot.ParentID.SpanID, _ = trace.SpanIDFromHex(*s.ParentID)
	}
	snapshot.StartTime = s.Timestamp
	if s.EndTime != nil {
		snapshot.EndTime = *s.EndTime
	}
	switch tracepb.Status_StatusCode(tracepb.Status_StatusCode_value[s.Status.Code]) {
	case tracepb.Status_STATUS_CODE_OK:
		snapshot.Status.Code = codes.Ok
	case tracepb.Status_STATUS_CODE_ERROR:
		snapshot.Status.Code = codes.Error
	default:
		snapshot.Status.Code = codes.Unset
	}
	snapshot.Status.Description = s.Status.Message
	snapshot.Links = make([]dagui.SpanLink, len(s.Links))
	for i, link := range s.Links {
		snapshot.Links[i].SpanContext.TraceID.TraceID, _ = trace.TraceIDFromHex(link.TraceID)
		snapshot.Links[i].SpanContext.SpanID.SpanID, _ = trace.SpanIDFromHex(link.SpanID)
		if purpose, ok := link.Attributes[telemetry.LinkPurposeAttr].(string); ok {
			snapshot.Links[i].Purpose = purpose
		}
	}
	snapshot.HasLogs = s.HasLogs
	for k, v := range s.Attributes {
		snapshot.ProcessAttribute(k, v)
	}
	snapshot.ChildCount = s.ChildCount
	return snapshot
}
