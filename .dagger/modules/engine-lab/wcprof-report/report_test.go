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

// syntheticDump builds a small dump shaped like the Query.node regression:
// two heavy Query.node loads with the same children, a cheap one, a nested
// client hosted by an exec, a lock wait and an op still open at dump time.
func syntheticDump(t *testing.T) *Graph {
	t.Helper()
	strs := []string{"", "Query.node", "Query.typeDef", "TypeDef.withOptional", "clientA", "clientB", "Container.withExec", "lock:foo", "clientC"}
	id := func(s string) uint32 {
		for i, v := range strs {
			if v == s {
				return uint32(i)
			}
		}
		t.Fatalf("unknown string %q", s)
		return 0
	}
	op := func(opID, parent uint64, kind, class, client, outcome string, start, end int64) wcprof.DumpEvent {
		return wcprof.DumpEvent{
			Type: "op", OpKind: kind, WorkType: "engine", Outcome: outcome,
			OpID: opID, ParentID: parent, ClassID: id(class), ClientID: id(client),
			StartNS: start * msNS, EndNS: end * msNS,
		}
	}
	events := []wcprof.DumpEvent{
		op(1, 0, "call", "Query.node", "clientA", "do_not_cache", 0, 100),
		op(2, 1, "call", "Query.typeDef", "clientA", "hit", 10, 30),
		op(3, 1, "call", "Query.typeDef", "clientA", "hit", 20, 50),
		op(4, 1, "call", "TypeDef.withOptional", "clientA", "executed", 55, 58),
		{Type: "wait", Reason: "lock", ParentID: 1, IdentID: id("lock:foo"), StartNS: 60 * msNS, EndNS: 70 * msNS},

		op(5, 0, "call", "Query.node", "clientA", "do_not_cache", 200, 210),
		op(6, 5, "call", "Query.typeDef", "clientA", "hit", 201, 202),
		op(7, 5, "call", "Query.typeDef", "clientA", "hit", 203, 204),
		op(8, 5, "call", "TypeDef.withOptional", "clientA", "hit", 205, 206),

		op(9, 0, "call", "Query.node", "clientB", "hit", 300, 301),

		op(10, 0, "exec", "Container.withExec", "clientB", "ok", 400, 1000),
		{Type: "link", LinkKind: "nested_client", ParentID: 10, IdentID: id("clientC"), StartNS: 450 * msNS, EndNS: 450 * msNS},
		op(11, 0, "call", "Query.typeDef", "clientC", "executed", 500, 510),
	}
	header := wcprof.DumpHeader{
		SchemaVersion:  wcprof.DumpSchemaVersion,
		EpochUnixNano:  1_000 * msNS,
		DumpedUnixNano: 2_100 * msNS,
		EventCount:     len(events),
		Strings:        strs,
		OpenOps: []wcprof.DumpOpenOp{
			{OpID: 12, Kind: "call", WorkType: "engine", ClassID: id("Query.node"), ClientID: id("clientB"), StartNS: 900 * msNS},
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(header); err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
	g, err := Load(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return g
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
		"1 call Query.node [do_not_cache] dur 100.00ms self 47.00ms",
		"⏳ wait lock 10.00ms → lock:foo",
		"  2 call Query.typeDef [hit]",
		"  3 call Query.typeDef [hit]",
	)
	// Same-class siblings collapse from the threshold on.
	out = report(t, g, Options{View: "tree", Op: 1, Collapse: 2})
	assertContains(t, out, "2× call Query.typeDef [hit:2]")
	// Without an op, the slowest matching op is picked.
	out = report(t, g, Options{View: "tree", Filter: Filter{Class: regexp.MustCompile(`withExec`)}})
	assertContains(t, out, "10 exec Container.withExec", "11 call Query.typeDef [executed]", "(nested client)")
	// Depth bounds the recursion.
	out = report(t, g, Options{View: "tree", Op: 1, Depth: 1})
	assertContains(t, out, "… 3 children (raise `depth`)")
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
