package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dagger/dagger/engine"
)

type sessionControlError struct {
	status int
	code   string
	err    error
}

type sessionAttachmentClaim struct {
	ID         string
	Generation uint64
	ClientID   string
}

func controlError(status int, code string, err error) sessionControlError {
	return sessionControlError{status: status, code: code, err: err}
}

func (err sessionControlError) Error() string {
	return err.err.Error()
}

func (err sessionControlError) Unwrap() error {
	return err.err
}

func (err sessionControlError) WriteTo(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.status)
	_ = json.NewEncoder(w).Encode(engine.SessionProtocolErrorResponse{
		Error: engine.SessionProtocolError{Code: err.code, Message: err.Error()},
	})
}

func sessionNotFoundError(sessionID string) sessionControlError {
	return controlError(http.StatusNotFound, engine.SessionErrorSessionNotFound,
		fmt.Errorf("session %s not found", sessionID))
}

func malformedSessionIDError(kind, id string, err error) sessionControlError {
	return controlError(http.StatusBadRequest, engine.SessionErrorMalformedID,
		fmt.Errorf("malformed %s %q: %w", kind, id, err))
}

func isSessionControlPath(path string) bool {
	return path == engine.SessionsEndpoint || strings.HasPrefix(path, engine.SessionsEndpoint+"/")
}

func (srv *Server) serveSessionControl(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+engine.SessionsEndpoint, httpHandlerFunc(srv.serveSessionList, struct{}{}))
	mux.HandleFunc("GET "+engine.SessionsEndpoint+"/{sessionID}", httpHandlerFunc(srv.serveSessionInspect, struct{}{}))
	mux.HandleFunc("DELETE "+engine.SessionsEndpoint+"/{sessionID}/attachments/{attachmentID}", httpHandlerFunc(srv.serveSessionAttachmentClose, struct{}{}))
	mux.HandleFunc("DELETE "+engine.SessionsEndpoint+"/{sessionID}", httpHandlerFunc(srv.serveSessionTerminate, struct{}{}))
	mux.HandleFunc("POST "+engine.SessionsEndpoint+"/{sessionID}/queries/primary", httpHandlerFunc(srv.servePrimaryQuerySubmit, struct{}{}))
	mux.HandleFunc("GET "+engine.SessionsEndpoint+"/{sessionID}/queries/primary", httpHandlerFunc(srv.servePrimaryQueryInspect, struct{}{}))
	mux.HandleFunc("GET "+engine.SessionsEndpoint+"/{sessionID}/queries/primary/result", httpHandlerFunc(srv.servePrimaryQueryResult, struct{}{}))
	mux.HandleFunc("GET "+engine.SessionsEndpoint+"/{sessionID}/telemetry/{signal}", httpHandlerFunc(srv.serveSessionTelemetry, struct{}{}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		controlError(http.StatusNotFound, engine.SessionErrorInvalidRequest,
			fmt.Errorf("unknown session control route %s %s", r.Method, r.URL.Path)).WriteTo(w)
	})
	mux.ServeHTTP(w, r)
}

func (srv *Server) serveSessionList(w http.ResponseWriter, _ *http.Request, _ struct{}) error {
	srv.daggerSessionsMu.RLock()
	sessions := make([]*daggerSession, 0, len(srv.daggerSessions))
	for _, sess := range srv.daggerSessions {
		sessions = append(sessions, sess)
	}
	srv.daggerSessionsMu.RUnlock()

	descriptors := make([]engine.SessionDescriptor, 0, len(sessions))
	for _, sess := range sessions {
		if descriptor, ok := srv.snapshotDetachableSession(sess); ok {
			descriptors = append(descriptors, descriptor)
		}
	}
	slices.SortFunc(descriptors, func(a, b engine.SessionDescriptor) int {
		return strings.Compare(a.ID, b.ID)
	})
	return writeSessionJSON(w, http.StatusOK, descriptors)
}

func (srv *Server) serveSessionInspect(w http.ResponseWriter, r *http.Request, _ struct{}) error {
	sessionID := r.PathValue("sessionID")
	if err := engine.ValidateSessionID(sessionID); err != nil {
		return malformedSessionIDError("session ID", sessionID, err)
	}
	sess, ok := srv.lookupDetachableSession(sessionID)
	if !ok {
		return sessionNotFoundError(sessionID)
	}
	descriptor, ok := srv.snapshotDetachableSession(sess)
	if !ok {
		return sessionNotFoundError(sessionID)
	}
	return writeSessionJSON(w, http.StatusOK, descriptor)
}

func (srv *Server) serveSessionAttachmentClose(w http.ResponseWriter, r *http.Request, _ struct{}) error {
	sessionID := r.PathValue("sessionID")
	attachmentID := r.PathValue("attachmentID")
	if err := engine.ValidateSessionID(sessionID); err != nil {
		return malformedSessionIDError("session ID", sessionID, err)
	}
	if err := engine.ValidateAttachmentID(attachmentID); err != nil {
		return malformedSessionIDError("attachment ID", attachmentID, err)
	}
	sess, ok := srv.lookupDetachableSession(sessionID)
	if !ok {
		return sessionNotFoundError(sessionID)
	}

	srv.hitSessionHook(sessionTestEvent{Name: sessionHookExplicitClose, SessionID: sessionID, AttachmentID: attachmentID})

	sess.attachmentMu.Lock()
	attachment := sess.currentAttachment
	if attachment == nil || attachment.ID != attachmentID {
		sess.attachmentMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	clientID := attachment.ClientID
	generation := attachment.Generation
	sess.attachmentMu.Unlock()

	// The manager wait is intentionally outside attachmentMu. Its registration
	// callback may need the same lock while unwinding.
	sess.attachables.Deregister(clientID)
	srv.detachAttachment(sess, attachmentID, generation)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (srv *Server) serveSessionTerminate(w http.ResponseWriter, r *http.Request, _ struct{}) error {
	sessionID := r.PathValue("sessionID")
	if err := engine.ValidateSessionID(sessionID); err != nil {
		return malformedSessionIDError("session ID", sessionID, err)
	}
	done := srv.startSessionTermination(sessionID)
	if done != nil {
		select {
		case <-done:
		case <-r.Context().Done():
			return r.Context().Err()
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (srv *Server) serveSessionTelemetry(w http.ResponseWriter, r *http.Request, _ struct{}) error {
	sessionID := r.PathValue("sessionID")
	if err := engine.ValidateSessionID(sessionID); err != nil {
		return malformedSessionIDError("session ID", sessionID, err)
	}
	sess, ok := srv.lookupDetachableSession(sessionID)
	if !ok {
		return sessionNotFoundError(sessionID)
	}
	sess.queryMu.RLock()
	q := sess.primaryQuery
	sess.queryMu.RUnlock()
	if q == nil {
		return controlError(http.StatusNotFound, engine.SessionErrorPrimaryQueryNotFound,
			fmt.Errorf("session %s has no primary query", sessionID))
	}
	creator := sessCreatorClient(sess)
	if creator == nil {
		return controlError(http.StatusGone, engine.SessionErrorTelemetryUnavailable,
			fmt.Errorf("session %s creator telemetry is unavailable", sessionID))
	}
	db, err := creator.TelemetryDB(r.Context())
	if err != nil {
		return controlError(http.StatusGone, engine.SessionErrorTelemetryUnavailable,
			fmt.Errorf("session %s creator telemetry is unavailable: %w", sessionID, err))
	}
	_ = db.Close()

	signal := r.PathValue("signal")
	opts := sessionSSEOptions{
		terminating: q.done,
		maxID: func() (int64, bool) {
			q.mu.RLock()
			defer q.mu.RUnlock()
			if !q.telemetryBoundSet {
				return 0, false
			}
			switch signal {
			case "traces":
				return q.telemetry.Traces, true
			case "logs":
				return q.telemetry.Logs, true
			case "metrics":
				return q.telemetry.Metrics, true
			default:
				return 0, false
			}
		},
		finalRow: func() {
			srv.hitSessionHook(sessionTestEvent{Name: sessionHookSSEFinalRow, SessionID: sessionID, Resource: signal})
		},
	}
	switch signal {
	case "traces":
		return srv.telemetryPubSub.tracesSubscribeHandler(w, r, creator, opts)
	case "logs":
		return srv.telemetryPubSub.logsSubscribeHandler(w, r, creator, opts)
	case "metrics":
		return srv.telemetryPubSub.metricsSubscribeHandler(w, r, creator, opts)
	default:
		return controlError(http.StatusNotFound, engine.SessionErrorInvalidRequest,
			fmt.Errorf("unknown telemetry signal %q", signal))
	}
}

func (srv *Server) lookupDetachableSession(sessionID string) (*daggerSession, bool) {
	srv.daggerSessionsMu.RLock()
	sess, ok := srv.daggerSessions[sessionID]
	srv.daggerSessionsMu.RUnlock()
	if !ok || sess.lifetime != sessionLifetimeDetachable || sess.state.Load() == sessionStateRemoved {
		return nil, false
	}
	return sess, true
}

func (srv *Server) claimObserverAttachment(opts *ClientInitOpts) (*sessionAttachmentClaim, error) {
	srv.hitSessionHook(sessionTestEvent{
		Name: sessionHookObserverBeforeValidation, SessionID: opts.SessionID,
		ClientID: opts.ClientID, AttachmentID: opts.AttachmentID,
	})
	if err := engine.ValidateSessionID(opts.SessionID); err != nil {
		return nil, malformedSessionIDError("session ID", opts.SessionID, err)
	}
	if err := engine.ValidateAttachmentID(opts.AttachmentID); err != nil {
		return nil, malformedSessionIDError("attachment ID", opts.AttachmentID, err)
	}
	sess, ok := srv.lookupDetachableSession(opts.SessionID)
	if !ok {
		return nil, sessionNotFoundError(opts.SessionID)
	}
	if sess.terminating.Load() {
		return nil, controlError(http.StatusConflict, engine.SessionErrorSessionTerminating,
			fmt.Errorf("session %s is terminating", opts.SessionID))
	}
	srv.hitSessionHook(sessionTestEvent{
		Name: sessionHookObserverAfterValidation, SessionID: opts.SessionID,
		ClientID: opts.ClientID, AttachmentID: opts.AttachmentID,
	})

	sess.attachmentMu.Lock()
	defer sess.attachmentMu.Unlock()
	if sess.terminating.Load() || sess.state.Load() != sessionStateInitialized {
		return nil, controlError(http.StatusConflict, engine.SessionErrorSessionTerminating,
			fmt.Errorf("session %s is terminating", opts.SessionID))
	}
	if sess.currentAttachment != nil {
		return nil, controlError(http.StatusConflict, engine.SessionErrorAlreadyAttached,
			fmt.Errorf("session %s already has an attachment", opts.SessionID))
	}
	sess.nextGeneration++
	attachment := &sessionAttachment{
		ID: opts.AttachmentID, Generation: sess.nextGeneration,
		ClientID: opts.ClientID, CreatedAt: srv.nowTime(),
	}
	sess.currentAttachment = attachment
	claim := &sessionAttachmentClaim{
		ID: attachment.ID, Generation: attachment.Generation,
		ClientID: attachment.ClientID,
	}
	opts.claimedAttachment = claim
	return claim, nil
}

func (srv *Server) claimCreatorAttachment(sess *daggerSession, opts *ClientInitOpts) (*sessionAttachmentClaim, error) {
	if opts.claimedAttachment != nil {
		return opts.claimedAttachment, nil
	}
	sess.attachmentMu.Lock()
	defer sess.attachmentMu.Unlock()
	if sess.terminating.Load() || sess.state.Load() != sessionStateInitialized {
		return nil, controlError(http.StatusConflict, engine.SessionErrorSessionTerminating,
			fmt.Errorf("session %s is terminating", sess.sessionID))
	}
	attachment := sess.currentAttachment
	if attachment == nil {
		sess.nextGeneration++
		attachment = &sessionAttachment{
			ID: opts.AttachmentID, Generation: sess.nextGeneration,
			ClientID: opts.ClientID, CreatedAt: srv.nowTime(),
		}
		sess.currentAttachment = attachment
		claim := &sessionAttachmentClaim{
			ID: attachment.ID, Generation: attachment.Generation,
			ClientID: attachment.ClientID,
		}
		opts.claimedAttachment = claim
		return claim, nil
	}
	if attachment.ID != opts.AttachmentID || attachment.ClientID != opts.ClientID {
		return nil, controlError(http.StatusConflict, engine.SessionErrorAlreadyAttached,
			fmt.Errorf("session %s already has an attachment", sess.sessionID))
	}
	return nil, controlError(http.StatusConflict, engine.SessionErrorAttachmentConnectionExists,
		fmt.Errorf("session %s already has a creator attachment connection", sess.sessionID))
}

func (srv *Server) snapshotDetachableSession(sess *daggerSession) (engine.SessionDescriptor, bool) {
	if sess == nil || sess.lifetime != sessionLifetimeDetachable || sess.state.Load() != sessionStateInitialized {
		return engine.SessionDescriptor{}, false
	}
	descriptor := engine.SessionDescriptor{
		ID:        sess.sessionID,
		CreatedAt: sess.createdAt,
	}

	sess.attachmentMu.Lock()
	if sess.terminating.Load() {
		descriptor.State = engine.SessionStateTerminating
	} else if attachment := sess.currentAttachment; attachment != nil {
		descriptor.State = engine.SessionStateAttached
		descriptor.Attachment = &engine.SessionAttachment{
			ID: attachment.ID, Generation: attachment.Generation,
			ClientID: attachment.ClientID, Ready: attachment.Ready,
			CreatedAt: attachment.CreatedAt,
		}
	} else {
		descriptor.State = engine.SessionStateDetached
		descriptor.DetachedAt = sess.detachedAt
	}
	sess.attachmentMu.Unlock()

	sess.queryMu.RLock()
	query := sess.primaryQuery
	if query != nil {
		snapshot := query.snapshot()
		descriptor.Query = &snapshot
	}
	sess.queryMu.RUnlock()
	if sess.state.Load() != sessionStateInitialized {
		return engine.SessionDescriptor{}, false
	}
	return descriptor, true
}

func (srv *Server) detachAttachment(sess *daggerSession, attachmentID string, generation uint64) bool {
	sess.attachmentMu.Lock()
	attachment := sess.currentAttachment
	if attachment == nil || attachment.ID != attachmentID || attachment.Generation != generation {
		sess.attachmentMu.Unlock()
		return false
	}
	clientID := attachment.ClientID
	var observer *daggerClient
	if clientID != sess.creatorClientID {
		// Remove the exact observer while attachment ownership is still held.
		// Once currentAttachment is cleared, a retry using the same client ID may
		// initialize a replacement; delayed cleanup must never remove that newer
		// client from the session.
		sess.clientMu.Lock()
		candidate := sess.clients[clientID]
		if candidate != nil && candidate.observerClient {
			delete(sess.clients, clientID)
			observer = candidate
		}
		sess.clientMu.Unlock()
	}
	sess.currentAttachment = nil
	sess.detachedAt = srv.nowTime()
	sess.attachmentMu.Unlock()

	srv.hitSessionHook(sessionTestEvent{
		Name: sessionHookAttachmentCleared, SessionID: sess.sessionID,
		ClientID: clientID, AttachmentID: attachmentID, Generation: generation,
	})
	if observer != nil {
		srv.cleanupObserverClient(sess, observer)
	}
	return true
}

func (srv *Server) cleanupObserverClient(sess *daggerSession, client *daggerClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = client.cleanupResources(ctx)
	srv.hitSessionHook(sessionTestEvent{
		Name: sessionHookObserverCleanup, SessionID: sess.sessionID, ClientID: client.clientID,
	})
}

func (srv *Server) startSessionTermination(sessionID string) <-chan struct{} {
	srv.daggerSessionsMu.RLock()
	sess, ok := srv.daggerSessions[sessionID]
	srv.daggerSessionsMu.RUnlock()
	if !ok {
		srv.recordTerminalSessionID(sessionID)
		return nil
	}
	if sess.lifetime != sessionLifetimeDetachable {
		return nil
	}

	sess.terminateMu.Lock()
	if sess.terminateDone != nil {
		done := sess.terminateDone
		sess.terminateMu.Unlock()
		return done
	}
	done := make(chan struct{})
	sess.terminateDone = done
	// Install the bounded terminal ID before exposing the transition or
	// beginning cleanup, so a delayed creator request can never recreate it.
	srv.recordTerminalSessionID(sessionID)
	sess.attachmentMu.Lock()
	sess.terminating.Store(true)
	sess.attachmentMu.Unlock()
	sess.terminateMu.Unlock()

	srv.hitSessionHook(sessionTestEvent{Name: sessionHookTerminalTransition, SessionID: sessionID})
	go srv.runSessionTermination(sess)
	return done
}

func (srv *Server) runSessionTermination(sess *daggerSession) {
	var err error
	sess.lifecycleMu.Lock()
	if sess.state.Load() == sessionStateInitialized {
		err = srv.removeDaggerSession(context.Background(), sess)
	}
	sess.lifecycleMu.Unlock()
	srv.deleteSession(sess)

	sess.terminateMu.Lock()
	sess.terminateErr = err
	close(sess.terminateDone)
	sess.terminateMu.Unlock()
}

func writeSessionJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return fmt.Errorf("encode session response: %w", err)
	}
	return nil
}
