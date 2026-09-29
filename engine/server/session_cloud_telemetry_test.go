package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine"
	engineclient "github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
)

// cloudReceiver is an OTLP/HTTP receiver standing in for Dagger Cloud. It
// records what arrives and the Authorization header of every request. Until
// release is closed (open, or the test's end), when hold is set, it reads each
// request and then waits; a request still connected then is recorded.
type cloudReceiver struct {
	*httptest.Server
	hold    bool
	release chan struct{}
	opened  sync.Once
	// refuse, while set, fails every export at once, recording nothing.
	refuse atomic.Bool
	// entered receives a value as each request arrives, when there is room.
	entered chan struct{}

	mu             sync.Mutex
	auths          []string
	spanNames      []string
	spanFrames     []string
	logBodies      []string
	payloadDigests []string
	metricNames    []string
}

func newCloudReceiver(t *testing.T, hold bool) *cloudReceiver {
	r := &cloudReceiver{hold: hold, release: make(chan struct{}), entered: make(chan struct{}, 16)}
	r.Server = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(func() {
		r.open()
		r.Server.Close()
	})
	return r
}

// open ends the hold: Cloud answers again.
func (r *cloudReceiver) open() {
	r.opened.Do(func() { close(r.release) })
}

func (r *cloudReceiver) serve(w http.ResponseWriter, req *http.Request) {
	// The engine's reachability probe, which Cloud answers even while an
	// export hangs.
	if req.Method == http.MethodHead {
		return
	}
	select {
	case r.entered <- struct{}{}:
	default:
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.refuse.Load() {
		http.Error(w, "outage", http.StatusInternalServerError)
		return
	}
	if r.hold {
		select {
		case <-r.release:
		case <-req.Context().Done():
			return
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auths = append(r.auths, req.Header.Get("Authorization"))
	switch req.URL.Path {
	case "/v1/traces":
		var msg coltracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, rs := range msg.GetResourceSpans() {
			for _, ss := range rs.GetScopeSpans() {
				for _, span := range ss.GetSpans() {
					r.spanNames = append(r.spanNames, span.GetName())
					for _, kv := range span.GetAttributes() {
						if kv.GetKey() == telemetry.DagCallAttr {
							r.spanFrames = append(r.spanFrames, kv.GetValue().GetStringValue())
						}
					}
				}
			}
		}
	case "/v1/logs":
		var msg collogspb.ExportLogsServiceRequest
		if err := proto.Unmarshal(body, &msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, rl := range msg.GetResourceLogs() {
			for _, sl := range rl.GetScopeLogs() {
				for _, rec := range sl.GetLogRecords() {
					r.logBodies = append(r.logBodies, rec.GetBody().GetStringValue())
					for _, kv := range rec.GetAttributes() {
						if kv.GetKey() == telemetryattrs.CallPayloadDigestAttr {
							r.payloadDigests = append(r.payloadDigests, kv.GetValue().GetStringValue())
						}
					}
				}
			}
		}
	case "/v1/metrics":
		var msg colmetricspb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, rm := range msg.GetResourceMetrics() {
			for _, sm := range rm.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					r.metricNames = append(r.metricNames, m.GetName())
				}
			}
		}
	default:
		http.NotFound(w, req)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (r *cloudReceiver) snapshot() (auths, spans, logs, metrics []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.auths...),
		append([]string(nil), r.spanNames...),
		append([]string(nil), r.logBodies...),
		append([]string(nil), r.metricNames...)
}

// newCloudTestSession is a one-client session whose main client carries md.
func newCloudTestSession(t *testing.T, srv *Server, md *engine.ClientMetadata) (*daggerSession, *clientRuntime) {
	t.Helper()
	srv.clientDBs = clientdb.NewDBs(t.TempDir())
	srv.wcprofSpanCount = newWcprofSpanCounter()
	srv.telemetryPubSub = NewPubSub(srv)
	md.SessionID, md.ClientID = "session", "root"
	root := &clientRuntime{clientRecord: &clientRecord{clientID: "root", clientMetadata: md}}
	sess := &daggerSession{
		sessionID:          "session",
		mainClientCallerID: root.clientID,
		clientRuntimes:     map[string]*clientRuntime{root.clientID: root},
		telemetryPubSub:    srv.telemetryPubSub,
	}
	root.daggerSession = sess
	installTestClientRecords(sess)
	srv.initializeSessionTelemetry(sess, md)
	if f := sess.cloudForwarder; f != nil {
		// A forwarder still publishing when the test ends stops with it.
		t.Cleanup(func() {
			f.stop(errors.New("test done"))
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			require.NoError(t, f.waitReleased(ctx))
		})
	}
	root.spanExporter = originSpanExporter{origin: root.clientID, next: sess.spanExporter}
	root.logExporter = originLogExporter{origin: root.clientID, next: sess.logExporter}
	srv.initializeClientMetrics(root)
	return sess, root
}

// publishRestToCloud ends the session's telemetry and waits for its forwarder
// to publish the rest of the store to Cloud and release.
func publishRestToCloud(ctx context.Context, t *testing.T, sess *daggerSession) {
	t.Helper()
	require.NoError(t, sess.shutdownTelemetry(ctx))
	require.NotNil(t, sess.cloudForwarder)
	require.NoError(t, sess.cloudForwarder.waitReleased(ctx))
}

// emitCloudTestTelemetry emits one span, one log record and one metric point
// from the client.
func emitCloudTestTelemetry(t *testing.T, sess *daggerSession, client *clientRuntime) {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), client.clientMetadata)
	ctx = telemetry.WithLoggerProvider(ctx, sess.loggerProvider)
	ctx, span := sess.tracerProvider.Tracer("test").Start(ctx, "cloud-span")
	rec := otellog.Record{}
	rec.SetTimestamp(time.Now())
	rec.SetBody(otellog.StringValue("cloud-log"))
	telemetry.Logger(ctx, "test").Emit(ctx, rec)
	gauge, err := client.meterProvider.Meter("test").Int64Gauge("cloud.metric")
	require.NoError(t, err)
	gauge.Record(ctx, 1)
	span.End()
}

func basicCloudAuth(token string) *auth.Cloud {
	return &auth.Cloud{Token: &oauth2.Token{AccessToken: token, TokenType: "Basic"}}
}

// A session whose main client asks the engine to publish exports its spans,
// logs and metrics to the client's Cloud URL with the client's credential,
// once each.
func TestSessionPublishesTelemetryToCloud(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	receiver := newCloudReceiver(t, false)
	sess, root := newCloudTestSession(t, &Server{}, &engine.ClientMetadata{
		CloudAuth:               basicCloudAuth("dag_test_token"),
		CloudURL:                receiver.URL,
		CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine,
	})
	require.NotNil(t, sess.cloudForwarder)
	require.Equal(t, 6, sess.telemetryDebug.ConfiguredSpanProcessors, "Cloud reads the store, not the providers")
	require.Equal(t, 4, sess.telemetryDebug.ConfiguredLogProcessors)

	emitCloudTestTelemetry(t, sess, root)
	sess.flushSessionCloudTelemetry(ctx)
	publishRestToCloud(ctx, t, sess)

	auths, spans, logs, metrics := receiver.snapshot()
	require.Equal(t, []string{"cloud-span"}, dedupe(spans), "the live snapshot and the end may both arrive")
	require.Equal(t, []string{"cloud-log"}, logs)
	require.Contains(t, metrics, "cloud.metric")
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("dag_test_token:"))
	require.NotEmpty(t, auths)
	for _, got := range auths {
		require.Equal(t, want, got)
	}
}

// A call span carries its call's frame and is the frame's only delivery: the
// session exports it on the protected call span lane to the client's store —
// where it settles the payload claim — and the forwarder publishes it from
// there to Cloud, frame included.
func TestSessionDeliversCallSpansToStoreAndCloud(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	receiver := newCloudReceiver(t, false)
	srv := &Server{}
	sess, root := newCloudTestSession(t, srv, &engine.ClientMetadata{
		CloudAuth:               basicCloudAuth("dag_test_token"),
		CloudURL:                receiver.URL,
		CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine,
	})
	body, digest := serverCallPayload(t, "lookup", "cloud")
	store := &callPayloadDeliveryStore{session: sess, targets: []string{root.clientID}}
	require.True(t, store.ClaimCallPayload(digest))

	spanCtx := engine.ContextWithClientMetadata(t.Context(), root.clientMetadata)
	_, span := sess.tracerProvider.Tracer("test").Start(spanCtx, "Thing.lookup", trace.WithAttributes(
		attribute.String(telemetry.DagDigestAttr, digest),
		attribute.String(telemetry.DagCallAttr, base64.StdEncoding.EncodeToString(body)),
	))
	span.End()
	require.NoError(t, sess.FlushTelemetry(ctx, "test"))
	require.Empty(t, sess.callPayloadMissingTargets(digest, store.targets),
		"a persisted call span delivers its frame")

	sess.flushSessionCloudTelemetry(ctx)
	publishRestToCloud(ctx, t, sess)
	auths, spans, _, _ := receiver.snapshot()
	require.NotEmpty(t, auths)
	require.Equal(t, []string{"Thing.lookup"}, dedupe(spans))
	receiver.mu.Lock()
	frames := slices.Clone(receiver.spanFrames)
	receiver.mu.Unlock()
	require.Contains(t, frames, base64.StdEncoding.EncodeToString(body), "the frame rides the span to Cloud")

	db, err := srv.clientDBs.Open(ctx, root.clientID)
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Read().SelectSpansSince(ctx, clientdb.SelectSpansSinceParams{Limit: 100})
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		require.Equal(t, "Thing.lookup", row.Name)
	}
}

func dedupe(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// Without the publisher flag, or without a credential, the engine exports
// nothing to Cloud: an older client forwards the session's telemetry itself.
func TestSessionWithoutPublisherStaysSilent(t *testing.T) {
	t.Parallel()
	for name, md := range map[string]*engine.ClientMetadata{
		"no flag":       {CloudAuth: basicCloudAuth("dag_test_token")},
		"no credential": {CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine},
		"other value":   {CloudAuth: basicCloudAuth("dag_test_token"), CloudTelemetryPublisher: "client"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			receiver := newCloudReceiver(t, false)
			md.CloudURL = receiver.URL
			sess, root := newCloudTestSession(t, &Server{}, md)
			require.Nil(t, sess.cloudForwarder)
			require.Nil(t, sess.cloudMetrics)
			require.Empty(t, sess.cloudFlushers)
			require.Equal(t, 6, sess.telemetryDebug.ConfiguredSpanProcessors)
			require.Equal(t, 4, sess.telemetryDebug.ConfiguredLogProcessors)

			emitCloudTestTelemetry(t, sess, root)
			sess.flushSessionCloudTelemetry(ctx)
			require.NoError(t, sess.shutdownTelemetry(ctx))
			auths, _, _, _ := receiver.snapshot()
			require.Empty(t, auths)
		})
	}
}

// Against a Cloud that accepts requests and never answers, the main client's
// whole shutdown request (the Cloud drain, the providers' and every client's
// metric flush, and the cleanup that reclaims the client when its last lease
// goes) waits on Cloud for one bound in total, not one per wait, and the
// final span still reaches the client's stream. The session's teardown
// afterwards waits on Cloud only to release the metric exporter, and the
// forwarder keeps holding its one batch in the background, the rest waiting
// in the store.
func TestSessionCloudTelemetryFlushIsBounded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	receiver := newCloudReceiver(t, true)
	const bound = 300 * time.Millisecond
	sess, root := newCloudTestSession(t, &Server{sessionCloudFlushTimeout: bound}, &engine.ClientMetadata{
		CloudAuth:               basicCloudAuth("dag_test_token"),
		CloudURL:                receiver.URL,
		CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine,
	})
	emitCloudTestTelemetry(t, sess, root)
	// A call payload too: it waits in the store like everything else.
	payloadCtx := engine.ContextWithClientMetadata(ctx, root.clientMetadata)
	payloadCtx = telemetry.WithLoggerProvider(payloadCtx, sess.loggerProvider)
	telemetry.Logger(payloadCtx, "test").Emit(payloadCtx, cloudPayloadRecordValue("xxh3:bounded"))

	// serveShutdown's Cloud steps for the main client, in order.
	start := time.Now()
	stopBudget := sess.startCloudShutdownBudget()
	sess.flushSessionCloudTelemetry(ctx)
	spanCtx := engine.ContextWithClientMetadata(ctx, root.clientMetadata)
	_, final := sess.tracerProvider.Tracer("test").Start(spanCtx, "final-span")
	final.End()
	require.NoError(t, sess.FlushTelemetry(ctx, "client shutdown"))
	stopBudget()
	// The request's cleanup: releasing the main client's last lease reclaims
	// its runtime, which flushes and shuts down its metric provider, Cloud
	// reader included, on the goroutine that has yet to send the response.
	sess.finishClientRuntimeReclamation(clientRuntimeReclamation{runtime: root})
	elapsed := time.Since(start)
	require.Less(t, elapsed, bound+250*time.Millisecond, "one bound for the whole shutdown request, its cleanup included, not one per wait")

	db, err := sess.telemetryPubSub.srv.clientDBs.Open(ctx, root.clientID)
	require.NoError(t, err)
	spans, err := db.Read().SelectSpansSince(ctx, clientdb.SelectSpansSinceParams{Limit: 100})
	require.NoError(t, err)
	require.NoError(t, db.Close())
	var names []string
	for _, span := range spans {
		names = append(names, span.Name)
	}
	require.Contains(t, names, "final-span", "the final span reaches the client's stream")

	start = time.Now()
	require.NoError(t, sess.shutdownTelemetry(ctx))
	require.Less(t, time.Since(start), bound+time.Second, "teardown waits on Cloud for the metric exporter's bound at most")
	select {
	case <-sess.cloudForwarder.done:
		t.Fatal("the forwarder gave up on the rest of the store while Cloud hangs")
	default:
	}
	spanCursor, _ := sess.cloudForwarder.lanes[0].position()
	require.Zero(t, spanCursor, "nothing Cloud did not take is counted as published")
}

// A publishing session asks its scale-out engine to publish too, with its
// main client's Cloud URL and credentials path. Both of the remote's streams
// reach the client routing: the one the remote confirms (the WithoutCloud
// exporters) marked as published, which the forwarder leaves out, the one it
// does not (EngineTrace and EngineLogs) as the session's own, which the
// forwarder publishes. A session that does not publish does not ask.
func TestScaleOutTelemetryFollowsTheSession(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	receiver := newCloudReceiver(t, false)
	srv := &Server{}
	sess, root := newCloudTestSession(t, srv, &engine.ClientMetadata{
		CloudAuth:               basicCloudAuth("dag_test_token"),
		CloudURL:                receiver.URL,
		CredentialsPath:         "/home/user/.config/dagger/credentials.json",
		CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine,
	})
	var params engineclient.Params
	sess.scaleOutTelemetryParams(root, &params)
	require.True(t, params.EngineCloudTelemetry)
	require.Equal(t, receiver.URL, params.CloudURL)
	require.Equal(t, "/home/user/.config/dagger/credentials.json", params.CloudCredentialsPath)
	require.Equal(t, root.spanExporter, params.EngineTrace)
	require.Equal(t, root.logExporter, params.EngineLogs)

	remote := tracetest.SpanStub{
		Name: "remote-span",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
		}),
		StartTime: time.Now(),
		EndTime:   time.Now(),
	}
	require.NoError(t, params.EngineTraceWithoutCloud.ExportSpans(ctx, []sdktrace.ReadOnlySpan{
		tracetest.SpanStub{
			Name: "not-to-cloud",
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
			}),
			StartTime: remote.StartTime, EndTime: remote.EndTime,
		}.Snapshot(),
	}))
	require.NoError(t, params.EngineTrace.ExportSpans(ctx, []sdktrace.ReadOnlySpan{remote.Snapshot()}))
	var rec, published otellog.Record
	rec.SetBody(otellog.StringValue("remote-log"))
	published.SetBody(otellog.StringValue("published-log"))
	require.NoError(t, params.EngineLogsWithoutCloud.Export(ctx, []sdklog.Record{controlTestRecord(t, published)}))
	require.NoError(t, params.EngineLogs.Export(ctx, []sdklog.Record{controlTestRecord(t, rec)}))

	db, err := srv.clientDBs.Open(ctx, root.clientID)
	require.NoError(t, err)
	storedSpans, err := db.Read().SelectSpansSince(ctx, clientdb.SelectSpansSinceParams{Limit: 100})
	require.NoError(t, err)
	storedLogs, err := db.Read().SelectLogsSince(ctx, clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.NoError(t, db.Close())
	require.Len(t, storedSpans, 2, "the client sees both streams")
	require.Len(t, storedLogs, 2, "the client sees both streams")

	sess.flushSessionCloudTelemetry(ctx)
	publishRestToCloud(ctx, t, sess)
	_, spans, logs, _ := receiver.snapshot()
	require.Equal(t, []string{"remote-span"}, spans)
	require.Equal(t, []string{"remote-log"}, logs)

	silent, silentRoot := newCloudTestSession(t, &Server{}, &engine.ClientMetadata{
		CloudAuth: basicCloudAuth("dag_test_token"),
		CloudURL:  receiver.URL,
	})
	t.Cleanup(func() { require.NoError(t, silent.shutdownTelemetry(context.Background())) })
	params = engineclient.Params{}
	silent.scaleOutTelemetryParams(silentRoot, &params)
	require.False(t, params.EngineCloudTelemetry)
	require.Empty(t, params.CloudURL)
	require.Equal(t, silentRoot.spanExporter, params.EngineTrace)
	require.Nil(t, params.EngineTraceWithoutCloud)
}

// Every telemetry stream of a session carries the session's one answer, a
// reconnect's included: confirmed when the main client asked and the session
// built its Cloud exporters, absent otherwise, including when building them
// failed.
func TestTelemetryStreamsConfirmCloudPublishing(t *testing.T) {
	t.Parallel()
	receiver := newCloudReceiver(t, false)
	for _, tc := range []struct {
		name string
		md   *engine.ClientMetadata
		want string
	}{
		{name: "publishing", want: engine.CloudTelemetryPublisherEngine, md: &engine.ClientMetadata{
			CloudAuth: basicCloudAuth("dag_test_token"), CloudURL: receiver.URL,
			CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine,
		}},
		{name: "not asked", md: &engine.ClientMetadata{
			CloudAuth: basicCloudAuth("dag_test_token"), CloudURL: receiver.URL,
		}},
		{name: "exporter setup failed", md: &engine.ClientMetadata{
			CloudAuth: basicCloudAuth("dag_test_token"), CloudURL: "http://[::1]:namedport",
			CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine,
		}},
		{name: "Cloud URL unreachable", md: &engine.ClientMetadata{
			CloudAuth: basicCloudAuth("dag_test_token"), CloudURL: refusedURL(t),
			CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sess, root := newCloudTestSession(t, &Server{}, tc.md)
			t.Cleanup(func() { require.NoError(t, sess.shutdownTelemetry(context.Background())) })
			shutdownCh := make(chan struct{})
			close(shutdownCh)
			root.shutdownCh = shutdownCh
			for _, cursor := range []string{"", "3"} {
				req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
				req.Header.Set("Accept", enginetel.LiveContentType)
				if cursor != "" {
					req.Header.Set(enginetel.LiveCursorHeader, cursor)
				}
				resp := httptest.NewRecorder()
				require.NoError(t, sess.telemetryPubSub.TracesSubscribeHandler(resp, req, root.clientRecord))
				require.Equal(t, http.StatusOK, resp.Code)
				require.Equal(t, tc.want, resp.Header().Get(engine.CloudTelemetryPublisherHeader), "cursor %q", cursor)
			}
		})
	}
}

// postTelemetry posts one span, one log record and one metric point to the
// engine's OTLP handlers as a process in one of the client's containers does.
func postTelemetry(t *testing.T, ps *PubSub, sessionID, clientID string) {
	t.Helper()
	resourcePB := telemetry.ResourcePtrToPB(resource.NewSchemaless(attribute.String("service.name", "posting-sdk")))
	span := tracetest.SpanStub{
		Name: "posted-span",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{7}, SpanID: trace.SpanID{7}, TraceFlags: trace.FlagsSampled,
		}),
		StartTime: time.Now(),
		EndTime:   time.Now(),
		Resource:  resource.NewSchemaless(attribute.String("service.name", "posting-sdk")),
	}.Snapshot()
	now := uint64(time.Now().UnixNano())
	for path, msg := range map[string]proto.Message{
		"/v1/traces": &coltracepb.ExportTraceServiceRequest{ResourceSpans: telemetry.SpansToPB([]sdktrace.ReadOnlySpan{span})},
		"/v1/logs": &collogspb.ExportLogsServiceRequest{ResourceLogs: []*otlplogsv1.ResourceLogs{{
			Resource: resourcePB,
			ScopeLogs: []*otlplogsv1.ScopeLogs{{LogRecords: []*otlplogsv1.LogRecord{{
				TimeUnixNano: now,
				TraceId:      []byte{7, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
				SpanId:       []byte{7, 0, 0, 0, 0, 0, 0, 0},
				Body:         &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "posted-log"}},
			}}}},
		}}},
		"/v1/metrics": &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*otlpmetricsv1.ResourceMetrics{{
			Resource: resourcePB,
			ScopeMetrics: []*otlpmetricsv1.ScopeMetrics{{Metrics: []*otlpmetricsv1.Metric{{
				Name: "posted.metric",
				Data: &otlpmetricsv1.Metric_Gauge{Gauge: &otlpmetricsv1.Gauge{DataPoints: []*otlpmetricsv1.NumberDataPoint{{
					TimeUnixNano: now,
					Value:        &otlpmetricsv1.NumberDataPoint_AsInt{AsInt: 1},
				}}}},
			}}}},
		}}},
	} {
		body, err := proto.Marshal(msg)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("X-Dagger-Session-ID", sessionID)
		req.Header.Set("X-Dagger-Client-ID", clientID)
		resp := httptest.NewRecorder()
		ps.ServeHTTP(resp, req)
		require.Equal(t, http.StatusCreated, resp.Code, "%s: %s", path, resp.Body.String())
	}
}

// Telemetry a process in a container posts reaches the client routing once
// and, when the session publishes, Cloud once, from the main client's store:
// posted telemetry never passes the session's providers.
func TestPostedTelemetryReachesCloudOnce(t *testing.T) {
	t.Parallel()
	for _, publishing := range []bool{true, false} {
		t.Run(fmt.Sprintf("publishing=%v", publishing), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			receiver := newCloudReceiver(t, false)
			md := &engine.ClientMetadata{CloudAuth: basicCloudAuth("dag_test_token"), CloudURL: receiver.URL}
			if publishing {
				md.CloudTelemetryPublisher = engine.CloudTelemetryPublisherEngine
			}
			srv := &Server{}
			sess, root := newCloudTestSession(t, srv, md)
			require.Equal(t, publishing, sess.publishesToCloud())
			sess.state.Store(sessionStateInitialized)
			srv.daggerSessions = map[string]*daggerSession{sess.sessionID: sess}

			postTelemetry(t, srv.telemetryPubSub, sess.sessionID, root.clientID)
			require.NoError(t, sess.FlushTelemetry(ctx, "test"))
			sess.flushSessionCloudTelemetry(ctx)

			db, err := srv.clientDBs.Open(ctx, root.clientID)
			require.NoError(t, err)
			spans, err := db.Read().SelectSpansSince(ctx, clientdb.SelectSpansSinceParams{Limit: 100})
			require.NoError(t, err)
			logs, err := db.Read().SelectLogsSince(ctx, clientdb.SelectLogsSinceParams{Limit: 100})
			require.NoError(t, err)
			metrics, err := db.Read().SelectMetricsSince(ctx, clientdb.SelectMetricsSinceParams{Limit: 100})
			require.NoError(t, err)
			require.NoError(t, db.Close())
			var clientSpans, clientLogs, clientMetrics int
			for _, span := range spans {
				if span.Name == "posted-span" {
					clientSpans++
				}
			}
			for _, row := range logs {
				if bytes.Contains(row.Body, []byte("posted-log")) {
					clientLogs++
				}
			}
			for _, rm := range clientdb.MetricsToPB(metrics) {
				for _, sm := range rm.GetScopeMetrics() {
					for _, m := range sm.GetMetrics() {
						if m.GetName() == "posted.metric" {
							clientMetrics++
						}
					}
				}
			}
			require.Equal(t, 1, clientSpans, "client span delivery")
			require.Equal(t, 1, clientLogs, "client log delivery")
			require.Equal(t, 1, clientMetrics, "client metric delivery")

			require.NoError(t, sess.shutdownTelemetry(ctx))
			if publishing {
				require.NoError(t, sess.cloudForwarder.waitReleased(ctx))
			}
			_, cloudSpans, cloudLogs, cloudMetrics := receiver.snapshot()
			count := func(names []string, name string) int {
				n := 0
				for _, got := range names {
					if got == name {
						n++
					}
				}
				return n
			}
			want := 0
			if publishing {
				want = 1
			}
			require.Equal(t, want, count(cloudSpans, "posted-span"), "Cloud span delivery")
			require.Equal(t, want, count(cloudLogs, "posted-log"), "Cloud log delivery")
			require.Equal(t, want, count(cloudMetrics, "posted.metric"), "Cloud metric delivery")
		})
	}
}

// fakeCredentialsFile records the file operations of one refresh.
type fakeCredentialsFile struct {
	mu       sync.Mutex
	reads    int
	writes   []string
	readErr  error
	writeErr error
	refresh  func(context.Context, *oauth2.Token) (*oauth2.Token, error)
}

func (f *fakeCredentialsFile) file() cloudCredentialsFile {
	return cloudCredentialsFile{
		read: func(context.Context) ([]byte, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.reads++
			return []byte(`{"access_token":"old","refresh_token":"r"}`), f.readErr
		},
		write: func(_ context.Context, data []byte) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writeErr != nil {
				return f.writeErr
			}
			f.writes = append(f.writes, string(data))
			return nil
		},
		refresh: func(ctx context.Context, token *oauth2.Token) (*oauth2.Token, error) {
			if f.refresh != nil {
				return f.refresh(ctx, token)
			}
			return &oauth2.Token{AccessToken: "new", RefreshToken: "r"}, nil
		},
	}
}

// A refresh reads the credentials file, exchanges the token and writes the
// new one back; with the gate closed it reads nothing.
func TestRefreshCredentialsFile(t *testing.T) {
	t.Parallel()
	f := &fakeCredentialsFile{}
	token, err := refreshCredentialsFile(t.Context(), "session", &cloudRefreshGate{}, f.file())
	require.NoError(t, err)
	require.Equal(t, "new", token.AccessToken)
	require.Len(t, f.writes, 1)
	require.Contains(t, f.writes[0], `"access_token":"new"`)

	closed := &cloudRefreshGate{}
	closed.stop()
	f = &fakeCredentialsFile{}
	_, err = refreshCredentialsFile(t.Context(), "session", closed, f.file())
	require.ErrorIs(t, err, errCloudRefreshSessionClosing)
	require.Zero(t, f.reads, "no file operation once the main client's shutdown began")
}

// The exchange with Cloud is outside the gate: stopping refreshes does not
// wait for it, and a refresh whose exchange completes after that keeps its
// token and does not write it back.
func TestRefreshCredentialsFileLateExchange(t *testing.T) {
	t.Parallel()
	exchanging, release := make(chan struct{}), make(chan struct{})
	f := &fakeCredentialsFile{refresh: func(context.Context, *oauth2.Token) (*oauth2.Token, error) {
		close(exchanging)
		<-release
		return &oauth2.Token{AccessToken: "late"}, nil
	}}
	gate := &cloudRefreshGate{}
	type result struct {
		token *oauth2.Token
		err   error
	}
	done := make(chan result, 1)
	go func() {
		token, err := refreshCredentialsFile(t.Context(), "session", gate, f.file())
		done <- result{token, err}
	}()
	<-exchanging
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	start := time.Now()
	require.NoError(t, gate.close(ctx), "no file operation in flight to wait for")
	require.Less(t, time.Since(start), 100*time.Millisecond)
	close(release)
	got := <-done
	require.NoError(t, got.err)
	require.Equal(t, "late", got.token.AccessToken, "the token is kept")
	require.Empty(t, f.writes, "and not written back after the gate closed")
}

// A write-back that fails, the client's attachables gone, costs the file its
// update, never the refreshed token; a read that fails fails the refresh.
func TestRefreshCredentialsFileAttachablesGone(t *testing.T) {
	t.Parallel()
	f := &fakeCredentialsFile{writeErr: errCloudRefreshSessionClosing}
	token, err := refreshCredentialsFile(t.Context(), "session", &cloudRefreshGate{}, f.file())
	require.NoError(t, err)
	require.Equal(t, "new", token.AccessToken)

	f = &fakeCredentialsFile{readErr: errCloudRefreshSessionClosing}
	_, err = refreshCredentialsFile(t.Context(), "session", &cloudRefreshGate{}, f.file())
	require.ErrorIs(t, err, errCloudRefreshSessionClosing)
}

// Stopping refreshes waits for one in flight, and no longer than its context.
func TestCloudRefreshGateWaitsForInflight(t *testing.T) {
	t.Parallel()
	gate := &cloudRefreshGate{}
	require.True(t, gate.enter())
	closed := make(chan error, 1)
	go func() { closed <- gate.close(t.Context()) }()
	select {
	case <-closed:
		t.Fatal("close returned while a refresh was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	require.False(t, gate.enter(), "no new refresh once closing")
	gate.exit()
	require.NoError(t, <-closed)

	stuck := &cloudRefreshGate{}
	require.True(t, stuck.enter())
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, stuck.close(ctx), context.DeadlineExceeded)
	stuck.exit()
}

// A call payload the engine emitted and a nested CLI then posted back reaches
// Cloud once, as it reaches the client once: the client routing suppresses a
// payload it already delivered, and Cloud is published from the client's
// store.
func TestCallPayloadReachesCloudOnce(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	receiver := newCloudReceiver(t, false)
	srv := &Server{}
	sess, root := newCloudTestSession(t, srv, &engine.ClientMetadata{
		CloudAuth:               basicCloudAuth("dag_test_token"),
		CloudURL:                receiver.URL,
		CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine,
	})
	sess.state.Store(sessionStateInitialized)
	srv.daggerSessions = map[string]*daggerSession{sess.sessionID: sess}
	const digest = "xxh3:cloud-payload-once"

	// The engine emits the payload once, in its call's span.
	emitCtx := engine.ContextWithClientMetadata(ctx, root.clientMetadata)
	emitCtx = telemetry.WithLoggerProvider(emitCtx, sess.loggerProvider)
	emitCtx = trace.ContextWithSpanContext(emitCtx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{7}, SpanID: trace.SpanID{7}, TraceFlags: trace.FlagsSampled,
	}))
	var rec otellog.Record
	rec.SetTimestamp(time.Now())
	rec.SetBody(otellog.BytesValue([]byte("payload")))
	rec.AddAttributes(
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType),
		otellog.String(telemetryattrs.CallPayloadDigestAttr, digest),
	)
	telemetry.Logger(emitCtx, "test").Emit(emitCtx, rec)
	require.NoError(t, sess.FlushTelemetry(ctx, "test"))

	// A nested CLI posts the same payload back.
	body, err := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: []*otlplogsv1.ResourceLogs{{
		Resource: telemetry.ResourcePtrToPB(resource.NewSchemaless(attribute.String("service.name", "nested-cli"))),
		ScopeLogs: []*otlplogsv1.ScopeLogs{{LogRecords: []*otlplogsv1.LogRecord{{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			TraceId:      []byte{7, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			SpanId:       []byte{7, 0, 0, 0, 0, 0, 0, 0},
			Body:         &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: []byte("payload")}},
			Attributes: []*otlpcommonv1.KeyValue{
				{Key: telemetry.ContentTypeAttr, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: telemetryattrs.CallPayloadContentType}}},
				{Key: telemetryattrs.CallPayloadDigestAttr, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: digest}}},
			},
		}}}},
	}}})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(body))
	req.Header.Set("X-Dagger-Session-ID", sess.sessionID)
	req.Header.Set("X-Dagger-Client-ID", root.clientID)
	resp := httptest.NewRecorder()
	srv.telemetryPubSub.ServeHTTP(resp, req)
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
	require.NoError(t, sess.FlushTelemetry(ctx, "test"))

	db, err := srv.clientDBs.Open(ctx, root.clientID)
	require.NoError(t, err)
	logs, err := db.Read().SelectLogsSince(ctx, clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.NoError(t, db.Close())
	require.Len(t, logs, 1, "the client gets the payload once")

	sess.flushSessionCloudTelemetry(ctx)
	publishRestToCloud(ctx, t, sess)
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	require.Equal(t, []string{digest}, receiver.payloadDigests, "Cloud gets the payload once")
}

// cloudPayloadRecordValue is a call payload as an API record, to emit.
func cloudPayloadRecordValue(digest string) otellog.Record {
	var rec otellog.Record
	rec.SetTimestamp(time.Now())
	rec.SetBody(otellog.BytesValue([]byte("payload " + digest)))
	rec.AddAttributes(
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType),
		otellog.String(telemetryattrs.CallPayloadDigestAttr, digest),
	)
	return rec
}

func cloudOtherRecord(i int) otellog.Record {
	var rec otellog.Record
	rec.SetTimestamp(time.Now())
	rec.SetBody(otellog.StringValue(fmt.Sprintf("exec output %d", i)))
	return rec
}

func histogramMetrics(counts []uint64, bounds []float64) *metricdata.ResourceMetrics {
	return &metricdata.ResourceMetrics{
		Resource: resource.NewSchemaless(attribute.String("service.name", "test")),
		ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: []metricdata.Metrics{{
			Name: "test.histogram",
			Data: metricdata.Histogram[int64]{
				Temporality: metricdata.CumulativeTemporality,
				DataPoints: []metricdata.HistogramDataPoint[int64]{{
					StartTime: time.Now(), Time: time.Now(),
					Count: 3, Bounds: bounds, BucketCounts: counts,
				}},
			},
		}}}},
	}
}

// A queued collection is a deep copy: the reader reusing its buffers after
// Export returns does not change what gets exported.
func TestCloudMetricQueueCopiesCollections(t *testing.T) {
	t.Parallel()
	// No worker: the collection stays queued.
	q := &cloudMetricQueue{wake: make(chan struct{}, 1)}
	counts := []uint64{1, 2}
	bounds := []float64{10}
	require.NoError(t, q.Export(t.Context(), histogramMetrics(counts, bounds)))
	counts[0], counts[1], bounds[0] = 99, 99, 99

	require.Len(t, q.queue, 1)
	point := q.queue[0].GetScopeMetrics()[0].GetMetrics()[0].GetHistogram().GetDataPoints()[0]
	require.Equal(t, []uint64{1, 2}, point.GetBucketCounts())
	require.Equal(t, []float64{10}, point.GetExplicitBounds())
}

func gaugeMetrics(value int64) *metricdata.ResourceMetrics {
	return &metricdata.ResourceMetrics{
		Resource: resource.NewSchemaless(attribute.String("service.name", "test")),
		ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: []metricdata.Metrics{{
			Name: "test.gauge",
			Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{Time: time.Now(), Value: value}}},
		}}}},
	}
}

// Against a Cloud that never answers, shutting the queue down takes one
// bound in all, draining, the exports in flight and the release: the first
// export waits on Cloud until its own bound, before the drain deadline, and
// the second then starts and is cancelled at that deadline.
func TestCloudMetricQueueShutdownIsBounded(t *testing.T) {
	t.Parallel()
	receiver := newCloudReceiver(t, true)
	_, _, metrics, err := enginetel.NewCloudExporters(t.Context(), basicCloudAuth("dag_test_token"), nil, receiver.URL)
	require.NoError(t, err)
	const bound = 300 * time.Millisecond
	q := newCloudMetricQueue(metrics, cloudFlushBound{sessionID: "session", timeout: bound})
	require.NoError(t, q.Export(t.Context(), gaugeMetrics(1)))
	require.NoError(t, q.Export(t.Context(), gaugeMetrics(2)))
	select {
	case <-receiver.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first export never reached Cloud")
	}
	// Shut down a little after the first export started, so that it reaches
	// its own bound, and the second export starts, before the drain
	// deadline.
	time.Sleep(bound / 3)

	start := time.Now()
	require.NoError(t, q.Shutdown(t.Context()))
	require.Less(t, time.Since(start), bound+150*time.Millisecond, "one bound for draining, the exports in flight and the release")
	select {
	case <-receiver.entered:
	default:
		t.Fatal("the second export never started before the drain deadline")
	}
	select {
	case <-q.drained:
	default:
		t.Fatal("the export in flight was not cancelled: the worker still waits on Cloud")
	}
}

// Stopping refreshes waits for a file operation in flight even when the
// Cloud budget is spent, within its own bound.
func TestStopCloudTokenRefreshWaitsDespiteSpentBudget(t *testing.T) {
	t.Parallel()
	sess := &daggerSession{
		sessionID:    "session",
		cloudRefresh: &cloudRefreshGate{},
		cloudBound:   cloudFlushBound{sessionID: "session", timeout: time.Millisecond, budget: &cloudShutdownBudget{}},
	}
	stopBudget := sess.startCloudShutdownBudget()
	defer stopBudget()
	time.Sleep(10 * time.Millisecond) // the budget is spent
	require.True(t, sess.cloudRefresh.enter())
	finished := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		sess.cloudRefresh.exit()
		close(finished)
	}()
	start := time.Now()
	sess.stopCloudTokenRefresh(t.Context())
	select {
	case <-finished:
	default:
		t.Fatal("stopped waiting before the file operation finished")
	}
	require.Less(t, time.Since(start), cloudRefreshFileOpWait)
}

// refusedURL is a Cloud URL whose port nothing listens on.
func refusedURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return "http://" + addr
}

// A session whose main client asked the engine to publish, with a Cloud URL
// the engine cannot reach, does not publish: nothing it or its containers
// emit goes to Cloud through it, and it does not ask a scale-out engine to
// publish either, so the client keeps forwarding the session's telemetry.
func TestUnreachableCloudURLLeavesTheClientForwarding(t *testing.T) {
	t.Parallel()
	sess, root := newCloudTestSession(t, &Server{}, &engine.ClientMetadata{
		CloudAuth:               basicCloudAuth("dag_test_token"),
		CloudURL:                refusedURL(t),
		CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine,
	})
	t.Cleanup(func() { require.NoError(t, sess.shutdownTelemetry(context.Background())) })

	require.False(t, sess.publishesToCloud())
	require.Nil(t, sess.cloudForwarder)
	require.Nil(t, sess.cloudMetrics)
	require.Equal(t, []sdkmetric.Exporter{root.metricExporter}, sess.postedMetricExporters(root.metricExporter))

	var params engineclient.Params
	sess.scaleOutTelemetryParams(root, &params)
	require.False(t, params.EngineCloudTelemetry)
	require.Empty(t, params.CloudURL)
	require.Nil(t, params.EngineTraceWithoutCloud)
}

// A Cloud URL's reachability is probed within cloudReachTimeout and then
// trusted for cloudReachOK when reached, cloudReachFailed when not.
func TestCloudReachabilityCache(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	probes := map[string]int{}
	unreachable := errors.New("unreachable")
	c := cloudReachability{
		now: func() time.Time { return now },
		probe: func(ctx context.Context, cloudURL string) error {
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.LessOrEqual(t, time.Until(deadline), cloudReachTimeout)
			probes[cloudURL]++
			if cloudURL == "http://down" {
				return unreachable
			}
			return nil
		},
	}
	ctx := context.Background()

	require.NoError(t, c.check(ctx, "http://up"))
	require.ErrorIs(t, c.check(ctx, "http://down"), unreachable)
	now = now.Add(cloudReachFailed - time.Second)
	require.NoError(t, c.check(ctx, "http://up"))
	require.ErrorIs(t, c.check(ctx, "http://down"), unreachable)
	require.Equal(t, map[string]int{"http://up": 1, "http://down": 1}, probes)

	now = now.Add(time.Second)
	require.ErrorIs(t, c.check(ctx, "http://down"), unreachable)
	require.NoError(t, c.check(ctx, "http://up"))
	require.Equal(t, map[string]int{"http://up": 1, "http://down": 2}, probes)

	now = time.Unix(0, 0).Add(cloudReachOK)
	require.NoError(t, c.check(ctx, "http://up"))
	require.Equal(t, 2, probes["http://up"])
}

// Concurrent checks of a URL without a current result share one probe, and
// all get its result: on a cold cache, and again once the result expired.
func TestCloudReachabilityCoalescesConcurrentChecks(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var (
			clockMu sync.Mutex
			clock   = time.Unix(0, 0)
		)
		var probes atomic.Int32
		var release chan struct{}
		unreachable := errors.New("unreachable")
		c := cloudReachability{
			now: func() time.Time {
				clockMu.Lock()
				defer clockMu.Unlock()
				return clock
			},
			probe: func(ctx context.Context, cloudURL string) error {
				probes.Add(1)
				<-release
				return unreachable
			},
		}

		for round := int32(1); round <= 2; round++ {
			release = make(chan struct{})
			errs := make(chan error, 8)
			for range 8 {
				go func() { errs <- c.check(context.Background(), "http://down") }()
			}
			// Every caller is now blocked, in the probe or waiting on it.
			synctest.Wait()
			probed := probes.Load()
			close(release)
			for range 8 {
				require.ErrorIs(t, <-errs, unreachable)
			}
			require.Equal(t, round, probed)

			clockMu.Lock()
			clock = clock.Add(cloudReachFailed)
			clockMu.Unlock()
		}
	})
}

// The main client's shutdown leaves the last Cloud flush to the engine unless
// the token may need a refresh first: that reads the client's credentials file
// through attachables that close with the client.
func TestCloudFlushCanOutliveClient(t *testing.T) {
	t.Parallel()
	now := time.Now()
	for _, tc := range []struct {
		name   string
		expiry time.Time
		want   bool
	}{
		{"token without expiry", time.Time{}, true},
		{"token valid for an hour", now.Add(time.Hour), true},
		{"token expiring within the margin", now.Add(cloudTokenRefreshMargin / 2), false},
		{"expired token", now.Add(-time.Minute), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := &daggerSession{}
			sess.setCloudTokenExpiry(tc.expiry)
			require.Equal(t, tc.want, sess.cloudFlushCanOutliveClient(now))
		})
	}

	// A refresh extends the expiry, so a later shutdown need not wait.
	sess := &daggerSession{}
	sess.setCloudTokenExpiry(now.Add(time.Second))
	require.False(t, sess.cloudFlushCanOutliveClient(now))
	sess.setCloudTokenExpiry(now.Add(time.Hour))
	require.True(t, sess.cloudFlushCanOutliveClient(now))
}

func TestShutdownCloudFlushWaitsOnlyWhenTheTokenMayNeedRefresh(t *testing.T) {
	t.Parallel()
	slowFlush := func() (*daggerSession, chan struct{}, chan struct{}) {
		release, done := make(chan struct{}), make(chan struct{})
		sess := &daggerSession{cloudFlushers: []func(context.Context){func(context.Context) {
			<-release
			close(done)
		}}}
		return sess, release, done
	}

	t.Run("fresh token: the client does not wait on Cloud", func(t *testing.T) {
		sess, release, done := slowFlush()
		sess.setCloudTokenExpiry(time.Now().Add(time.Hour))
		returned := make(chan struct{})
		go func() {
			sess.flushSessionCloudTelemetryForShutdown(t.Context())
			close(returned)
		}()
		select {
		case <-returned:
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown waited for Cloud")
		}
		close(release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the flush did not finish after the client was released")
		}
	})

	t.Run("token near expiry: the client waits for the flush", func(t *testing.T) {
		sess, release, done := slowFlush()
		sess.setCloudTokenExpiry(time.Now().Add(time.Second))
		returned := make(chan struct{})
		go func() {
			sess.flushSessionCloudTelemetryForShutdown(t.Context())
			close(returned)
		}()
		select {
		case <-returned:
			t.Fatal("shutdown returned before the flush finished")
		case <-time.After(100 * time.Millisecond):
		}
		close(release)
		<-done
		select {
		case <-returned:
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown did not return after the flush")
		}
	})
}
