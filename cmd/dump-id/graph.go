package main

import (
	"fmt"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// edge is a reference from one call to another. Every reference is a place the
// referenced call has to be (re-)constructed when the recipe is loaded.
type edge struct {
	from, to string
	label    string // "receiver", "module", "arg:name", "implicit:name"
}

// graph is the flat callsByDigest map plus the reference structure over it.
//
// The important bit for forensics: a recipe is a DAG, not a chain. A call is
// reachable as a receiver AND as a nested ID argument (LiteralID), possibly
// many times over. The encoded form deduplicates by digest; a naive load that
// re-executes what it walks does not.
type graph struct {
	root  string
	calls map[string]*callpbv1.Call

	children map[string][]edge
	parents  map[string][]edge

	pathMemo map[string]*big.Int
	pathBusy map[string]bool
}

func newGraph(recipe *callpbv1.RecipeDAG) *graph {
	g := &graph{
		root:     recipe.RootDigest,
		calls:    recipe.CallsByDigest,
		children: map[string][]edge{},
		parents:  map[string][]edge{},
		pathMemo: map[string]*big.Int{},
		pathBusy: map[string]bool{},
	}
	for dgst, c := range g.calls {
		if c.ReceiverDigest != "" {
			g.addEdge(dgst, c.ReceiverDigest, "receiver")
		}
		if c.Module != nil && c.Module.CallDigest != "" {
			g.addEdge(dgst, c.Module.CallDigest, "module")
		}
		for _, a := range c.Args {
			g.litEdges(dgst, "arg:"+a.GetName(), a.GetValue())
		}
		for _, a := range c.ImplicitInputs {
			g.litEdges(dgst, "implicit:"+a.GetName(), a.GetValue())
		}
	}
	return g
}

func (g *graph) addEdge(from, to, label string) {
	e := edge{from: from, to: to, label: label}
	g.children[from] = append(g.children[from], e)
	g.parents[to] = append(g.parents[to], e)
}

func (g *graph) litEdges(from, label string, lit *callpbv1.Literal) {
	switch v := lit.GetValue().(type) {
	case nil:
	case *callpbv1.Literal_CallDigest:
		if v.CallDigest != "" {
			g.addEdge(from, v.CallDigest, label)
		}
	case *callpbv1.Literal_List:
		for i, elem := range v.List.GetValues() {
			g.litEdges(from, label+"["+strconv.Itoa(i)+"]", elem)
		}
	case *callpbv1.Literal_Object:
		for _, f := range v.Object.GetValues() {
			g.litEdges(from, label+"."+f.GetName(), f.GetValue())
		}
	}
}

// paths counts how many distinct root→call paths exist through the DAG.
//
// That is exactly the number of times the call appears in the FULLY EXPANDED
// tree — i.e. how often a loader that walks the recipe without deduplicating
// by digest would (re-)execute it.
func (g *graph) paths(dgst string) *big.Int {
	if v, ok := g.pathMemo[dgst]; ok {
		return v
	}
	if dgst == g.root {
		one := big.NewInt(1)
		g.pathMemo[dgst] = one
		return one
	}
	if g.pathBusy[dgst] {
		// content-addressed DAGs cannot cycle; be defensive anyway
		return big.NewInt(0)
	}
	g.pathBusy[dgst] = true
	sum := big.NewInt(0)
	for _, e := range g.parents[dgst] {
		sum.Add(sum, g.paths(e.from))
	}
	g.pathBusy[dgst] = false
	g.pathMemo[dgst] = sum
	return sum
}

// typeOf returns the GraphQL type name a call returns.
func (g *graph) typeOf(dgst string) string {
	c := g.calls[dgst]
	if c == nil || c.Type == nil {
		return "?"
	}
	return typeStr(c.Type)
}

func typeStr(t *callpbv1.Type) string {
	if t == nil {
		return "?"
	}
	if t.Elem != nil {
		s := "[" + typeStr(t.Elem) + "]"
		if t.NonNull {
			s += "!"
		}
		return s
	}
	s := t.NamedType
	if t.NonNull {
		s += "!"
	}
	return s
}

// qualName is Receiver.Type.field, the name a reader recognizes
// (e.g. "Staff.spawn" vs "LLM.spawn").
func (g *graph) qualName(dgst string) string {
	c := g.calls[dgst]
	if c == nil {
		return "<missing>"
	}
	recv := "Query"
	if c.ReceiverDigest != "" {
		rc := g.calls[c.ReceiverDigest]
		if rc != nil && rc.Type != nil {
			recv = rc.Type.NamedType
		} else {
			recv = "?"
		}
	}
	return recv + "." + c.Field
}

// spine returns the receiver chain of a call, bottom (rootmost receiver) first.
func (g *graph) spine(dgst string) []string {
	var out []string
	for d := dgst; d != ""; {
		out = append(out, d)
		c := g.calls[d]
		if c == nil {
			break
		}
		d = c.ReceiverDigest
	}
	// reverse
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

type formatOpts struct {
	maxLit int
	spine  int
}

func (o formatOpts) clip(s string) string {
	if o.maxLit <= 0 || len(s) <= o.maxLit {
		return s
	}
	return s[:o.maxLit] + fmt.Sprintf("…(%d)", len(s))
}

// selfDisplay renders one selector: field(arg: value, …).
func (g *graph) selfDisplay(dgst string, o formatOpts) string {
	c := g.calls[dgst]
	if c == nil {
		return "<missing:" + short(dgst) + ">"
	}
	var b strings.Builder
	b.WriteString(c.Field)
	if len(c.Args) > 0 {
		b.WriteString("(")
		for i, a := range c.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(a.GetName())
			b.WriteString(": ")
			b.WriteString(g.litDisplay(a.GetValue(), o))
		}
		b.WriteString(")")
	}
	if c.Nth != 0 {
		fmt.Fprintf(&b, "#%d", c.Nth)
	}
	return b.String()
}

func (g *graph) litDisplay(lit *callpbv1.Literal, o formatOpts) string {
	switch v := lit.GetValue().(type) {
	case nil:
		return "<nil>"
	case *callpbv1.Literal_CallDigest:
		return "<ID " + g.qualName(v.CallDigest) + " " + short(v.CallDigest) + ">"
	case *callpbv1.Literal_Null:
		return "null"
	case *callpbv1.Literal_Bool:
		return strconv.FormatBool(v.Bool)
	case *callpbv1.Literal_Enum:
		return v.Enum
	case *callpbv1.Literal_Int:
		return strconv.FormatInt(v.Int, 10)
	case *callpbv1.Literal_Float:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case *callpbv1.Literal_String_:
		return strconv.Quote(o.clip(v.String_))
	case *callpbv1.Literal_DigestedString:
		return "<digested-string " + short(v.DigestedString.GetDigest()) + ">"
	case *callpbv1.Literal_List:
		parts := make([]string, 0, len(v.List.GetValues()))
		for _, e := range v.List.GetValues() {
			parts = append(parts, g.litDisplay(e, o))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *callpbv1.Literal_Object:
		parts := make([]string, 0, len(v.Object.GetValues()))
		for _, f := range v.Object.GetValues() {
			parts = append(parts, f.GetName()+": "+g.litDisplay(f.GetValue(), o))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return "<?>"
	}
}

// pathDisplay renders a call's receiver spine as sel.sel.sel, keeping at most
// o.spine trailing selectors.
func (g *graph) pathDisplay(dgst string, o formatOpts) string {
	sp := g.spine(dgst)
	elided := 0
	if o.spine > 0 && len(sp) > o.spine {
		elided = len(sp) - o.spine
		sp = sp[elided:]
	}
	parts := make([]string, 0, len(sp))
	for _, d := range sp {
		parts = append(parts, g.selfDisplay(d, o))
	}
	s := strings.Join(parts, ".")
	if elided > 0 {
		s = fmt.Sprintf("…(%d more).%s", elided, s)
	}
	return s
}

func short(dgst string) string {
	if len(dgst) > 20 {
		return dgst[:20] + "…"
	}
	return dgst
}

type fieldStat struct {
	name       string
	distinct   int
	refs       int      // in-edges (with multiplicity)
	asReceiver int      // in-edges labelled "receiver"
	expansions *big.Int // sum of root→call path counts
}

func (g *graph) fieldStats() []fieldStat {
	byName := map[string]*fieldStat{}
	for dgst := range g.calls {
		n := g.qualName(dgst)
		st := byName[n]
		if st == nil {
			st = &fieldStat{name: n, expansions: big.NewInt(0)}
			byName[n] = st
		}
		st.distinct++
		for _, e := range g.parents[dgst] {
			st.refs++
			if e.label == "receiver" {
				st.asReceiver++
			}
		}
		st.expansions.Add(st.expansions, g.paths(dgst))
	}
	out := make([]fieldStat, 0, len(byName))
	for _, st := range byName {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		c := out[i].expansions.Cmp(out[j].expansions)
		if c != 0 {
			return c > 0
		}
		if out[i].distinct != out[j].distinct {
			return out[i].distinct > out[j].distinct
		}
		return out[i].name < out[j].name
	})
	return out
}

func (g *graph) printStats(w io.Writer, src *source, o formatOpts) {
	fmt.Fprintf(w, "== %s ==\n", src.label)
	if src.meta != nil {
		fmt.Fprintf(w, "session name:  %s\n", o.clip(src.meta.Name))
		fmt.Fprintf(w, "session model: %s\n", src.meta.Model)
		fmt.Fprintf(w, "session saved: %s\n", src.meta.CreatedAt)
	}
	fmt.Fprintf(w, "encoded:       %d base64 chars, %d protobuf bytes\n", len(src.encoded), src.rawBytes)
	fmt.Fprintf(w, "root:          %s %s\n", g.qualName(g.root), g.typeOf(g.root))
	fmt.Fprintf(w, "root digest:   %s\n", g.root)
	fmt.Fprintf(w, "distinct calls: %d\n", len(g.calls))
	fmt.Fprintf(w, "root spine depth: %d selectors\n", len(g.spine(g.root)))

	total := big.NewInt(0)
	maxPaths := big.NewInt(0)
	maxDgst := ""
	unreachable := 0
	for dgst := range g.calls {
		p := g.paths(dgst)
		total.Add(total, p)
		if p.Sign() == 0 {
			unreachable++
		}
		if p.Cmp(maxPaths) > 0 {
			maxPaths = p
			maxDgst = dgst
		}
	}
	fmt.Fprintf(w, "naive expansion: %s call nodes if walked without dedupe\n", total)
	if maxDgst != "" {
		fmt.Fprintf(w, "hottest node:  %s ×%s (%s)\n", g.qualName(maxDgst), maxPaths, short(maxDgst))
	}
	if unreachable > 0 {
		fmt.Fprintf(w, "unreachable from root: %d\n", unreachable)
	}

	// module provenance
	type modKey struct{ name, ref, pin string }
	mods := map[modKey]int{}
	for _, c := range g.calls {
		if c.Module == nil {
			continue
		}
		mods[modKey{c.Module.Name, c.Module.Ref, c.Module.Pin}]++
	}
	if len(mods) > 0 {
		fmt.Fprintf(w, "\nmodule provenance frames (calls carrying each):\n")
		keys := make([]modKey, 0, len(mods))
		for k := range mods {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return mods[keys[i]] > mods[keys[j]] })
		for _, k := range keys {
			fmt.Fprintf(w, "  %-16s %-40s pin=%s  ×%d\n", k.name, o.clip(k.ref), short(k.pin), mods[k])
		}
	}

	// extra digests / effect IDs / implicit inputs
	var withExtra, withEffects, withImplicit, withView int
	for _, c := range g.calls {
		if len(c.ExtraDigests) > 0 {
			withExtra++
		}
		if len(c.EffectIds) > 0 {
			withEffects++
		}
		if len(c.ImplicitInputs) > 0 {
			withImplicit++
		}
		if c.View != "" {
			withView++
		}
	}
	fmt.Fprintf(w, "\ncalls with extraDigests=%d effectIds=%d implicitInputs=%d view=%d\n",
		withExtra, withEffects, withImplicit, withView)

	fmt.Fprintf(w, "\n%-44s %8s %6s %8s %14s\n", "CALL", "DISTINCT", "REFS", "AS-RECV", "EXPANSIONS")
	for _, st := range g.fieldStats() {
		fmt.Fprintf(w, "%-44s %8d %6d %8d %14s\n", st.name, st.distinct, st.refs, st.asReceiver, st.expansions)
	}
}

func (g *graph) printFind(w io.Writer, re *regexp.Regexp, o formatOpts) {
	type hit struct {
		dgst string
		name string
	}
	var hits []hit
	for dgst := range g.calls {
		n := g.qualName(dgst)
		if re.MatchString(n) || re.MatchString(g.calls[dgst].Field) {
			hits = append(hits, hit{dgst, n})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		c := g.paths(hits[i].dgst).Cmp(g.paths(hits[j].dgst))
		if c != 0 {
			return c > 0
		}
		return hits[i].dgst < hits[j].dgst
	})
	fmt.Fprintf(w, "== %d call(s) matching /%s/ ==\n", len(hits), re)
	for i, h := range hits {
		c := g.calls[h.dgst]
		fmt.Fprintf(w, "\n[%d] %s -> %s\n", i+1, h.name, typeStr(c.Type))
		fmt.Fprintf(w, "    digest:     %s\n", h.dgst)
		fmt.Fprintf(w, "    refs:       %d (receiver-of: %d)  expansions: %s\n",
			len(g.parents[h.dgst]), countLabel(g.parents[h.dgst], "receiver"), g.paths(h.dgst))
		if c.Module != nil {
			fmt.Fprintf(w, "    module:     %s (%s)\n", c.Module.Name, o.clip(c.Module.Ref))
		}
		fmt.Fprintf(w, "    path:       %s\n", g.pathDisplay(h.dgst, o))
		for _, a := range c.Args {
			fmt.Fprintf(w, "    arg %s: %s\n", a.GetName(), g.litDisplay(a.GetValue(), o))
		}
		for _, a := range c.ImplicitInputs {
			fmt.Fprintf(w, "    implicit %s: %s\n", a.GetName(), g.litDisplay(a.GetValue(), o))
		}
		if len(g.parents[h.dgst]) > 0 {
			fmt.Fprintf(w, "    referenced by:\n")
			for _, e := range g.parents[h.dgst] {
				fmt.Fprintf(w, "      %-24s %s %s\n", e.label, g.qualName(e.from), short(e.from))
			}
		}
	}
}

func countLabel(edges []edge, label string) int {
	n := 0
	for _, e := range edges {
		if e.label == label {
			n++
		}
	}
	return n
}

// printTree walks the root's receiver spine bottom-up, expanding ID-valued
// arguments inline. This is the shape that matters for a conversation recipe:
// the LLM chain is a spine, but each withTools(object:) argument hangs a whole
// other chain off it.
func (g *graph) printTree(w io.Writer, o formatOpts, maxDepth int) {
	fmt.Fprintf(w, "== spine of %s (%d selectors) ==\n", g.qualName(g.root), len(g.spine(g.root)))
	g.printSpine(w, g.root, "", o, 0, maxDepth, map[string]bool{})
}

func (g *graph) printSpine(w io.Writer, dgst, indent string, o formatOpts, depth, maxDepth int, seen map[string]bool) {
	sp := g.spine(dgst)
	for i, d := range sp {
		c := g.calls[d]
		if c == nil {
			fmt.Fprintf(w, "%s%3d. <missing %s>\n", indent, i+1, short(d))
			continue
		}
		mod := ""
		if c.Module != nil {
			mod = "  [mod:" + c.Module.Name + "]"
		}
		fmt.Fprintf(w, "%s%3d. %s%s   %s\n", indent, i+1, g.selfDisplay(d, o), mod, short(d))
		if maxDepth > 0 && depth >= maxDepth {
			continue
		}
		for _, e := range g.children[d] {
			if e.label == "receiver" || e.label == "module" {
				continue
			}
			if seen[e.to] {
				fmt.Fprintf(w, "%s     ↳ %s = <seen %s>\n", indent, e.label, short(e.to))
				continue
			}
			seen[e.to] = true
			fmt.Fprintf(w, "%s     ↳ %s = ID (%d selectors):\n", indent, e.label, len(g.spine(e.to)))
			g.printSpine(w, e.to, indent+"        ", o, depth+1, maxDepth, seen)
		}
	}
}

func printDiff(w io.Writer, a, b *source, o formatOpts) {
	fmt.Fprintf(w, "== diff %s -> %s ==\n", a.label, b.label)
	fmt.Fprintf(w, "encoded:  %d -> %d protobuf bytes (%+d)\n", a.rawBytes, b.rawBytes, b.rawBytes-a.rawBytes)
	fmt.Fprintf(w, "calls:    %d -> %d (%+d)\n", len(a.graph.calls), len(b.graph.calls), len(b.graph.calls)-len(a.graph.calls))

	sa, sb := a.graph.spine(a.graph.root), b.graph.spine(b.graph.root)
	common := 0
	for common < len(sa) && common < len(sb) && sa[common] == sb[common] {
		common++
	}
	fmt.Fprintf(w, "root spine: %d -> %d selectors, identical prefix of %d\n", len(sa), len(sb), common)
	if common < len(sa) {
		fmt.Fprintf(w, "\nonly in %s spine (from position %d):\n", a.label, common+1)
		for i := common; i < len(sa); i++ {
			fmt.Fprintf(w, "  %3d. %s\n", i+1, a.graph.selfDisplay(sa[i], o))
		}
	}
	if common < len(sb) {
		fmt.Fprintf(w, "\nonly in %s spine (from position %d):\n", b.label, common+1)
		for i := common; i < len(sb); i++ {
			fmt.Fprintf(w, "  %3d. %s\n", i+1, b.graph.selfDisplay(sb[i], o))
		}
	}

	onlyA := map[string]int{}
	onlyB := map[string]int{}
	for d := range a.graph.calls {
		if _, ok := b.graph.calls[d]; !ok {
			onlyA[a.graph.qualName(d)]++
		}
	}
	for d := range b.graph.calls {
		if _, ok := a.graph.calls[d]; !ok {
			onlyB[b.graph.qualName(d)]++
		}
	}
	printBucket(w, "calls only in "+a.label, onlyA)
	printBucket(w, "calls only in "+b.label, onlyB)

	// expansion deltas per field
	fmt.Fprintf(w, "\n%-44s %14s %14s %10s\n", "CALL", "EXPAND(A)", "EXPAND(B)", "DELTA")
	statsA := map[string]fieldStat{}
	for _, st := range a.graph.fieldStats() {
		statsA[st.name] = st
	}
	statsB := map[string]fieldStat{}
	names := map[string]bool{}
	for _, st := range b.graph.fieldStats() {
		statsB[st.name] = st
		names[st.name] = true
	}
	for n := range statsA {
		names[n] = true
	}
	type row struct {
		name       string
		ea, eb, dl *big.Int
	}
	var rows []row
	for n := range names {
		ea := big.NewInt(0)
		if st, ok := statsA[n]; ok {
			ea = st.expansions
		}
		eb := big.NewInt(0)
		if st, ok := statsB[n]; ok {
			eb = st.expansions
		}
		dl := new(big.Int).Sub(eb, ea)
		if dl.Sign() == 0 {
			continue
		}
		rows = append(rows, row{n, ea, eb, dl})
	}
	sort.Slice(rows, func(i, j int) bool {
		c := rows[i].dl.CmpAbs(rows[j].dl)
		if c != 0 {
			return c > 0
		}
		return rows[i].name < rows[j].name
	})
	for _, r := range rows {
		fmt.Fprintf(w, "%-44s %14s %14s %10s\n", r.name, r.ea, r.eb, r.dl)
	}
}

func printBucket(w io.Writer, title string, m map[string]int) {
	if len(m) == 0 {
		fmt.Fprintf(w, "\n%s: none\n", title)
		return
	}
	keys := make([]string, 0, len(m))
	total := 0
	for k, v := range m {
		keys = append(keys, k)
		total += v
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	fmt.Fprintf(w, "\n%s (%d):\n", title, total)
	for _, k := range keys {
		fmt.Fprintf(w, "  %-44s %d\n", k, m[k])
	}
}
