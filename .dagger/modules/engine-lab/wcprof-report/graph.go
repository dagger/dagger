package main

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/dagger/dagger/engine/wcprof"
)

// Op is one reconstructed operation interval, with names resolved.
type Op struct {
	ID       uint64
	ParentID uint64
	Kind     string
	Work     string
	Outcome  string
	Class    string
	Ident    string
	Client   string
	ResultID uint64
	Start    int64
	End      int64
	// Open marks ops still running at dump time; End is the dump time.
	Open bool

	Parent   *Op
	Children []*Op // sorted by Start
	Waits    []*Wait
	// Reparented marks roots of a nested client attached under the exec op
	// hosting that client (a nested_client link), or its exec.processRun
	// phase, not via a recorded parent.
	Reparented bool

	// Self is the op's interval minus the union of its children's intervals
	// and its own waits: the time it was plausibly doing its own work.
	Self int64
}

// Dur is the op's wall duration.
func (op *Op) Dur() int64 { return op.End - op.Start }

// Key is the aggregation key: ops are grouped by kind + class, so a call and
// the shared call_exec of the same field stay apart.
func (op *Op) Key() string { return op.Kind + " " + op.Class }

// Wait is one recorded blocked-on interval.
type Wait struct {
	WaiterID uint64
	TargetID uint64
	Waiter   *Op // nil when the waiting op was not recorded
	Target   *Op // nil for resource waits or unrecorded targets
	Reason   string
	Ident    string
	Start    int64
	End      int64
}

// Dur is the wait's wall duration.
func (w *Wait) Dur() int64 { return w.End - w.Start }

// Link is one non-blocking correlation event.
type Link struct {
	FromID   uint64
	TargetID uint64
	ResultID uint64
	Kind     string
	Ident    string
	At       int64
}

// Graph is the op graph reconstructed from one dump.
type Graph struct {
	Header *wcprof.DumpHeader
	Ops    []*Op // sorted by Start, then ID
	ByID   map[uint64]*Op
	Waits  []*Wait
	Links  []*Link
	// Start/End bound all recorded ops (recorder-relative ns). Reported
	// times are relative to Start.
	Start, End int64
}

// Load reads a wcprof dump and reconstructs its op graph.
func Load(r io.Reader) (*Graph, error) {
	header, events, err := wcprof.ReadDump(r)
	if err != nil {
		return nil, err
	}
	return Build(header, events), nil
}

// Build reconstructs the op graph from parsed dump data.
func Build(header *wcprof.DumpHeader, events []wcprof.DumpEvent) *Graph {
	str := func(id uint32) string {
		if int(id) >= len(header.Strings) {
			return fmt.Sprintf("<bad-string-%d>", id)
		}
		return header.Strings[id]
	}

	g := &Graph{Header: header, ByID: make(map[uint64]*Op)}
	for _, ev := range events {
		switch ev.Type {
		case "op":
			op := &Op{
				ID:       ev.OpID,
				ParentID: ev.ParentID,
				Kind:     ev.OpKind,
				Work:     ev.WorkType,
				Outcome:  ev.Outcome,
				Class:    str(ev.ClassID),
				Ident:    str(ev.IdentID),
				Client:   str(ev.ClientID),
				ResultID: ev.ResultID,
				Start:    ev.StartNS,
				End:      max(ev.EndNS, ev.StartNS),
			}
			g.Ops = append(g.Ops, op)
			g.ByID[op.ID] = op
		case "wait":
			g.Waits = append(g.Waits, &Wait{
				WaiterID: ev.ParentID,
				TargetID: ev.TargetID,
				Reason:   ev.Reason,
				Ident:    str(ev.IdentID),
				Start:    ev.StartNS,
				End:      max(ev.EndNS, ev.StartNS),
			})
		case "link":
			g.Links = append(g.Links, &Link{
				FromID:   ev.ParentID,
				TargetID: ev.TargetID,
				ResultID: ev.ResultID,
				Kind:     ev.LinkKind,
				Ident:    str(ev.IdentID),
				At:       ev.StartNS,
			})
		}
	}

	// Ops still running at dump time end at the dump timestamp.
	dumpRel := header.DumpedUnixNano - header.EpochUnixNano
	for _, oo := range header.OpenOps {
		if _, ok := g.ByID[oo.OpID]; ok {
			continue
		}
		op := &Op{
			ID:       oo.OpID,
			ParentID: oo.ParentID,
			Kind:     oo.Kind,
			Work:     oo.WorkType,
			Outcome:  "open",
			Class:    str(oo.ClassID),
			Ident:    str(oo.IdentID),
			Client:   str(oo.ClientID),
			Start:    oo.StartNS,
			End:      max(dumpRel, oo.StartNS),
			Open:     true,
		}
		g.Ops = append(g.Ops, op)
		g.ByID[op.ID] = op
	}

	slices.SortFunc(g.Ops, func(a, b *Op) int {
		if a.Start != b.Start {
			return cmpInt64(a.Start, b.Start)
		}
		return cmpInt64(int64(a.ID), int64(b.ID))
	})

	// Exec ops by ident, so exec-reason ident waits can resolve to them.
	execByIdent := make(map[string]*Op)
	for _, op := range g.Ops {
		if op.Kind == "exec" && op.Ident != "" {
			if cur, ok := execByIdent[op.Ident]; !ok || op.Dur() > cur.Dur() {
				execByIdent[op.Ident] = op
			}
		}
	}
	for _, w := range g.Waits {
		if t, ok := g.ByID[w.TargetID]; ok && w.TargetID != 0 {
			w.Target = t
		} else if w.Reason == "exec" && w.Ident != "" {
			w.Target = execByIdent[w.Ident]
		}
		if waiter, ok := g.ByID[w.WaiterID]; ok && w.WaiterID != 0 {
			w.Waiter = waiter
			waiter.Waits = append(waiter.Waits, w)
		}
	}

	// Roots of a nested client belong under the exec hosting that client.
	hosts := make(map[string]*Op)
	for _, l := range g.Links {
		if l.Kind != "nested_client" || l.Ident == "" {
			continue
		}
		if from, ok := g.ByID[l.FromID]; ok {
			hosts[l.Ident] = from
		}
	}
	for _, op := range g.Ops {
		if op.ParentID != 0 {
			if p, ok := g.ByID[op.ParentID]; ok && p != op {
				op.Parent = p
				// g.Ops is start-sorted, so children come out start-sorted too.
				p.Children = append(p.Children, op)
			}
		}
	}
	// Recorded parents are wired first: placing a nested root needs the
	// host exec's subtree.
	adopted := make(map[*Op]bool)
	for _, op := range g.Ops {
		if op.Parent != nil {
			continue
		}
		if h, ok := hosts[op.Client]; ok && h != op {
			if p := nestedClientParent(h, op); p != op {
				op.Parent = p
				op.Reparented = true
				p.Children = append(p.Children, op)
				adopted[p] = true
			}
		}
	}
	for p := range adopted {
		slices.SortStableFunc(p.Children, func(a, b *Op) int { return cmpInt64(a.Start, b.Start) })
	}

	for i, op := range g.Ops {
		slices.SortFunc(op.Waits, func(a, b *Wait) int { return cmpInt64(a.Start, b.Start) })
		op.Self = selfTime(op)
		if i == 0 {
			g.Start, g.End = op.Start, op.End
			continue
		}
		g.Start = min(g.Start, op.Start)
		g.End = max(g.End, op.End)
	}
	return g
}

// nestedClientParent is where a root op of a nested client hosted by host
// goes: under the exec's exec.processRun phase running when the root
// started, so the client's requests are part of the process run that made
// them (its self time is then the user's own work between requests, and the
// critical path follows into them), or under the exec when no process run
// covers it. Engines before the link moved to the exec op recorded it from
// the exec's setupNestedClient phase, which ends before the process starts;
// such a host is replaced by its exec.
func nestedClientParent(host, root *Op) *Op {
	exec := host
	for exec.Kind == "exec_phase" && exec.Parent != nil {
		exec = exec.Parent
	}
	if exec.Kind != "exec" {
		return host
	}
	if run := findProcessRun(exec, root.Start); run != nil {
		return run
	}
	return exec
}

// findProcessRun returns the exec.processRun phase under op whose interval
// contains t, or nil.
func findProcessRun(op *Op, t int64) *Op {
	for _, ch := range op.Children {
		if ch.Kind != "exec_phase" || ch.Start > t || t >= ch.End {
			continue
		}
		if ch.Class == "exec.processRun" {
			return ch
		}
		if run := findProcessRun(ch, t); run != nil {
			return run
		}
	}
	return nil
}

// selfTime is the op's duration minus the union of its children's intervals
// and its waits, each clipped to the op's own interval.
func selfTime(op *Op) int64 {
	return uncovered(op.Start, op.End, op.Children, op.Waits)
}

// uncovered is the time in [start, end) not covered by any of the children
// or waits.
func uncovered(start, end int64, children []*Op, waits []*Wait) int64 {
	if len(children) == 0 && len(waits) == 0 {
		return end - start
	}
	cuts := make([][2]int64, 0, len(children)+len(waits))
	for _, c := range children {
		cuts = append(cuts, [2]int64{c.Start, c.End})
	}
	for _, w := range waits {
		cuts = append(cuts, [2]int64{w.Start, w.End})
	}
	slices.SortFunc(cuts, func(a, b [2]int64) int { return cmpInt64(a[0], b[0]) })
	var covered int64
	cursor := start
	for _, c := range cuts {
		s, e := max(c[0], cursor), min(c[1], end)
		if e > s {
			covered += e - s
			cursor = e
		}
	}
	return end - start - covered
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Filter selects ops by class regex (minus an exclusion regex), client
// substring and exact kind.
type Filter struct {
	Class        *regexp.Regexp
	ExcludeClass *regexp.Regexp
	Client       string
	Kind         string
}

// Match reports whether op passes the filter.
func (f Filter) Match(op *Op) bool {
	if op == nil {
		return false
	}
	if f.Kind != "" && op.Kind != f.Kind {
		return false
	}
	if f.Client != "" && !strings.Contains(op.Client, f.Client) {
		return false
	}
	if f.Class != nil && !f.Class.MatchString(op.Class) {
		return false
	}
	if f.ExcludeClass != nil && f.ExcludeClass.MatchString(op.Class) {
		return false
	}
	return true
}

// Empty reports whether the filter selects everything.
func (f Filter) Empty() bool {
	return f.Class == nil && f.ExcludeClass == nil && f.Client == "" && f.Kind == ""
}

func (f Filter) String() string {
	var parts []string
	if f.Class != nil {
		parts = append(parts, fmt.Sprintf("class=~%q", f.Class.String()))
	}
	if f.ExcludeClass != nil {
		parts = append(parts, fmt.Sprintf("class!~%q", f.ExcludeClass.String()))
	}
	if f.Client != "" {
		parts = append(parts, fmt.Sprintf("client~%q", f.Client))
	}
	if f.Kind != "" {
		parts = append(parts, "kind="+f.Kind)
	}
	if len(parts) == 0 {
		return "all ops"
	}
	return strings.Join(parts, " ")
}
