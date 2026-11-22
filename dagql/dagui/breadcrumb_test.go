package dagui

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TestErrorCauseBreadcrumb covers the breadcrumb of an error origin beneath
// the row it bubbled up to, both from the origin's tree and from its span.
func TestErrorCauseBreadcrumb(t *testing.T) {
	type step struct {
		name                               string
		internal, encapsulate, passthrough bool
	}
	for _, tc := range []struct {
		name string
		// The ancestors of the origin from the row down, the row first.
		path []step
		// What the breadcrumb names, from the row down.
		want []string
	}{
		{
			// viztest's fail-multi: the passthrough request is left out.
			name: "beneath a collapsed row",
			path: []step{{name: "failMulti"}, {name: "roll-up"}, {name: "sub-thing 1"}, {name: "POST /query", passthrough: true}},
			want: []string{"roll-up", "sub-thing 1"},
		},
		{
			// A module build failure: the internal entrypoint plumbing is
			// left out, its encapsulation with it, and the build stands for
			// the calls beneath it.
			name: "follows visibility",
			path: []step{{name: "viztestGo"}, {name: "call module entrypoint", internal: true, encapsulate: true},
				{name: "build viztest-go", encapsulate: true}, {name: "binary"}, {name: "gobuild"}},
			want: []string{"build viztest-go"},
		},
		{
			// A module whose runtime fails to build while the workspace
			// loads: the module's load and its build are named, and the
			// steps between them, and beneath the build, fold.
			name: "names every landmark",
			path: []step{{name: "load workspace: .", encapsulate: true}, {name: "loading type definitions", internal: true, encapsulate: true},
				{name: "load module: ./broken", encapsulate: true}, {name: "ModuleSource.asModule"},
				{name: "build broken", encapsulate: true}, {name: "binary"}},
			want: []string{"load module: ./broken", "build broken"},
		},
		{
			// The shell loading type definitions on its own: the calls of its
			// queries fold beneath it.
			name: "folds beneath a landmark",
			path: []step{{name: "refresh"}, {name: "loading type definitions", encapsulate: true},
				{name: "Query.currentTypeDefs"}, {name: "ModuleSource.asModule"}},
			want: []string{"loading type definitions"},
		},
		{
			// A module a function loads itself: the steps above the outermost
			// landmark are named as they are.
			name: "keeps steps above landmarks",
			path: []step{{name: "loadOther"}, {name: "ModuleSource.asModule"},
				{name: "build other", encapsulate: true}, {name: "binary"}},
			want: []string{"ModuleSource.asModule", "build other"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(100, 0)
			failed := sdktrace.Status{Code: codes.Error, Description: "boom"}
			traceID := TraceID{TraceID: trace.TraceID{1}}
			var snapshots []SpanSnapshot
			var parent SpanID
			for i, s := range tc.path {
				snapshots = append(snapshots, SpanSnapshot{
					ID: testID(byte(i + 1)), TraceID: traceID, ParentID: parent, Name: s.name,
					Internal: s.internal, Encapsulate: s.encapsulate, Passthrough: s.passthrough,
					StartTime: start, EndTime: start.Add(time.Second), Status: failed,
				})
				parent = testID(byte(i + 1))
			}
			originID := testID(byte(len(tc.path) + 1))
			snapshots = append(snapshots, SpanSnapshot{
				ID: originID, TraceID: traceID, ParentID: parent, Name: "withExec",
				StartTime: start, EndTime: start.Add(time.Second), Status: failed,
			})
			db := NewDB()
			db.ImportSnapshots(snapshots)
			origin := db.Spans.Map[originID]
			row := testID(1)

			var fromSpan []string
			for _, s := range origin.ErrorCauseBreadcrumb(row) {
				fromSpan = append(fromSpan, s.Name)
			}
			slices.Reverse(fromSpan)
			require.Equal(t, tc.want, fromSpan)

			var fromTree []string
			for _, tree := range treeOf(origin).ErrorCauseBreadcrumb(row) {
				fromTree = append(fromTree, tree.Span.Name)
			}
			slices.Reverse(fromTree)
			require.Equal(t, tc.want, fromTree)
		})
	}
}

// TestErrorCauseBreadcrumbSkipsUnreceived covers an origin fetched by ID
// whose parent never arrived: the placeholder has nothing to name.
func TestErrorCauseBreadcrumbSkipsUnreceived(t *testing.T) {
	start := time.Unix(100, 0)
	traceID := TraceID{TraceID: trace.TraceID{1}}
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		{ID: testID(1), TraceID: traceID, Name: "dagger call", StartTime: start, EndTime: start.Add(time.Second)},
		{ID: testID(3), TraceID: traceID, ParentID: testID(2), Name: "withExec", StartTime: start, EndTime: start.Add(time.Second),
			Status: sdktrace.Status{Code: codes.Error, Description: "exit code: 1"}},
	})
	origin := db.Spans.Map[testID(3)]
	require.False(t, origin.ParentSpan.Received)
	require.Empty(t, origin.ErrorCauseBreadcrumb(testID(1)))
	require.Empty(t, treeOf(origin).ErrorCauseBreadcrumb(testID(1)))
}

// treeOf returns a tree for span whose parents are its parent spans'.
func treeOf(span *Span) *TraceTree {
	tree := &TraceTree{Span: span}
	for t, p := tree, span.ParentSpan; p != nil; t, p = t.Parent, p.ParentSpan {
		t.Parent = &TraceTree{Span: p}
	}
	return tree
}
