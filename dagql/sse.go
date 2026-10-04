package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// SSE is the graphql-sse "distinct connections mode" transport
// (https://github.com/enisdenjo/graphql-sse/blob/master/PROTOCOL.md), served
// on the ordinary GraphQL endpoint: a POST of the GraphQL request JSON with
// "Accept: text/event-stream" is answered 200 with a text/event-stream of
//
//	event: next
//	data: <ExecutionResult JSON>
//
// events, one per value, ending with
//
//	event: complete
//	data:
//
// Errors arrive as a "next" whose result carries "errors", then "complete".
// The client ends a subscription by closing the request; that cancels the
// request context, and with it the resolver.
//
// gqlgen ships a transport.SSE, but it writes "complete" with no data line
// (which an EventSource never dispatches), so dagql carries its own.
type SSE struct {
	// KeepAlive, if nonzero, writes an SSE comment (": ping") after this much
	// silence, so idle subscriptions survive proxies and connection reapers.
	KeepAlive time.Duration
}

var _ graphql.Transport = SSE{}

// SSEKeepAlive is the keep-alive interval NewDefaultHandler configures.
const SSEKeepAlive = 15 * time.Second

func (t SSE) Supports(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if !acceptsEventStream(r.Header.Values("Accept")) {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	return mediaType == "application/json"
}

func acceptsEventStream(accepts []string) bool {
	for _, accept := range accepts {
		for _, part := range strings.Split(accept, ",") {
			mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(part))
			if err == nil && mediaType == "text/event-stream" {
				return true
			}
		}
	}
	return false
}

func (t SSE) Do(w http.ResponseWriter, r *http.Request, exec graphql.GraphExecutor) {
	ctx := WithStreamingTransport(r.Context())

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		writeJSONResponse(w, exec.DispatchError(ctx, gqlerror.List{gqlerror.Errorf("streaming unsupported by this connection")}))
		return
	}

	start := graphql.Now()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		writeJSONResponse(w, exec.DispatchError(ctx, gqlerror.List{gqlerror.Errorf("could not read request body: %s", err)}))
		return
	}
	params := &graphql.RawParams{}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(params); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		writeJSONResponse(w, exec.DispatchError(ctx, gqlerror.List{gqlerror.Errorf("json request body could not be decoded: %s", err)}))
		return
	}
	params.Headers = r.Header
	params.ReadTime = graphql.TraceTiming{Start: start, End: graphql.Now()}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	conn := &sseConn{w: w, f: flusher}
	conn.flush()

	if t.KeepAlive > 0 {
		kaCtx, stop := context.WithCancel(ctx)
		defer stop()
		go conn.keepAlive(kaCtx, t.KeepAlive)
	}

	rc, opErr := exec.CreateOperationContext(ctx, params)
	if opErr != nil {
		conn.next(exec.DispatchError(graphql.WithOperationContext(ctx, rc), opErr))
		conn.complete()
		return
	}

	responses, ctx := exec.DispatchOperation(ctx, rc)
	for {
		resp := responses(ctx)
		if resp == nil {
			break
		}
		if !conn.next(resp) {
			// The client is gone; the request context is (or is about to
			// be) canceled, which ends the resolver.
			return
		}
	}
	conn.complete()
}

type sseConn struct {
	mu   sync.Mutex
	w    io.Writer
	f    http.Flusher
	last time.Time
	dead bool
}

func (c *sseConn) write(s string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead {
		return false
	}
	if _, err := io.WriteString(c.w, s); err != nil {
		c.dead = true
		return false
	}
	c.f.Flush()
	c.last = time.Now()
	return true
}

func (c *sseConn) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.f.Flush()
	c.last = time.Now()
}

func (c *sseConn) next(resp *graphql.Response) bool {
	payload, err := json.Marshal(resp)
	if err != nil {
		payload, _ = json.Marshal(&graphql.Response{
			Errors: gqlerror.List{gqlerror.Errorf("marshal response: %s", err)},
		})
	}
	return c.write(FormatSSEEvent("next", payload))
}

func (c *sseConn) complete() {
	c.write(FormatSSEEvent("complete", nil))
}

func (c *sseConn) keepAlive(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			idle := time.Since(c.last) >= interval
			c.mu.Unlock()
			if idle && !c.write(": ping\n\n") {
				return
			}
		}
	}
}

// FormatSSEEvent renders one SSE event as dagql writes it: the event line,
// then a data line (empty for a nil payload, which "complete" uses), then a
// blank line. payload must not contain newlines (JSON from json.Marshal
// never does).
func FormatSSEEvent(event string, payload []byte) string {
	if len(payload) == 0 {
		return fmt.Sprintf("event: %s\ndata:\n\n", event)
	}
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, payload)
}

func writeJSONResponse(w io.Writer, resp *graphql.Response) {
	b, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	_, _ = w.Write(b)
}
