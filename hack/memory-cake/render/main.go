// Command render draws the memory cake from the agent's checkpoints: two
// stacked bars of the engine's heap at the same turn, BEFORE and AFTER the
// memory PRs, each layer coloured by the PR group that addresses it and the
// part it removes hatched and labelled with its PRs; stacked areas of the
// layers over the turns of both sides; and a table.
//
//	go run ./hack/memory-cake/render -before DIR -after DIR -out DIR
//
// DIRs hold the agent's checkpoints/tNNN.json. It writes data.json,
// cake.svg, areas.svg and index.html into -out, and refuses to render when
// a checkpoint's layers do not sum to its total.
package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- inputs

type checkpoint struct {
	Side    string  `json:"side"`
	Turn    int     `json:"turn"`
	Elapsed float64 `json:"elapsedSeconds"`
	Errors  int     `json:"toolErrors"`
	Heap    struct {
		TotalBytes int64 `json:"totalBytes"`
		Layers     []struct {
			ID    string `json:"id"`
			Bytes int64  `json:"bytes"`
			Top   []struct {
				Func  string `json:"func"`
				Bytes int64  `json:"bytes"`
			} `json:"top"`
		} `json:"layers"`
	} `json:"heap"`
	MemStats map[string]int64 `json:"memstats"`
}

func (c checkpoint) layer(id string) int64 {
	for _, l := range c.Heap.Layers {
		if l.ID == id {
			return l.Bytes
		}
	}
	return 0
}

func load(dir string) ([]checkpoint, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "checkpoints", "t*.json"))
	if err != nil {
		return nil, err
	}
	var cps []checkpoint
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var c checkpoint
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		var sum int64
		for _, l := range c.Heap.Layers {
			sum += l.Bytes
		}
		if sum != c.Heap.TotalBytes {
			return nil, fmt.Errorf("%s: layers sum to %d, total is %d", p, sum, c.Heap.TotalBytes)
		}
		cps = append(cps, c)
	}
	sort.Slice(cps, func(i, j int) bool { return cps[i].Turn < cps[j].Turn })
	return cps, nil
}

// ---------------------------------------------------------------- the cake

// layerDef describes a layer of the cake, bottom to top.
type layerDef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Short  string `json:"short"`
	Detail string `json:"detail"`
	Color  string `json:"color"`
	Group  string `json:"group"` // the PR group addressing it; "" for none
}

var layerDefs = []layerDef{
	{"other", "Other / unattributed", "other", "every sample no rule below matches", "#e3e3e3", ""},
	{"dagql", "dagql result bookkeeping (unaddressed)", "dagql results", "initCompletedResult, session result installs", "#a5a5a5", ""},
	{"telemetry", "Other telemetry store (unaddressed)", "telemetry rows", "engine/clientdb outside the span and log indexes: span/log rows and streams", "#c9b8d8", ""},
	{"schema", "Schema building", "schema building", "buildSchema, SchemaBuilder(Memo), introspection, SchemaASTCache, Server.Fork, module servers", "#4e79a7", "schema"},
	{"prepared", "Dang SDK prepared dirs", "Dang prepared dirs", "cachedPreparedDangDir → dang.PrepareDir", "#f28e2b", "dang-prepared"},
	{"arbitrary", "Other session-owned arbitrary results", "other arbitrary results", "received Dang values, converted module receivers, other GetOrInitArbitrary values", "#edc948", "session-values"},
	{"contenthash", "Content-hash trees", "content-hash trees", "engine/contenthash, cacheMetadata.SetExternal", "#59a14f", "contenthash"},
	{"leases", "Snapshot owner leases", "snapshot leases", "snapshot owner lease records, internal/fastbolt", "#9c755f", "leases"},
	{"spans", "Telemetry span index", "span index", "clientdb spanLookup", "#e15759", "span-index"},
	{"logs", "Telemetry log index", "log index", "clientdb logLookup", "#ff9da7", "log-index"},
}

type group struct {
	ID    string `json:"id"`
	PRs   []int  `json:"prs"`
	Color string `json:"color"`
	Ink   string `json:"ink"`
}

var groups = []group{
	{"schema", []int{15202, 15215, 15216}, "#4e79a7", "#2c5282"},
	{"dang-prepared", []int{15223}, "#f28e2b", "#b35f0c"},
	{"session-values", []int{15224, 15226}, "#edc948", "#8a6d00"},
	{"contenthash", []int{15212, 15213}, "#59a14f", "#2f6b27"},
	{"leases", []int{15232}, "#9c755f", "#5e4632"},
	{"span-index", []int{15220}, "#e15759", "#b02a2d"},
	{"log-index", []int{15221}, "#ff9da7", "#c2185b"},
}

type pr struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Note   string `json:"note,omitempty"`
}

var prs = []pr{
	{15202, "core: dedupe and bound schema builder memos", ""},
	{15215, "core: share module schemas across client roots", ""},
	{15216, "core: share artifact module servers across clients", ""},
	{15223, "dang: bound prepared programs per module", ""},
	{15224, "dang: bound received values kept per session", ""},
	{15226, "core: bound receivers a session keeps converted", ""},
	{15212, "engine: forget contenthash trees of collected snapshots", ""},
	{15213, "engine: keep contenthash trees on disk", ""},
	{15232, "engine/snapshots: one ancestry lease for dagql owners", ""},
	{15220, "clientdb: intern the span index's span IDs", ""},
	{15221, "clientdb: intern the log index, pack row metadata", ""},
	{15218, "core: filter FindSpans in the store, release imports", "Releases idle imported Cloud traces and filters FindSpans in the store; this workload imports no Cloud trace, so it has no layer of its own (its effect on FindSpans' transient memory is not retained heap)."},
	{15219, "core: stream FindCalls' frames in chunks", "Lowers peak memory while FindCalls runs, not retained heap: no layer."},
}

// Layer is one layer of both bars, in bytes.
type Layer struct {
	layerDef
	Before int64 `json:"before"`
	After  int64 `json:"after"`
	// Removed is what the layer lost (before - after), when it shrank.
	Removed int64 `json:"removed"`
	// Grew is what the layer gained (after - before), when it grew.
	Grew int64 `json:"grew"`
	// SlopeBefore and SlopeAfter are the layer's growth per turn, bytes,
	// fitted over the second half of each side's checkpoints.
	SlopeBefore float64 `json:"slopeBefore"`
	SlopeAfter  float64 `json:"slopeAfter"`
}

// Series is one side's layers over its checkpoints.
type Series struct {
	Side      string             `json:"side"`
	Turns     []int              `json:"turns"`
	Layers    map[string][]int64 `json:"layers"`
	Total     []int64            `json:"total"`
	HeapInuse []int64            `json:"heapInuse"`
	HeapAlloc []int64            `json:"heapAlloc"`
	Sys       []int64            `json:"sys"`
	Elapsed   []float64          `json:"elapsedSeconds"`
	Errors    []int              `json:"toolErrors"`
}

type Data struct {
	Title       string   `json:"title"`
	Subtitle    string   `json:"subtitle"`
	Caption     string   `json:"caption"`
	Repo        string   `json:"repo"`
	Turn        int      `json:"turn"`
	TotalBefore int64    `json:"totalBefore"`
	TotalAfter  int64    `json:"totalAfter"`
	Layers      []Layer  `json:"layers"` // bottom to top
	Groups      []group  `json:"groups"`
	PRs         []pr     `json:"prs"`
	Before      Series   `json:"beforeSeries"`
	After       Series   `json:"afterSeries"`
	MemStats    [2]map[string]int64 `json:"memstatsAtTurn"`
	Footnotes   []string `json:"footnotes"`

	groups map[string]group
}

func main() {
	beforeDir := flag.String("before", "", "BEFORE's output directory")
	afterDir := flag.String("after", "", "AFTER's output directory")
	outDir := flag.String("out", ".", "where to write data.json, cake.svg, areas.svg and index.html")
	title := flag.String("title", "Dagger engine heap: what the open memory PRs take out of it (measured)", "")
	subtitle := flag.String("subtitle", "", "")
	caption := flag.String("caption", "", "")
	notes := flag.String("notes", "", "file of extra footnotes, one per line")
	flag.Parse()

	before, err := load(*beforeDir)
	check(err)
	after, err := load(*afterDir)
	check(err)
	if len(before) == 0 || len(after) == 0 {
		check(fmt.Errorf("no checkpoints (before: %d, after: %d)", len(before), len(after)))
	}
	d := build(before, after)
	d.Title, d.Subtitle, d.Caption = *title, *subtitle, *caption
	if *notes != "" {
		raw, err := os.ReadFile(*notes)
		check(err)
		for _, line := range strings.Split(string(raw), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				d.Footnotes = append(d.Footnotes, line)
			}
		}
	}
	check(d.validate())

	raw, err := json.MarshalIndent(d, "", "  ")
	check(err)
	check(os.MkdirAll(*outDir, 0o755))
	check(os.WriteFile(filepath.Join(*outDir, "data.json"), raw, 0o644))
	cake := d.cakeSVG()
	check(wellFormed(cake))
	areas := d.areasSVG()
	check(wellFormed(areas))
	check(os.WriteFile(filepath.Join(*outDir, "cake.svg"), []byte(cake), 0o644))
	check(os.WriteFile(filepath.Join(*outDir, "areas.svg"), []byte(areas), 0o644))
	check(os.WriteFile(filepath.Join(*outDir, "index.html"), []byte(d.html(cake, areas)), 0o644))
	d.printTable(os.Stdout)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "render:", err)
		os.Exit(1)
	}
}

func series(side string, cps []checkpoint) Series {
	s := Series{Side: side, Layers: map[string][]int64{}}
	for _, c := range cps {
		s.Turns = append(s.Turns, c.Turn)
		for _, l := range layerDefs {
			s.Layers[l.ID] = append(s.Layers[l.ID], c.layer(l.ID))
		}
		s.Total = append(s.Total, c.Heap.TotalBytes)
		s.HeapInuse = append(s.HeapInuse, c.MemStats["HeapInuse"])
		s.HeapAlloc = append(s.HeapAlloc, c.MemStats["HeapAlloc"])
		s.Sys = append(s.Sys, c.MemStats["Sys"])
		s.Elapsed = append(s.Elapsed, c.Elapsed)
		s.Errors = append(s.Errors, c.Errors)
	}
	return s
}

// slope fits a least-squares line to ys over xs from the middle on: the
// steady growth per turn, after warm-up.
func slope(xs []int, ys []int64) float64 {
	n := len(xs)
	start := n / 2
	if n-start < 2 {
		start = 0
	}
	if n-start < 2 {
		return 0
	}
	var sx, sy, sxx, sxy, m float64
	for i := start; i < n; i++ {
		x, y := float64(xs[i]), float64(ys[i])
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
		m++
	}
	den := m*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (m*sxy - sx*sy) / den
}

func build(before, after []checkpoint) *Data {
	// Compare at the last turn both sides reached.
	bi, ai := len(before)-1, len(after)-1
	for bi >= 0 && ai >= 0 && before[bi].Turn != after[ai].Turn {
		if before[bi].Turn > after[ai].Turn {
			bi--
		} else {
			ai--
		}
	}
	if bi < 0 || ai < 0 {
		check(fmt.Errorf("the sides share no checkpoint turn"))
	}
	b, a := before[bi], after[ai]
	d := &Data{
		Repo:   "https://github.com/dagger/dagger",
		Turn:   b.Turn,
		Groups: groups,
		PRs:    prs,
		Before: series("before", before[:bi+1]),
		After:  series("after", after[:ai+1]),
	}
	d.MemStats = [2]map[string]int64{b.MemStats, a.MemStats}
	for _, def := range layerDefs {
		l := Layer{layerDef: def, Before: b.layer(def.ID), After: a.layer(def.ID)}
		if l.After < l.Before {
			l.Removed = l.Before - l.After
		} else {
			l.Grew = l.After - l.Before
		}
		l.SlopeBefore = slope(d.Before.Turns, d.Before.Layers[def.ID])
		l.SlopeAfter = slope(d.After.Turns, d.After.Layers[def.ID])
		d.Layers = append(d.Layers, l)
		d.TotalBefore += l.Before
		d.TotalAfter += l.After
	}
	return d
}

func (d *Data) validate() error {
	d.groups = map[string]group{}
	for _, g := range d.Groups {
		d.groups[g.ID] = g
	}
	var b, a int64
	for _, l := range d.Layers {
		if l.Group != "" {
			if _, ok := d.groups[l.Group]; !ok {
				return fmt.Errorf("layer %s: unknown group %q", l.ID, l.Group)
			}
		}
		if l.After+l.Removed-l.Grew != l.Before {
			return fmt.Errorf("layer %s: after %d + removed %d - grew %d != before %d", l.ID, l.After, l.Removed, l.Grew, l.Before)
		}
		b += l.Before
		a += l.After
	}
	if b != d.TotalBefore || a != d.TotalAfter {
		return fmt.Errorf("layers sum to %d/%d, totals are %d/%d", b, a, d.TotalBefore, d.TotalAfter)
	}
	return nil
}

func (d *Data) groupLabel(id string) string {
	var parts []string
	for _, n := range d.groups[id].PRs {
		parts = append(parts, "#"+strconv.Itoa(n))
	}
	return strings.Join(parts, " + ")
}

// ---------------------------------------------------------------- units

// unit picks MiB or GiB for the chart's scale.
type unit struct {
	name string
	div  float64
}

func unitFor(max int64) unit {
	if max >= 2<<30 {
		return unit{"GiB", 1 << 30}
	}
	return unit{"MiB", 1 << 20}
}

func (u unit) v(b int64) float64 { return float64(b) / u.div }

func (u unit) fmt(b int64) string {
	if u.name == "GiB" {
		return fmt.Sprintf("%.2f %s", u.v(b), u.name)
	}
	return fmt.Sprintf("%.0f %s", u.v(b), u.name)
}

// niceMax rounds v up to a gridline-friendly maximum and step.
func niceMax(v float64) (max, step float64) {
	if v <= 0 {
		return 1, 0.2
	}
	mag := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10} {
		if m*mag >= v {
			max = m * mag
			break
		}
	}
	step = max / 10
	for _, s := range []float64{1, 2, 2.5, 5, 10} {
		if max/(s*mag/10) <= 12 {
			step = s * mag / 10
			break
		}
	}
	return max, step
}

func mb(b int64) string { return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20)) }

// ---------------------------------------------------------------- the cake SVG

const (
	svgW   = 1180.0
	top    = 150.0
	base   = 760.0
	bx, bw = 370.0, 150.0
	ax, aw = 680.0, 150.0
	arrowX = 560.0
	gap    = 15.0
	font   = `font-family="-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif"`
)

func esc(s string) string { return html.EscapeString(s) }

type label struct {
	segY, y float64
	ord     int
	text    string
	ink     string
	bold    bool
	italic  bool
}

func place(ls []label, maxY float64) {
	sort.SliceStable(ls, func(i, j int) bool {
		if ls[i].segY != ls[j].segY {
			return ls[i].segY < ls[j].segY
		}
		return ls[i].ord > ls[j].ord
	})
	for i := range ls {
		ls[i].y = ls[i].segY
		if i > 0 && ls[i].y < ls[i-1].y+gap {
			ls[i].y = ls[i-1].y + gap
		}
	}
	if n := len(ls); n > 0 && ls[n-1].y > maxY {
		ls[n-1].y = maxY
		for i := n - 2; i >= 0; i-- {
			if ls[i].y > ls[i+1].y-gap {
				ls[i].y = ls[i+1].y - gap
			}
		}
	}
}

func drawLabels(b *strings.Builder, ls []label, right bool) {
	edge, lx, anchor, dir := bx, bx-22, "end", -1.0
	if right {
		edge, lx, anchor, dir = ax+aw, ax+aw+22, "start", 1.0
	}
	for _, l := range ls {
		fmt.Fprintf(b, `<path d="M%.1f %.1f H%.1f L%.1f %.1f" fill="none" stroke="#9a9a9a" stroke-width="0.8"/>`+"\n",
			edge, l.segY, edge+dir*8, lx-dir*4, l.y)
		attrs := ""
		if l.bold {
			attrs += ` font-weight="600"`
		}
		if l.italic {
			attrs += ` font-style="italic"`
		}
		fmt.Fprintf(b, `<text x="%.1f" y="%.1f" text-anchor="%s" font-size="12" fill="%s"%s class="halo">%s</text>`+"\n",
			lx, l.y+4, anchor, l.ink, attrs, esc(l.text))
	}
}

func inkOn(hex string) string {
	r, g, bl := rgb(hex)
	if 0.2126*r+0.7152*g+0.0722*bl > 0.55 {
		return "#222"
	}
	return "#fff"
}

func rgb(hex string) (float64, float64, float64) {
	v, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	return float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255
}

func wrap(s string, width int) []string {
	var lines []string
	var cur []rune
	for _, w := range strings.Fields(s) {
		ww := []rune(w)
		if len(cur) > 0 && len(cur)+1+len(ww) > width {
			lines = append(lines, string(cur))
			cur = nil
		}
		if len(cur) > 0 {
			cur = append(cur, ' ')
		}
		cur = append(cur, ww...)
	}
	if len(cur) > 0 {
		lines = append(lines, string(cur))
	}
	return lines
}

func (d *Data) defs() string {
	var defs strings.Builder
	for _, g := range d.Groups {
		fmt.Fprintf(&defs, `<pattern id="hatch-%s" patternUnits="userSpaceOnUse" width="8" height="8" patternTransform="rotate(45)"><rect width="8" height="8" fill="%s" fill-opacity="0.13"/><line x1="0" y1="0" x2="0" y2="8" stroke="%s" stroke-width="2.6" stroke-opacity="0.6"/></pattern>`+"\n",
			g.ID, g.Color, g.Color)
	}
	defs.WriteString(`<pattern id="hatch-none" patternUnits="userSpaceOnUse" width="8" height="8" patternTransform="rotate(45)"><rect width="8" height="8" fill="#888" fill-opacity="0.1"/><line x1="0" y1="0" x2="0" y2="8" stroke="#888" stroke-width="2.6" stroke-opacity="0.5"/></pattern>` + "\n")
	defs.WriteString(`<marker id="arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0 L10 5 L0 10 z" fill="#333"/></marker>` + "\n")
	return defs.String()
}

func svgDoc(w, h float64, title, defs, body string) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" %s role="img" aria-label="%s">
<style>.halo{paint-order:stroke;stroke:#fff;stroke-width:3px;stroke-linejoin:round}</style>
<defs>
%s</defs>
<rect width="100%%" height="100%%" fill="#fff"/>
%s</svg>
`, w, h, w, h, font, esc(title), defs, body)
}

func (d *Data) cakeSVG() string {
	u := unitFor(max(d.TotalBefore, d.TotalAfter))
	maxV, step := niceMax(u.v(max(d.TotalBefore, d.TotalAfter)) * 1.04)
	scale := (base - top) / maxV
	yOf := func(b int64) float64 { return base - u.v(b)*scale }
	h := func(b int64) float64 { return u.v(b) * scale }

	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	p(`<text x="40" y="40" font-size="22" font-weight="700" fill="#111">%s</text>`, esc(d.Title))
	y := 64.0
	for _, line := range wrap(d.Subtitle, 160) {
		p(`<text x="40" y="%.0f" font-size="13" fill="#555">%s</text>`, y, esc(line))
		y += 18
	}
	p(`<text x="40" y="%.0f" font-size="13" fill="#555">Both bars are measured: pprof in-use heap after a forced GC, at turn %d of the same scripted agent. Hatched outline = the part of a layer gone AFTER, labelled with the PRs addressing that layer.</text>`, y, d.Turn)

	for g := 0.0; g <= maxV+1e-9; g += step {
		yy := base - g*scale
		stroke := "#ececec"
		if g == 0 {
			stroke = "#888"
		}
		p(`<line x1="70" x2="%.0f" y1="%.1f" y2="%.1f" stroke="%s" stroke-width="1"/>`, svgW-20, yy, yy, stroke)
		p(`<text x="62" y="%.1f" text-anchor="end" font-size="11" fill="#888">%s %s</text>`, yy+4, strconv.FormatFloat(g, 'f', -1, 64), u.name)
	}

	// Before bar.
	var left []label
	var cum int64
	for i, l := range d.Layers {
		hh := h(l.Before)
		y0 := yOf(cum + l.Before)
		p(`<rect x="%.1f" y="%.2f" width="%.1f" height="%.2f" fill="%s" stroke="#fff" stroke-width="1"><title>%s: %s (%s)</title></rect>`,
			bx, y0, bw, hh, l.Color, esc(l.Name), u.fmt(l.Before), esc(l.Detail))
		if hh >= 16 {
			p(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="13" font-weight="600" fill="%s">%s</text>`,
				bx+bw/2, y0+hh/2+4.5, inkOn(l.Color), u.fmt(l.Before))
		}
		left = append(left, label{segY: y0 + hh/2, ord: i, text: fmt.Sprintf("%s  %s", l.Name, u.fmt(l.Before)), ink: "#222"})
		cum += l.Before
	}
	place(left, base-6)
	drawLabels(&b, left, false)

	// After bar: what remains, solid...
	var right []label
	cum = 0
	for i, l := range d.Layers {
		hh := h(l.After)
		y0 := yOf(cum + l.After)
		if hh > 0 {
			p(`<rect x="%.1f" y="%.2f" width="%.1f" height="%.2f" fill="%s" stroke="#fff" stroke-width="1"><title>%s: %s</title></rect>`,
				ax, y0, aw, hh, l.Color, esc(l.Name), u.fmt(l.After))
		}
		if hh >= 16 {
			p(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="13" font-weight="600" fill="%s">%s</text>`,
				ax+aw/2, y0+hh/2+4.5, inkOn(l.Color), u.fmt(l.After))
		}
		text := l.Short + "  " + u.fmt(l.After)
		if l.Grew > 0 {
			text += fmt.Sprintf(" (+%s)", u.fmt(l.Grew))
		} else if l.Removed == 0 {
			text += " (unchanged)"
		}
		right = append(right, label{segY: y0 + hh/2, ord: i, text: text, ink: "#222"})
		cum += l.After
	}
	solidTop := yOf(d.TotalAfter)

	// ...then each removed part as a hatched outline on top.
	cum = d.TotalAfter
	ord := len(d.Layers)
	for _, l := range d.Layers {
		if l.Removed <= 0 {
			continue
		}
		gid, gl, ink, color := "none", "no PR", "#555", "#888"
		if l.Group != "" {
			g := d.groups[l.Group]
			gid, gl, ink, color = g.ID, d.groupLabel(g.ID), g.Ink, g.Color
		}
		hh := h(l.Removed)
		y0 := yOf(cum + l.Removed)
		p(`<rect x="%.1f" y="%.2f" width="%.1f" height="%.2f" fill="url(#hatch-%s)" stroke="%s" stroke-width="1.5" stroke-dasharray="5 3"><title>%s: −%s of %s</title></rect>`,
			ax+0.75, y0+0.75, aw-1.5, math.Max(hh-1.5, 0.5), gid, color, esc(gl), u.fmt(l.Removed), esc(l.Name))
		if hh >= 18 {
			p(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="13" font-weight="700" fill="%s" class="halo">%s</text>`,
				ax+aw/2, y0+hh/2+4.5, ink, esc(gl))
		}
		right = append(right, label{
			segY: y0 + hh/2, ord: ord,
			text: fmt.Sprintf("−%s %s · %s", u.fmt(l.Removed), l.Short, gl),
			ink:  ink, bold: true, italic: true,
		})
		ord++
		cum += l.Removed
	}
	place(right, base-6)
	drawLabels(&b, right, true)

	topY := yOf(max(d.TotalBefore, cum))
	p(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="17" font-weight="700" fill="#111">%s</text>`, bx+bw/2, yOf(d.TotalBefore)-26, u.fmt(d.TotalBefore))
	p(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="11" fill="#555">measured (pprof in-use)</text>`, bx+bw/2, yOf(d.TotalBefore)-10)
	p(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="17" font-weight="700" fill="#111">%s</text>`, ax+aw/2, topY-26, u.fmt(d.TotalAfter))
	p(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="11" fill="#555">measured (pprof in-use)</text>`, ax+aw/2, topY-10)

	delta := d.TotalBefore - d.TotalAfter
	p(`<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" stroke="#999" stroke-dasharray="4 3"/>`, bx+bw, ax, yOf(d.TotalBefore), yOf(d.TotalBefore))
	p(`<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" stroke="#111" stroke-width="2.5"/>`, ax-14, ax+aw, solidTop, solidTop)
	if math.Abs(yOf(d.TotalBefore)-solidTop) > 12 {
		p(`<line x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f" stroke="#333" stroke-width="1.5" marker-end="url(#arrow)"/>`, arrowX, arrowX, yOf(d.TotalBefore)+4, solidTop-4)
	}
	mid := (yOf(d.TotalBefore) + solidTop) / 2
	sign := "−"
	if delta < 0 {
		sign = "+"
	}
	p(`<text x="%.0f" y="%.1f" font-size="15" font-weight="700" fill="#111">%s%s</text>`, arrowX+10, mid-4, sign, u.fmt(abs(delta)))
	p(`<text x="%.0f" y="%.1f" font-size="12" fill="#555">(%s%.0f%%)</text>`, arrowX+10, mid+13, sign, 100*math.Abs(float64(delta))/float64(d.TotalBefore))
	p(`<text x="%.1f" y="%.1f" text-anchor="end" font-size="13" font-weight="700" fill="#111" class="halo">%s remain</text>`, ax-18, solidTop+17, u.fmt(d.TotalAfter))

	p(`<text x="%.1f" y="%.0f" text-anchor="middle" font-size="14" font-weight="700" fill="#111">Before</text>`, bx+bw/2, base+22)
	p(`<text x="%.1f" y="%.0f" text-anchor="middle" font-size="12" fill="#555">main @ 0120da0, turn %d</text>`, bx+bw/2, base+39, d.Turn)
	p(`<text x="%.1f" y="%.0f" text-anchor="middle" font-size="14" font-weight="700" fill="#111">After all PRs</text>`, ax+aw/2, base+22)
	p(`<text x="%.1f" y="%.0f" text-anchor="middle" font-size="12" fill="#555">memory-fixes-combined-lease, turn %d</text>`, ax+aw/2, base+39, d.Turn)

	y = base + 78
	p(`<text x="40" y="%.0f" font-size="13" font-weight="700" fill="#111">PR groups</text>`, y)
	x := 120.0
	for _, g := range d.Groups {
		gl := d.groupLabel(g.ID)
		w := 14 + 6 + float64(len([]rune(gl)))*7.2 + 22
		if x+w > svgW-30 {
			x = 120
			y += 22
		}
		p(`<rect x="%.1f" y="%.0f" width="14" height="14" fill="url(#hatch-%s)" stroke="%s" stroke-width="1.5" stroke-dasharray="3 2"/>`, x, y-11, g.ID, g.Color)
		p(`<text x="%.1f" y="%.0f" font-size="12" font-weight="600" fill="%s">%s</text>`, x+20, y+1, g.Ink, esc(gl))
		x += w
	}

	y += 30
	for i, line := range wrap(d.Caption, 150) {
		weight := "400"
		if i == 0 {
			weight = "600"
		}
		p(`<text x="40" y="%.0f" font-size="13" font-weight="%s" fill="#8a1c1c">%s</text>`, y, weight, esc(line))
		y += 18
	}
	for i, f := range d.Footnotes {
		for j, line := range wrap(f, 170) {
			prefix := "   "
			if j == 0 {
				prefix = strconv.Itoa(i+1) + ". "
			}
			p(`<text x="44" y="%.0f" font-size="11.5" fill="#555" xml:space="preserve">%s%s</text>`, y+6, prefix, esc(line))
			y += 15
		}
	}
	return svgDoc(svgW, y+20, d.Title, d.defs(), b.String())
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ---------------------------------------------------------------- areas SVG

func (d *Data) areasSVG() string {
	const (
		w      = 1180.0
		panelW = 500.0
		ph     = 380.0
		y0     = 90.0
	)
	var maxB int64
	for _, s := range []Series{d.Before, d.After} {
		for _, t := range s.Total {
			maxB = max(maxB, t)
		}
		for _, t := range s.HeapInuse {
			maxB = max(maxB, t)
		}
	}
	u := unitFor(maxB)
	maxV, step := niceMax(u.v(maxB) * 1.04)
	maxTurn := 1
	for _, s := range []Series{d.Before, d.After} {
		for _, t := range s.Turns {
			maxTurn = max(maxTurn, t)
		}
	}
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	p(`<text x="40" y="36" font-size="20" font-weight="700" fill="#111">Engine heap by layer over the agent's turns</text>`)
	p(`<text x="40" y="58" font-size="13" fill="#555">Stacked pprof in-use heap after a forced GC, per checkpoint; the dashed line is runtime HeapInuse. Same scale on both sides.</text>`)
	for pi, s := range []Series{d.Before, d.After} {
		x0 := 90.0 + float64(pi)*(panelW+90)
		xOf := func(t int) float64 { return x0 + float64(t)/float64(maxTurn)*panelW }
		yOf := func(v float64) float64 { return y0 + ph - v/maxV*ph }
		name := map[string]string{"before": "Before (main @ 0120da0)", "after": "After all PRs (memory-fixes-combined-lease)"}[s.Side]
		p(`<text x="%.0f" y="%.0f" font-size="14" font-weight="700" fill="#111">%s</text>`, x0, y0-12, esc(name))
		for g := 0.0; g <= maxV+1e-9; g += step {
			yy := yOf(g)
			p(`<line x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f" stroke="%s"/>`, x0, x0+panelW, yy, yy, map[bool]string{true: "#888", false: "#ececec"}[g == 0])
			p(`<text x="%.0f" y="%.1f" text-anchor="end" font-size="10.5" fill="#888">%s</text>`, x0-6, yy+4, strconv.FormatFloat(g, 'f', -1, 64))
		}
		p(`<text x="%.0f" y="%.0f" font-size="10.5" fill="#888" text-anchor="end">%s</text>`, x0-6, y0-2, u.name)
		tstep := max(1, maxTurn/10)
		for t := 0; t <= maxTurn; t += tstep {
			p(`<text x="%.1f" y="%.0f" text-anchor="middle" font-size="10.5" fill="#888">%d</text>`, xOf(t), y0+ph+16, t)
		}
		p(`<text x="%.0f" y="%.0f" text-anchor="middle" font-size="11" fill="#555">turn</text>`, x0+panelW/2, y0+ph+34)
		if len(s.Turns) == 0 {
			continue
		}
		cum := make([]float64, len(s.Turns))
		for _, l := range d.Layers {
			vals := s.Layers[l.ID]
			var pts []string
			for i, t := range s.Turns {
				pts = append(pts, fmt.Sprintf("%.1f,%.1f", xOf(t), yOf(cum[i]+u.v(vals[i]))))
			}
			for i := len(s.Turns) - 1; i >= 0; i-- {
				pts = append(pts, fmt.Sprintf("%.1f,%.1f", xOf(s.Turns[i]), yOf(cum[i])))
			}
			for i := range cum {
				cum[i] += u.v(vals[i])
			}
			p(`<polygon points="%s" fill="%s" stroke="#fff" stroke-width="0.6"><title>%s</title></polygon>`, strings.Join(pts, " "), l.Color, esc(l.Name))
		}
		var inuse []string
		for i, t := range s.Turns {
			inuse = append(inuse, fmt.Sprintf("%.1f,%.1f", xOf(t), yOf(u.v(s.HeapInuse[i]))))
		}
		p(`<polyline points="%s" fill="none" stroke="#111" stroke-width="1.4" stroke-dasharray="5 3"><title>HeapInuse</title></polyline>`, strings.Join(inuse, " "))
		last := len(s.Turns) - 1
		p(`<text x="%.1f" y="%.1f" text-anchor="end" font-size="12" font-weight="700" fill="#111" class="halo">%s</text>`, xOf(s.Turns[last]), yOf(cum[last])-8, u.fmt(s.Total[last]))
	}
	// Legend.
	y := y0 + ph + 64
	x := 90.0
	for i := len(d.Layers) - 1; i >= 0; i-- {
		l := d.Layers[i]
		w := 14 + 6 + float64(len([]rune(l.Short)))*7 + 20
		if x+w > 1150 {
			x = 90
			y += 20
		}
		p(`<rect x="%.1f" y="%.0f" width="14" height="14" fill="%s"/>`, x, y-11, l.Color)
		p(`<text x="%.1f" y="%.0f" font-size="12" fill="#222">%s</text>`, x+20, y+1, esc(l.Short))
		x += w
	}
	p(`<line x1="%.1f" x2="%.1f" y1="%.0f" y2="%.0f" stroke="#111" stroke-width="1.4" stroke-dasharray="5 3"/>`, x, x+24, y-4, y-4)
	p(`<text x="%.1f" y="%.0f" font-size="12" fill="#222">HeapInuse</text>`, x+30, y+1)
	return svgDoc(w, y+24, "Engine heap by layer over turns", d.defs(), b.String())
}

func wellFormed(s string) error {
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("svg is not well-formed: %w", err)
		}
	}
}

// ---------------------------------------------------------------- HTML

func (d *Data) prLink(n int) string {
	return fmt.Sprintf(`<a href="%s/pull/%d">#%d</a>`, esc(d.Repo), n, n)
}

func (d *Data) html(cake, areas string) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	p(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif;max-width:1180px;margin:24px auto;padding:0 16px;color:#222;line-height:1.45}
figure{margin:0 0 24px}
figure svg{max-width:100%%;height:auto;border:1px solid #eee}
.caption{background:#fff4f4;border-left:4px solid #8a1c1c;padding:10px 14px}
table{border-collapse:collapse;width:100%%;font-size:14px;margin:8px 0 24px}
th,td{border-bottom:1px solid #e4e4e4;padding:6px 8px;text-align:left;vertical-align:top}
th{background:#fafafa}
td.n{text-align:right;white-space:nowrap;font-variant-numeric:tabular-nums}
.sw{display:inline-block;width:12px;height:12px;border-radius:2px;margin-right:6px;vertical-align:-1px}
.muted{color:#666;font-size:13px}
tfoot td{font-weight:700}
</style>
</head>
<body>`, esc(d.Title))
	p(`<h1>%s</h1>`, esc(d.Title))
	p(`<p>%s</p>`, esc(d.Subtitle))
	if d.Caption != "" {
		p(`<p class="caption">%s</p>`, esc(d.Caption))
	}
	p(`<figure>%s</figure>`, cake)
	p(`<figure>%s</figure>`, areas)

	p(`<h2>Layers at turn %d (bottom to top)</h2>`, d.Turn)
	p(`<table><thead><tr><th>Layer</th><th>Before (MiB)</th><th>After (MiB)</th><th>Change (MiB)</th><th>Growth before (MiB/turn)</th><th>Growth after (MiB/turn)</th><th>Addressed by</th></tr></thead><tbody>`)
	for _, l := range d.Layers {
		by := `<span class="muted">no PR</span>`
		if l.Group != "" {
			g := d.groups[l.Group]
			var links []string
			for _, n := range g.PRs {
				links = append(links, d.prLink(n))
			}
			by = fmt.Sprintf(`<span class="sw" style="background:%s"></span>%s`, g.Color, strings.Join(links, " + "))
		}
		p(`<tr><td><span class="sw" style="background:%s"></span><strong>%s</strong><br><span class="muted">%s</span></td><td class="n">%.1f</td><td class="n">%.1f</td><td class="n">%+.1f</td><td class="n">%.2f</td><td class="n">%.2f</td><td>%s</td></tr>`,
			l.Color, esc(l.Name), esc(l.Detail), mib(l.Before), mib(l.After), mib(l.After-l.Before), l.SlopeBefore/(1<<20), l.SlopeAfter/(1<<20), by)
	}
	sb := slope(d.Before.Turns, d.Before.Total)
	sa := slope(d.After.Turns, d.After.Total)
	p(`</tbody><tfoot><tr><td>Total (pprof in-use)</td><td class="n">%.1f</td><td class="n">%.1f</td><td class="n">%+.1f (%+.0f%%)</td><td class="n">%.2f</td><td class="n">%.2f</td><td></td></tr></tfoot></table>`,
		mib(d.TotalBefore), mib(d.TotalAfter), mib(d.TotalAfter-d.TotalBefore), 100*float64(d.TotalAfter-d.TotalBefore)/float64(d.TotalBefore), sb/(1<<20), sa/(1<<20))

	p(`<h2>Runtime memory at turn %d</h2>`, d.Turn)
	p(`<table><thead><tr><th>MemStats (after the profile's forced GC)</th><th>Before (MiB)</th><th>After (MiB)</th></tr></thead><tbody>`)
	for _, k := range []string{"HeapAlloc", "HeapInuse", "HeapSys", "Sys"} {
		p(`<tr><td>%s</td><td class="n">%.1f</td><td class="n">%.1f</td></tr>`, k, mib(d.MemStats[0][k]), mib(d.MemStats[1][k]))
	}
	p(`</tbody></table>`)

	p(`<h2>Checkpoints</h2>`)
	for _, s := range []Series{d.Before, d.After} {
		p(`<h3>%s</h3>`, esc(s.Side))
		p(`<table><thead><tr><th>Turn</th><th>Elapsed</th>`)
		for i := len(d.Layers) - 1; i >= 0; i-- {
			p(`<th>%s</th>`, esc(d.Layers[i].Short))
		}
		p(`<th>Total</th><th>HeapInuse</th><th>Tool errors</th></tr></thead><tbody>`)
		for i, t := range s.Turns {
			p(`<tr><td class="n">%d</td><td class="n">%.0f min</td>`, t, s.Elapsed[i]/60)
			for j := len(d.Layers) - 1; j >= 0; j-- {
				p(`<td class="n">%.1f</td>`, mib(s.Layers[d.Layers[j].ID][i]))
			}
			p(`<td class="n">%.1f</td><td class="n">%.1f</td><td class="n">%d</td></tr>`, mib(s.Total[i]), mib(s.HeapInuse[i]), s.Errors[i])
		}
		p(`</tbody></table>`)
	}

	p(`<h2>PRs</h2>`)
	p(`<table><thead><tr><th>PR</th><th>Title</th><th>Note</th></tr></thead><tbody>`)
	for _, pr := range d.PRs {
		p(`<tr><td style="white-space:nowrap">%s</td><td>%s</td><td class="muted">%s</td></tr>`, d.prLink(pr.Number), esc(pr.Title), esc(pr.Note))
	}
	p(`</tbody></table>`)
	if len(d.Footnotes) > 0 {
		p(`<h2>Notes</h2><ol>`)
		for _, f := range d.Footnotes {
			p(`<li>%s</li>`, esc(f))
		}
		p(`</ol>`)
	}
	p(`<p class="muted">Rendered from the agent's checkpoints by <code>go run ./hack/memory-cake/render</code>, which checks that every checkpoint's layers sum to its total; the data is in <code>data.json</code>.</p>`)
	p(`</body>
</html>`)
	return b.String()
}

func mib(b int64) float64 { return float64(b) / (1 << 20) }

func (d *Data) printTable(w io.Writer) {
	fmt.Fprintf(w, "turn %d\n%-42s %10s %10s %10s %9s %9s\n", d.Turn, "layer (bottom→top)", "before", "after", "change", "MiB/t b", "MiB/t a")
	for _, l := range d.Layers {
		fmt.Fprintf(w, "%-42s %10s %10s %+9.1f %9.2f %9.2f\n", l.Name, mb(l.Before), mb(l.After), mib(l.After-l.Before), l.SlopeBefore/(1<<20), l.SlopeAfter/(1<<20))
	}
	fmt.Fprintf(w, "%-42s %10s %10s %+9.1f %9.2f %9.2f\n", "TOTAL", mb(d.TotalBefore), mb(d.TotalAfter), mib(d.TotalAfter-d.TotalBefore),
		slope(d.Before.Turns, d.Before.Total)/(1<<20), slope(d.After.Turns, d.After.Total)/(1<<20))
}
