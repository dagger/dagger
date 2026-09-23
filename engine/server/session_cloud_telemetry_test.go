package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
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
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
)

// cloudReceiver is an OTLP/HTTP receiver standing in for Dagger Cloud. It
// records what arrives and the Authorization header of every request. Until
// release is closed, when hold is set, it reads each request and then waits.
type cloudReceiver struct {
	*httptest.Server
	hold    bool
	release chan struct{}

	mu          sync.Mutex
	auths       []string
	spanNames   []string
	logBodies   []string
	metricNames []string
}

func newCloudReceiver(t *testing.T, hold bool) *cloudReceiver {
	r := &cloudReceiver{hold: hold, release: make(chan struct{})}
	r.Server = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(func() {
		close(r.release)
		r.Server.Close()
	})
	return r
}

func (r *cloudReceiver) serve(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.hold {
		select {
		case <-r.release:
		case <-req.Context().Done():
		}
		return
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
	root.spanExporter = originSpanExporter{origin: root.clientID, next: sess.spanExporter}
	root.logExporter = originLogExporter{origin: root.clientID, next: sess.logExporter}
	srv.initializeClientMetrics(root)
	return sess, root
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
	require.NotNil(t, sess.cloudSpans)
	require.Equal(t, 5, sess.telemetryDebug.ConfiguredSpanProcessors)
	require.Equal(t, 4, sess.telemetryDebug.ConfiguredLogProcessors)

	emitCloudTestTelemetry(t, sess, root)
	sess.flushSessionCloudTelemetry(ctx)
	require.NoError(t, sess.shutdownTelemetry(ctx))

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
			require.Nil(t, sess.cloudSpans)
			require.Nil(t, sess.cloudLogs)
			require.Nil(t, sess.cloudMetrics)
			require.Equal(t, 4, sess.telemetryDebug.ConfiguredSpanProcessors)

			emitCloudTestTelemetry(t, sess, root)
			sess.flushSessionCloudTelemetry(ctx)
			require.NoError(t, sess.shutdownTelemetry(ctx))
			auths, _, _, _ := receiver.snapshot()
			require.Empty(t, auths)
		})
	}
}

// Against a Cloud that accepts requests and never answers, the main client's
// whole shutdown (the Cloud flush, then the providers' and every client's
// metric flush) waits on Cloud for one bound in total, not one per wait, and
// the final span still reaches the client's stream. The session's teardown
// afterwards is bounded as well.
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

	// serveShutdown's Cloud steps for the main client, in order.
	start := time.Now()
	stopBudget := sess.startCloudShutdownBudget()
	sess.flushSessionCloudTelemetry(ctx)
	spanCtx := engine.ContextWithClientMetadata(ctx, root.clientMetadata)
	_, final := sess.tracerProvider.Tracer("test").Start(spanCtx, "final-span")
	final.End()
	require.NoError(t, sess.FlushTelemetry(ctx, "client shutdown"))
	stopBudget()
	elapsed := time.Since(start)
	require.Less(t, elapsed, bound+250*time.Millisecond, "one bound for the whole shutdown request, not one per wait")

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
	require.Less(t, time.Since(start), 4*bound+time.Second, "teardown waits one bound per exporter at most")
}

// A publishing session asks its scale-out engine to publish too, with its
// main client's Cloud URL and credentials path. If the remote does not, the
// client uses EngineTrace and EngineLogs, which reach Cloud through the
// session's processors; the WithoutCloud ones do not. A session that does
// not publish does not ask.
func TestScaleOutTelemetryFollowsTheSession(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	receiver := newCloudReceiver(t, false)
	sess, root := newCloudTestSession(t, &Server{}, &engine.ClientMetadata{
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
	require.Equal(t, root.spanExporter, params.EngineTraceWithoutCloud)
	require.Equal(t, root.logExporter, params.EngineLogsWithoutCloud)

	remote := tracetest.SpanStub{
		Name: "remote-span",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
		}),
		StartTime: time.Now(),
		EndTime:   time.Now(),
	}
	require.NoError(t, params.EngineTraceWithoutCloud.ExportSpans(ctx, []sdktrace.ReadOnlySpan{
		tracetest.SpanStub{Name: "not-to-cloud", SpanContext: remote.SpanContext, StartTime: remote.StartTime, EndTime: remote.EndTime}.Snapshot(),
	}))
	require.NoError(t, params.EngineTrace.ExportSpans(ctx, []sdktrace.ReadOnlySpan{remote.Snapshot()}))
	var rec sdklog.Record
	rec.SetBody(otellog.StringValue("remote-log"))
	require.NoError(t, params.EngineLogs.Export(ctx, []sdklog.Record{rec}))
	sess.flushSessionCloudTelemetry(ctx)
	require.NoError(t, sess.shutdownTelemetry(ctx))

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
// and, when the session publishes, Cloud once: posted telemetry never passes
// the session's providers, where the Cloud processors sit.
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

// Once the main client's shutdown begins, or its attachables are gone, a
// token refresh fails at once instead of reaching for them: that wait
// ignores deadlines and held the client's /shutdown response.
func TestCloudTokenRefreshFailsOnceClosing(t *testing.T) {
	t.Parallel()
	srv := &Server{}
	sess, root := newCloudTestSession(t, srv, &engine.ClientMetadata{CloudAuth: basicCloudAuth("dag_test_token")})
	root.metadataSealed = true
	sess.state.Store(sessionStateInitialized)
	srv.daggerSessions = map[string]*daggerSession{sess.sessionID: sess}
	t.Cleanup(func() { require.NoError(t, sess.shutdownTelemetry(context.Background())) })

	sess.cloudRefresh = &cloudRefreshGate{}
	sess.stopCloudTokenRefresh(t.Context())
	start := time.Now()
	_, err := srv.refreshSessionCloudToken(t.Context(), sess, "/credentials.json")
	require.ErrorIs(t, err, errCloudRefreshSessionClosing, "no refresh once the main client's shutdown began")
	require.Less(t, time.Since(start), time.Second)

	sess.cloudRefresh = &cloudRefreshGate{}
	sess.attachables = newSessionAttachableManager()
	_, err = srv.refreshSessionCloudToken(t.Context(), sess, "/credentials.json")
	require.ErrorIs(t, err, errCloudRefreshSessionClosing, "no attachables registered for the main client")

	sess.attachables = nil
	closing, cancelClosing := context.WithCancelCause(context.Background())
	sess.closingCtx = closing
	cancelClosing(errors.New("closing"))
	start = time.Now()
	_, err = srv.refreshSessionCloudToken(t.Context(), sess, "/credentials.json")
	require.ErrorIs(t, err, errCloudRefreshSessionClosing)
	require.Less(t, time.Since(start), time.Second)
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
