package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
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

// FindSpans' `match` values: what a query is tested against.
const (
	// spanMatchName tests the span's own name, its service hostname, and --
	// for a test or check -- its full test identity.
	spanMatchName = "name"
	// spanMatchPath also tests the span's whole ancestor path, so a query
	// naming a parent matches every descendant beneath it.
	spanMatchPath = "path"
)

// spanPathMaxBytes is the breadcrumb length past which a FindSpans row elides
// the middle of the path; spanPathSegmentMaxBytes clamps each kept segment.
const (
	spanPathMaxBytes        = 120
	spanPathSegmentMaxBytes = 60
)

// spanSearch is one FindSpans request.
type spanSearch struct {
	Query  string
	Root   string
	Status string
	// Match is spanMatchName (the default) or spanMatchPath.
	Match  string
	Limit  int
	Offset int
	// Caller is the agent the search runs for. Its own spans -- its tool
	// calls and conversation, which an investigator searching a trace keeps
	// finding instead of its subject -- are left out unless IncludeSelf.
	Caller      traceCaller
	IncludeSelf bool
}

// traceCaller identifies the agent a trace builtin is called by, so searches
// can leave out its own work.
type traceCaller struct {
	// AgentID is the calling agent's runtime handle; empty when the caller
	// is no agent (a synchronous LLM, an MCP client).
	AgentID string
	// TraceID is the trace the caller runs in; a restored agent keeps its
	// handle, so its spans in an imported trace are its predecessor's, not
	// its own. Empty matches any trace.
	TraceID string
}

// traceCallerFromContext reads the calling agent from a builtin's context:
// tools run beneath the agent loop that binds it (AgentToContext).
func traceCallerFromContext(ctx context.Context) traceCaller {
	agent, ok := AgentFromContext(ctx)
	if !ok {
		return traceCaller{}
	}
	caller := traceCaller{AgentID: agent.Self().Handle}
	if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
		caller.TraceID = sc.TraceID().String()
	}
	return caller
}

// ownsSpan reports whether sp is the caller's own work: its nearest agent
// loop span (sp itself included) is the caller's. Nearest, not any: a worker
// the caller spawned runs beneath the caller's span, and its work is the
// worker's. memo caches the answer per span along each walked chain.
func (c traceCaller) ownsSpan(sp *dagui.Span, memo map[dagui.SpanID]bool) bool {
	if c.AgentID == "" {
		return false
	}
	var chain []dagui.SpanID
	owned := false
	for p := sp; p != nil && len(chain) < 4096; p = p.ParentSpan {
		if v, ok := memo[p.ID]; ok {
			owned = v
			break
		}
		chain = append(chain, p.ID)
		if p.Agent && p.AgentID != "" {
			owned = p.AgentID == c.AgentID && (c.TraceID == "" || p.TraceID.String() == c.TraceID)
			break
		}
	}
	for _, id := range chain {
		memo[id] = owned
	}
	return owned
}

// findSpans loads a throwaway search view including ancestors: full test
// identities and breadcrumbs cannot be filtered by a leaf-name byte search.
func findSpans(ctx context.Context, search spanSearch) (string, error) {
	clientDB, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer clientDB.Close()
	stores := clientDB.InspectionStores()
	if search.Root != "" {
		return findSpanPageIn(ctx, inspectionStoreForSpan(clientDB, search.Root), search)
	}
	return findSpanPageIn(ctx, clientDB, search, stores[1:]...)
}

// findSpansIn keeps the default tail-page behavior for internal callers.
func findSpansIn(ctx context.Context, read *clientdb.DB, query, root string, limit int, extra ...*clientdb.DB) (string, error) {
	return findSpanPageIn(ctx, read, spanSearch{Query: query, Root: root, Limit: limit}, extra...)
}

func findSpanPageIn(ctx context.Context, read *clientdb.DB, search spanSearch, extra ...*clientdb.DB) (string, error) {
	if search.Offset < 0 {
		return "", fmt.Errorf("offset must be non-negative")
	}
	switch search.Status {
	case "", "ERROR", "FAIL", "failed", "run", "idle", "ok":
	default:
		return "", fmt.Errorf("invalid status %q: want ERROR, FAIL, failed, run, idle or ok", search.Status)
	}
	switch search.Match {
	case "":
		search.Match = spanMatchName
	case spanMatchName, spanMatchPath:
	default:
		return "", fmt.Errorf("invalid match %q: want %s or %s", search.Match, spanMatchName, spanMatchPath)
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

// hiddenOwnSpansNote says how many matches were the caller's own spans, and
// how to see them.
func hiddenOwnSpansNote(hidden int) string {
	return fmt.Sprintf("(%d matching spans of your own -- your tool calls and messages -- hidden; includeSelf: true lists them)", hidden)
}

// ingestAgentControl feeds db the newest control record of every agent read
// has seen, so renderers can tell an idle agent's open loop span from a
// working one (dagui.DB.AgentState). Display-only: an unreadable record is
// skipped, leaving its agent's span to its own status.
func ingestAgentControl(ctx context.Context, read *clientdb.DB, db *dagui.DB) error {
	rows, err := read.AgentControlRows(ctx)
	if err != nil {
		return fmt.Errorf("select agent control records: %w", err)
	}
	records := make([]sdklog.Record, 0, len(rows))
	for _, row := range rows {
		rec, err := clientdb.DecodeLogRecord(row)
		if err != nil {
			continue
		}
		records = append(records, rec)
	}
	db.IngestLogs(records)
	return nil
}

// spanPathSeg is one level of a span's breadcrumb.
type spanPathSeg struct {
	name string
	// agent marks an agent loop span: the level that says whose work the
	// span is, kept when a long path is elided.
	agent bool
}

// spanSearchPath uses the test index's full identity (including parallel
// continuations), not guesses from error status or a leaf named "cleanup".
// The path runs root-most first and ends with sp itself.
func spanSearchPath(sp *dagui.Span, tests *dagui.TestView) []spanPathSeg {
	var tail []spanPathSeg
	seen := map[dagui.SpanID]bool{}
	for p := sp; p != nil && !seen[p.ID]; p = p.ParentSpan {
		seen[p.ID] = true
		if node := tests.BySpan[p.ID]; node != nil {
			tail = append(tail, spanPathSeg{name: node.FullName})
			break
		}
		tail = append(tail, spanPathSeg{name: p.Name, agent: p.Agent && p.AgentID != ""})
	}
	slices.Reverse(tail)
	return tail
}

func joinSpanPath(path []spanPathSeg) string {
	names := make([]string, len(path))
	for i, seg := range path {
		names[i] = seg.name
	}
	return strings.Join(names, "/")
}

// renderSpanPath is a breadcrumb bounded for a listing row: a long path keeps
// its root-most level, its two leaf-most levels, and the nearest agent loop
// span above those (whose work the span is -- or, for an agent's own loop
// span, who spawned it), eliding each run of levels between them as "…(N)…".
func renderSpanPath(path []spanPathSeg) string {
	full := strings.ReplaceAll(joinSpanPath(path), "\n", " ")
	if len(full) <= spanPathMaxBytes {
		return full
	}
	keep := make([]bool, len(path))
	keep[0] = true
	for i := max(0, len(path)-2); i < len(path); i++ {
		keep[i] = true
	}
	for i := len(path) - 3; i > 0; i-- {
		if path[i].agent {
			keep[i] = true
			break
		}
	}
	var parts []string
	gap := 0
	for i, seg := range path {
		if !keep[i] {
			gap++
			continue
		}
		if gap > 0 {
			parts = append(parts, fmt.Sprintf("…(%d)…", gap))
			gap = 0
		}
		parts = append(parts, clampLineBytes(strings.ReplaceAll(seg.name, "\n", " "), spanPathSegmentMaxBytes))
	}
	return strings.Join(parts, "/")
}

// spanNameMatches tests a query against what names sp itself: its name, its
// service hostname, and its full test identity if it is a test.
func spanNameMatches(sp *dagui.Span, tests *dagui.TestView, query string) bool {
	if strings.Contains(sp.Name, query) || strings.Contains(sp.ServiceName, query) {
		return true
	}
	node := tests.BySpan[sp.ID]
	return node != nil && strings.Contains(node.FullName, query)
}

// renderSpanSearch lists the matching spans; hidden counts the matches left
// out as the caller's own.
func renderSpanSearch(db *dagui.DB, eligible map[string]bool, search spanSearch) (string, int) {
	query, status, limit, offset := search.Query, search.Status, search.Limit, search.Offset
	tests := db.TestView()
	var matches []*dagui.Span
	hidden := 0
	owned := map[dagui.SpanID]bool{}
	for _, sp := range db.Spans.Order {
		if !sp.Received || !eligible[sp.ID.String()] {
			continue
		}
		if query != "" && !spanNameMatches(sp, tests, query) &&
			(search.Match != spanMatchPath || !strings.Contains(joinSpanPath(spanSearchPath(sp, tests)), query)) {
			continue
		}
		state := idtui.SpanStatusIn(db, sp)
		if status == "failed" {
			if state != "ERROR" && state != "FAIL" {
				continue
			}
		} else if status != "" && state != status {
			continue
		}
		if !search.IncludeSelf && search.Caller.ownsSpan(sp, owned) {
			hidden++
			continue
		}
		matches = append(matches, sp)
	}
	slices.SortFunc(matches, func(a, b *dagui.Span) int {
		if c := a.StartTime.Compare(b.StartTime); c != 0 {
			return c
		}
		if c := strings.Compare(a.TraceID.String(), b.TraceID.String()); c != 0 {
			return c
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	if len(matches) == 0 {
		return "", hidden
	}
	if offset >= len(matches) {
		return fmt.Sprintf("offset %d skips all %d matching spans; retry offset 0", offset, len(matches)), hidden
	}
	end := len(matches) - offset
	start := 0
	if limit > 0 {
		start = max(0, end-limit)
	}
	var rows []string
	bytes := 0
	for i := end - 1; i >= start; i-- {
		sp := matches[i]
		name := clampLineBytes(strings.ReplaceAll(sp.Name, "\n", " "), 240)
		if sp.Service {
			name += "  [service " + sp.ServiceName + "]"
		}
		if path := spanSearchPath(sp, tests); joinSpanPath(path) != sp.Name {
			name += "  [path=" + renderSpanPath(path) + "]"
		}
		row := fmt.Sprintf("%s  %-5s  %s\n", sp.ID, idtui.SpanStatusIn(db, sp), name)
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
		next := search
		next.Offset = offset + len(rows)
		next.Limit = max(limit, 1)
		fmt.Fprintf(&out, "... %d earlier matching spans omitted; next: %s ...\n", start, next.call())
	}
	out.WriteString(strings.Join(rows, ""))
	return out.String(), hidden
}

// call spells the FindSpans call that repeats this search, naming the
// optional arguments only when they differ from their defaults.
func (search spanSearch) call() string {
	args := fmt.Sprintf("query: %q, span: %q, status: %q", search.Query, search.Root, search.Status)
	if search.Match != "" && search.Match != spanMatchName {
		args += fmt.Sprintf(", match: %q", search.Match)
	}
	if search.IncludeSelf {
		args += ", includeSelf: true"
	}
	return fmt.Sprintf("FindSpans(%s, offset: %d, limit: %d)", args, search.Offset, search.Limit)
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
	if err := ingestAgentControl(ctx, read, db); err != nil {
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
