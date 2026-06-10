package wcanalyze

import (
	"bytes"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/wcprof"
)

const ms = int64(time.Millisecond)

// fixtureStrings builds a string table and a lookup from value to ID.
type fixtureStrings struct {
	values []string
	ids    map[string]uint32
}

func newFixtureStrings() *fixtureStrings {
	return &fixtureStrings{values: []string{""}, ids: map[string]uint32{"": 0}}
}

func (s *fixtureStrings) id(v string) uint32 {
	if id, ok := s.ids[v]; ok {
		return id
	}
	id := uint32(len(s.values))
	s.values = append(s.values, v)
	s.ids[v] = id
	return id
}

func opEvent(s *fixtureStrings, id, parent uint64, kind, class, ident, outcome string, startNS, endNS int64) wcprof.DumpEvent {
	return wcprof.DumpEvent{
		Type: "op", OpKind: kind, WorkType: "engine", Outcome: outcome,
		OpID: id, ParentID: parent,
		ClassID: s.id(class), IdentID: s.id(ident),
		StartNS: startNS, EndNS: endNS,
	}
}

func waitEvent(s *fixtureStrings, waiter, target uint64, ident, reason string, startNS, endNS int64) wcprof.DumpEvent {
	return wcprof.DumpEvent{
		Type: "wait", Reason: reason,
		ParentID: waiter, TargetID: target, IdentID: s.id(ident),
		StartNS: startNS, EndNS: endNS,
	}
}

// sequentialFixture: a root session op running two sequential calls; each
// call op waits on its call_exec child.
//
//	R [0,1000ms]
//	├── C1 call (executed) [0,400] ── waits on E1
//	│    └── E1 call_exec Container.withExec [0,400] (self 400ms)
//	└── C2 call (executed) [400,1000] ── waits on E2
//	     └── E2 call_exec Container.from [400,1000] (self 600ms)
func sequentialFixture(t *testing.T) *Graph {
	t.Helper()
	s := newFixtureStrings()
	events := []wcprof.DumpEvent{
		opEvent(s, 1, 0, "session_phase", "session.query", "", "ok", 0, 1000*ms),
		opEvent(s, 2, 1, "call", "Container.withExec", "d1", "executed", 0, 400*ms),
		opEvent(s, 3, 2, "call_exec", "Container.withExec", "d1", "ok", 0, 400*ms),
		waitEvent(s, 2, 3, "", "call_exec", 0, 400*ms),
		opEvent(s, 4, 1, "call", "Container.from", "d2", "executed", 400*ms, 1000*ms),
		opEvent(s, 5, 4, "call_exec", "Container.from", "d2", "ok", 400*ms, 1000*ms),
		waitEvent(s, 4, 5, "", "call_exec", 400*ms, 1000*ms),
	}
	header := &wcprof.DumpHeader{
		SchemaVersion: wcprof.DumpSchemaVersion,
		Strings:       s.values,
		EventCount:    len(events),
	}
	g, err := Build(header, events)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestSelfTime(t *testing.T) {
	g := sequentialFixture(t)

	if got := g.Ops[3].SelfNS(); got != 400*ms {
		t.Fatalf("E1 self = %v, want 400ms", time.Duration(got))
	}
	if got := g.Ops[5].SelfNS(); got != 600*ms {
		t.Fatalf("E2 self = %v, want 600ms", time.Duration(got))
	}
	// the call op's time is fully covered by its child + wait
	if got := g.Ops[2].SelfNS(); got != 0 {
		t.Fatalf("C1 self = %v, want 0", time.Duration(got))
	}
	// the root's time is fully covered by its children
	if got := g.Ops[1].SelfNS(); got != 0 {
		t.Fatalf("R self = %v, want 0", time.Duration(got))
	}
}

func TestBaselineMatchesActual(t *testing.T) {
	g := sequentialFixture(t)
	if got := ActualMakespanNS(g); got != 1000*ms {
		t.Fatalf("actual makespan = %v, want 1s", time.Duration(got))
	}
	sim := NewSimulation(g, nil)
	makespan, err := sim.Run()
	if err != nil {
		t.Fatal(err)
	}
	if makespan != 1000*ms {
		t.Fatalf("baseline makespan = %v, want 1s", time.Duration(makespan))
	}
	if sim.CycleWarnings != 0 {
		t.Fatalf("unexpected cycle warnings: %d", sim.CycleWarnings)
	}
}

func TestWhatIfSequential(t *testing.T) {
	g := sequentialFixture(t)

	cases := []struct {
		class  string
		factor float64
		want   int64
	}{
		// halving withExec's 400ms self saves 200ms end to end (C2 starts earlier)
		{"Container.withExec", 0.5, 200 * ms},
		// eliminating it saves the full 400ms
		{"Container.withExec", 0, 400 * ms},
		// halving from's 600ms saves 300ms
		{"Container.from", 0.5, 300 * ms},
	}
	for _, tc := range cases {
		sim := NewSimulation(g, map[ClassKey]float64{
			{Kind: "call_exec", Class: tc.class}: tc.factor,
		})
		makespan, err := sim.Run()
		if err != nil {
			t.Fatal(err)
		}
		saved := 1000*ms - makespan
		if saved != tc.want {
			t.Fatalf("whatif %s x%v: saved %v, want %v", tc.class, tc.factor, time.Duration(saved), time.Duration(tc.want))
		}
	}
}

// singleflight: two callers join one execution; speeding the class up should
// count the shared work once.
func TestWhatIfSingleflight(t *testing.T) {
	s := newFixtureStrings()
	events := []wcprof.DumpEvent{
		opEvent(s, 1, 0, "session_phase", "session.query", "", "ok", 0, 400*ms),
		opEvent(s, 2, 1, "call", "Container.withExec", "d1", "executed", 0, 400*ms),
		opEvent(s, 3, 2, "call_exec", "Container.withExec", "d1", "ok", 0, 400*ms),
		waitEvent(s, 2, 3, "", "call_exec", 0, 400*ms),
		opEvent(s, 4, 1, "call", "Container.withExec", "d1", "joined", 0, 400*ms),
		waitEvent(s, 4, 3, "", "singleflight", 0, 400*ms),
	}
	header := &wcprof.DumpHeader{SchemaVersion: wcprof.DumpSchemaVersion, Strings: s.values, EventCount: len(events)}
	g, err := Build(header, events)
	if err != nil {
		t.Fatal(err)
	}

	sim := NewSimulation(g, nil)
	baseline, err := sim.Run()
	if err != nil {
		t.Fatal(err)
	}
	if baseline != 400*ms {
		t.Fatalf("baseline = %v, want 400ms", time.Duration(baseline))
	}

	sim = NewSimulation(g, map[ClassKey]float64{
		{Kind: "call_exec", Class: "Container.withExec"}: 0.5,
	})
	makespan, err := sim.Run()
	if err != nil {
		t.Fatal(err)
	}
	if saved := baseline - makespan; saved != 200*ms {
		t.Fatalf("singleflight saved = %v, want 200ms", time.Duration(saved))
	}
}

// Two sequential roots with an idle gap between them: the gap is preserved,
// and savings in the first root pull the second root earlier.
func TestRootChaining(t *testing.T) {
	s := newFixtureStrings()
	events := []wcprof.DumpEvent{
		opEvent(s, 1, 0, "session_phase", "session.query", "", "ok", 0, 100*ms),
		opEvent(s, 2, 0, "session_phase", "session.query2", "", "ok", 150*ms, 250*ms),
	}
	header := &wcprof.DumpHeader{SchemaVersion: wcprof.DumpSchemaVersion, Strings: s.values, EventCount: len(events)}
	g, err := Build(header, events)
	if err != nil {
		t.Fatal(err)
	}

	sim := NewSimulation(g, nil)
	baseline, err := sim.Run()
	if err != nil {
		t.Fatal(err)
	}
	if baseline != 250*ms {
		t.Fatalf("baseline = %v, want 250ms", time.Duration(baseline))
	}

	sim = NewSimulation(g, map[ClassKey]float64{
		{Kind: "session_phase", Class: "session.query"}: 0,
	})
	makespan, err := sim.Run()
	if err != nil {
		t.Fatal(err)
	}
	// first root drops to 0; the 50ms think-time gap is preserved; second
	// root still takes 100ms => 150ms total
	if makespan != 150*ms {
		t.Fatalf("makespan = %v, want 150ms", time.Duration(makespan))
	}
}

// exec-reason waits with no target op ID resolve to the exec op by ident.
func TestExecIdentWaitResolution(t *testing.T) {
	s := newFixtureStrings()
	events := []wcprof.DumpEvent{
		opEvent(s, 1, 0, "session_phase", "session.query", "", "ok", 0, 500*ms),
		opEvent(s, 2, 1, "lazy", "Container.withExec", "", "ok", 0, 500*ms),
		opEvent(s, 3, 2, "exec", "exec.run", "callDigest1", "ok", 100*ms, 500*ms),
		waitEvent(s, 2, 0, "callDigest1", "exec", 100*ms, 500*ms),
	}
	header := &wcprof.DumpHeader{SchemaVersion: wcprof.DumpSchemaVersion, Strings: s.values, EventCount: len(events)}
	g, err := Build(header, events)
	if err != nil {
		t.Fatal(err)
	}
	lazyOp := g.Ops[2]
	if len(lazyOp.Waits) != 1 {
		t.Fatalf("lazy op waits = %d, want 1", len(lazyOp.Waits))
	}
	if lazyOp.Waits[0].Target == nil || lazyOp.Waits[0].Target.ID != 3 {
		t.Fatalf("exec ident wait did not resolve to exec op: %+v", lazyOp.Waits[0])
	}
	// lazy self: 500 - exec child(100..500) - wait(100..500) = first 100ms
	if got := lazyOp.SelfNS(); got != 100*ms {
		t.Fatalf("lazy self = %v, want 100ms", time.Duration(got))
	}
}

// nested-client link reparents the nested session root under the exec op.
func TestNestedClientReparenting(t *testing.T) {
	s := newFixtureStrings()
	events := []wcprof.DumpEvent{
		opEvent(s, 1, 0, "session_phase", "session.query", "", "ok", 0, 500*ms),
		opEvent(s, 2, 1, "exec", "exec.run", "dig", "ok", 0, 500*ms),
		{Type: "link", LinkKind: "nested_client", ParentID: 2, IdentID: s.id("nested-client-1")},
		// nested client's own query root: no recorded parent, but carries the
		// nested client ID
		{
			Type: "op", OpKind: "session_phase", WorkType: "engine", Outcome: "ok",
			OpID: 3, ParentID: 0, ClassID: s.id("session.serveQuery"), ClientID: s.id("nested-client-1"),
			StartNS: 100 * ms, EndNS: 400 * ms,
		},
	}
	header := &wcprof.DumpHeader{SchemaVersion: wcprof.DumpSchemaVersion, Strings: s.values, EventCount: len(events)}
	g, err := Build(header, events)
	if err != nil {
		t.Fatal(err)
	}
	nested := g.Ops[3]
	if nested.Parent == nil || nested.Parent.ID != 2 {
		t.Fatalf("nested root not reparented under exec, parent=%+v", nested.Parent)
	}
	if !nested.Reparented {
		t.Fatal("expected Reparented flag")
	}
	if len(g.Roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(g.Roots))
	}
}

func TestRunWhatIfsRanking(t *testing.T) {
	g := sequentialFixture(t)
	baseline, results, err := RunWhatIfs(g, []float64{0, 0.5}, ms)
	if err != nil {
		t.Fatal(err)
	}
	if baseline != 1000*ms {
		t.Fatalf("baseline = %v", time.Duration(baseline))
	}
	if len(results) < 2 {
		t.Fatalf("expected >=2 classes, got %d", len(results))
	}
	bySaving := map[string]int64{}
	for _, res := range results {
		if res.Key.Kind == "call_exec" {
			bySaving[res.Key.Class] = res.SavedNS[0.5]
		}
	}
	if bySaving["Container.from"] != 300*ms || bySaving["Container.withExec"] != 200*ms {
		t.Fatalf("unexpected what-if savings: %+v", bySaving)
	}
}

func TestReportRenders(t *testing.T) {
	g := sequentialFixture(t)
	var buf bytes.Buffer
	if err := WriteReport(&buf, g, ReportOptions{}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"what-if", "Container.withExec", "top classes", "blocking chain"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}
