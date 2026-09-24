package core

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/clientdb"
)

// Trace inspection for the LLM builtins: the views the TUI console serves
// over HTTP (dagql/idtui/frontend_console.go -- /spans, /span, /timings),
// answered from the engine instead. The renderers are shared with the
// console (idtui.RenderSpanList, RenderSpanDetail, RenderSpanTimings); what
// differs is where the DB comes from. The console reads the frontend's live
// DB; here each call materializes exactly the spans its view needs from the
// client's telemetry store into a throwaway DB, following the same scoped
// loading discipline as renderTraceReport -- nothing session-wide is
// retained between calls.

// findSpansDefaultLimit bounds a FindSpans result when the caller gives no
// limit: enough to list every step of a typical run, small enough that a
// session-wide "" query can't dump thousands of rows into the context.
const findSpansDefaultLimit = 100

// traceViewReport, traceViewInspect and traceViewTimings are the values of
// ReadTrace's `view` argument.
const (
	traceViewReport  = "report"
	traceViewInspect = "inspect"
	traceViewTimings = "timings"
)

// findSpans lists the spans whose name (or service hostname) contains query,
// session-wide or beneath root, ordered by start time, then trace ID and
// span ID, keeping the newest limit matches.
//
// The store's row index seeds the load -- every span, or root's log scope --
// and a raw byte prefilter on the query trims it before anything is decoded
// into dagui: a span whose name or attributes (the service hostname lives
// there) don't contain the query bytes can't match. The prefilter is a
// superset (the bytes may match inside an unrelated attribute); the shared
// renderer applies the exact name/service-name test. Only the candidates are
// loaded -- a flat listing needs no ancestors to frame it, and the renderer
// skips the placeholders their parent pointers allocate -- plus each
// candidate's cause-linked children, which are what a FAIL status (a failure
// riding on a link) is computed from.
func findSpans(ctx context.Context, query, root string, limit int) (string, error) {
	clientDB, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer clientDB.Close()
	stores := clientDB.InspectionStores()
	if root != "" {
		return findSpansIn(ctx, inspectionStoreForSpan(clientDB, root), query, root, limit)
	}
	return findSpansIn(ctx, clientDB, query, root, limit, stores[1:]...)
}

// findSpansIn is findSpans against an already-open store.
func findSpansIn(ctx context.Context, read *clientdb.DB, query, root string, limit int, extra ...*clientdb.DB) (string, error) {
	primary := read
	db := dagui.NewDB()
	searched := 0
	for _, read := range append([]*clientdb.DB{read}, extra...) {
		var seed map[string]struct{}
		if root != "" {
			if !read.HasSpan(root) {
				return "", fmt.Errorf("no span %q in this trace", root)
			}
			seed = read.SpanLogScope(root)
		} else {
			seed = read.SpanIDs()
		}
		rows, err := read.SelectSpansLatest(ctx, seed)
		if err != nil {
			return "", fmt.Errorf("select spans: %w", err)
		}
		needle := []byte(query)
		scope := make(map[string]struct{})
		for _, row := range rows {
			if query != "" && !strings.Contains(row.Name, query) && !bytes.Contains(row.Attributes, needle) {
				continue
			}
			scope[row.SpanID] = struct{}{}
			for _, linked := range read.CausalChildren(row.SpanID) {
				scope[linked] = struct{}{}
			}
		}
		searched += len(rows)
		if len(scope) > 0 {
			if err := ingestInspectionSpanScope(ctx, primary, read, db, scope); err != nil {
				return "", err
			}
		}
	}
	listing := idtui.RenderSpanList(db, query, limit)
	if listing == "" {
		return findSpansEmpty(query, root, searched), nil
	}
	return listing, nil
}

// findSpansEmpty phrases a no-match answer with the numbers a reader needs
// to widen the search.
func findSpansEmpty(query, root string, searched int) string {
	where := "this session"
	if root != "" {
		where = "the subtree of span " + root
	}
	if query == "" {
		return fmt.Sprintf("(no spans in %s)", where)
	}
	return fmt.Sprintf("(no spans matching %q among the %d in %s)", query, searched, where)
}

// inspectSpan renders one span's detail view: its own state, the parent
// chain up to the root, and its direct children -- the ancestor closure of
// the span plus one edge down, the smallest load that shows both directions.
func inspectSpan(ctx context.Context, spanID string) (string, error) {
	clientDB, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer clientDB.Close()
	return inspectSpanIn(ctx, inspectionStoreForSpan(clientDB, spanID), spanID)
}

// inspectSpanIn is inspectSpan against an already-open store.
func inspectSpanIn(ctx context.Context, read *clientdb.DB, spanID string) (string, error) {
	scope := read.ChildSpanIDs(spanID)
	scope[spanID] = struct{}{}
	db := dagui.NewDB()
	if err := ingestSpanScope(ctx, read, db, read.AncestorClosure(scope)); err != nil {
		return "", err
	}
	// A failed span's error origins may point outside the loaded scope;
	// resolve them so the detail names them rather than listing blanks.
	if missing := unreceivedErrorOrigins(db); len(missing) > 0 {
		if err := ingestSpanScope(ctx, read, db, missing); err != nil {
			return "", err
		}
	}
	id, err := trace.SpanIDFromHex(spanID)
	if err != nil {
		return "", fmt.Errorf("invalid span ID %q: %w", spanID, err)
	}
	detail, ok := idtui.RenderSpanDetail(db, dagui.SpanID{SpanID: id})
	if !ok {
		return "", fmt.Errorf("no span %q in this trace", spanID)
	}
	return detail, nil
}

// spanTimings renders the chronological wall-time table of root's subtree:
// the same containment a report loads (root's log scope, closed over
// ancestors), minus the logs, which the view never shows.
func spanTimings(ctx context.Context, root string, minDuration time.Duration, limit int) (string, error) {
	clientDB, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer clientDB.Close()
	return spanTimingsIn(ctx, inspectionStoreForSpan(clientDB, root), root, minDuration, limit, time.Now())
}

// spanTimingsIn is spanTimings against an already-open store.
func spanTimingsIn(ctx context.Context, read *clientdb.DB, root string, minDuration time.Duration, limit int, now time.Time) (string, error) {
	db := dagui.NewDB()
	if err := ingestSpanScope(ctx, read, db, read.AncestorClosure(read.SpanLogScope(root))); err != nil {
		return "", err
	}
	id, err := trace.SpanIDFromHex(root)
	if err != nil {
		return "", fmt.Errorf("invalid span ID %q: %w", root, err)
	}
	timings, ok := idtui.RenderSpanTimings(db, dagui.SpanID{SpanID: id}, minDuration, limit, now)
	if !ok {
		return "", fmt.Errorf("no span %q in this trace", root)
	}
	return timings, nil
}
