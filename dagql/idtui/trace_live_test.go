package idtui

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// liveSource fetches a trace from Dagger Cloud lazily, mirroring the CLI's
// traceLoader (internal/cmd/dagger/trace.go): it streams the priority spans up
// front, then backfills a span's children on demand carrying Before/partial
// bookkeeping (so the listen stream terminates instead of tailing), and streams
// a span's logs when asked. This is the live data behind the trace console.
type liveSource struct {
	t        *testing.T
	ctx      context.Context
	client   *cloud.Client
	orgID    string
	traceHex string

	mu             sync.Mutex
	partial        bool
	spanUpdateTime *time.Time
}

// newLiveSource connects to Cloud (auth + client + current org). It fails the
// test on any auth/connection error.
func newLiveSource(t *testing.T, traceID string) *liveSource {
	t.Helper()
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
	return &liveSource{t: t, ctx: ctx, client: client, orgID: cloudAuth.Org.ID, traceHex: traceID}
}

func (l *liveSource) traceID() string { return l.traceHex }

// ingest updates the partial/spanUpdateTime bookkeeping and appends the batch as
// snapshots, mirroring traceLoader.ingest.
func (l *liveSource) ingest(spans []cloud.SpanData, out *[]dagui.SpanSnapshot) {
	l.mu.Lock()
	for i := range spans {
		s := &spans[i]
		if s.Partial {
			l.partial = true
		}
		if l.spanUpdateTime == nil || s.UpdateTime.After(*l.spanUpdateTime) {
			t := s.UpdateTime
			l.spanUpdateTime = &t
		}
	}
	l.mu.Unlock()
	for i := range spans {
		*out = append(*out, cloudSpanToSnapshot(spans[i]))
	}
}

func (l *liveSource) loadInitial() []dagui.SpanSnapshot {
	var out []dagui.SpanSnapshot
	if err := l.client.StreamSpansWith(l.ctx, l.orgID, l.traceHex,
		cloud.SpanStreamOpts{Root: true, Incremental: true},
		func(b []cloud.SpanData) { l.ingest(b, &out) }); err != nil {
		l.t.Fatalf("load priority spans: %v", err)
	}
	return out
}

func (l *liveSource) children(id dagui.SpanID) []dagui.SpanSnapshot {
	l.mu.Lock()
	partial := l.partial
	before := l.spanUpdateTime
	l.mu.Unlock()
	if !partial {
		// The whole trace arrived with the priority set; expanding is local.
		return nil
	}
	var out []dagui.SpanSnapshot
	if err := l.client.StreamSpansWith(l.ctx, l.orgID, l.traceHex,
		cloud.SpanStreamOpts{Root: false, Before: before, Listen: []string{id.String()}, Incremental: true},
		func(b []cloud.SpanData) { l.ingest(b, &out) }); err != nil {
		l.t.Logf("backfill children of %s: %v", id, err)
	}
	return out
}

func (l *liveSource) logs(id dagui.SpanID, descendants bool) []cloud.LogMessage {
	var msgs []cloud.LogMessage
	if err := l.client.StreamLogs(l.ctx, l.orgID, l.traceHex, id.String(), descendants,
		func(m []cloud.LogMessage) { msgs = append(msgs, m...) }); err != nil {
		l.t.Logf("stream logs %s: %v", id, err)
	}
	return msgs
}

// cloudSpanToSnapshot converts a Cloud API span into a dagui snapshot. It
// mirrors spanDataToSnapshot in internal/cmd/dagger/trace.go (which is package
// main and can't be imported); ProcessAttribute does the heavy lifting.
func cloudSpanToSnapshot(s cloud.SpanData) dagui.SpanSnapshot {
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
