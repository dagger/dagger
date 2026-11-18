package core

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/clientdb"
)

// findSpanPageFullLoad is FindSpans as it was before it filtered in the
// store: every searched span, with its ancestors, loaded into one DB. It is
// the reference the store-side filter must render identically to.
func findSpanPageFullLoad(ctx context.Context, read *clientdb.DB, search spanSearch, extra ...*clientdb.DB) (string, error) {
	if search.Match == "" {
		search.Match = spanMatchName
	}
	primary := read
	db := dagui.NewDB()
	searched := 0
	eligible := map[string]bool{}
	for _, read := range append([]*clientdb.DB{read}, extra...) {
		var seed map[string]struct{}
		if search.Root != "" {
			if !read.HasSpan(search.Root) {
				return "", fmt.Errorf("no span %q in this trace", search.Root)
			}
			seed = read.SpanLogScope(search.Root)
		} else {
			seed = read.SpanIDs()
		}
		searched += len(seed)
		for id := range seed {
			eligible[id] = true
		}
		if len(seed) > 0 {
			if err := ingestInspectionSpanScope(ctx, primary, read, db, read.AncestorClosure(seed)); err != nil {
				return "", err
			}
		}
		if err := ingestAgentControl(ctx, read, db); err != nil {
			return "", err
		}
	}
	listing, hidden := renderSpanSearch(db, eligible, search)
	if listing == "" {
		var empty string
		if search.Status != "" {
			empty = fmt.Sprintf("(no spans matching query %q and status %q among %d searched spans)", search.Query, search.Status, searched)
		} else {
			empty = findSpansEmpty(search.Query, search.Root, searched)
		}
		if hidden > 0 {
			empty += "\n" + hiddenOwnSpansNote(hidden)
		}
		return empty, nil
	}
	if hidden > 0 {
		listing += hiddenOwnSpansNote(hidden) + "\n"
	}
	return listing, nil
}

// failLinkStore is a finished session whose failure reaches a span only
// through cause links, and whose open spans the ended root leaves running:
//
//	root (ended)
//	├── Container.asService (install)
//	├── lazy (cause-links install)
//	├── exec (ERROR, cause-links lazy)  -> lazy and install read FAIL
//	└── stuck (never ended)             -> ok: left running
func failLinkStore(t *testing.T) *clientdb.DB {
	t.Helper()
	store, err := clientdb.NewDBs(t.TempDir()).Open(t.Context(), "fail-link")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	const traceID = "00000000000000000000000000000abc"
	id := func(i int) string { return fmt.Sprintf("%016x", 0x100+i) }
	ended := func(at int64) sql.NullInt64 { return sql.NullInt64{Int64: at, Valid: true} }
	rows := []clientdb.Span{
		{SpanID: id(0), Name: "root", StartTime: 10, EndTime: ended(100)},
		{SpanID: id(1), ParentSpanID: validSpanID(id(0)), Name: "Container.asService", StartTime: 11, EndTime: ended(12)},
		{SpanID: id(2), ParentSpanID: validSpanID(id(0)), Name: "lazy", StartTime: 13, EndTime: ended(14),
			Links: marshalCauseLink(t, traceID, id(1))},
		{SpanID: id(3), ParentSpanID: validSpanID(id(0)), Name: "exec", StartTime: 15, EndTime: ended(16),
			StatusCode: int64(codes.Error), StatusMessage: "exit code: 1", Links: marshalCauseLink(t, traceID, id(2))},
		{SpanID: id(4), ParentSpanID: validSpanID(id(0)), Name: "stuck", StartTime: 17},
		// A span whose parent never arrived: its breadcrumb starts empty.
		{SpanID: id(5), ParentSpanID: validSpanID("00000000000009ff"), Name: "orphan", StartTime: 18, EndTime: ended(19)},
		// A second tree: the first root's end leaves it running too.
		{SpanID: id(6), Name: "second root", StartTime: 20},
		{SpanID: id(7), ParentSpanID: validSpanID(id(6)), Name: "second stuck", StartTime: 21},
	}
	for i := range rows {
		rows[i].TraceID = traceID
		rows[i].Attributes = marshalSpanAttrs(t)
	}
	_, err = store.AppendSpans(rows)
	require.NoError(t, err)
	return store
}

// TestFindSpansMatchesFullLoad renders every kind of search through the
// store-side filter and through the full load, over sessions exercising what
// a row depends on -- ancestors, tests, services, agents and ownership,
// cause-linked failures, an ended root, imports -- and requires identical
// output.
func TestFindSpansMatchesFullLoad(t *testing.T) {
	ctx := t.Context()
	// agentInspectStore's placeholder links and resources warn on every
	// load; thousands of loads would bury the output.
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.DiscardHandler))
	inspect, inspectIDs := traceInspectStore(t)
	// Test identities, as TestFindSpansFullTestIdentity adds them.
	testAttr := func(key, value string) *otlpcommonv1.KeyValue {
		return &otlpcommonv1.KeyValue{Key: key, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: value}}}
	}
	testRows := []clientdb.Span{
		{SpanID: "0000000000000011", Name: "cleanup", ParentSpanID: validSpanID(inspectIDs["test"]), Attributes: marshalSpanAttrs(t, testAttr("test.case.name", "TestProvision/docker/cleanup"), testAttr("test.status", "failure")), StatusCode: int64(codes.Error)},
		{SpanID: "0000000000000012", Name: "cleanup", ParentSpanID: validSpanID(inspectIDs["test"]), Attributes: marshalSpanAttrs(t, testAttr("test.case.name", "TestProvision/podman/cleanup"), testAttr("test.status", "success"))},
		{SpanID: "0000000000000013", Name: "continue", ParentSpanID: validSpanID("0000000000000011"), Attributes: marshalSpanAttrs(t), StatusCode: int64(codes.Error)},
	}
	for i := range testRows {
		testRows[i].TraceID = "000102030405060708090a0b0c0d0e0f"
		testRows[i].StartTime = int64(100 + i)
		testRows[i].EndTime = sql.NullInt64{Int64: 200, Valid: true}
	}
	_, err := inspect.AppendSpans(testRows)
	require.NoError(t, err)
	agents, agentIDs := agentInspectStore(t)
	session := searchSessionStore(t, 700)

	live, err := clientdb.NewDBs(t.TempDir()).Open(ctx, "live")
	require.NoError(t, err)
	t.Cleanup(func() { live.Close() })
	_, err = live.AppendSpans(searchSessionRows(t, 60))
	require.NoError(t, err)
	for _, traceID := range []string{"11111111111111111111111111111111", "22222222222222222222222222222222"} {
		rows := searchSessionRows(t, 40)
		for i := range rows {
			rows[i].TraceID = traceID
			// Collide with the live store for the first spans, as an
			// imported trace of this very session would.
			if i >= 5 {
				rows[i].SpanID = traceID[:4] + rows[i].SpanID[4:]
				if rows[i].ParentSpanID.Valid && rows[i].ParentSpanID.String >= "0000000000000006" {
					rows[i].ParentSpanID.String = traceID[:4] + rows[i].ParentSpanID.String[4:]
				}
			}
		}
		_, err := live.ImportTrace(ctx, traceID, func(dst *clientdb.DB) error {
			_, err := dst.AppendSpans(rows)
			return err
		})
		require.NoError(t, err)
	}

	chief := traceCaller{AgentID: "chief-handle", TraceID: agentInspectTraceID}
	sessionChief := traceCaller{AgentID: "chief-handle", TraceID: searchSessionTraceID}
	type fixture struct {
		name    string
		read    *clientdb.DB
		extra   []*clientdb.DB
		roots   []string
		queries []string
		callers []traceCaller
	}
	fixtures := []fixture{
		{"inspect", inspect, nil, []string{"", inspectIDs["build"], inspectIDs["install"]},
			[]string{"", "go ", "cache", "redis", "/", "root/go", "build/compile", "nope",
				"TestProvision", "docker/cleanup/cont", "cleanup", "test/cleanup"}, []traceCaller{{}}},
		{"agents", agents, nil, []string{"", agentIDs["chief"]},
			[]string{"", "doer", "FindSpans", "chief/Staff", "git"}, []traceCaller{{}, chief, {AgentID: "chief-handle", TraceID: "ffffffffffffffffffffffffffffffff"}}},
		{"failLink", failLinkStore(t), nil, []string{""},
			[]string{"", "exec", "/orphan", "root/l", "stuck"}, []traceCaller{{}}},
		{"session", session, nil, []string{"", "0000000000000003"},
			[]string{"", "needle", "tool call 7/", "TestPkg", "TestPkg50/case", "cache1", "Container.with", "go"}, []traceCaller{{}, sessionChief}},
		{"imports", live, live.InspectionStores()[1:], []string{""},
			[]string{"", "needle", "tool call 1", "cache", "Test"}, []traceCaller{{}, sessionChief}},
	}

	statuses := []string{"", "ERROR", "FAIL", "failed", "run", "idle", "ok"}
	pages := []struct{ limit, offset int }{{0, 0}, {2, 0}, {3, 2}, {100, 0}, {1, 1000}}
	compared := 0
	for _, fx := range fixtures {
		for _, root := range fx.roots {
			for _, query := range fx.queries {
				for _, match := range []string{spanMatchName, spanMatchPath} {
					for _, status := range statuses {
						for _, page := range pages {
							for _, caller := range fx.callers {
								for _, self := range []bool{false, true} {
									if self && caller.AgentID == "" {
										continue
									}
									search := spanSearch{Query: query, Root: root, Status: status, Match: match,
										Limit: page.limit, Offset: page.offset, Caller: caller, IncludeSelf: self}
									extra := fx.extra
									if root != "" {
										extra = nil
									}
									want, wantErr := findSpanPageFullLoad(ctx, fx.read, search, extra...)
									got, gotErr := findSpanPageIn(ctx, fx.read, search, extra...)
									if wantErr != nil {
										require.EqualError(t, gotErr, wantErr.Error(), "%s %+v", fx.name, search)
										continue
									}
									require.NoError(t, gotErr, "%s %+v", fx.name, search)
									require.Equal(t, want, got, "%s %+v", fx.name, search)
									compared++
								}
							}
						}
					}
				}
			}
		}
	}
	t.Logf("compared %d searches", compared)
}
