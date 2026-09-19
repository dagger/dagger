package daggercmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/cloud/otlpstream"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
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
		msg = spanExport(
			&tracepb.Span{SpanId: f.root, Name: "dagger check", StartTimeUnixNano: 1, EndTimeUnixNano: 3},
			&tracepb.Span{
				SpanId: f.check, ParentSpanId: f.root, Name: "lint", StartTimeUnixNano: 1, EndTimeUnixNano: 2,
				Status:     &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR},
				Attributes: []*commonpb.KeyValue{{Key: telemetry.CheckNameAttr, Value: strVal("lint")}},
			},
		)
	case "/v1/logs/trace-id":
		msg = &collogspb.ExportLogsServiceRequest{
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
	default:
		http.NotFound(w, r)
		return
	}
	payload, err := proto.Marshal(msg)
	require.NoError(f.t, err)
	_ = fw.WriteData(payload)
	_ = fw.WriteTerminal()
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
func TestCloudLogsWritesTextOutputOnly(t *testing.T) {
	f, client := newFakeCloudLogs(t)

	prevSpan, prevCheck, prevTest, prevDesc, prevOut := logsSpan, logsCheck, logsTest, logsDescendants, logsOutput
	t.Cleanup(func() {
		logsSpan, logsCheck, logsTest, logsDescendants, logsOutput = prevSpan, prevCheck, prevTest, prevDesc, prevOut
	})
	logsSpan, logsCheck, logsTest, logsDescendants, logsOutput = "", "lint", "", false, ""

	cli := &CloudCLI{otlpClient: client}
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetContext(context.Background())
	require.NoError(t, cli.CloudLogs(cmd, []string{"trace-id"}))

	require.Equal(t, "hello world\n", out.String())

	qs := f.queries()
	require.Len(t, qs, 2)
	require.Equal(t, url.Values{
		"span_id": {dagui.SpanID{SpanID: [8]byte(f.check)}.String()}, "descendants": {"true"},
	}, qs[1], "the check's logs are rolled up, every record class")
}
