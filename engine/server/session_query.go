package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

const (
	maxDetachedQueryRequestSize = 16 << 20
	maxDetachedQueryResultSize  = 64 << 20
	detachedTelemetryFlushLimit = 10 * time.Second
)

const (
	detachedRequestFilename  = "request.json"
	detachedResultFilename   = "result.body"
	detachedMetadataFilename = "result.json"
)

type detachedQuery struct {
	mu sync.RWMutex

	id                string
	sessionID         string
	creator           *daggerClient
	presentation      engine.QueryPresentation
	state             engine.SessionQueryState
	createdAt         time.Time
	startedAt         time.Time
	completedAt       time.Time
	result            engine.SessionResultMetadata
	resultReady       bool
	errorMessage      string
	telemetry         engine.SessionTelemetryBound
	telemetryBoundSet bool

	requestPath  string
	resultPath   string
	metadataPath string
	traceContext trace.SpanContext
	baggage      baggage.Baggage
	done         chan struct{}
	doneOnce     sync.Once
}

func (q *detachedQuery) snapshot() engine.SessionQuery {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return engine.SessionQuery{
		ID: q.id, Status: q.state, CreatedAt: q.createdAt,
		StartedAt: q.startedAt, CompletedAt: q.completedAt,
		ResultAvailable: q.resultReady && !q.result.Discarded,
		Presentation:    q.presentation, Error: q.errorMessage,
		Telemetry: q.telemetry,
	}
}

func (srv *Server) servePrimaryQuerySubmit(w http.ResponseWriter, r *http.Request, _ struct{}) error {
	sessionID := r.PathValue("sessionID")
	if err := engine.ValidateSessionID(sessionID); err != nil {
		return malformedSessionIDError("session ID", sessionID, err)
	}
	sess, ok := srv.lookupDetachableSession(sessionID)
	if !ok {
		return sessionNotFoundError(sessionID)
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxDetachedQueryRequestSize+1))
	if err != nil {
		return controlError(http.StatusBadRequest, engine.SessionErrorInvalidRequest,
			fmt.Errorf("read primary query request: %w", err))
	}
	if len(body) > maxDetachedQueryRequestSize {
		return controlError(http.StatusRequestEntityTooLarge, engine.SessionErrorRequestTooLarge,
			fmt.Errorf("primary query request exceeds %d bytes", maxDetachedQueryRequestSize))
	}
	var submit engine.SubmitPrimaryQueryRequest
	if err := json.Unmarshal(body, &submit); err != nil {
		return controlError(http.StatusBadRequest, engine.SessionErrorInvalidRequest,
			fmt.Errorf("decode primary query request: %w", err))
	}
	if err := engine.ValidateAttachmentID(submit.AttachmentID); err != nil {
		return malformedSessionIDError("attachment ID", submit.AttachmentID, err)
	}
	if len(submit.Request) == 0 || !json.Valid(submit.Request) {
		return controlError(http.StatusBadRequest, engine.SessionErrorInvalidRequest,
			errors.New("primary query GraphQL request must be valid JSON"))
	}
	spanContext := trace.SpanContextFromContext(r.Context())
	if !spanContext.IsValid() {
		return controlError(http.StatusBadRequest, engine.SessionErrorInvalidRequest,
			errors.New("detached query requires a valid trace context"))
	}

	// Attachment ownership, terminal transition, the single-query claim, and
	// the outer DagQL drain reservation are one acceptance decision. Once this
	// block succeeds, teardown waits through storage, execution, result commit,
	// telemetry flush, and final status publication.
	sess.attachmentMu.Lock()
	if sess.terminating.Load() {
		sess.attachmentMu.Unlock()
		return controlError(http.StatusConflict, engine.SessionErrorSessionTerminating,
			fmt.Errorf("session %s is terminating", sessionID))
	}
	attachment := sess.currentAttachment
	if attachment == nil || attachment.ID != submit.AttachmentID {
		sess.attachmentMu.Unlock()
		return controlError(http.StatusConflict, engine.SessionErrorAttachmentMismatch,
			fmt.Errorf("attachment %s does not own session %s", submit.AttachmentID, sessionID))
	}
	sess.queryMu.Lock()
	if sess.primaryQuery != nil || sess.primaryQueryClaimed {
		sess.queryMu.Unlock()
		sess.attachmentMu.Unlock()
		return controlError(http.StatusConflict, engine.SessionErrorPrimaryQueryExists,
			fmt.Errorf("session %s already has a primary query", sessionID))
	}
	sess.primaryQueryClaimed = true
	sess.queryMu.Unlock()
	sess.dagqlMu.Lock()
	if sess.dagqlClosing {
		sess.dagqlMu.Unlock()
		sess.queryMu.Lock()
		sess.primaryQueryClaimed = false
		sess.queryMu.Unlock()
		sess.attachmentMu.Unlock()
		return controlError(http.StatusConflict, engine.SessionErrorSessionTerminating,
			fmt.Errorf("session %s is closing", sessionID))
	}
	sess.dagqlInFlight++
	sess.dagqlMu.Unlock()
	sess.attachmentMu.Unlock()

	accepted := false
	defer func() {
		if accepted {
			return
		}
		sess.queryMu.Lock()
		sess.primaryQueryClaimed = false
		sess.queryMu.Unlock()
		srv.releaseDetachedQueryDrain(sess)
	}()

	queryID, err := engine.GenerateQueryID()
	if err != nil {
		return fmt.Errorf("generate primary query ID: %w", err)
	}
	dir, err := srv.sessionResultDir(sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create primary query directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure primary query directory: %w", err)
	}
	requestPath := filepath.Join(dir, detachedRequestFilename)
	if err := atomicWriteSessionFile(requestPath, submit.Request, 0o600); err != nil {
		return fmt.Errorf("store primary query request: %w", err)
	}
	srv.hitSessionHook(sessionTestEvent{Name: sessionHookQueryBodyCommitted, SessionID: sessionID})

	now := srv.nowTime()
	q := &detachedQuery{
		id: queryID, sessionID: sessionID, creator: sessCreatorClient(sess),
		presentation: submit.Presentation, state: engine.SessionQueryStateRunning,
		createdAt: now, requestPath: requestPath,
		resultPath:   filepath.Join(dir, detachedResultFilename),
		metadataPath: filepath.Join(dir, detachedMetadataFilename),
		traceContext: spanContext, baggage: baggage.FromContext(r.Context()),
		done: make(chan struct{}),
	}
	if q.creator == nil {
		return errors.New("detachable session creator client is unavailable")
	}
	sess.queryMu.Lock()
	sess.primaryQuery = q
	sess.primaryQueryClaimed = false
	sess.queryMu.Unlock()
	accepted = true
	go srv.runDetachedQuery(sess, q)

	return writeSessionJSON(w, http.StatusAccepted, engine.SubmitPrimaryQueryResponse{
		SessionID: sessionID, QueryID: queryID, Status: engine.SessionQueryStateRunning,
	})
}

func sessCreatorClient(sess *daggerSession) *daggerClient {
	sess.clientMu.RLock()
	defer sess.clientMu.RUnlock()
	return sess.clients[sess.creatorClientID]
}

func (srv *Server) runDetachedQuery(sess *daggerSession, q *detachedQuery) {
	defer srv.releaseDetachedQueryDrain(sess)
	srv.hitSessionHook(sessionTestEvent{Name: sessionHookQueryGoroutineStarted, SessionID: sess.sessionID})

	q.mu.Lock()
	q.startedAt = srv.nowTime()
	q.mu.Unlock()

	ctx := srv.detachedQueryContext(sess, q)
	req, err := q.newHTTPRequest(ctx)
	var writer *detachedResultWriter
	if err == nil {
		writer, err = newDetachedResultWriter(q.resultPath)
	}
	if err == nil {
		err = srv.executeGraphQL(ctx, q.creator, writer, req)
		if err != nil {
			writeDetachedExecutionError(writer, err)
		}
	}

	var committed detachedResultCommit
	if writer != nil {
		committed = writer.commit()
		if committed.err != nil {
			err = errors.Join(err, committed.err)
		}
		if committed.available {
			srv.hitSessionHook(sessionTestEvent{Name: sessionHookResultBodyCommitted, SessionID: sess.sessionID})
		}
	}

	completedAt := srv.nowTime()
	state := engine.SessionQueryStateSucceeded
	switch {
	case committed.discarded:
		state = engine.SessionQueryStateResultDiscarded
	case context.Cause(ctx) != nil:
		state = engine.SessionQueryStateCanceled
	case err != nil || committed.statusCode >= http.StatusBadRequest || graphqlResultHasErrors(q.resultPath):
		state = engine.SessionQueryStateFailed
	}

	metadata := engine.SessionResultMetadata{
		StatusCode:  committed.statusCode,
		Header:      committed.header,
		CompletedAt: completedAt,
		Discarded:   committed.discarded,
	}
	if err != nil {
		metadata.Error = err.Error()
	}
	metadataBytes, metadataErr := json.Marshal(metadata)
	if metadataErr == nil {
		metadataErr = atomicWriteSessionFile(q.metadataPath, metadataBytes, 0o600)
	}
	if metadataErr != nil {
		err = errors.Join(err, fmt.Errorf("store primary query result metadata: %w", metadataErr))
		state = engine.SessionQueryStateFailed
		metadata.Error = err.Error()
	}

	flushCtx, cancel := context.WithTimeout(context.Background(), detachedTelemetryFlushLimit)
	flushErr := q.creator.FlushTelemetry(flushCtx)
	cancel()
	srv.hitSessionHook(sessionTestEvent{Name: sessionHookCreatorTelemetryFlush, SessionID: sess.sessionID, ClientID: q.creator.clientID})
	if flushErr != nil {
		err = errors.Join(err, fmt.Errorf("flush creator telemetry: %w", flushErr))
	}
	telemetryBound, boundErr := srv.creatorTelemetryBound(context.Background(), q.creator)
	q.mu.Lock()
	q.telemetry = telemetryBound
	q.telemetryBoundSet = boundErr == nil
	q.mu.Unlock()
	srv.hitSessionHook(sessionTestEvent{Name: sessionHookCompletionCursorCapture, SessionID: sess.sessionID, ClientID: q.creator.clientID})
	if boundErr != nil {
		err = errors.Join(err, fmt.Errorf("capture creator telemetry cursors: %w", boundErr))
	}

	q.mu.Lock()
	q.state = state
	q.completedAt = completedAt
	q.result = metadata
	q.resultReady = committed.available && metadataErr == nil
	if err != nil {
		q.errorMessage = err.Error()
	}
	q.mu.Unlock()
	srv.hitSessionHook(sessionTestEvent{Name: sessionHookQueryStatusCompleted, SessionID: sess.sessionID})
	q.doneOnce.Do(func() { close(q.done) })
}

func (srv *Server) creatorTelemetryBound(ctx context.Context, creator *daggerClient) (engine.SessionTelemetryBound, error) {
	db, err := creator.TelemetryDB(ctx)
	if err != nil {
		return engine.SessionTelemetryBound{}, err
	}
	defer db.Close()
	last := db.LastIDs()
	return engine.SessionTelemetryBound{Traces: last.Spans, Logs: last.Logs, Metrics: last.Metrics}, nil
}

func (srv *Server) releaseDetachedQueryDrain(sess *daggerSession) {
	sess.dagqlMu.Lock()
	sess.dagqlInFlight--
	if sess.dagqlInFlight == 0 {
		sess.dagqlCond.Broadcast()
	}
	sess.dagqlMu.Unlock()
}

func (srv *Server) detachedQueryContext(sess *daggerSession, q *detachedQuery) context.Context {
	ctx := srv.withShutdownCancel(sess.closingCtx)
	ctx = engine.ContextWithClientMetadata(ctx, q.creator.clientMetadata)
	ctx = analytics.WithContext(ctx, sess.analytics)
	ctx = trace.ContextWithRemoteSpanContext(ctx, q.traceContext)
	ctx = baggage.ContextWithBaggage(ctx, q.baggage)
	ctx = dagql.ContextWithCache(ctx, srv.engineCache)
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithField("trace", q.traceContext.TraceID().String()).
		WithField("span", q.traceContext.SpanID().String()).
		WithField("client_id", q.creator.clientID).
		WithField("session_id", sess.sessionID))
	ctx = slog.WithLogger(ctx, slog.FromContext(ctx).With(
		"trace", q.traceContext.TraceID().String(),
		"span", q.traceContext.SpanID().String(),
		"client_id", q.creator.clientID,
		"session_id", sess.sessionID,
	))
	return ctx
}

func (q *detachedQuery) newHTTPRequest(ctx context.Context) (*http.Request, error) {
	body, err := os.ReadFile(q.requestPath)
	if err != nil {
		return nil, fmt.Errorf("read stored primary query: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, engine.QueryEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("reconstruct primary query request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (srv *Server) servePrimaryQueryInspect(w http.ResponseWriter, r *http.Request, _ struct{}) error {
	q, err := srv.primaryQueryFromRequest(r)
	if err != nil {
		return err
	}
	return writeSessionJSON(w, http.StatusOK, q.snapshot())
}

func (srv *Server) servePrimaryQueryResult(w http.ResponseWriter, r *http.Request, _ struct{}) error {
	q, err := srv.primaryQueryFromRequest(r)
	if err != nil {
		return err
	}
	q.mu.RLock()
	state := q.state
	metadata := q.result
	resultReady := q.resultReady
	resultPath := q.resultPath
	q.mu.RUnlock()
	if state == engine.SessionQueryStateRunning {
		return controlError(http.StatusConflict, engine.SessionErrorQueryRunning,
			fmt.Errorf("primary query %s is still running", q.id))
	}
	if state == engine.SessionQueryStateResultDiscarded || metadata.Discarded {
		return controlError(http.StatusGone, engine.SessionErrorResultDiscarded,
			fmt.Errorf("primary query %s result exceeded the saved-result limit", q.id))
	}
	if !resultReady {
		return controlError(http.StatusNotFound, engine.SessionErrorPrimaryQueryNotFound,
			fmt.Errorf("primary query %s has no saved result", q.id))
	}
	file, err := os.Open(resultPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return controlError(http.StatusNotFound, engine.SessionErrorPrimaryQueryNotFound,
				fmt.Errorf("primary query %s result not found", q.id))
		}
		return fmt.Errorf("open saved primary query result: %w", err)
	}
	defer file.Close()
	if values := metadata.Header["Content-Type"]; len(values) > 0 {
		w.Header()["Content-Type"] = append([]string(nil), values...)
	}
	w.WriteHeader(metadata.StatusCode)
	if _, err := io.Copy(w, file); err != nil {
		return fmt.Errorf("serve saved primary query result: %w", err)
	}
	return nil
}

func (srv *Server) primaryQueryFromRequest(r *http.Request) (*detachedQuery, error) {
	sessionID := r.PathValue("sessionID")
	if err := engine.ValidateSessionID(sessionID); err != nil {
		return nil, malformedSessionIDError("session ID", sessionID, err)
	}
	sess, ok := srv.lookupDetachableSession(sessionID)
	if !ok {
		return nil, sessionNotFoundError(sessionID)
	}
	sess.queryMu.RLock()
	q := sess.primaryQuery
	sess.queryMu.RUnlock()
	if q == nil {
		return nil, controlError(http.StatusNotFound, engine.SessionErrorPrimaryQueryNotFound,
			fmt.Errorf("session %s has no primary query", sessionID))
	}
	return q, nil
}

type detachedResultWriter struct {
	header     http.Header
	file       *os.File
	tmpPath    string
	finalPath  string
	statusCode int
	written    int64
	discarded  bool
	writeErr   error
}

type detachedResultCommit struct {
	statusCode int
	header     map[string][]string
	available  bool
	discarded  bool
	err        error
}

func newDetachedResultWriter(finalPath string) (*detachedResultWriter, error) {
	tmpPath := finalPath + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create saved primary query result: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(tmpPath)
		return nil, fmt.Errorf("secure saved primary query result: %w", err)
	}
	return &detachedResultWriter{
		header: make(http.Header), file: file, tmpPath: tmpPath, finalPath: finalPath,
	}, nil
}

func (writer *detachedResultWriter) Header() http.Header {
	return writer.header
}

func (writer *detachedResultWriter) WriteHeader(statusCode int) {
	if writer.statusCode == 0 {
		writer.statusCode = statusCode
	}
}

func (writer *detachedResultWriter) Write(data []byte) (int, error) {
	if writer.statusCode == 0 {
		writer.statusCode = http.StatusOK
	}
	originalLen := len(data)
	remaining := int64(maxDetachedQueryResultSize) - writer.written
	if remaining < int64(len(data)) {
		writer.discarded = true
		if remaining < 0 {
			remaining = 0
		}
		data = data[:remaining]
	}
	if len(data) > 0 && writer.writeErr == nil {
		n, err := writer.file.Write(data)
		writer.written += int64(n)
		if err != nil {
			writer.writeErr = err
		}
	}
	// Absorb all bytes even after the cap or a filesystem error. gqlgen must be
	// allowed to finish normally; commit owns reporting and discarding.
	return originalLen, nil
}

func (writer *detachedResultWriter) commit() detachedResultCommit {
	if writer.statusCode == 0 {
		writer.statusCode = http.StatusOK
	}
	result := detachedResultCommit{
		statusCode: writer.statusCode,
		header:     map[string][]string{},
		discarded:  writer.discarded,
	}
	if values := writer.header.Values("Content-Type"); len(values) > 0 {
		result.header["Content-Type"] = append([]string(nil), values...)
	}
	if writer.writeErr == nil {
		writer.writeErr = writer.file.Sync()
	}
	if closeErr := writer.file.Close(); writer.writeErr == nil {
		writer.writeErr = closeErr
	}
	if writer.discarded || writer.writeErr != nil {
		_ = os.Remove(writer.tmpPath)
		result.err = writer.writeErr
		return result
	}
	if err := os.Rename(writer.tmpPath, writer.finalPath); err != nil {
		_ = os.Remove(writer.tmpPath)
		result.err = fmt.Errorf("commit saved primary query result: %w", err)
		return result
	}
	if err := syncSessionDirectory(filepath.Dir(writer.finalPath)); err != nil {
		result.err = err
		return result
	}
	result.available = true
	return result
}

func writeDetachedExecutionError(w http.ResponseWriter, err error) {
	var gqlErr gqlError
	if errors.As(err, &gqlErr) {
		gqlErr.WriteTo(w)
		return
	}
	var httpErr httpError
	if errors.As(err, &httpErr) {
		httpErr.WriteTo(w)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func graphqlResultHasErrors(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var envelope struct {
		Errors json.RawMessage `json:"errors"`
	}
	if json.NewDecoder(file).Decode(&envelope) != nil {
		return false
	}
	errorsJSON := strings.TrimSpace(string(envelope.Errors))
	return errorsJSON != "" && errorsJSON != "null" && errorsJSON != "[]"
}

func atomicWriteSessionFile(path string, data []byte, mode os.FileMode) error {
	tmpPath := path + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	cleanup := func() {
		file.Close()
		os.Remove(tmpPath)
	}
	if err := file.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return syncSessionDirectory(filepath.Dir(path))
}

func syncSessionDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
