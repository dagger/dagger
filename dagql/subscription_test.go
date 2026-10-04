package dagql_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/internal/points"
	"github.com/dagger/dagger/engine"
)

// subscriptionTestServer serves a tiny schema with subscriptions over the
// default handler (and so over dagql.SSE), the way the engine's /query
// endpoint does.
type subscriptionTestServer struct {
	srv   *dagql.Server
	cache *dagql.Cache
	http  *httptest.Server
	// endlessDone is closed when the endless resolver returns.
	endlessDone chan struct{}
}

func newSubscriptionTestServer(t *testing.T) *subscriptionTestServer {
	t.Helper()
	cache := newCache(t)
	srv := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](srv)

	ts := &subscriptionTestServer{
		srv:         srv,
		cache:       cache,
		endlessDone: make(chan struct{}),
	}
	dagql.Subscriptions{
		// ticks emits n points, each SELECTED through Query.point so its ID
		// is an honest, loadable chain.
		dagql.Subscribe("ticks", func(ctx context.Context, args struct {
			N int
		}, emit func(dagql.ObjectResult[*points.Point]) error) error {
			if args.N < 0 {
				return fmt.Errorf("n must be >= 0, got %d", args.N)
			}
			srv := dagql.CurrentDagqlServer(ctx)
			for i := 1; i <= args.N; i++ {
				var pt dagql.ObjectResult[*points.Point]
				if err := srv.Select(ctx, srv.Root(), &pt, dagql.Selector{
					Field: "point",
					Args: []dagql.NamedInput{
						{Name: "x", Value: dagql.NewInt(i)},
						{Name: "y", Value: dagql.NewInt(i * 10)},
					},
				}); err != nil {
					return err
				}
				if err := emit(pt); err != nil {
					return err
				}
			}
			return nil
		}).Doc("Emit n points, then end."),

		dagql.Subscribe("count", func(ctx context.Context, args struct {
			N int
		}, emit func(dagql.Int) error) error {
			for i := 1; i <= args.N; i++ {
				if err := emit(dagql.NewInt(i)); err != nil {
					return err
				}
			}
			return nil
		}),

		// endless emits until its reader goes away.
		dagql.Subscribe("endless", func(ctx context.Context, _ struct{}, emit func(dagql.Int) error) error {
			defer close(ts.endlessDone)
			for i := 1; ; i++ {
				if err := emit(dagql.NewInt(i)); err != nil {
					return err
				}
				select {
				case <-ctx.Done():
					return context.Cause(ctx)
				case <-time.After(10 * time.Millisecond):
				}
			}
		}),
	}.Install(srv)

	h := dagql.NewDefaultHandler(srv)
	ts.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := engine.ContextWithClientMetadata(r.Context(), testClientMetadata())
		ctx = dagql.ContextWithCache(ctx, cache)
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(ts.http.Close)
	return ts
}

func (ts *subscriptionTestServer) post(t *testing.T, ctx context.Context, query string, stream bool) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.http.URL+"/query", strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

type sseEvent struct {
	Event string
	Data  string
}

// readSSE parses a complete text/event-stream body, skipping comments.
func readSSE(t *testing.T, r io.Reader) []sseEvent {
	t.Helper()
	var events []sseEvent
	scanner := bufio.NewScanner(r)
	var cur *sseEvent
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if cur != nil {
				events = append(events, *cur)
				cur = nil
			}
		case strings.HasPrefix(line, ":"):
		default:
			if cur == nil {
				cur = &sseEvent{}
			}
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				cur.Event = value
			case "data":
				cur.Data += value
			}
		}
	}
	require.NoError(t, scanner.Err())
	require.Nil(t, cur, "stream ended mid-event")
	return events
}

func TestSubscriptionSSEFraming(t *testing.T) {
	ts := newSubscriptionTestServer(t)

	resp := ts.post(t, t.Context(), `subscription { count(n: 2) }`, true)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	// The exact bytes on the wire: the graphql-sse distinct connections
	// framing that clients (Dang's) parse.
	require.Equal(t,
		"event: next\ndata: {\"data\":{\"count\":1}}\n\n"+
			"event: next\ndata: {\"data\":{\"count\":2}}\n\n"+
			"event: complete\ndata:\n\n",
		string(raw))
}

func TestSubscriptionObjectsHaveHonestIDs(t *testing.T) {
	ts := newSubscriptionTestServer(t)

	resp := ts.post(t, t.Context(), `subscription Ticks { tick: ticks(n: 3) { id x y } }`, true)
	defer resp.Body.Close()
	events := readSSE(t, resp.Body)
	require.Len(t, events, 4)
	require.Equal(t, "complete", events[3].Event)
	require.Empty(t, events[3].Data)

	ctx := dagql.ContextWithCache(testContext(), ts.cache)
	for i, ev := range events[:3] {
		require.Equal(t, "next", ev.Event)
		var payload struct {
			Data struct {
				Tick struct {
					ID string
					X  int
					Y  int
				}
			}
			Errors []any
		}
		require.NoError(t, json.Unmarshal([]byte(ev.Data), &payload), ev.Data)
		require.Empty(t, payload.Errors)
		require.Equal(t, i+1, payload.Data.Tick.X)
		require.Equal(t, (i+1)*10, payload.Data.Tick.Y)

		// Each pushed value's ID is the honest chain point(x:, y:): it
		// loads back to the same value, outside the subscription.
		var id call.ID
		require.NoError(t, id.Decode(payload.Data.Tick.ID))
		require.Equal(t, "Point", id.Type().NamedType())
		loaded, err := ts.srv.Load(ctx, &id)
		require.NoError(t, err)
		var x int
		require.NoError(t, ts.srv.Select(ctx, loaded, &x, dagql.Selector{Field: "x"}))
		require.Equal(t, i+1, x)
	}
}

func TestSubscriptionResolverError(t *testing.T) {
	ts := newSubscriptionTestServer(t)

	resp := ts.post(t, t.Context(), `subscription { ticks(n: -1) { x } }`, true)
	defer resp.Body.Close()
	events := readSSE(t, resp.Body)
	require.Len(t, events, 2)
	require.Equal(t, "next", events[0].Event)
	require.Contains(t, events[0].Data, `"errors"`)
	require.Contains(t, events[0].Data, "n must be \\u003e= 0")
	require.Equal(t, "complete", events[1].Event)
}

func TestSubscriptionValidationErrorIsStreamed(t *testing.T) {
	ts := newSubscriptionTestServer(t)

	// Two root fields: the spec's single-root-field rule.
	resp := ts.post(t, t.Context(), `subscription { a: count(n: 1) b: count(n: 1) }`, true)
	defer resp.Body.Close()
	events := readSSE(t, resp.Body)
	require.Len(t, events, 2)
	require.Equal(t, "next", events[0].Event)
	require.Contains(t, events[0].Data, `"errors"`)
	require.Equal(t, "complete", events[1].Event)
}

func TestSubscriptionRequiresEventStream(t *testing.T) {
	ts := newSubscriptionTestServer(t)

	resp := ts.post(t, t.Context(), `subscription { count(n: 2) }`, false)
	defer resp.Body.Close()
	require.NotEqual(t, "text/event-stream", resp.Header.Get("Content-Type"))
	var payload struct {
		Data   any
		Errors []struct{ Message string }
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Len(t, payload.Errors, 1)
	require.Contains(t, payload.Errors[0].Message, "Accept: text/event-stream")
}

func TestSubscriptionQueryOverEventStream(t *testing.T) {
	ts := newSubscriptionTestServer(t)

	// A query sent graphql-sse style is one next and a complete; plain
	// queries are unaffected.
	resp := ts.post(t, t.Context(), `{ point(x: 1) { x } }`, true)
	defer resp.Body.Close()
	events := readSSE(t, resp.Body)
	require.Equal(t, []sseEvent{
		{Event: "next", Data: `{"data":{"point":{"x":1}}}`},
		{Event: "complete"},
	}, events)

	plain := ts.post(t, t.Context(), `{ point(x: 1) { x } }`, false)
	defer plain.Body.Close()
	raw, err := io.ReadAll(plain.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"data":{"point":{"x":1}}}`, string(raw))
}

func TestSubscriptionCanceledByClient(t *testing.T) {
	ts := newSubscriptionTestServer(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resp := ts.post(t, ctx, `subscription { endless }`, true)
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "event: next\n", line)
	line, err = reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "data: {\"data\":{\"endless\":1}}\n", line)

	// Aborting the request is how a graphql-sse client unsubscribes.
	cancel()
	select {
	case <-ts.endlessDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the resolver kept running after the client went away")
	}
	_, err = io.ReadAll(resp.Body)
	require.True(t, err == nil || errors.Is(err, context.Canceled), "unexpected read error: %v", err)
}

func TestSubscriptionSchema(t *testing.T) {
	ts := newSubscriptionTestServer(t)

	schema := ts.srv.Schema()
	require.NotNil(t, schema.Subscription)
	require.Equal(t, dagql.SubscriptionTypeName, schema.Subscription.Name)
	ticks := schema.Subscription.Fields.ForName("ticks")
	require.NotNil(t, ticks)
	require.Equal(t, "Point!", ticks.Type.String())
	require.Equal(t, "Int!", ticks.Arguments.ForName("n").Type.String())

	// A server without subscription fields has no Subscription root.
	bare := newExternalDagqlServerForTest(t, Query{})
	require.Nil(t, bare.Schema().Subscription)
}
