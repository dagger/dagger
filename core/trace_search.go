package core

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// A FindSpans search filters in the store before it builds a DB.
//
// Loading every searched span into a dagui.DB -- a session's worth of them
// for a root-less search, at a few KB each -- just to keep a page of matches
// dominated the engine's allocation in long agent sessions. Instead, a
// search first scans the searched spans' newest rows in place
// (clientdb.ScanSpansLatest), keeping a few dozen bytes of each, and decides
// from the rows what it can:
//
//   - a name or path match, exactly, for a span whose matching doesn't
//     involve a test identity or service hostname (decided from attributes,
//     so such spans are "maybe" and resolved in a small DB of their own);
//   - which spans a status filter can possibly keep: ERROR is the span's own
//     status, FAIL needs a failed span cause-linking to it, run and idle need
//     the span itself still open.
//
// Only what can still match is loaded, with exactly the spans its row
// depends on: its ancestors (breadcrumb, test identity, whose work it is),
// the spans that cause-link into it (a FAIL comes from a failed linker), and
// the session's root span (once it ends, dagui reads every span still open
// as left running). A search without a status filter goes further: its
// matches are decided without a DB, so only the page it shows is loaded.
//
// The rows are what the full load would render -- same spans, same order,
// same statuses -- which TestFindSpansMatchesFullLoad checks against the
// full load.

// searchSpan is what a search keeps of one span it scanned.
type searchSpan struct {
	// store indexes the store the span was read from: the first one holding
	// it, as the full load ingests stores in order and keeps the first.
	store int
	id    string
	// parent is the span's parent, "" when it has none; parentValid is
	// whether dagui would see one (ParentID.IsValid).
	parent      string
	parentValid bool
	// sort key: start time, then normalized trace and span IDs.
	start   int64
	traceID string
	spanID  string
	// validID is whether dagui would ingest the span at all.
	validID bool
	// eligible is whether the span is among the searched spans.
	eligible bool
	match    searchDecision
	// statusMaybe is whether the status filter may keep it.
	statusMaybe bool
	failed      bool // own status ERROR
	open        bool // own end time not after its start
	agent       bool // may be an agent loop span
	test        bool // may carry a test identity
	// name is kept only for path searches.
	name string
}

type searchDecision uint8

const (
	searchNo searchDecision = iota
	searchYes
	searchMaybe
)

var (
	searchTestCaseMarker    = []byte(semconv.TestCaseNameKey)
	searchTestSuiteMarker   = []byte(semconv.TestSuiteNameKey)
	searchServiceNameMarker = []byte(telemetryattrs.ServiceNameAttr)
	searchAgentIDMarker     = []byte(telemetryattrs.AgentIDAttr)
)

// spanSearchScan is one search's scan over its stores.
type spanSearchScan struct {
	search   spanSearch
	stores   []*clientdb.DB
	spans    map[string]*searchSpan
	order    []*searchSpan // in ingestion order: by store, then row
	searched int
	// root is the span the full load's DB would take for the session root:
	// the first parentless span ingested.
	root *searchSpan
	// traceIDs interns normalized trace IDs.
	traceIDs map[string]string
}

// scanSpanSearch scans the searched spans of every store, live first.
func scanSpanSearch(ctx context.Context, search spanSearch, stores []*clientdb.DB) (*spanSearchScan, error) {
	sc := &spanSearchScan{
		search:   search,
		stores:   stores,
		spans:    map[string]*searchSpan{},
		traceIDs: map[string]string{},
	}
	query := []byte(search.Query)
	for i, read := range stores {
		var seed, scope map[string]struct{}
		if search.Root != "" {
			if !read.HasSpan(search.Root) {
				return nil, fmt.Errorf("no span %q in this trace", search.Root)
			}
			seed = read.SpanLogScope(search.Root)
			scope = read.AncestorClosure(seed)
		} else {
			// Every span's ancestors are spans of the store.
			seed = read.SpanIDs()
			scope = seed
		}
		sc.searched += len(seed)
		err := read.ScanSpansLatest(ctx, scope, func(row *clientdb.SpanSummary) error {
			_, eligible := seed[string(row.SpanID)]
			if sp := sc.spans[string(row.SpanID)]; sp != nil {
				// Another store's copy wins; the span is still searched.
				sp.eligible = sp.eligible || eligible
				return nil
			}
			sp := sc.add(i, row)
			sp.eligible = eligible
			if sc.root == nil && sp.validID && !sp.parentValid {
				sc.root = sp
			}
			sp.failed = row.StatusCode == int64(codes.Error)
			sp.open = !row.Ended || row.EndTime < row.StartTime
			sp.agent = bytes.Contains(row.Attributes, searchAgentIDMarker)
			sp.test = bytes.Contains(row.Attributes, searchTestCaseMarker) ||
				bytes.Contains(row.Attributes, searchTestSuiteMarker)
			switch {
			case len(query) == 0 || bytes.Contains(row.Name, query):
				sp.match = searchYes
			case (sp.test || bytes.Contains(row.Attributes, searchServiceNameMarker)) &&
				bytes.Contains(row.Attributes, query):
				// A test identity or service hostname may hold it.
				sp.match = searchMaybe
			}
			if search.Match == spanMatchPath && len(query) > 0 {
				sp.name = string(row.Name)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if search.Match == spanMatchPath && search.Query != "" {
		sc.matchPaths()
	}
	sc.filterStatus()
	return sc, nil
}

// add keeps row as the span it describes.
func (sc *spanSearchScan) add(store int, row *clientdb.SpanSummary) *searchSpan {
	sp := &searchSpan{
		store: store,
		id:    string(row.SpanID),
		start: row.StartTime,
	}
	if sid, err := trace.SpanIDFromHex(sp.id); err == nil && sid.IsValid() {
		sp.validID = true
		// SpanIDFromHex takes lowercase hex only, so the ID is already in
		// SpanID.String's form.
		sp.spanID = sp.id
	}
	tid := sc.traceIDs[string(row.TraceID)]
	if tid == "" {
		parsed, _ := trace.TraceIDFromHex(string(row.TraceID))
		tid = parsed.String()
		sc.traceIDs[string(row.TraceID)] = tid
	}
	sp.traceID = tid
	if row.HasParent {
		sp.parent = string(row.ParentSpanID)
		pid, _ := trace.SpanIDFromHex(sp.parent)
		sp.parentValid = pid.IsValid()
	}
	sc.spans[sp.id] = sp
	sc.order = append(sc.order, sp)
	return sp
}

// parentOf is the span dagui links sp beneath: nil for a root, and for a
// parent no store holds (dagui's unreceived placeholder) a span of its own
// with no name and no parent.
func (sc *spanSearchScan) parentOf(sp *searchSpan) (*searchSpan, bool) {
	if !sp.parentValid {
		return nil, false
	}
	return sc.spans[sp.parent], true
}

// matchPaths decides the path searches the names can: a span whose
// breadcrumb runs through no test (whose identity would replace the path
// above it) matches exactly when its names, root-most first and joined by
// "/", contain the query. Each span carries only the last len(query)-1 bytes
// of its path down to its children.
func (sc *spanSearchScan) matchPaths() {
	query := sc.search.Query
	keep := len(query) - 1
	type pathState struct {
		tail string // the path's last keep bytes
		hit  bool
		test bool // a test on the path: undecidable here
	}
	memo := make(map[*searchSpan]pathState, len(sc.spans))
	var chain []*searchSpan
	for _, start := range sc.order {
		if !start.eligible || start.match == searchYes {
			continue
		}
		// Walk up to a decided ancestor, then decide back down.
		chain = chain[:0]
		var above pathState
		haveAbove, placeholder := false, false
		onChain := map[*searchSpan]bool{}
		for sp := start; sp != nil; {
			if st, ok := memo[sp]; ok {
				above, haveAbove = st, true
				break
			}
			if onChain[sp] {
				// A parent cycle: dagui's breadcrumb stops at the repeat.
				// Leave everything on it undecided.
				above, haveAbove = pathState{test: true}, true
				break
			}
			onChain[sp] = true
			chain = append(chain, sp)
			parent, ok := sc.parentOf(sp)
			if ok && parent == nil {
				placeholder = true
				break
			}
			sp = parent
		}
		for i := len(chain) - 1; i >= 0; i-- {
			sp := chain[i]
			var st pathState
			switch {
			case haveAbove:
				joined := above.tail + "/" + sp.name
				st = pathState{hit: above.hit || strings.Contains(joined, query), test: above.test, tail: suffix(joined, keep)}
			case placeholder && i == len(chain)-1:
				// Beneath an unreceived parent, whose name is empty.
				joined := "/" + sp.name
				st = pathState{hit: strings.Contains(joined, query), tail: suffix(joined, keep)}
			default:
				st = pathState{hit: strings.Contains(sp.name, query), tail: suffix(sp.name, keep)}
			}
			st.test = st.test || sp.test
			memo[sp] = st
			above, haveAbove = st, true
		}
		st := memo[start]
		switch {
		case st.test && start.match == searchNo:
			start.match = searchMaybe
		case st.hit && start.match == searchNo:
			start.match = searchYes
		}
	}
}

func suffix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// filterStatus marks the spans the status filter may keep.
func (sc *spanSearchScan) filterStatus() {
	var failedLinks map[*searchSpan]bool
	if status := sc.search.Status; status == "FAIL" || status == "failed" {
		failedLinks = sc.causedFailure()
	}
	for _, sp := range sc.order {
		switch sc.search.Status {
		case "", "ok":
			sp.statusMaybe = sc.search.Status == "" || !sp.failed
		case "ERROR":
			sp.statusMaybe = sp.failed
		case "FAIL":
			sp.statusMaybe = !sp.failed && failedLinks[sp]
		case "failed":
			sp.statusMaybe = sp.failed || failedLinks[sp]
		case "run":
			sp.statusMaybe = sp.open
		case "idle":
			sp.statusMaybe = sp.open && sp.agent
		}
	}
}

// causedFailure returns the spans a failed span may cause-link into, over
// any number of links: dagui propagates a failure to every span a failed
// span's cause links reach.
func (sc *spanSearchScan) causedFailure() map[*searchSpan]bool {
	reached := map[*searchSpan]bool{}
	var queue []*searchSpan
	for _, sp := range sc.order {
		if sp.failed {
			queue = append(queue, sp)
		}
	}
	for len(queue) > 0 {
		sp := queue[0]
		queue = queue[1:]
		for _, id := range sc.stores[sp.store].CausalParents(sp.id) {
			target := sc.spans[id]
			if target == nil || reached[target] {
				continue
			}
			reached[target] = true
			queue = append(queue, target)
		}
	}
	return reached
}

// candidates are the searched spans that may match.
func (sc *spanSearchScan) candidates() []*searchSpan {
	var out []*searchSpan
	for _, sp := range sc.order {
		if sp.eligible && sp.validID && sp.match != searchNo && sp.statusMaybe {
			out = append(out, sp)
		}
	}
	return out
}

// load builds a DB holding spans and everything their rows depend on, read
// from their stores in order, as the full load would ingest them.
func (sc *spanSearchScan) load(ctx context.Context, spans []*searchSpan, withRoot bool) (*dagui.DB, error) {
	need := map[*searchSpan]bool{}
	queue := make([]*searchSpan, 0, len(spans))
	for _, sp := range spans {
		if !need[sp] {
			need[sp] = true
			queue = append(queue, sp)
		}
	}
	// The spans cause-linking into them, transitively: their failures are
	// the spans' FAIL. dagui links by span ID, so a linker may be in any
	// store.
	for len(queue) > 0 {
		sp := queue[0]
		queue = queue[1:]
		for _, read := range sc.stores {
			for _, id := range read.CausalChildren(sp.id) {
				linker := sc.spans[id]
				if linker == nil || need[linker] {
					continue
				}
				need[linker] = true
				queue = append(queue, linker)
			}
		}
	}
	if withRoot && sc.root != nil {
		need[sc.root] = true
	}
	// Their ancestors, as dagui links them: by span ID, whichever store
	// holds the parent.
	for sp := range need {
		for p, ok := sc.parentOf(sp); ok && p != nil && !need[p]; p, ok = sc.parentOf(p) {
			need[p] = true
		}
	}
	scopes := make([]map[string]struct{}, len(sc.stores))
	for sp := range need {
		if scopes[sp.store] == nil {
			scopes[sp.store] = map[string]struct{}{}
		}
		scopes[sp.store][sp.id] = struct{}{}
	}
	db := newTraceToolDB()
	primary := sc.stores[0]
	for i, read := range sc.stores {
		if len(scopes[i]) > 0 {
			if err := ingestInspectionSpanScope(ctx, primary, read, db, scopes[i]); err != nil {
				return nil, err
			}
		}
		if err := ingestAgentControl(ctx, read, db); err != nil {
			return nil, err
		}
	}
	return db, nil
}

// spanOf is sp's span in db.
func spanOf(db *dagui.DB, sp *searchSpan) *dagui.Span {
	sid, err := trace.SpanIDFromHex(sp.id)
	if err != nil {
		return nil
	}
	return db.Spans.Map[dagui.SpanID{SpanID: sid}]
}

// renderLoaded lists the candidates from a DB of them all: for a status
// filter, which needs each candidate's status in context.
func (sc *spanSearchScan) renderLoaded(ctx context.Context) (string, int, error) {
	cands := sc.candidates()
	db, err := sc.load(ctx, cands, true)
	if err != nil {
		return "", 0, err
	}
	eligible := make(map[string]bool, len(cands))
	for _, sp := range cands {
		eligible[sp.id] = true
	}
	listing, hidden := renderSpanSearch(db, eligible, sc.search)
	return listing, hidden, nil
}

// renderPage lists the matches of a search without a status filter, which
// the scan decides without a DB, loading only the page it shows.
func (sc *spanSearchScan) renderPage(ctx context.Context) (string, int, error) {
	cands := sc.candidates()
	// Resolve the matches only a DB can decide -- test identities and
	// service hostnames -- and the agent loop spans that say whose work a
	// span is, in a DB of just those and their ancestors.
	var probe []*searchSpan
	for _, sp := range cands {
		if sp.match == searchMaybe {
			probe = append(probe, sp)
		}
	}
	ownership := !sc.search.IncludeSelf && sc.search.Caller.AgentID != ""
	if ownership {
		for _, sp := range sc.order {
			if sp.agent {
				probe = append(probe, sp)
			}
		}
	}
	var probeDB *dagui.DB
	var probeTests *dagui.TestView
	if len(probe) > 0 {
		var err error
		if probeDB, err = sc.load(ctx, probe, false); err != nil {
			return "", 0, err
		}
		probeTests = probeDB.TestView()
	}
	var matches []*searchSpan
	for _, sp := range cands {
		if sp.match == searchMaybe {
			span := spanOf(probeDB, sp)
			if span == nil || !span.Received || !sc.matchesExactly(span, probeTests) {
				continue
			}
		}
		matches = append(matches, sp)
	}
	hidden := 0
	if ownership {
		matches, hidden = sc.dropOwned(matches, probeDB)
	}
	slices.SortFunc(matches, compareSearchSpans)
	// The page: the rows renderSpanMatches can reach.
	search := sc.search
	end := len(matches) - search.Offset
	lo := 0
	if search.Limit > 0 {
		lo = max(0, end-search.Limit)
	}
	spans := make([]*dagui.Span, len(matches))
	if end <= 0 {
		// No page: renderSpanMatches only says so.
		return renderSpanMatches(nil, nil, spans, search), hidden, nil
	}
	page := matches[lo:end]
	db, err := sc.load(ctx, page, true)
	if err != nil {
		return "", 0, err
	}
	for i, sp := range page {
		if spans[lo+i] = spanOf(db, sp); spans[lo+i] == nil {
			return "", 0, fmt.Errorf("span %s vanished from its store", sp.id)
		}
	}
	return renderSpanMatches(db, db.TestView(), spans, search), hidden, nil
}

// dropOwned removes the matches that are the caller's own work, as decided
// by the agent loop spans in probeDB, and counts them.
func (sc *spanSearchScan) dropOwned(matches []*searchSpan, probeDB *dagui.DB) ([]*searchSpan, int) {
	owners := map[*searchSpan]bool{} // agent loop spans: the caller's or not
	for _, sp := range sc.order {
		if !sp.agent {
			continue
		}
		if span := spanOf(probeDB, sp); span != nil && span.Agent && span.AgentID != "" {
			c := sc.search.Caller
			owners[sp] = span.AgentID == c.AgentID && (c.TraceID == "" || span.TraceID.String() == c.TraceID)
		}
	}
	owned := map[*searchSpan]bool{}
	hidden := 0
	kept := matches[:0]
	for _, sp := range matches {
		if sc.owns(sp, owners, owned) {
			hidden++
			continue
		}
		kept = append(kept, sp)
	}
	return kept, hidden
}

// compareSearchSpans orders spans as FindSpans lists them: by start time,
// then trace ID, then span ID.
func compareSearchSpans(a, b *searchSpan) int {
	if a.start != b.start {
		if a.start < b.start {
			return -1
		}
		return 1
	}
	if c := strings.Compare(a.traceID, b.traceID); c != 0 {
		return c
	}
	return strings.Compare(a.spanID, b.spanID)
}

// matchesExactly is the query test renderSpanSearch applies.
func (sc *spanSearchScan) matchesExactly(span *dagui.Span, tests *dagui.TestView) bool {
	query := sc.search.Query
	return spanNameMatches(span, tests, query) ||
		(sc.search.Match == spanMatchPath && strings.Contains(joinSpanPath(spanSearchPath(span, tests)), query))
}

// owns is traceCaller.ownsSpan over the scanned spans: whether sp's nearest
// agent loop span is the caller's.
func (sc *spanSearchScan) owns(sp *searchSpan, owners, memo map[*searchSpan]bool) bool {
	var chain []*searchSpan
	owned := false
	for p := sp; p != nil && len(chain) < 4096; {
		if v, ok := memo[p]; ok {
			owned = v
			break
		}
		chain = append(chain, p)
		if v, ok := owners[p]; ok {
			owned = v
			break
		}
		parent, ok := sc.parentOf(p)
		if ok && parent == nil {
			// An unreceived parent is no agent, and has no parent.
			break
		}
		p = parent
	}
	for _, p := range chain {
		memo[p] = owned
	}
	return owned
}
