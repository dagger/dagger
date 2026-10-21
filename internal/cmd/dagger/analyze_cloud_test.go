package daggercmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/cloud/otlpstream"
	"github.com/dagger/dagger/internal/tracesource"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/proto"
)

// fakeCloudLogs serves /v1/traces (the priority spans: a root and a failed
// check) and /v1/logs (a text record, an empty EOF marker, a span-name
// record, a call payload) and records every request's query.
type fakeCloudLogs struct {
	t        *testing.T
	mu       sync.Mutex
	requests []*url.URL
	root     []byte
	check    []byte
}

func newFakeCloudLogs(t *testing.T) (*fakeCloudLogs, *cloud.OTLPClient) {
	t.Helper()
	f := &fakeCloudLogs{
		t:     t,
		root:  []byte{1, 1, 1, 1, 1, 1, 1, 1},
		check: []byte{2, 2, 2, 2, 2, 2, 2, 2},
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	client, err := cloud.NewOTLPClient(context.Background(), &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "token", TokenType: "Bearer"},
	})
	require.NoError(t, err)
	client, err = client.WithBaseURL(srv.URL)
	require.NoError(t, err)
	return f, client
}

func (f *fakeCloudLogs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL)
	f.mu.Unlock()
	w.Header().Set("Content-Type", otlpstream.ContentType)
	fw := otlpstream.NewFrameWriter(w)
	var msg proto.Message
	switch r.URL.Path {
	case "/v1/traces/trace-id":
		msg = f.spans()
	case "/v1/logs/trace-id":
		msg = f.logs()
	default:
		http.NotFound(w, r)
		return
	}
	payload, err := proto.Marshal(msg)
	require.NoError(f.t, err)
	_ = fw.WriteData(payload)
	_ = fw.WriteTerminal()
}

// spans is the trace's priority spans: a root and a failed check.
func (f *fakeCloudLogs) spans() *coltracepb.ExportTraceServiceRequest {
	return spanExport(
		&tracepb.Span{SpanId: f.root, Name: "dagger check", StartTimeUnixNano: 1, EndTimeUnixNano: 3},
		&tracepb.Span{
			SpanId: f.check, ParentSpanId: f.root, Name: "lint", StartTimeUnixNano: 1, EndTimeUnixNano: 2,
			Status:     &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR},
			Attributes: []*commonpb.KeyValue{{Key: telemetry.CheckNameAttr, Value: strVal("lint")}},
		},
	)
}

// logs is the check's log stream: text, an EOF marker, a span-name record
// and a call payload.
func (f *fakeCloudLogs) logs() *collogspb.ExportLogsServiceRequest {
	return &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{
					{SpanId: f.check, Body: strVal("hello ")},
					{SpanId: f.check, Body: strVal("")}, // EOF marker
					{SpanId: f.check, Body: strVal("renamed"), Attributes: []*commonpb.KeyValue{
						{Key: telemetryattrs.LogRoleAttr, Value: strVal(telemetryattrs.LogRoleSpanName)},
					}},
					{SpanId: f.check, Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: f.callPayload()}}, Attributes: []*commonpb.KeyValue{
						{Key: telemetry.ContentTypeAttr, Value: strVal(telemetryattrs.CallPayloadContentType)},
					}},
					{SpanId: f.check, Body: strVal("world")},
				},
			}},
		}},
	}
}

func (f *fakeCloudLogs) queries() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	var qs []url.Values
	for _, u := range f.requests {
		qs = append(qs, u.Query())
	}
	return qs
}

func (f *fakeCloudLogs) callPayload() []byte {
	payload, err := proto.Marshal(&callpbv1.Call{Digest: "xxh3:abc", Field: "container"})
	require.NoError(f.t, err)
	return payload
}

func strVal(s string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
}

// --check resolves against the priority spans; --span stands alone; the
// empty selector is the root, rolled up.
func TestSpanSelectorResolveSpanOverOTLP(t *testing.T) {
	f, client := newFakeCloudLogs(t)
	ctx := context.Background()

	id, descendants, err := spanSelector{check: "lint"}.resolveSpan(ctx, client, "trace-id")
	require.NoError(t, err)
	require.Equal(t, dagui.SpanID{SpanID: [8]byte(f.check)}.String(), id)
	require.True(t, descendants)

	id, descendants, err = spanSelector{}.resolveSpan(ctx, client, "trace-id")
	require.NoError(t, err)
	require.Equal(t, dagui.SpanID{SpanID: [8]byte(f.root)}.String(), id)
	require.True(t, descendants)

	id, descendants, err = spanSelector{span: "0102030405060708"}.resolveSpan(ctx, client, "trace-id")
	require.NoError(t, err)
	require.Equal(t, "0102030405060708", id)
	require.False(t, descendants)

	_, _, err = spanSelector{check: "nope"}.resolveSpan(ctx, client, "trace-id")
	require.EqualError(t, err, `no check named "nope" in trace trace-id`)
	_, _, err = spanSelector{test: "TestNope"}.resolveSpan(ctx, client, "trace-id")
	require.EqualError(t, err, `no test named "TestNope" in trace trace-id`)

	for _, q := range f.queries() {
		require.Equal(t, url.Values{"root": {"true"}, "incremental": {"true"}}, q,
			"name resolution loads the priority spans only")
	}
}

// The command end to end against the fake: no org is resolved, the check's
// subtree logs are requested, and only the traced program's text output is
// written -- not the EOF marker, the span-name record, or the call payload.
// The engine retains no archive of this trace, so Cloud serves it.
func TestCloudLogsWritesTextOutputOnly(t *testing.T) {
	f, client := newFakeCloudLogs(t)

	cli := &CloudCLI{otlpClient: client, openLocalTrace: func(context.Context, string) (tracesource.Source, func() error, error) {
		return nil, nil, archive.ErrCleanMiss
	}}
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	o := &traceViewOptions{sel: spanSelector{check: "lint"}, log: true, timeout: time.Minute}
	cleanup, n, err := cli.writeTraceLogsFrom(t.Context(), cmd, "trace-id", o.sel, o)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.NoError(t, cleanup())

	require.Equal(t, "hello world\n", out.String())

	qs := f.queries()
	require.Len(t, qs, 2)
	require.Equal(t, url.Values{
		"span_id": {dagui.SpanID{SpanID: [8]byte(f.check)}.String()}, "descendants": {"true"},
	}, qs[1], "the check's logs are rolled up, every record class")
}

// localLogsArchive serves the fake trace as a retained engine archive.
type localLogsArchive struct {
	f    *fakeCloudLogs
	logs []archive.StreamOptions
}

func (a *localLogsArchive) Traces(_ context.Context, _ string, opts archive.StreamOptions, cb func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error) {
	return opts.HighWater, cb(opts.HighWater, a.f.spans())
}
func (a *localLogsArchive) Logs(_ context.Context, _ string, opts archive.StreamOptions, cb func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	a.logs = append(a.logs, opts)
	return opts.HighWater, cb(opts.HighWater, a.f.logs())
}
func (a *localLogsArchive) Metrics(_ context.Context, _ string, opts archive.StreamOptions, _ func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error) {
	return opts.HighWater, nil
}

// TestTraceLogsDumpRunsNoFrontend: the non-pager --log dump (`-o file`, or
// stdout redirected) is scripted output. It must not start the frontend: a
// live TUI would wait for a telemetry EOF this path never sends, staying open
// until a key press. The command returns on its own.
func TestTraceLogsDumpRunsNoFrontend(t *testing.T) {
	prev := Frontend
	t.Cleanup(func() { Frontend = prev })
	Frontend = &idtui.FrontendMock{RunFunc: func(context.Context, dagui.FrontendOpts, func(context.Context) (cleanups.CleanupF, error)) error {
		t.Error("the log dump must not start the frontend")
		return nil
	}}
	f := &fakeCloudLogs{t: t, root: []byte{1, 1, 1, 1, 1, 1, 1, 1}, check: []byte{2, 2, 2, 2, 2, 2, 2, 2}}
	local := &localLogsArchive{f: f}
	closed := false
	seal := time.Unix(10, 0)
	cli := &CloudCLI{openLocalTrace: func(_ context.Context, traceID string) (tracesource.Source, func() error, error) {
		return tracesource.NewArchive(local, archive.Manifest{TraceID: traceID, SealAt: &seal, HighWater: archive.HighWater{Spans: 2, Logs: 5}}),
			func() error { closed = true; return nil }, nil
	}}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	out := filepath.Join(t.TempDir(), "out.txt")
	o := &traceViewOptions{sel: spanSelector{check: "lint"}, log: true, output: out, timeout: time.Minute}
	done := make(chan error, 1)
	go func() { done <- cli.writeTraceLogs(cmd, "trace-id", o.sel, o) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the log dump did not return without user input")
	}
	got, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", string(got))
	require.Equal(t, "wrote 2 log messages to "+out+"\n", stderr.String())
	require.True(t, closed, "the source is closed when the dump returns")
}

// TestTraceLogsReadLocalArchiveWithoutCloud: like the tree view and the
// pager, the non-pager --log path (redirected stdout, -o, 'dagger cloud
// logs') reads a retained engine archive, with no Cloud credentials at all.
func TestTraceLogsReadLocalArchiveWithoutCloud(t *testing.T) {
	// No credentials anywhere: consulting Cloud would fail the command.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DAGGER_CLOUD_TOKEN", "")
	f := &fakeCloudLogs{t: t, root: []byte{1, 1, 1, 1, 1, 1, 1, 1}, check: []byte{2, 2, 2, 2, 2, 2, 2, 2}}
	local := &localLogsArchive{f: f}
	closed := false
	seal := time.Unix(10, 0)
	cli := &CloudCLI{openLocalTrace: func(_ context.Context, traceID string) (tracesource.Source, func() error, error) {
		return tracesource.NewArchive(local, archive.Manifest{TraceID: traceID, SealAt: &seal, HighWater: archive.HighWater{Spans: 2, Logs: 5}}),
			func() error { closed = true; return nil }, nil
	}}
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	o := &traceViewOptions{sel: spanSelector{check: "lint"}, log: true, timeout: time.Minute}
	cleanup, _, err := cli.writeTraceLogsFrom(t.Context(), cmd, "trace-id", o.sel, o)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", out.String())
	require.Len(t, local.logs, 1)
	require.Equal(t, dagui.SpanID{SpanID: [8]byte(f.check)}.String(), local.logs[0].Logs.SpanID)
	require.True(t, local.logs[0].Logs.Descendants)
	require.False(t, closed, "the source outlives the stream until cleanup")
	require.NoError(t, cleanup())
	require.True(t, closed)
}
