package main

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/wcprof"
)

const msNS = int64(1_000_000)

// dumpBuilder assembles a synthetic dump with interned strings.
type dumpBuilder struct {
	t      *testing.T
	strs   []string
	events []wcprof.DumpEvent
	open   []wcprof.DumpOpenOp
}

func newDump(t *testing.T) *dumpBuilder {
	return &dumpBuilder{t: t, strs: []string{""}}
}

func (b *dumpBuilder) id(s string) uint32 {
	for i, v := range b.strs {
		if v == s {
			return uint32(i)
		}
	}
	b.strs = append(b.strs, s)
	return uint32(len(b.strs) - 1)
}

func (b *dumpBuilder) op(opID, parent uint64, kind, class, client, outcome string, start, end int64) {
	b.events = append(b.events, wcprof.DumpEvent{
		Type: "op", OpKind: kind, WorkType: "engine", Outcome: outcome,
		OpID: opID, ParentID: parent, ClassID: b.id(class), ClientID: b.id(client),
		StartNS: start * msNS, EndNS: end * msNS,
	})
}

func (b *dumpBuilder) wait(waiter, target uint64, reason, ident string, start, end int64) {
	b.events = append(b.events, wcprof.DumpEvent{
		Type: "wait", Reason: reason, ParentID: waiter, TargetID: target, IdentID: b.id(ident),
		StartNS: start * msNS, EndNS: end * msNS,
	})
}

// call records a call op executed through its own call_exec op (id+1): the
// call's only content is waiting on the exec, like a real dagql miss.
func (b *dumpBuilder) call(opID, parent uint64, class, client string, start, end int64) {
	b.op(opID, parent, "call", class, client, "executed", start, end)
	b.op(opID+1, opID, "call_exec", class, client, "ok", start, end)
	b.wait(opID, opID+1, "call_exec", "", start, end)
}

func (b *dumpBuilder) graph() *Graph {
	b.t.Helper()
	header := wcprof.DumpHeader{
		SchemaVersion:  wcprof.DumpSchemaVersion,
		EpochUnixNano:  1_000 * msNS,
		DumpedUnixNano: 2_100 * msNS,
		EventCount:     len(b.events),
		Strings:        b.strs,
		OpenOps:        b.open,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(header); err != nil {
		b.t.Fatal(err)
	}
	for _, ev := range b.events {
		if err := enc.Encode(ev); err != nil {
			b.t.Fatal(err)
		}
	}
	g, err := Load(&buf)
	if err != nil {
		b.t.Fatal(err)
	}
	return g
}

// syntheticDump builds a small dump shaped like the Query.node regression:
// two heavy Query.node loads with the same children, a cheap one, a nested
// client hosted by an exec, a lock wait and an op still open at dump time.
func syntheticDump(t *testing.T) *Graph {
	t.Helper()
	b := newDump(t)
	b.op(1, 0, "call", "Query.node", "clientA", "do_not_cache", 0, 100)
	b.op(2, 1, "call", "Query.typeDef", "clientA", "hit", 10, 30)
	b.op(3, 1, "call", "Query.typeDef", "clientA", "hit", 20, 50)
	b.op(4, 1, "call", "TypeDef.withOptional", "clientA", "executed", 55, 58)
	b.wait(1, 0, "lock", "lock:foo", 60, 70)

	b.op(5, 0, "call", "Query.node", "clientA", "do_not_cache", 200, 210)
	b.op(6, 5, "call", "Query.typeDef", "clientA", "hit", 201, 202)
	b.op(7, 5, "call", "Query.typeDef", "clientA", "hit", 203, 204)
	b.op(8, 5, "call", "TypeDef.withOptional", "clientA", "hit", 205, 206)

	b.op(9, 0, "call", "Query.node", "clientB", "hit", 300, 301)

	b.op(10, 0, "exec", "Container.withExec", "clientB", "ok", 400, 1000)
	b.events = append(b.events, wcprof.DumpEvent{Type: "link", LinkKind: "nested_client", ParentID: 10, IdentID: b.id("clientC"), StartNS: 450 * msNS, EndNS: 450 * msNS})
	b.op(11, 0, "call", "Query.typeDef", "clientC", "executed", 500, 510)

	b.open = append(b.open, wcprof.DumpOpenOp{OpID: 12, Kind: "call", WorkType: "engine", ClassID: b.id("Query.node"), ClientID: b.id("clientB"), StartNS: 900 * msNS})
	return b.graph()
}

// treeDump builds a Workspace.withCommit-shaped subtree: executed calls
// folded with their call_exec ops, a burst of same-class siblings, siblings
// of one class at the start and the end, and a lock wait in between.
func treeDump(t *testing.T) *Graph {
	t.Helper()
	b := newDump(t)
	b.call(1, 0, "Workspace.withCommit", "c", 0, 100) // exec op 2
	b.call(3, 2, "GitRef.asWorkspace", "c", 2, 10)    // exec op 4
	b.op(5, 4, "call", "Query.git", "c", "hit", 3, 4)
	for i := range int64(4) {
		b.op(uint64(6+i), 2, "call", "Query.node", "c", "hit", 20+i, 21+i)
	}
	b.op(10, 2, "call", "Directory.entries", "c", "do_not_cache", 30, 40)
	b.op(11, 2, "call", "Query.node", "c", "hit", 50, 51)
	b.wait(2, 0, "lock", "lock:foo", 60, 70)
	b.call(12, 2, "GitRef.asWorkspace", "c", 90, 98) // exec op 13
	return b.graph()
}

func report(t *testing.T, g *Graph, opts Options) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Report(&buf, g, opts); err != nil {
		t.Fatalf("view %s: %v", opts.View, err)
	}
	return buf.String()
}

func assertContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestGraph(t *testing.T) {
	g := syntheticDump(t)
	if len(g.Ops) != 12 {
		t.Fatalf("ops: got %d, want 12", len(g.Ops))
	}
	// 100ms minus the children's union [10,50)+[55,58) and the wait [60,70).
	if got, want := g.ByID[1].Self, 47*msNS; got != want {
		t.Errorf("self of op 1: got %s, want %s", fmtDur(got), fmtDur(want))
	}
	if got := len(g.ByID[1].Children); got != 3 {
		t.Errorf("children of op 1: got %d, want 3", got)
	}
	if p := g.ByID[11].Parent; p == nil || p.ID != 10 || !g.ByID[11].Reparented {
		t.Errorf("op 11 should be re-parented under the exec hosting its client, got parent %v", p)
	}
	if g.ByID[10].Self != 590*msNS {
		t.Errorf("self of exec op 10: got %s", fmtDur(g.ByID[10].Self))
	}
	open := g.ByID[12]
	if !open.Open || open.End != 1_100*msNS || open.Outcome != "open" {
		t.Errorf("open op 12 should end at dump time: %+v", open)
	}
	if g.Start != 0 || g.End != 1_100*msNS {
		t.Errorf("span: got [%d, %d)", g.Start, g.End)
	}
}

func TestSummary(t *testing.T) {
	out := report(t, syntheticDump(t), Options{View: "summary"})
	assertContains(t, out,
		"wcprof dump: 12 ops, 1 waits, 1 links over 1.10s",
		"calls: 11 (",
		"do_not_cache:2",
		"clientA",
		"call Query.typeDef",
	)
}

func TestClasses(t *testing.T) {
	g := syntheticDump(t)
	out := report(t, g, Options{View: "classes", Filter: Filter{Class: regexp.MustCompile(`^Query\.`)}})
	assertContains(t, out, "call Query.node", "call Query.typeDef", "do_not_cache:2")
	if strings.Contains(out, "withOptional") {
		t.Errorf("class filter leaked TypeDef.withOptional:\n%s", out)
	}
	out = report(t, g, Options{View: "classes", Filter: Filter{Client: "clientB"}, Sort: "count"})
	assertContains(t, out, "3 ops in 2 classes")
	if strings.Contains(out, "typeDef") {
		t.Errorf("client filter leaked clientA ops:\n%s", out)
	}
}

func TestBreakdown(t *testing.T) {
	g := syntheticDump(t)
	out := report(t, g, Options{View: "breakdown", Filter: Filter{Class: regexp.MustCompile(`^Query\.node$`)}})
	assertContains(t, out,
		"== call Query.node: 4 ops",
		"direct children (6 in 2 classes)",
		"shapes (ops with an identical child multiset): 2",
		// ops 1 and 5 share a shape; 9 and the open 12 have no children
		"#1: 2 ops",
		"2× call Query.typeDef",
		"1× call TypeDef.withOptional",
		"#2: 2 ops",
		"(no children)",
	)
	var buf bytes.Buffer
	if err := Report(&buf, g, Options{View: "breakdown"}); err == nil {
		t.Error("breakdown without a class regex should fail")
	}
}

func TestClients(t *testing.T) {
	out := report(t, syntheticDump(t), Options{View: "clients", Buckets: 4})
	assertContains(t, out, "from 3 clients", "clientA", "clientB", "clientC", "4× call Query.typeDef")
}

func TestTree(t *testing.T) {
	g := syntheticDump(t)
	out := report(t, g, Options{View: "tree", Op: 1})
	assertContains(t, out,
		"1 call Query.node [do_not_cache] dur 100.00ms self 47.00ms @+0ns",
		"  +10.00ms 2 call Query.typeDef [hit]",
		"  +20.00ms 3 call Query.typeDef [hit]",
		"  +60.00ms ⏳ wait lock 10.00ms → lock:foo",
	)
	// Same-class siblings collapse from the threshold on.
	out = report(t, g, Options{View: "tree", Op: 1, Collapse: 2})
	assertContains(t, out, "+10.00ms..+20.00ms 2× call Query.typeDef [hit:2]")
	// Without an op, the slowest matching op is picked.
	out = report(t, g, Options{View: "tree", Filter: Filter{Class: regexp.MustCompile(`withExec`)}})
	assertContains(t, out, "10 exec Container.withExec", "+100.00ms 11 call Query.typeDef [executed]", "(nested client)")
}

func TestTreeDepth(t *testing.T) {
	g := treeDump(t)
	// Depth counts levels below the root: 1 shows the direct children,
	// marking the ones with unexpanded children.
	out := report(t, g, Options{View: "tree", Op: 1, Depth: 1})
	assertContains(t, out,
		"  +2.00ms 3 call GitRef.asWorkspace [executed] dur 8.00ms self 7.00ms (exec 4) ▸ 1\n",
		"  +90.00ms 12 call GitRef.asWorkspace",
		"raise `depth`",
	)
	if strings.Contains(out, "Query.git") {
		t.Errorf("depth 1 expanded a grandchild:\n%s", out)
	}
	out = report(t, g, Options{View: "tree", Op: 1, Depth: 2})
	assertContains(t, out, "    +1.00ms 5 call Query.git [hit]")
}

func TestTreeFold(t *testing.T) {
	out := report(t, treeDump(t), Options{View: "tree", Op: 1})
	// The call and its call_exec show as one node, with the exec's
	// children directly under it and no call_exec wait.
	assertContains(t, out,
		"1 call Workspace.withCommit [executed] dur 100.00ms self 59.00ms (exec 2) @+0ns\n",
		"\n  +2.00ms 3 call GitRef.asWorkspace [executed] dur 8.00ms self 7.00ms (exec 4)\n",
		"\n    +1.00ms 5 call Query.git [hit]",
	)
	for _, unwanted := range []string{"2 call_exec", "4 call_exec", "wait call_exec"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("folded tree still shows %q:\n%s", unwanted, out)
		}
	}
	// A call doing more than waiting on its exec is not folded.
	b := newDump(t)
	b.call(1, 0, "Workspace.withCommit", "c", 0, 100)
	b.op(3, 1, "lazy", "Directory.sync", "c", "ok", 0, 5)
	out = report(t, b.graph(), Options{View: "tree", Op: 1})
	assertContains(t, out, "1 call Workspace.withCommit [executed] dur 100.00ms self 0ns @", "2 call_exec Workspace.withCommit", "wait call_exec")

	// Result publication runs after the exec ends, while the call is still
	// open: the folded self counts it once, not as both the call's self and
	// the exec's child, and it does not count as an unexpanded child.
	b = newDump(t)
	b.op(10, 0, "call", "Workspace.withCommit", "c", "do_not_cache", 0, 200)
	b.op(1, 10, "call", "Workspace.directory", "c", "executed", 0, 100)
	b.op(2, 1, "call_exec", "Workspace.directory", "c", "ok", 0, 90)
	b.wait(1, 2, "call_exec", "", 0, 90)
	b.op(3, 2, "internal", "dagql.publishResult", "", "ok", 90, 95)
	out = report(t, b.graph(), Options{View: "tree", Op: 10, Depth: 1})
	assertContains(t, out, "  +0ns 1 call Workspace.directory [executed] dur 100.00ms self 95.00ms (exec 2)\n")
	out = report(t, b.graph(), Options{View: "tree", Op: 10, Depth: 2})
	assertContains(t, out, "    +90.00ms 3 internal dagql.publishResult [ok] dur 5.00ms self 5.00ms")
}

func TestTreeOrder(t *testing.T) {
	out := report(t, treeDump(t), Options{View: "tree", Op: 1})
	// Children render in start order: the second GitRef.asWorkspace stays
	// at the end, the Query.node burst collapses into one run, and the
	// straggler after Directory.entries renders on its own.
	want := []string{
		"+2.00ms 3 call GitRef.asWorkspace",
		"+20.00ms..+23.00ms 4× call Query.node [hit:4]",
		"+30.00ms 10 call Directory.entries [do_not_cache]",
		"+50.00ms 11 call Query.node [hit]",
		"+60.00ms ⏳ wait lock 10.00ms → lock:foo",
		"+90.00ms 12 call GitRef.asWorkspace",
	}
	pos := -1
	for _, w := range want {
		i := strings.Index(out, w)
		if i < 0 {
			t.Fatalf("output missing %q:\n%s", w, out)
		}
		if i < pos {
			t.Errorf("%q is out of order:\n%s", w, out)
		}
		pos = i
	}
}

func TestChildren(t *testing.T) {
	g := treeDump(t)
	out := report(t, g, Options{View: "children", Op: 1})
	assertContains(t, out,
		"(folded with its call_exec op 2",
		"8 children in 3 classes, 1 waits totaling 10.00ms; self 59.00ms; sorted by start",
		"         3    +2.00ms     8.00ms     7.00ms  executed       call GitRef.asWorkspace (exec 4) ▸ 1",
		"         6   +20.00ms     1.00ms     1.00ms  hit            call Query.node",
		"         -   +60.00ms    10.00ms          -  -              wait lock → lock:foo",
	)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if got := len(lines); got != 4+9 {
		t.Errorf("want 4 header lines and 9 rows, got %d:\n%s", got, out)
	}
	out = report(t, g, Options{View: "children", Op: 1, Sort: "dur"})
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if !strings.Contains(lines[4], "call Directory.entries") {
		t.Errorf("sorting by dur should put the 10ms Directory.entries first (tied with the wait):\n%s", out)
	}
}

func TestExcludeClass(t *testing.T) {
	g := syntheticDump(t)
	notTypeDef := Filter{ExcludeClass: regexp.MustCompile(`typeDef`)}
	out := report(t, g, Options{View: "classes", Filter: notTypeDef})
	assertContains(t, out, `class!~"typeDef"`, "call Query.node", "call TypeDef.withOptional")
	if strings.Contains(out, "Query.typeDef") {
		t.Errorf("excludeClass leaked Query.typeDef:\n%s", out)
	}
	// It combines with class: everything under Query. except typeDef.
	out = report(t, g, Options{View: "classes", Filter: Filter{Class: regexp.MustCompile(`^Query\.`), ExcludeClass: regexp.MustCompile(`typeDef`)}})
	assertContains(t, out, "4 ops in 1 classes")
	// Tree roots and events honor it too.
	out = report(t, g, Options{View: "tree", Filter: Filter{ExcludeClass: regexp.MustCompile(`withExec|node`)}})
	assertContains(t, out, "tree: slowest of 7 ops", "3 call Query.typeDef")
	var buf bytes.Buffer
	if err := Report(&buf, g, Options{View: "events", Filter: notTypeDef}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"Query.typeDef"`) {
		t.Errorf("excludeClass leaked into events:\n%s", buf.String())
	}
}

func TestInternalExcluded(t *testing.T) {
	b := newDump(t)
	b.call(1, 0, "Query.foo", "c", 0, 10) // exec op 2
	// publication runs after the exec, parented under it; many of these
	// overlap, so their summed self time dwarfs the real work
	for i := range uint64(3) {
		b.op(3+i, 2, "internal", "dagql.publishResult", "", "ok", 10, 1000)
	}
	g := b.graph()
	for _, view := range []string{"summary", "classes"} {
		out := report(t, g, Options{View: view})
		assertContains(t, out, `(left out: 3 internal-kind ops, self_tot 2.97s; kind: "internal" ranks them)`, "call Query.foo")
		if strings.Contains(out, "internal dagql.publishResult") {
			t.Errorf("%s ranks internal ops by default:\n%s", view, out)
		}
		assertContains(t, out, "concurrent")
	}
	out := report(t, g, Options{View: "classes", Filter: Filter{Kind: "internal"}})
	assertContains(t, out, "internal dagql.publishResult")
	if strings.Contains(out, "left out") {
		t.Errorf("kind internal should rank internal ops, not leave them out:\n%s", out)
	}
}

func TestCompare(t *testing.T) {
	before := newDump(t)
	before.op(1, 0, "call", "Query.node", "c", "hit", 0, 100)
	before.op(2, 0, "call", "Query.node", "c", "hit", 200, 300)
	before.op(3, 0, "call", "Query.typeDef", "c", "hit", 400, 410)
	before.op(4, 0, "internal", "dagql.publishResult", "", "ok", 0, 500)
	after := newDump(t)
	after.op(1, 0, "call", "Query.node", "c", "hit", 0, 10)
	after.op(2, 0, "call", "Query.node", "c", "hit", 20, 30)
	after.op(3, 0, "call", "Query.typeDef", "c", "hit", 40, 60)
	after.op(4, 0, "call", "Host.directory", "c", "executed", 70, 75)

	g := after.graph()
	out := report(t, g, Options{View: "compare", Against: before.graph(), Name: "after", AgainstName: "before"})
	assertContains(t, out,
		`compare "after" (4 ops over 75.00ms) against "before" (4 ops over 500.00ms)`,
		"before → after",
		"(left out: 1 → 0 internal-kind ops",
		"      3       4   210.00ms   45.00ms  -165.00ms   0.21", // all matching ops
		"      2       2   200.00ms   20.00ms  -180.00ms   0.10   100.00ms   10.00ms   0.10   200.00ms   20.00ms  -180.00ms  call Query.node",
		"      0       1        0ns    5.00ms    +5.00ms    new        0ns    5.00ms    new",
	)
	// Sorted by the largest absolute change in duration total.
	iNode, iTypeDef, iHost := strings.Index(out, "call Query.node"), strings.Index(out, "call Query.typeDef"), strings.Index(out, "call Host.directory")
	if iNode >= iTypeDef || iTypeDef >= iHost {
		t.Errorf("rows not sorted by |Δdur|:\n%s", out)
	}
	// Filters apply to both sides.
	out = report(t, g, Options{View: "compare", Against: before.graph(), Filter: Filter{ExcludeClass: regexp.MustCompile(`node`)}})
	if strings.Contains(out, "Query.node") {
		t.Errorf("excludeClass leaked into compare:\n%s", out)
	}
	var buf bytes.Buffer
	if err := Report(&buf, g, Options{View: "compare"}); err == nil {
		t.Error("compare without a baseline should fail")
	}
}

// critDump builds a root with overlapping children, a singleflight join into
// another caller's shared execution, a folded call and a lock wait:
//
//	1 call Workspace.withCommit        [0,100)
//	  2 call Query.a                   [0,30)   overlaps 3; only [0,10) is on the path
//	  3 call Query.b [joined]          [10,60)  ⏳ singleflight → 20 [10,60)
//	  4 call Query.c (exec 5)          [70,90)
//	  ⏳ lock lock:foo                  [90,95)
//	21 call Query.b [executed]         [5,60)   the executor, another root
//	  20 call_exec Query.b             [5,60)
//	    22 lazy Directory.sync         [20,55)
func critDump(t *testing.T) *Graph {
	t.Helper()
	b := newDump(t)
	b.op(1, 0, "call", "Workspace.withCommit", "c", "do_not_cache", 0, 100)
	b.op(2, 1, "call", "Query.a", "c", "hit", 0, 30)
	b.op(3, 1, "call", "Query.b", "c", "joined", 10, 60)
	b.wait(3, 20, "singleflight", "", 10, 60)
	b.call(4, 1, "Query.c", "c", 70, 90) // exec op 5
	b.wait(1, 0, "lock", "lock:foo", 90, 95)
	b.op(21, 0, "call", "Query.b", "d", "executed", 5, 60)
	b.op(20, 21, "call_exec", "Query.b", "d", "ok", 5, 60)
	b.wait(21, 20, "call_exec", "", 5, 60)
	b.op(22, 20, "lazy", "Directory.sync", "d", "ok", 20, 55)
	return b.graph()
}

func TestCritpath(t *testing.T) {
	g := critDump(t)
	out := report(t, g, Options{View: "critpath", Op: 1})
	// On-path time sums to the root's 100ms. Query.a overlaps Query.b, so
	// only its first 10ms count; the joiner's 50ms go to the shared exec
	// and the lazy op under it, not to the joining call.
	assertContains(t, out,
		"critpath of op 1: 100.00ms",
		"     35.00ms  35.0%        1  lazy Directory.sync\n",
		"     20.00ms  20.0%        1  call Query.c\n",
		"     15.00ms  15.0%        1  call Workspace.withCommit\n",
		"     15.00ms  15.0%        1  call_exec Query.b\n",
		"     10.00ms  10.0%        1  call Query.a\n",
		"      5.00ms   5.0%        1  wait lock lock:foo\n",
		"         0ns   0.0%        1  call Query.b\n",
	)
	// The path, in time order, following the join into the shared exec.
	want := []string{
		"\n+0ns dur 100.00ms own 15.00ms  1 call Workspace.withCommit [do_not_cache]\n",
		"\n  +0ns dur 10.00ms own 10.00ms  2 call Query.a [hit]\n",
		"\n  +10.00ms dur 50.00ms own 0ns  3 call Query.b [joined]\n",
		"\n    +10.00ms dur 50.00ms own 15.00ms  ⏳ singleflight → 20 call_exec Query.b [ok]\n",
		"\n      +20.00ms dur 35.00ms own 35.00ms  22 lazy Directory.sync [ok]\n",
		"\n  +70.00ms dur 20.00ms own 20.00ms  4 call Query.c [executed] (exec 5)\n",
		"\n  +90.00ms dur 5.00ms own 5.00ms  ⏳ lock → lock:foo\n",
	}
	pos := -1
	for _, w := range want {
		i := strings.Index(out, w)
		if i < 0 {
			t.Fatalf("output missing %q:\n%s", w, out)
		}
		if i < pos {
			t.Errorf("%q is out of order:\n%s", w, out)
		}
		pos = i
	}

	// Without an op, every outermost matching op is walked and summed.
	out = report(t, g, Options{View: "critpath", Filter: Filter{Class: regexp.MustCompile(`^Query\.b$`), Kind: "call"}})
	assertContains(t, out,
		"critpath of 2 ops matching",
		"105.00ms summed",
		"     70.00ms  66.7%        2  lazy Directory.sync\n",
		"+0ns dur 55.00ms own 20.00ms  21 call Query.b [executed] (exec 20)",
	)

	// Depth bounds the printed path, not the attribution.
	out = report(t, g, Options{View: "critpath", Op: 1, Depth: 1})
	assertContains(t, out, "3 call Query.b [joined] ▸ 1", "35.00ms  35.0%")
	if strings.Contains(out, "22 lazy Directory.sync [ok]") {
		t.Errorf("depth 1 printed a grandchild:\n%s", out)
	}
}

// nestedClientDump builds a module function call whose exec hosts a nested
// client, with the nested_client link recorded from hostOp: the exec (3), or
// its setupNestedClient phase (4) as engines before the link moved recorded
// it.
func nestedClientDump(t *testing.T, hostOp uint64) *Graph {
	t.Helper()
	b := newDump(t)
	b.call(1, 0, "mod:Mod.fn", "c", 0, 100) // exec op 2
	b.op(3, 2, "exec", "exec.run", "c", "ok", 5, 100)
	b.op(4, 3, "exec_phase", "exec.setupNestedClient", "", "ok", 5, 6)
	b.op(5, 3, "exec_phase", "exec.runContainer", "", "ok", 6, 100)
	b.op(6, 5, "exec_phase", "exec.containerStart", "", "ok", 6, 10)
	b.op(7, 5, "exec_phase", "exec.processRun", "", "ok", 10, 100)
	b.events = append(b.events, wcprof.DumpEvent{Type: "link", LinkKind: "nested_client", ParentID: hostOp, IdentID: b.id("nested"), StartNS: 15 * msNS, EndNS: 15 * msNS})
	b.op(8, 0, "session_phase", "session.serveQuery", "nested", "ok", 20, 80)
	b.call(9, 8, "Container.withExec", "nested", 21, 79) // exec op 10
	return b.graph()
}

func TestCritpathNestedClient(t *testing.T) {
	for _, host := range []uint64{3, 4} {
		g := nestedClientDump(t, host)
		if p := g.ByID[8].Parent; p == nil || p.ID != 7 || !g.ByID[8].Reparented {
			t.Errorf("host %d: the nested client's root should be under the exec's processRun, got parent %v", host, p)
		}
		// 90ms of process run minus the 60ms request.
		if got, want := g.ByID[7].Self, 30*msNS; got != want {
			t.Errorf("host %d: self of processRun: got %s, want %s", host, fmtDur(got), fmtDur(want))
		}
		out := report(t, g, Options{View: "critpath", Op: 1})
		assertContains(t, out,
			"     58.00ms  58.0%        1  call Container.withExec\n",
			"     30.00ms  30.0%        1  exec_phase exec.processRun\n",
		)
		want := []string{
			"\n      +10.00ms dur 90.00ms own 30.00ms  7 exec_phase exec.processRun [ok]\n",
			"\n        +20.00ms dur 60.00ms own 2.00ms  8 session_phase session.serveQuery [ok]\n",
			"\n          +21.00ms dur 58.00ms own 58.00ms  9 call Container.withExec [executed] (exec 10)\n",
		}
		pos := -1
		for _, w := range want {
			i := strings.Index(out, w)
			if i < 0 {
				t.Fatalf("host %d: output missing %q:\n%s", host, w, out)
			}
			if i < pos {
				t.Errorf("host %d: %q is out of order:\n%s", host, w, out)
			}
			pos = i
		}
	}
}

func TestCritpathShortSegments(t *testing.T) {
	// The threshold is 1% of the 10s path: 100ms.
	b := newDump(t)
	b.op(1, 0, "call", "Query.root", "c", "do_not_cache", 0, 10000)
	// A run of short segments totaling 5ms: hidden, and counted.
	for i := range int64(5) {
		b.op(uint64(10+i), 1, "call", "Query.tiny", "c", "hit", 100+2*i, 101+2*i)
	}
	b.op(20, 1, "call", "Query.big", "c", "hit", 200, 5000)
	// A run of short segments totaling 170ms: its two largest print until
	// the rest (90ms) is under the threshold, and the rest folds.
	b.op(30, 1, "call", "Query.mid", "c", "hit", 5000, 5040)
	for i := range int64(10) {
		b.op(uint64(40+i), 1, "call", "Query.small", "c", "hit", 5040+5*i, 5045+5*i)
	}
	b.op(31, 1, "call", "Query.mid", "c", "hit", 5090, 5130)
	b.op(32, 1, "call", "Query.mid", "c", "hit", 5130, 5170)
	out := report(t, b.graph(), Options{View: "critpath", Op: 1})
	want := []string{
		"\n  +200.00ms dur 4.80s own 4.80s  20 call Query.big [hit]\n",
		"\n  +5.00s dur 40.00ms own 40.00ms  30 call Query.mid [hit]\n",
		"\n  +5.04s..+5.17s … 11 shorter segments, 90.00ms: call Query.small:10 call Query.mid:1\n",
		"\n  +5.09s dur 40.00ms own 40.00ms  31 call Query.mid [hit]\n",
		"\n(5 segments not shown: runs of them totaling under 1% of the path, 5.00ms)\n",
	}
	pos := -1
	for _, w := range want {
		i := strings.Index(out, w)
		if i < 0 {
			t.Fatalf("output missing %q:\n%s", w, out)
		}
		if i < pos {
			t.Errorf("%q is out of order:\n%s", w, out)
		}
		pos = i
	}
	if strings.Contains(out, "Query.tiny [hit]") || strings.Contains(out, "32 call Query.mid") {
		t.Errorf("printed a segment that should be hidden or folded:\n%s", out)
	}
}

func TestWaits(t *testing.T) {
	g := critDump(t)
	out := report(t, g, Options{View: "waits"})
	assertContains(t, out,
		"waits: 2 (waiters matching all ops), 55.00ms summed",
		"(left out: 2 waits of a call on its own call_exec child",
		"by reason: singleflight 1 (50.00ms), lock 1 (5.00ms)",
		"        1       1    50.00ms   50.00ms   50.00ms  singleflight call_exec Query.b\n",
		"        1       1     5.00ms    5.00ms    5.00ms  lock         lock:foo\n",
		"        1    50.00ms   50.00ms   50.00ms  singleflight call Query.b → call_exec Query.b\n",
		"        1     5.00ms    5.00ms    5.00ms  lock         call Workspace.withCommit → lock:foo\n",
	)
	// Filters select waits by their waiter.
	out = report(t, g, Options{View: "waits", Filter: Filter{ExcludeClass: regexp.MustCompile(`withCommit`)}})
	assertContains(t, out, "waits: 1 (")
	if strings.Contains(out, "lock:foo") {
		t.Errorf("excludeClass on the waiter leaked the lock wait:\n%s", out)
	}
}

func TestEvents(t *testing.T) {
	g := syntheticDump(t)
	var buf bytes.Buffer
	if err := Report(&buf, g, Options{View: "events", Filter: Filter{Client: "clientA"}}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	// 8 clientA ops + the wait whose waiter (op 1) is a clientA op.
	if len(lines) != 9 {
		t.Fatalf("got %d events, want 9:\n%s", len(lines), buf.String())
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &ev); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"type": "op", "id": 2.0, "kind": "call", "class": "Query.typeDef", "client": "clientA",
		"parent": 1.0, "parent_class": "Query.node", "outcome": "hit",
		"start_ms": 10.0, "dur_ms": 20.0, "self_ms": 20.0,
	}
	for k, v := range want {
		if ev[k] != v {
			t.Errorf("event field %s: got %v, want %v (event %s)", k, ev[k], v, lines[1])
		}
	}
	var wait map[string]any
	if err := json.Unmarshal([]byte(lines[8]), &wait); err != nil {
		t.Fatal(err)
	}
	if wait["type"] != "wait" || wait["reason"] != "lock" || wait["waiter_class"] != "Query.node" || wait["dur_ms"] != 10.0 {
		t.Errorf("unexpected wait event %s", lines[8])
	}
}

func TestLimit(t *testing.T) {
	out := report(t, syntheticDump(t), Options{View: "summary", Limit: 3})
	if got := strings.Count(out, "\n"); got != 4 {
		t.Errorf("want 3 lines plus the truncation note, got %d:\n%s", got, out)
	}
	assertContains(t, out, "more lines truncated")
}
