package main

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/wcprof"
)

// exec records an exec_phase op carrying a user command, as the recorder
// writes exec.processRun.
func (b *dumpBuilder) exec(opID, parent uint64, argv []string, start, end int64) {
	meta, err := json.Marshal(argv)
	if err != nil {
		b.t.Fatal(err)
	}
	b.events = append(b.events, wcprof.DumpEvent{
		Type: "op", OpKind: "exec_phase", WorkType: "user", Outcome: "ok",
		OpID: opID, ParentID: parent, ClassID: b.id("exec.processRun"), MetaID: b.id(string(meta)),
		StartNS: start * msNS, EndNS: end * msNS,
	})
}

// twoPackages is a walk of two package requests, each a call holding a lazy
// op holding one exec with a compile script naming its package.
func twoPackages(t *testing.T) *Graph {
	b := newDump(t)
	b.op(1, 0, "call", "Gobuild.binary", "m", "executed", 0, 100)
	b.op(2, 1, "call", "Directory.sync", "m", "executed", 0, 40)
	b.op(3, 2, "lazy", "Container.withExec", "m", "ok", 5, 40)
	b.exec(4, 3, []string{"sh", "-c", "set -e\n# example.com/a\ncompile"}, 10, 35)
	b.op(5, 1, "call", "Directory.sync", "m", "executed", 30, 90)
	b.op(6, 5, "lazy", "Container.withExec", "m", "ok", 35, 90)
	b.exec(7, 6, []string{"sh", "-c", "set -e\n# example.com/b\ncompile"}, 40, 85)
	return b.graph()
}

var pkgLabel = regexp.MustCompile(`\n# ([^\n]+)\n`)

func TestLabelBy(t *testing.T) {
	g := twoPackages(t)
	if n := g.LabelBy(pkgLabel); n != 2 {
		t.Fatalf("matched %d commands, want 2", n)
	}
	for id, want := range map[uint64]string{2: "example.com/a", 3: "example.com/a", 4: "example.com/a", 5: "example.com/b", 7: "example.com/b", 1: ""} {
		if got := g.ByID[id].Label; got != want {
			t.Errorf("op %d label %q, want %q", id, got, want)
		}
	}
}

func TestAncestors(t *testing.T) {
	g := twoPackages(t)
	got := report(t, g, Options{View: "ancestors", Filter: Filter{Class: regexp.MustCompile(`^exec\.processRun$`)}, Levels: 2})
	want := "exec_phase exec.processRun ← lazy Container.withExec ← call Directory.sync"
	if !strings.Contains(got, want) || !strings.Contains(got, "       2    70.00ms    45.00ms  ") {
		t.Errorf("ancestors missing %q with 2 ops and 70ms:\n%s", want, got)
	}
}

func TestOccupancy(t *testing.T) {
	g := twoPackages(t)
	// The two execs: [10,35] and [40,85] over a 100ms dump: 70ms in progress,
	// never together; 25+10 ms in the first 50ms bucket, 35 ms in the second.
	got := report(t, g, Options{View: "occupancy", Filter: Filter{Class: regexp.MustCompile(`^exec\.processRun$`)}, Buckets: 2})
	if !strings.Contains(got, "       2    0.7      1 |    0.7    0.7  exec_phase exec.processRun") {
		t.Errorf("unexpected occupancy:\n%s", got)
	}
	g.LabelBy(pkgLabel)
	got = report(t, g, Options{View: "occupancy", By: "label", Filter: Filter{Class: regexp.MustCompile(`^Directory\.sync$`)}, Buckets: 1})
	if !strings.Contains(got, "example.com/b") || !strings.Contains(got, "example.com/a") {
		t.Errorf("occupancy by label misses a package:\n%s", got)
	}
}

func TestWindowQuantities(t *testing.T) {
	// The two package requests, [0,40] and [30,90], overlap in [30,40]:
	// occupied counts it once (90ms), self time sums both requests' own 5ms.
	// The chain: the second request's exec [40,85], its lazy op from 35, its
	// request from 30, the first request's exec from 10 back to its lazy op.
	g := twoPackages(t)
	got := report(t, g, Options{View: "window", Op: 1, FromMS: -1, ToMS: -1})
	for _, want := range []string{
		"on the critical chain (sums to the window):",
		"     65.00ms  65.0%  exec_phase exec.processRun",
		"     90.00ms    10.00ms      2  call Directory.sync",
		"     70.00ms    70.00ms      2  exec_phase exec.processRun",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("window missing %q:\n%s", want, got)
		}
	}
	// A narrower window is clipped.
	got = report(t, g, Options{View: "window", Op: 1, FromMS: 40, ToMS: 60})
	if !strings.Contains(got, "(20.00ms)") || !strings.Contains(got, "     20.00ms 100.0%  exec_phase exec.processRun") ||
		!strings.Contains(got, "     20.00ms    20.00ms      1  exec_phase exec.processRun") || strings.Contains(got, "  0.0%  ") {
		t.Errorf("clipped window:\n%s", got)
	}
	var buf bytes.Buffer
	if err := Report(&buf, g, Options{View: "window"}); err == nil {
		t.Error("window without -op: no error")
	}
}

func TestCompareByLabel(t *testing.T) {
	a, b := twoPackages(t), twoPackages(t)
	a.LabelBy(pkgLabel)
	b.LabelBy(pkgLabel)
	got := report(t, b, Options{View: "compare", By: "label", Against: a, Filter: Filter{Class: regexp.MustCompile(`^exec\.processRun$`)}})
	if !strings.Contains(got, "example.com/a") || !strings.Contains(got, "example.com/b") {
		t.Errorf("compare by label misses a package:\n%s", got)
	}
}

func TestUnionLen(t *testing.T) {
	if got := unionLen([][2]int64{{0, 10}, {5, 20}, {30, 40}}); got != 30 {
		t.Errorf("unionLen = %d, want 30", got)
	}
}

func TestWindowIncludesDescendantsThatOutliveTheirParent(t *testing.T) {
	// Root [0,100]; its child [0,40] starts publication work [40,80] that
	// outlives it. A window inside [40,80] must count the publication.
	b := newDump(t)
	b.op(1, 0, "call", "Root", "c", "executed", 0, 100)
	b.op(2, 1, "call", "Child", "c", "executed", 0, 40)
	b.op(3, 2, "internal", "dagql.publishResult", "c", "ok", 40, 80)
	g := b.graph()
	got := report(t, g, Options{View: "window", Op: 1, FromMS: 50, ToMS: 70})
	assertContains(t, got,
		"(20.00ms)",
		"     20.00ms    20.00ms      1  internal dagql.publishResult")
}

func TestOpenOpKeepsItsCommand(t *testing.T) {
	b := newDump(t)
	b.op(1, 0, "call", "Directory.sync", "m", "executed", 0, 40)
	meta, err := json.Marshal([]string{"sh", "-c", "set -e\n# example.com/open\ncompile"})
	if err != nil {
		t.Fatal(err)
	}
	b.open = append(b.open, wcprof.DumpOpenOp{
		OpID: 2, ParentID: 1, Kind: "exec_phase", WorkType: "user",
		ClassID: b.id("exec.processRun"), MetaID: b.id(string(meta)), StartNS: 10 * msNS,
	})
	g := b.graph()
	op := g.ByID[2]
	if op == nil || !op.Open || len(op.Argv) != 3 {
		t.Fatalf("open op: %+v", op)
	}
	if n := g.LabelBy(pkgLabel); n != 1 || op.Label != "example.com/open" || g.ByID[1].Label != "example.com/open" {
		t.Errorf("open op label: matched %d, op %q, parent %q", n, op.Label, g.ByID[1].Label)
	}
}

func TestCompareByLabelHeading(t *testing.T) {
	a, b := twoPackages(t), twoPackages(t)
	a.LabelBy(pkgLabel)
	b.LabelBy(pkgLabel)
	got := report(t, b, Options{View: "compare", By: "label", Against: a})
	if !strings.Contains(got, "Δself  label") {
		t.Errorf("compare by label heading:\n%s", got)
	}
}
