package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/core"
	coreschema "github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/baggage"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestDetachedResultWriterCommitsExactResponse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resultPath := filepath.Join(dir, detachedResultFilename)
	writer, err := newDetachedResultWriter(resultPath)
	require.NoError(t, err)
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Connection", "close")
	writer.WriteHeader(http.StatusTeapot)
	body := []byte(`{"data":{"answer":"exact"}}`)
	_, err = writer.Write(body)
	require.NoError(t, err)
	commit := writer.commit()
	require.NoError(t, commit.err)
	require.True(t, commit.available)
	require.False(t, commit.discarded)
	require.Equal(t, http.StatusTeapot, commit.statusCode)
	require.Equal(t, map[string][]string{"Content-Type": {"application/json; charset=utf-8"}}, commit.header)
	stored, err := os.ReadFile(resultPath)
	require.NoError(t, err)
	require.Equal(t, body, stored)
	_, err = os.Stat(resultPath + ".tmp")
	require.ErrorIs(t, err, os.ErrNotExist)
	info, err := os.Stat(resultPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestDetachedResultWriterAbsorbsAndDiscardsOverflow(t *testing.T) {
	t.Parallel()
	resultPath := filepath.Join(t.TempDir(), detachedResultFilename)
	writer, err := newDetachedResultWriter(resultPath)
	require.NoError(t, err)
	chunk := []byte(strings.Repeat("x", 1<<20))
	for range maxDetachedQueryResultSize / len(chunk) {
		n, err := writer.Write(chunk)
		require.NoError(t, err)
		require.Equal(t, len(chunk), n)
	}
	n, err := writer.Write([]byte("overflow"))
	require.NoError(t, err)
	require.Equal(t, len("overflow"), n, "overflow bytes were not absorbed")
	commit := writer.commit()
	require.True(t, commit.discarded)
	require.False(t, commit.available)
	_, err = os.Stat(resultPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(resultPath + ".tmp")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestAtomicSessionFileIsInvisibleUntilRename(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), detachedRequestFilename)
	require.NoError(t, atomicWriteSessionFile(path, []byte(`{"query":"query Query { version }"}`), 0o600))
	_, err := os.Stat(path + ".tmp")
	require.ErrorIs(t, err, os.ErrNotExist)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestSessionResultStartupDiscardsStaleProcessDirectories(t *testing.T) {
	t.Parallel()
	workerRoot := t.TempDir()
	stale := filepath.Join(workerRoot, "session-results", "old-process", testSessionID, detachedResultFilename)
	require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0o700))
	require.NoError(t, os.WriteFile(stale, []byte("stale"), 0o600))
	srv := &Server{workerRootDir: workerRoot}
	require.NoError(t, srv.initializeSessionResultsRoot())
	_, err := os.Stat(stale)
	require.ErrorIs(t, err, os.ErrNotExist)
	info, err := os.Stat(srv.sessionResultsRoot)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestDetachedQueryContextRestoresIdentityTraceBaggageAndCancellation(t *testing.T) {
	t.Parallel()
	srv := &Server{}
	srv.shutdownCtx, srv.shutdownCancel = context.WithCancelCause(context.Background())
	sess := &daggerSession{sessionID: testSessionID, analytics: analytics.New(analytics.Config{DoNotTrack: true})}
	sess.closingCtx, sess.cancelClosing = context.WithCancelCause(context.Background())
	metadata := &engine.ClientMetadata{SessionID: testSessionID, ClientID: "creator"}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled, Remote: true,
	})
	member, err := baggage.NewMember("detached-test", "present")
	require.NoError(t, err)
	bag, err := baggage.New(member)
	require.NoError(t, err)
	q := &detachedQuery{
		creator:      &daggerClient{clientID: "creator", clientMetadata: metadata},
		traceContext: spanContext, baggage: bag,
	}

	ctx := srv.detachedQueryContext(sess, q)
	gotMetadata, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(t, err)
	require.Same(t, metadata, gotMetadata)
	require.Equal(t, spanContext, trace.SpanContextFromContext(ctx))
	require.Equal(t, "present", baggage.FromContext(ctx).Member("detached-test").Value())
	sess.cancelClosing(errSessionClosing)
	require.Eventually(t, func() bool { return ctx.Err() != nil }, time.Second, time.Millisecond)
}

func TestPrimaryQueryDisconnectBeforeStorageIsNotAccepted(t *testing.T) {
	t.Parallel()
	srv, sess, _ := newDetachedQuerySubmitTestSession(t)
	requestCtx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	body := &cancelBlockingBody{ctx: requestCtx, started: started}
	req := httptest.NewRequest(http.MethodPost, engine.SessionsEndpoint+"/"+testSessionID+"/queries/primary", nil).
		WithContext(detachedQueryTraceContext(requestCtx))
	req.Body = body
	req.SetPathValue("sessionID", testSessionID)

	errResult := make(chan error, 1)
	go func() {
		errResult <- srv.servePrimaryQuerySubmit(httptest.NewRecorder(), req, struct{}{})
	}()
	<-started
	cancel()

	var protocolErr sessionControlError
	require.ErrorAs(t, <-errResult, &protocolErr)
	require.Equal(t, engine.SessionErrorInvalidRequest, protocolErr.code)
	sess.queryMu.RLock()
	require.Nil(t, sess.primaryQuery)
	require.False(t, sess.primaryQueryClaimed)
	sess.queryMu.RUnlock()
	sess.dagqlMu.Lock()
	require.Zero(t, sess.dagqlInFlight)
	sess.dagqlMu.Unlock()
	_, err := os.Stat(filepath.Join(srv.sessionResultsRoot, testSessionID, detachedRequestFilename))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPrimaryQueryDisconnectAfterStorageCompletesIndependently(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachedQuerySubmitTestSession(t)
	prepareEngineOnlyDetachedQueryClient(t, srv, creator)
	hooks := &sessionTestHooks{Events: make(chan sessionTestEvent, 32)}
	barrier := make(chan struct{})
	hooks.setBarrier(sessionHookQueryBodyCommitted, barrier)
	srv.sessionTestHooks = hooks

	requestBody := json.RawMessage(`{"query":"query Query { version }"}`)
	requestCtx, cancel := context.WithCancel(t.Context())
	req := newDetachedQuerySubmitRequest(t, requestCtx, testAttachmentID, requestBody)
	recorder := httptest.NewRecorder()
	errResult := make(chan error, 1)
	go func() {
		errResult <- srv.servePrimaryQuerySubmit(recorder, req, struct{}{})
	}()
	waitForSessionHookCount(t, hooks.Events, sessionHookQueryBodyCommitted, 1)
	cancel()
	close(barrier)

	require.NoError(t, <-errResult)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	sess.queryMu.RLock()
	query := sess.primaryQuery
	sess.queryMu.RUnlock()
	require.NotNil(t, query)
	select {
	case <-query.done:
	case <-time.After(10 * time.Second):
		t.Fatal("stored detached query did not complete after submitter disconnect")
	}
	snapshot := query.snapshot()
	require.Equal(t, engine.SessionQueryStateSucceeded, snapshot.Status)
	require.True(t, snapshot.ResultAvailable)
	stored, err := os.ReadFile(query.requestPath)
	require.NoError(t, err)
	require.Equal(t, []byte(requestBody), stored)
}

func TestAcceptedEngineOnlyQuerySurvivesCreatorAttachmentClose(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachedQuerySubmitTestSession(t)
	prepareEngineOnlyDetachedQueryClient(t, srv, creator)
	hooks := &sessionTestHooks{Events: make(chan sessionTestEvent, 32)}
	barrier := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(barrier) }) }
	t.Cleanup(release)
	hooks.setBarrier(sessionHookGraphQLEntered, barrier)
	srv.sessionTestHooks = hooks

	recorder := httptest.NewRecorder()
	req := newDetachedQuerySubmitRequest(t, t.Context(), testAttachmentID,
		json.RawMessage(`{"query":"query Query { version }"}`))
	require.NoError(t, srv.servePrimaryQuerySubmit(recorder, req, struct{}{}))
	require.Equal(t, http.StatusAccepted, recorder.Code)
	waitForSessionHookCount(t, hooks.Events, sessionHookGraphQLEntered, 1)

	require.True(t, srv.detachAttachment(sess, testAttachmentID, 1))
	require.Nil(t, sess.currentAttachment)
	release()

	sess.queryMu.RLock()
	query := sess.primaryQuery
	sess.queryMu.RUnlock()
	require.NotNil(t, query)
	select {
	case <-query.done:
	case <-time.After(10 * time.Second):
		t.Fatal("accepted engine-only query did not complete after creator attachment close")
	}
	snapshot := query.snapshot()
	require.Equal(t, engine.SessionQueryStateSucceeded, snapshot.Status)
	require.True(t, snapshot.ResultAvailable)

	resultReq := httptest.NewRequest(http.MethodGet, engine.SessionsEndpoint+"/"+testSessionID+"/queries/primary/result", nil)
	resultReq.SetPathValue("sessionID", testSessionID)
	resultRecorder := httptest.NewRecorder()
	require.NoError(t, srv.servePrimaryQueryResult(resultRecorder, resultReq, struct{}{}))
	require.Equal(t, http.StatusOK, resultRecorder.Code)
	var result struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(resultRecorder.Body.Bytes(), &result))
	require.Equal(t, engine.FullVersion(), result.Data.Version)
}

func TestPrimaryQueryRequestSizeBoundary(t *testing.T) {
	base := engine.SubmitPrimaryQueryRequest{
		AttachmentID: testAttachmentID,
		Request:      json.RawMessage(`{"query":"query Query { version }"}`),
	}
	encoded, err := json.Marshal(base)
	require.NoError(t, err)
	require.Less(t, len(encoded), maxDetachedQueryRequestSize)
	base.Presentation.Kind = strings.Repeat("x", maxDetachedQueryRequestSize-len(encoded))
	encoded, err = json.Marshal(base)
	require.NoError(t, err)
	require.Len(t, encoded, maxDetachedQueryRequestSize)

	t.Run("exact limit accepted", func(t *testing.T) {
		srv, sess, _ := newDetachedQuerySubmitTestSession(t)
		req := httptest.NewRequest(http.MethodPost, engine.SessionsEndpoint+"/"+testSessionID+"/queries/primary", bytes.NewReader(encoded)).
			WithContext(detachedQueryTraceContext(t.Context()))
		req.SetPathValue("sessionID", testSessionID)
		recorder := httptest.NewRecorder()
		require.NoError(t, srv.servePrimaryQuerySubmit(recorder, req, struct{}{}))
		require.Equal(t, http.StatusAccepted, recorder.Code)
		sess.queryMu.RLock()
		query := sess.primaryQuery
		sess.queryMu.RUnlock()
		require.NotNil(t, query)
		<-query.done
	})

	t.Run("one byte over rejected", func(t *testing.T) {
		srv, sess, _ := newDetachedQuerySubmitTestSession(t)
		oversized := append(append([]byte(nil), encoded...), ' ')
		req := httptest.NewRequest(http.MethodPost, engine.SessionsEndpoint+"/"+testSessionID+"/queries/primary", bytes.NewReader(oversized)).
			WithContext(detachedQueryTraceContext(t.Context()))
		req.SetPathValue("sessionID", testSessionID)
		var protocolErr sessionControlError
		err := srv.servePrimaryQuerySubmit(httptest.NewRecorder(), req, struct{}{})
		require.ErrorAs(t, err, &protocolErr)
		require.Equal(t, http.StatusRequestEntityTooLarge, protocolErr.status)
		require.Equal(t, engine.SessionErrorRequestTooLarge, protocolErr.code)
		sess.queryMu.RLock()
		require.Nil(t, sess.primaryQuery)
		sess.queryMu.RUnlock()
	})
}

func TestPrimaryQuerySubmissionConflictsDoNotClaimQuery(t *testing.T) {
	tests := []struct {
		name         string
		body         []byte
		prepare      func(*daggerSession)
		wantStatus   int
		wantCode     string
		queryPresent bool
	}{
		{name: "invalid envelope", body: []byte(`{`), wantStatus: http.StatusBadRequest, wantCode: engine.SessionErrorInvalidRequest},
		{name: "attachment mismatch", body: mustDetachedQueryEnvelope(t, "att_bbbbbbbbbbbbbbbbbbbbbbbbbb"), wantStatus: http.StatusConflict, wantCode: engine.SessionErrorAttachmentMismatch},
		{
			name: "primary query exists", body: mustDetachedQueryEnvelope(t, testAttachmentID),
			prepare: func(sess *daggerSession) {
				sess.primaryQuery = &detachedQuery{id: "qry_aaaaaaaaaaaaaaaaaaaaaaaaaa"}
			},
			wantStatus: http.StatusConflict, wantCode: engine.SessionErrorPrimaryQueryExists, queryPresent: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv, sess, _ := newDetachedQuerySubmitTestSession(t)
			if test.prepare != nil {
				test.prepare(sess)
			}
			req := httptest.NewRequest(http.MethodPost, engine.SessionsEndpoint+"/"+testSessionID+"/queries/primary", bytes.NewReader(test.body)).
				WithContext(detachedQueryTraceContext(t.Context()))
			req.SetPathValue("sessionID", testSessionID)
			var protocolErr sessionControlError
			err := srv.servePrimaryQuerySubmit(httptest.NewRecorder(), req, struct{}{})
			require.ErrorAs(t, err, &protocolErr)
			require.Equal(t, test.wantStatus, protocolErr.status)
			require.Equal(t, test.wantCode, protocolErr.code)
			sess.queryMu.RLock()
			require.Equal(t, test.queryPresent, sess.primaryQuery != nil)
			require.False(t, sess.primaryQueryClaimed)
			sess.queryMu.RUnlock()
			sess.dagqlMu.Lock()
			require.Zero(t, sess.dagqlInFlight)
			sess.dagqlMu.Unlock()
		})
	}
}

func TestDetachedQueryPublishesCanceledState(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachedQuerySubmitTestSession(t)
	dir, err := srv.sessionResultDir(testSessionID)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	requestPath := filepath.Join(dir, detachedRequestFilename)
	require.NoError(t, os.WriteFile(requestPath, []byte(`{"query":"query Query { version }"}`), 0o600))
	query := &detachedQuery{
		id: "qry_aaaaaaaaaaaaaaaaaaaaaaaaaa", sessionID: testSessionID, creator: creator,
		state: engine.SessionQueryStateRunning, requestPath: requestPath,
		resultPath: filepath.Join(dir, detachedResultFilename), metadataPath: filepath.Join(dir, detachedMetadataFilename),
		traceContext: trace.SpanContextFromContext(detachedQueryTraceContext(t.Context())), done: make(chan struct{}),
	}
	sess.dagqlMu.Lock()
	sess.dagqlInFlight = 1
	sess.dagqlMu.Unlock()
	sess.cancelClosing(errSessionClosing)

	srv.runDetachedQuery(sess, query)
	require.Equal(t, engine.SessionQueryStateCanceled, query.snapshot().Status)
	select {
	case <-query.done:
	default:
		t.Fatal("canceled query did not publish completion")
	}
}

func TestPrimaryQueryResultStateErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		state      engine.SessionQueryState
		discarded  bool
		wantStatus int
		wantCode   string
	}{
		{name: "running", state: engine.SessionQueryStateRunning, wantStatus: http.StatusConflict, wantCode: engine.SessionErrorQueryRunning},
		{name: "discarded", state: engine.SessionQueryStateResultDiscarded, discarded: true, wantStatus: http.StatusGone, wantCode: engine.SessionErrorResultDiscarded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv, sess, _ := newDetachableLifecycleTestSession(t, 0)
			sess.primaryQuery = &detachedQuery{
				id: "qry_aaaaaaaaaaaaaaaaaaaaaaaaaa", state: test.state,
				result: engine.SessionResultMetadata{Discarded: test.discarded},
			}
			req := httptest.NewRequest(http.MethodGet, engine.SessionsEndpoint+"/"+testSessionID+"/queries/primary/result", nil)
			req.SetPathValue("sessionID", testSessionID)
			var protocolErr sessionControlError
			err := srv.servePrimaryQueryResult(httptest.NewRecorder(), req, struct{}{})
			require.ErrorAs(t, err, &protocolErr)
			require.Equal(t, test.wantStatus, protocolErr.status)
			require.Equal(t, test.wantCode, protocolErr.code)
		})
	}
}

func TestPrimaryQueryResultPreservesSavedStatusHeaderAndBody(t *testing.T) {
	t.Parallel()
	srv, sess, _ := newDetachableLifecycleTestSession(t, 0)
	resultPath := filepath.Join(t.TempDir(), detachedResultFilename)
	body := []byte(`{"errors":[{"message":"saved failure"}]}`)
	require.NoError(t, os.WriteFile(resultPath, body, 0o600))
	q := &detachedQuery{
		id: "qry_aaaaaaaaaaaaaaaaaaaaaaaaaa", state: engine.SessionQueryStateFailed,
		resultPath: resultPath, resultReady: true,
		result: engine.SessionResultMetadata{
			StatusCode: http.StatusUnprocessableEntity,
			Header:     map[string][]string{"Content-Type": {"application/json"}},
		},
	}
	sess.primaryQuery = q
	req := httptest.NewRequest(http.MethodGet, engine.SessionsEndpoint+"/"+testSessionID+"/queries/primary/result", nil)
	req.SetPathValue("sessionID", testSessionID)
	recorder := httptest.NewRecorder()
	require.NoError(t, srv.servePrimaryQueryResult(recorder, req, struct{}{}))
	require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.Equal(t, body, recorder.Body.Bytes())
}

func TestGraphQLSavedErrorsDetermineFailedOutcome(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "result.body")
	require.NoError(t, os.WriteFile(path, []byte(`{"data":null,"errors":[{"message":"bad query"}]}`), 0o600))
	require.True(t, graphqlResultHasErrors(path))
	require.NoError(t, os.WriteFile(path, []byte(`{"data":{"value":1}}`), 0o600))
	require.False(t, graphqlResultHasErrors(path))
}

func TestSessionResultMetadataRoundTrip(t *testing.T) {
	t.Parallel()
	metadata := engine.SessionResultMetadata{
		StatusCode:  http.StatusCreated,
		Header:      map[string][]string{"Content-Type": {"application/json"}},
		CompletedAt: time.Now().UTC().Truncate(time.Second),
	}
	encoded, err := json.Marshal(metadata)
	require.NoError(t, err)
	var decoded engine.SessionResultMetadata
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, metadata, decoded)
}

func newDetachedQuerySubmitTestSession(t *testing.T) (*Server, *daggerSession, *daggerClient) {
	t.Helper()
	srv, sess, creator := newDetachableLifecycleTestSession(t, 0)
	srv.clientDBs = clientdb.NewDBs(t.TempDir())
	srv.telemetryPubSub = NewPubSub(srv)
	sess.telemetryPubSub = srv.telemetryPubSub
	creator.clientMetadata = &engine.ClientMetadata{
		SessionID: testSessionID, ClientID: creator.clientID, ClientSecretToken: creator.secretToken,
		DetachableSession: true, AttachmentID: testAttachmentID,
	}
	sess.currentAttachment = &sessionAttachment{
		ID: testAttachmentID, Generation: 1, ClientID: creator.clientID, Ready: true,
	}
	return srv, sess, creator
}

func prepareEngineOnlyDetachedQueryClient(t *testing.T, srv *Server, creator *daggerClient) {
	t.Helper()
	ctx := dagql.ContextWithCache(t.Context(), srv.engineCache)
	ctx = engine.ContextWithClientMetadata(ctx, creator.clientMetadata)
	base, err := coreschema.NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	root := core.NewRoot(srv)
	coreMod := base.CoreMod("")
	creator.dagqlRoot = root
	creator.defaultDeps = core.NewSchemaBuilder(root, []core.Mod{coreMod})
	creator.servedMods = core.NewSchemaBuilder(root, []core.Mod{coreMod})
	creator.pendingWorkspaceLoad = true
	creator.workspaceLoaded = true
	creator.tracerProvider = sdktrace.NewTracerProvider()
	creator.loggerProvider = sdklog.NewLoggerProvider()
	creator.meterProvider = sdkmetric.NewMeterProvider()
	t.Cleanup(func() {
		require.NoError(t, creator.tracerProvider.Shutdown(context.Background()))
		require.NoError(t, creator.loggerProvider.Shutdown(context.Background()))
		require.NoError(t, creator.meterProvider.Shutdown(context.Background()))
	})
}

func newDetachedQuerySubmitRequest(t *testing.T, ctx context.Context, attachmentID string, request json.RawMessage) *http.Request {
	t.Helper()
	payload := engine.SubmitPrimaryQueryRequest{AttachmentID: attachmentID, Request: request}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, engine.SessionsEndpoint+"/"+testSessionID+"/queries/primary", bytes.NewReader(body)).
		WithContext(detachedQueryTraceContext(ctx))
	req.SetPathValue("sessionID", testSessionID)
	return req
}

func mustDetachedQueryEnvelope(t *testing.T, attachmentID string) []byte {
	t.Helper()
	body, err := json.Marshal(engine.SubmitPrimaryQueryRequest{
		AttachmentID: attachmentID,
		Request:      json.RawMessage(`{"query":"query Query { version }"}`),
	})
	require.NoError(t, err)
	return body
}

func detachedQueryTraceContext(ctx context.Context) context.Context {
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled, Remote: true,
	})
	return trace.ContextWithRemoteSpanContext(ctx, spanContext)
}

type cancelBlockingBody struct {
	ctx     context.Context
	started chan struct{}
}

func (body *cancelBlockingBody) Read([]byte) (int, error) {
	close(body.started)
	<-body.ctx.Done()
	return 0, context.Cause(body.ctx)
}

func (*cancelBlockingBody) Close() error { return nil }

var _ io.ReadCloser = (*cancelBlockingBody)(nil)
