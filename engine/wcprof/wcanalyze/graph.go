// Package wcanalyze reconstructs an operation graph from a wcprof dump and
// runs offline wall-clock bottleneck analysis over it: self-time accounting,
// a replay-based counterfactual simulator, and per-class what-if rankings.
package wcanalyze

import (
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/dagger/dagger/engine/wcprof"
)

// Op is one reconstructed operation interval.
type Op struct {
	ID       uint64
	ParentID uint64
	Kind     string
	WorkType string
	Outcome  string
	Class    string
	Ident    string
	ClientID string
	ResultID uint64
	StartNS  int64
	EndNS    int64
	// Open marks ops that had not ended at dump time; EndNS is the dump time.
	Open bool

	Parent   *Op
	Children []*Op // sorted by StartNS
	Waits    []*WaitEdge
	// Reparented marks ops whose parent was assigned via a nested-client
	// link rather than a recorded parent ID.
	Reparented bool

	// selfSegments is the op interval minus child intervals and waits,
	// computed lazily by the analysis.
	selfSegments []segment
}

func (op *Op) Duration() int64 {
	return op.EndNS - op.StartNS
}

// WaitEdge is one recorded blocked-on interval.
type WaitEdge struct {
	Waiter      *Op // nil if the waiting code had no profiled op
	Target      *Op // nil for unresolved or resource waits
	TargetIdent string
	Reason      string
	StartNS     int64
	EndNS       int64
}

func (w *WaitEdge) Duration() int64 {
	return w.EndNS - w.StartNS
}

// Graph is the reconstructed op graph for one dump.
type Graph struct {
	Ops   map[uint64]*Op
	Roots []*Op // ops with no (resolved) parent, sorted by StartNS

	// OrphanWaits are waits whose waiter op is unknown.
	OrphanWaits []*WaitEdge

	DroppedEvents uint64
	OpenOps       int

	// TraceStartNS/TraceEndNS bound all recorded activity.
	TraceStartNS int64
	TraceEndNS   int64
}

// ClassKey identifies an operation class for aggregation: ops are grouped by
// kind+class (e.g. call_exec / "Container.withExec").
type ClassKey struct {
	Kind  string
	Class string
}

func (k ClassKey) String() string {
	return k.Kind + ":" + k.Class
}

// Load reads a wcprof dump and reconstructs the op graph.
func Load(r io.Reader) (*Graph, error) {
	header, events, err := wcprof.ReadDump(r)
	if err != nil {
		return nil, err
	}
	return Build(header, events)
}

// Build reconstructs the op graph from parsed dump data.
//
//nolint:gocyclo // linear reconstruction flow over the event union
func Build(header *wcprof.DumpHeader, events []wcprof.DumpEvent) (*Graph, error) {
	str := func(id uint32) string {
		if int(id) >= len(header.Strings) {
			return fmt.Sprintf("<bad-string-%d>", id)
		}
		return header.Strings[id]
	}

	g := &Graph{
		Ops:           make(map[uint64]*Op),
		DroppedEvents: header.DroppedEvents,
	}

	dumpRelNS := header.DumpedUnixNano - header.EpochUnixNano

	type rawWait struct {
		waiterID uint64
		targetID uint64
		ident    string
		reason   string
		startNS  int64
		endNS    int64
	}
	type rawLink struct {
		fromID   uint64
		targetID uint64
		ident    string
		kind     string
		resultID uint64
	}
	var waits []rawWait
	var links []rawLink

	for _, ev := range events {
		switch ev.Type {
		case "op":
			g.Ops[ev.OpID] = &Op{
				ID:       ev.OpID,
				ParentID: ev.ParentID,
				Kind:     ev.OpKind,
				WorkType: ev.WorkType,
				Outcome:  ev.Outcome,
				Class:    str(ev.ClassID),
				Ident:    str(ev.IdentID),
				ClientID: str(ev.ClientID),
				ResultID: ev.ResultID,
				StartNS:  ev.StartNS,
				EndNS:    max(ev.EndNS, ev.StartNS),
			}
		case "wait":
			waits = append(waits, rawWait{
				waiterID: ev.ParentID,
				targetID: ev.TargetID,
				ident:    str(ev.IdentID),
				reason:   ev.Reason,
				startNS:  ev.StartNS,
				endNS:    max(ev.EndNS, ev.StartNS),
			})
		case "link":
			links = append(links, rawLink{
				fromID:   ev.ParentID,
				targetID: ev.TargetID,
				ident:    str(ev.IdentID),
				kind:     ev.LinkKind,
				resultID: ev.ResultID,
			})
		}
	}

	// Ops still open at dump time get the dump timestamp as their end.
	for _, oo := range header.OpenOps {
		if _, exists := g.Ops[oo.OpID]; exists {
			continue
		}
		g.Ops[oo.OpID] = &Op{
			ID:       oo.OpID,
			ParentID: oo.ParentID,
			Kind:     oo.Kind,
			WorkType: oo.WorkType,
			Class:    str(oo.ClassID),
			Ident:    str(oo.IdentID),
			ClientID: str(oo.ClientID),
			StartNS:  oo.StartNS,
			EndNS:    max(dumpRelNS, oo.StartNS),
			Open:     true,
		}
		g.OpenOps++
	}

	// Index exec ops by ident so exec-reason ident waits can resolve to them.
	execByIdent := make(map[string]*Op)
	for _, op := range g.Ops {
		if op.Kind == wcprof.OpKindExec.String() && op.Ident != "" {
			// prefer the longest-running exec for an ident if duplicated
			if cur, ok := execByIdent[op.Ident]; !ok || op.Duration() > cur.Duration() {
				execByIdent[op.Ident] = op
			}
		}
	}

	// Attach waits.
	for _, rw := range waits {
		w := &WaitEdge{
			TargetIdent: rw.ident,
			Reason:      rw.reason,
			StartNS:     rw.startNS,
			EndNS:       rw.endNS,
		}
		if t, ok := g.Ops[rw.targetID]; ok {
			w.Target = t
		} else if rw.reason == "exec" && rw.ident != "" {
			if t, ok := execByIdent[rw.ident]; ok {
				w.Target = t
			}
		}
		if waiter, ok := g.Ops[rw.waiterID]; ok {
			w.Waiter = waiter
			waiter.Waits = append(waiter.Waits, w)
		} else {
			g.OrphanWaits = append(g.OrphanWaits, w)
		}
	}

	// Nested-client links: clientID -> hosting exec op.
	nestedClientExec := make(map[string]*Op)
	for _, rl := range links {
		if rl.kind != "nested_client" || rl.ident == "" {
			continue
		}
		if from, ok := g.Ops[rl.fromID]; ok {
			nestedClientExec[rl.ident] = from
		}
	}

	// Wire parents. Roots belonging to a nested client get re-parented under
	// the exec op hosting that client.
	for _, op := range g.Ops {
		if op.ParentID != 0 {
			if parent, ok := g.Ops[op.ParentID]; ok && parent != op {
				op.Parent = parent
				continue
			}
		}
		if hostExec, ok := nestedClientExec[op.ClientID]; ok && hostExec != op {
			op.Parent = hostExec
			op.Reparented = true
		}
	}
	for _, op := range g.Ops {
		if op.Parent != nil {
			op.Parent.Children = append(op.Parent.Children, op)
		} else {
			g.Roots = append(g.Roots, op)
		}
	}
	for _, op := range g.Ops {
		slices.SortFunc(op.Children, func(a, b *Op) int {
			if a.StartNS != b.StartNS {
				return int(a.StartNS - b.StartNS)
			}
			return int(a.ID - b.ID)
		})
		slices.SortFunc(op.Waits, func(a, b *WaitEdge) int {
			return int(a.StartNS - b.StartNS)
		})
	}
	slices.SortFunc(g.Roots, func(a, b *Op) int {
		if a.StartNS != b.StartNS {
			return int(a.StartNS - b.StartNS)
		}
		return int(a.ID - b.ID)
	})

	first := true
	for _, op := range g.Ops {
		if first {
			g.TraceStartNS, g.TraceEndNS = op.StartNS, op.EndNS
			first = false
			continue
		}
		g.TraceStartNS = min(g.TraceStartNS, op.StartNS)
		g.TraceEndNS = max(g.TraceEndNS, op.EndNS)
	}

	return g, nil
}

// segment is a half-open interval [Start, End).
type segment struct {
	Start, End int64
}

// subtractIntervals returns base minus the union of cuts (cuts may overlap
// and extend beyond base).
func subtractIntervals(base segment, cuts []segment) []segment {
	if len(cuts) == 0 {
		if base.End > base.Start {
			return []segment{base}
		}
		return nil
	}
	sorted := slices.Clone(cuts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })

	var out []segment
	cursor := base.Start
	for _, cut := range sorted {
		if cut.End <= cursor || cut.Start >= base.End {
			continue
		}
		if cut.Start > cursor {
			out = append(out, segment{cursor, min(cut.Start, base.End)})
		}
		cursor = max(cursor, cut.End)
		if cursor >= base.End {
			return out
		}
	}
	if cursor < base.End {
		out = append(out, segment{cursor, base.End})
	}
	return out
}

func sumSegments(segs []segment) int64 {
	var total int64
	for _, s := range segs {
		total += s.End - s.Start
	}
	return total
}

// SelfSegments returns the op's interval minus its children's intervals and
// its own wait intervals: the time the op was plausibly doing its own work.
func (op *Op) SelfSegments() []segment {
	if op.selfSegments != nil {
		return op.selfSegments
	}
	cuts := make([]segment, 0, len(op.Children)+len(op.Waits))
	for _, c := range op.Children {
		cuts = append(cuts, segment{c.StartNS, c.EndNS})
	}
	for _, w := range op.Waits {
		cuts = append(cuts, segment{w.StartNS, w.EndNS})
	}
	segs := subtractIntervals(segment{op.StartNS, op.EndNS}, cuts)
	if segs == nil {
		segs = []segment{}
	}
	op.selfSegments = segs
	return segs
}

// SelfNS is the total self time of the op.
func (op *Op) SelfNS() int64 {
	return sumSegments(op.SelfSegments())
}

// Key returns the op's aggregation class key.
func (op *Op) Key() ClassKey {
	return ClassKey{Kind: op.Kind, Class: op.Class}
}
