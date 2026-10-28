package dagql_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/internal/points"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/wcprof"
)

// A profiled GraphQL request's steps are ops under the request's op, in the
// order they ran and within it, with the calls it makes nested under its
// resolve step. An unprofiled request records no steps, and a profiled query
// over a WebSocket, whose connection writes many responses, no write step.
func TestRequestStepsWcprof(t *testing.T) {
	wcprof.EnsureRecorder()
	cache := newCache(t)
	srv := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](srv)

	serve := func(profiled bool) *client.Client {
		return client.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := engine.ContextWithClientMetadata(r.Context(), testClientMetadata())
			ctx = dagql.ContextWithCache(ctx, cache)
			if profiled {
				ctx = wcprof.ContextWithProfiling(ctx)
				var op *wcprof.Op
				ctx, op = wcprof.BeginOp(ctx, wcprof.OpKindSessionPhase, "steptest.query", wcprof.OpOpts{})
				defer op.End(wcprof.OutcomeOK)
			}
			dagql.ServeHTTPWithWriteStep(dagql.NewDefaultHandler(srv), w, r.WithContext(ctx))
		}))
	}
	const query = `query { point(x: 6, y: 7) { shiftLeft { x } } }`
	var res struct {
		Point struct{ ShiftLeft struct{ X int } }
	}

	before := countGraphQLSteps(t)
	req(t, serve(false), query, &res)
	require.Equal(t, before, countGraphQLSteps(t), "the unprofiled request recorded steps")
	req(t, serve(true), query, &res)
	require.Equal(t, 5, res.Point.ShiftLeft.X)

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	byID := map[uint64]wcprof.DumpEvent{}
	var queryOp wcprof.DumpEvent
	for _, ev := range events {
		if ev.Type != "op" {
			continue
		}
		byID[ev.OpID] = ev
		if header.Strings[ev.ClassID] == "steptest.query" {
			queryOp = ev
		}
	}
	require.NotZero(t, queryOp.OpID, "no request op")

	steps := map[string]wcprof.DumpEvent{}
	for _, ev := range byID {
		if ev.ParentID == queryOp.OpID {
			steps[header.Strings[ev.ClassID]] = ev
		}
	}
	order := []string{"graphql.read", "graphql.parse", "graphql.validate", "graphql.selections", "graphql.resolve", "graphql.marshal", "graphql.write"}
	prevEnd := queryOp.StartNS
	for _, name := range order {
		step, ok := steps[name]
		require.True(t, ok, "no %s step under the request", name)
		require.LessOrEqual(t, prevEnd, step.StartNS, "%s starts before the step before it ends", name)
		require.LessOrEqual(t, step.StartNS, step.EndNS, name)
		prevEnd = step.EndNS
	}
	require.LessOrEqual(t, prevEnd, queryOp.EndNS, "the last step ends after the request")

	resolveID := steps["graphql.resolve"].OpID
	var calls int
	for _, ev := range byID {
		if ev.OpKind != wcprof.OpKindCall.String() || !underOp(byID, ev, queryOp.OpID) {
			continue
		}
		calls++
		require.True(t, underOp(byID, ev, resolveID), "call %s isn't under the resolve step", header.Strings[ev.ClassID])
	}
	require.NotZero(t, calls, "the request made no calls")

	writes := countSteps(t, "graphql.write")
	resolves := countSteps(t, "graphql.resolve")
	require.NoError(t, serve(true).WebsocketOnce(query, &res))
	require.Equal(t, 5, res.Point.ShiftLeft.X)
	// gqlgen's WebSocket transport asks for responses until there are none,
	// so the query may resolve more than once before the connection closes
	require.Greater(t, countSteps(t, "graphql.resolve"), resolves, "the WebSocket query wasn't profiled")
	require.Equal(t, writes, countSteps(t, "graphql.write"), "the WebSocket query recorded a write step")
}

// underOp reports whether ev is (transitively) under the op with id.
func underOp(byID map[uint64]wcprof.DumpEvent, ev wcprof.DumpEvent, id uint64) bool {
	for p := ev.ParentID; p != 0; p = byID[p].ParentID {
		if p == id {
			return true
		}
	}
	return false
}

// countGraphQLSteps counts the request steps recorded so far.
func countGraphQLSteps(t *testing.T) int {
	return countSteps(t, "graphql.")
}

// countSteps counts the ops recorded so far whose class starts with prefix.
func countSteps(t *testing.T, prefix string) int {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	var n int
	for _, ev := range events {
		if ev.Type == "op" && strings.HasPrefix(header.Strings[ev.ClassID], prefix) {
			n++
		}
	}
	return n
}
