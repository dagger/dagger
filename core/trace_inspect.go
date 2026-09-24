package core

import (
	"context"
	"fmt"
	"slices"
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

// findSpans loads a throwaway search view including ancestors: full test
// identities and breadcrumbs cannot be filtered by a leaf-name byte search.
func findSpans(ctx context.Context, query, root, status string, limit, offset int) (string, error) {
	clientDB, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer clientDB.Close()
	stores := clientDB.InspectionStores()
	if root != "" {
		return findSpanPageIn(ctx, inspectionStoreForSpan(clientDB, root), query, root, status, limit, offset)
	}
	return findSpanPageIn(ctx, clientDB, query, root, status, limit, offset, stores[1:]...)
}

// findSpansIn keeps the default tail-page behavior for internal callers.
func findSpansIn(ctx context.Context, read *clientdb.DB, query, root string, limit int, extra ...*clientdb.DB) (string, error) {
	return findSpanPageIn(ctx, read, query, root, "", limit, 0, extra...)
}

func findSpanPageIn(ctx context.Context, read *clientdb.DB, query, root, status string, limit, offset int, extra ...*clientdb.DB) (string, error) {
	if offset < 0 {
		return "", fmt.Errorf("offset must be non-negative")
	}
	switch status {
	case "", "ERROR", "FAIL", "failed", "run", "ok":
	default:
		return "", fmt.Errorf("invalid status %q: want ERROR, FAIL, failed, run or ok", status)
	}
	primary := read
	db := dagui.NewDB()
	searched := 0
	eligible := map[string]bool{}
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
		searched += len(seed)
		for id := range seed {
			eligible[id] = true
		}
		if len(seed) > 0 {
			if err := ingestInspectionSpanScope(ctx, primary, read, db, read.AncestorClosure(seed)); err != nil {
				return "", err
			}
		}
	}
	listing := renderSpanSearch(db, eligible, query, root, status, limit, offset)
	if listing == "" {
		if status != "" {
			return fmt.Sprintf("(no spans matching query %q and status %q among %d searched spans)", query, status, searched), nil
		}
		return findSpansEmpty(query, root, searched), nil
	}
	return listing, nil
}

// spanSearchIdentity uses the test index's full identity (including parallel
// continuations), not guesses from error status or a leaf named "cleanup".
func spanSearchIdentity(sp *dagui.Span, tests *dagui.TestView) string {
	var tail []string
	seen := map[dagui.SpanID]bool{}
	for p := sp; p != nil && !seen[p.ID]; p = p.ParentSpan {
		seen[p.ID] = true
		if node := tests.BySpan[p.ID]; node != nil {
			slices.Reverse(tail)
			return strings.Join(append([]string{node.FullName}, tail...), "/")
		}
		tail = append(tail, p.Name)
	}
	slices.Reverse(tail)
	return strings.Join(tail, "/")
}

func renderSpanSearch(db *dagui.DB, eligible map[string]bool, query, root, status string, limit, offset int) string {
	tests := db.TestView()
	type match struct {
		span     *dagui.Span
		identity string
	}
	var matches []match
	for _, sp := range db.Spans.Order {
		if !sp.Received || !eligible[sp.ID.String()] {
			continue
		}
		identity := spanSearchIdentity(sp, tests)
		if query != "" && !strings.Contains(sp.Name, query) && !strings.Contains(sp.ServiceName, query) && !strings.Contains(identity, query) {
			continue
		}
		state := idtui.SpanStatus(sp)
		if status == "failed" {
			if state != "ERROR" && state != "FAIL" {
				continue
			}
		} else if status != "" && state != status {
			continue
		}
		matches = append(matches, match{sp, identity})
	}
	slices.SortFunc(matches, func(a, b match) int {
		if c := a.span.StartTime.Compare(b.span.StartTime); c != 0 {
			return c
		}
		if c := strings.Compare(a.span.TraceID.String(), b.span.TraceID.String()); c != 0 {
			return c
		}
		return strings.Compare(a.span.ID.String(), b.span.ID.String())
	})
	if len(matches) == 0 {
		return ""
	}
	if offset >= len(matches) {
		return fmt.Sprintf("offset %d skips all %d matching spans; retry offset 0", offset, len(matches))
	}
	end := len(matches) - offset
	start := 0
	if limit > 0 {
		start = max(0, end-limit)
	}
	var rows []string
	bytes := 0
	for i := end - 1; i >= start; i-- {
		m := matches[i]
		sp := m.span
		name := clampLineBytes(strings.ReplaceAll(sp.Name, "\n", " "), 240)
		if sp.Service {
			name += "  [service " + sp.ServiceName + "]"
		}
		if m.identity != sp.Name {
			name += "  [path=" + clampLineBytes(strings.ReplaceAll(m.identity, "\n", " "), 500) + "]"
		}
		row := fmt.Sprintf("%s  %-5s  %s\n", sp.ID, idtui.SpanStatus(sp), name)
		if bytes+len(row) > 24*1024 {
			start = i + 1
			break
		}
		rows = append(rows, row)
		bytes += len(row)
	}
	slices.Reverse(rows)
	var out strings.Builder
	if status != "" {
		out.WriteString("Status matches are navigation, not proof of error causality.\n")
	}
	if start > 0 {
		fmt.Fprintf(&out, "... %d earlier matching spans omitted; next: FindSpans(query: %q, span: %q, status: %q, offset: %d, limit: %d) ...\n", start, query, root, status, offset+len(rows), max(limit, 1))
	}
	out.WriteString(strings.Join(rows, ""))
	return out.String()
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
