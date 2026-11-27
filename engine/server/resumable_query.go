package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"sync"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
)

func reconnectGracePeriod(md *engine.ClientMetadata) time.Duration {
	if md == nil || md.ReconnectGracePeriod <= 0 {
		return 0
	}
	return min(md.ReconnectGracePeriod, engine.MaxReconnectGracePeriod)
}

// serveReconnect serves the requests of a client that may reconnect: it
// refuses a reconnect to a session the engine doesn't have, releases the
// queries the client is done with, and runs resumable queries. It reports
// whether it answered r.
func (srv *Server) serveReconnect(w http.ResponseWriter, r *http.Request, md *engine.ClientMetadata, grace time.Duration, handler http.Handler) bool {
	id := r.Header.Get(engine.ResumableQueryHeader)
	released := r.Header.Values(engine.ResumableQueryReleaseHeader)
	reconnect := r.Header.Get(engine.ReconnectHeader) != ""
	if id == "" && len(released) == 0 && !reconnect {
		return false
	}
	// a reconnect must not create a session, nor a query outlive one
	if _, err := srv.archiveRequestRecord(md.ClientID, md.SessionID, md.ClientSecretToken); err != nil {
		http.Error(w, fmt.Sprintf("cannot resume session: %v", err), http.StatusGone)
		return true
	}

	prefix := md.SessionID + "/" + md.ClientID + "/"
	for _, released := range released {
		srv.resumableQueries.release(prefix + released)
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	if id == "" || r.Method != http.MethodPost || r.URL.Path != engine.QueryEndpoint {
		return false
	}
	srv.resumableQueries.serve(w, r, prefix+id, reconnect, grace, handler)
	return true
}

// resumableQueries runs queries sent with engine.ResumableQueryHeader apart
// from their connections, so a client that reconnects can send the request
// again and get the response of the query it started.
type resumableQueries struct {
	mu      sync.Mutex
	queries map[string]*resumableQuery
}

type resumableQuery struct {
	cancel  context.CancelCauseFunc
	done    chan struct{}
	resp    *httptest.ResponseRecorder
	waiting int
	expiry  *time.Timer
}

// serve starts the query with key unless it is running, and answers r with
// its response. A resend only attaches to a query the engine still has. Once
// no request waits for it, the query is cancelled and forgotten after grace,
// unless the client releases it first.
func (qs *resumableQueries) serve(w http.ResponseWriter, r *http.Request, key string, resend bool, grace time.Duration, handler http.Handler) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("read request body: %v", err), http.StatusBadRequest)
		return
	}
	q, err := qs.attach(r, body, key, resend, handler)
	if err != nil {
		http.Error(w, err.Error(), http.StatusGone)
		return
	}
	defer qs.detach(q, key, grace)

	// tell the client the engine has the query, so it may send it again
	w.WriteHeader(http.StatusProcessing)
	select {
	case <-q.done:
	case <-r.Context().Done():
		return
	}
	for k, v := range q.resp.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(q.resp.Code)
	_, _ = w.Write(q.resp.Body.Bytes())
}

// attach adds a waiter to the query with key, starting it if it isn't running.
func (qs *resumableQueries) attach(r *http.Request, body []byte, key string, resend bool, handler http.Handler) (*resumableQuery, error) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	if qs.queries == nil {
		qs.queries = map[string]*resumableQuery{}
	}
	q, ok := qs.queries[key]
	if !ok && resend {
		return nil, errors.New("the engine no longer has this query: it ended without the client, or the engine restarted")
	}
	if !ok {
		q = startResumableQuery(r, body, handler)
		qs.queries[key] = q
	}
	if q.expiry != nil {
		q.expiry.Stop()
		q.expiry = nil
	}
	q.waiting++
	return q, nil
}

func startResumableQuery(r *http.Request, body []byte, handler http.Handler) *resumableQuery {
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(r.Context()))
	q := &resumableQuery{
		cancel: cancel,
		done:   make(chan struct{}),
		resp:   httptest.NewRecorder(),
	}
	req := r.Clone(ctx)
	req.Body = io.NopCloser(bytes.NewReader(body))
	go func() {
		defer close(q.done)
		defer cancel(errors.New("resumable query done"))
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic serving resumable query", "panic", v, "stack", string(debug.Stack()))
				q.resp = httptest.NewRecorder()
				q.resp.WriteHeader(http.StatusInternalServerError)
			}
		}()
		handler.ServeHTTP(q.resp, req)
	}()
	return q
}

// detach removes a waiter. The last one leaves the query, and its response,
// for grace, since the client may not have received it.
func (qs *resumableQueries) detach(q *resumableQuery, key string, grace time.Duration) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	q.waiting--
	if q.waiting > 0 {
		return
	}
	var expiry *time.Timer
	expiry = time.AfterFunc(grace, func() {
		qs.mu.Lock()
		defer qs.mu.Unlock()
		if q.expiry != expiry {
			return
		}
		q.cancel(fmt.Errorf("client did not reconnect within %s", grace))
		delete(qs.queries, key)
	})
	q.expiry = expiry
}

// release cancels the query with key, if it still runs, and forgets it. A
// client releases a query once it has its response, or no longer wants it.
func (qs *resumableQueries) release(key string) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	q, ok := qs.queries[key]
	if !ok {
		return
	}
	q.cancel(errors.New("client released the query"))
	if q.expiry != nil {
		q.expiry.Stop()
		q.expiry = nil
	}
	delete(qs.queries, key)
}
