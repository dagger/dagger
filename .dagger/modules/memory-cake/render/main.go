// Command render draws the memory cake from cakeJson documents (see
// ../main.dang): it merges them (a later document's demo replaces an earlier
// one's, for reruns) and writes, into -out:
//
//	data.json   the merged document, annotated with families and workloads;
//	            it is itself a valid input, to re-render without re-measuring
//	cake.svg    the engine cake, the CLI cake and the transient panel
//	index.html  a self-contained page: the SVG inline, the table, footnotes
//	cake.md     the markdown table
//
// It checks the geometry it drew (stack sums, contiguity, label spacing,
// bounds) and that the SVG parses, and fails if any check does.
//
//	go run . -out ../results doc1.json doc2.json ...
package main

import (
	"bytes"
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
	"strings"
)

type Measurement struct {
	Ref           string `json:"ref"`
	Sha           string `json:"sha"`
	CLI           bool   `json:"cli"`
	Seconds       int    `json:"seconds"`
	Baseline      int64  `json:"baseline"`
	Live          int64  `json:"live"`
	After         int64  `json:"after"`
	Focus         int64  `json:"focus"`
	FocusBaseline int64  `json:"focusBaseline"`
	FocusAfter    int64  `json:"focusAfter"`
	Alloc         int64  `json:"alloc"`
	FocusAlloc    int64  `json:"focusAlloc"`
	FocusTop      string `json:"focusTop"`
	FocusAllocTop string `json:"focusAllocTop"`
}

type Demo struct {
	PR         string       `json:"pr"`
	Title      string       `json:"title"`
	Focus      string       `json:"focus,omitempty"`
	FocusAlloc string       `json:"focusAlloc,omitempty"`
	CLI        bool         `json:"cli"`
	Before     *Measurement `json:"before,omitempty"`
	After      *Measurement `json:"after,omitempty"`
	Error      string       `json:"error,omitempty"`

	// Added by render.
	Family   string `json:"family,omitempty"`
	Short    string `json:"short,omitempty"`
	Workload string `json:"workload,omitempty"`
}

type Tool struct {
	Tool           string `json:"tool"`
	Searches       int    `json:"searches"`
	AllocPerSearch int64  `json:"allocPerSearch"`
	HeapStart      int64  `json:"heapStart"`
	Peak           int64  `json:"peak"`
}

type Trace struct {
	Ref   string `json:"ref"`
	Sha   string `json:"sha,omitempty"`
	Text  string `json:"text,omitempty"`
	Tools []Tool `json:"tools,omitempty"`
	Error string `json:"error,omitempty"`
}

func (t *Trace) tool(name string) *Tool {
	if t == nil {
		return nil
	}
	for i := range t.Tools {
		if t.Tools[i].Tool == name {
			return &t.Tools[i]
		}
	}
	return nil
}

type Traces struct {
	Before *Trace `json:"before"`
	After  *Trace `json:"after"`
}

type Doc struct {
	Before     string   `json:"before"`
	After      string   `json:"after"`
	BeforeSha  string   `json:"beforeSha,omitempty"`
	AfterSha   string   `json:"afterSha,omitempty"`
	Markdown   string   `json:"markdown,omitempty"`
	Demos      []*Demo  `json:"demos"`
	TracePeaks *Traces  `json:"tracePeaks"`
	Notes      []string `json:"notes,omitempty"`
}

type family struct {
	key, name string
	colors    []string
}

var families = []family{
	{"schema", "schema servers", []string{"#1f4e9c", "#3b78d8", "#8fb4ee"}},
	{"contenthash", "contenthash + leases", []string{"#a34100", "#e8741c", "#f6b26b"}},
	{"dang", "Dang + receivers", []string{"#1e7b3a", "#3fae5c", "#94d6a4"}},
	{"telemetry", "telemetry", []string{"#5b2a86", "#8e5cc4", "#c3a6e6"}},
}

type meta struct {
	family, short, workload string
}

// Cake order, bottom layer first; the CLI demos last.
var order = []string{
	"15202", "15215", "15216",
	"15213", "15212", "15232",
	"15223", "15224", "15226",
	"15220", "15221",
	"15227", "15228", "15229",
}

var metas = map[string]meta{
	"15202": {"schema", "schema builder memos", "1 session loads 300 distinct Dang modules (Directory.asModule), 10 per query"},
	"15215": {"schema", "module schemas per client root", "300 nested-client calls of a Go module function that calls a Dang dependency"},
	"15216": {"schema", "artifact module servers per client", "40 concurrent nested clients run the checks of a 10-module Dang workspace"},
	"15213": {"contenthash", "contenthash trees on disk", "1 session hashes a 20k-file directory and 200 one-file edits of it"},
	"15212": {"contenthash", "trees of collected snapshots", "200 one-file edits of a 20k-file directory hashed in separate sessions, then a cache prune"},
	"15232": {"contenthash", "one ancestry lease per owner", "16 withNewFile chains of 1000 synced results each, each owning its snapshot"},
	"15223": {"dang", "Dang prepared programs", "40 comment-only variants of a 300-function Dang module, one call each"},
	"15224": {"dang", "Dang received values", "250 chained calls on a Dang object whose list state grows by 256 per call"},
	"15226": {"dang", "converted module receivers", "200 distinct results of a 1 MiB Dang object, one function call each"},
	"15220": {"telemetry", "span index span IDs", "60k cheap uncached API calls with the session's telemetry index open"},
	"15221": {"telemetry", "log index interning", "60k cheap uncached API calls, each logging its payload, log index open"},
	"15227": {"telemetry", "CLI metric points + log churn", "a long agent session's telemetry streamed into a headless-TUI `dagger run`: 100 metric exports x 200 calls x 8 gauges, exemplars"},
	"15228": {"telemetry", "pruned span trees pin walkers", "300k spans of a long agent session (tests, logs) streamed into a headless TUI rendering frames"},
	"15229": {"telemetry", "interned span digests/attrs", "300k spans, mostly dagql calls with frames, inputs, outputs, cache attrs, into a headless TUI"},
}

func colorOf(pr string) string {
	m := metas[pr]
	i := 0
	for _, p := range order {
		if p == pr {
			break
		}
		if metas[p].family == m.family {
			i++
		}
	}
	for _, f := range families {
		if f.key == m.family {
			return f.colors[i%len(f.colors)]
		}
	}
	return "#999"
}

const mibF = 1024 * 1024

func mib(b int64) float64 { return float64(b) / mibF }

func fmtMiB(b int64) string { return fmt.Sprintf("%.1f MiB", mib(b)) }

func fmtSigned(b int64) string {
	if b < 0 {
		return "−" + fmtMiB(-b)
	}
	return "+" + fmtMiB(b)
}

func ratio(before, after int64) string {
	if after <= 0 || mib(after) < 0.05 {
		if before <= 0 {
			return "–"
		}
		return "→0"
	}
	return fmt.Sprintf("%.1f×", float64(before)/float64(after))
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// ---- checks ----------------------------------------------------------------

var failures []string
var passed int

func check(ok bool, format string, args ...any) {
	if ok {
		passed++
		return
	}
	failures = append(failures, fmt.Sprintf(format, args...))
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// ---- svg -------------------------------------------------------------------

const width = 1240.0

type svg struct {
	bytes.Buffer
	maxY float64
}

func (s *svg) el(format string, args ...any) { fmt.Fprintf(s, format+"\n", args...) }

func (s *svg) inBounds(what string, x, y float64) {
	check(x >= 0 && x <= width && y >= 0, "%s at (%.1f, %.1f) is outside the canvas", what, x, y)
	if y > s.maxY {
		s.maxY = y
	}
}

// Font sizes by class, for estimating text extents.
var fontSize = map[string]float64{"h1": 22, "h2": 17, "sub": 12, "lbl": 12, "small": 11, "bar": 13, "total": 13, "delta": 14}

func (s *svg) text(x, y float64, anchor, class, txt string) {
	s.inBounds("text "+txt, x, y)
	// ~0.56 em per character in a sans-serif font: a generous estimate.
	w := 0.56 * fontSize[class] * float64(len([]rune(txt)))
	var left float64
	switch anchor {
	case "end":
		left = x - w
	case "middle":
		left = x - w/2
	default:
		left = x
	}
	check(left >= 0 && left+w <= width, "text %q (est. %.0f..%.0f) overflows the canvas", txt, left, left+w)
	s.el(`<text x="%.1f" y="%.1f" text-anchor="%s" class="%s">%s</text>`, x, y, anchor, class, esc(txt))
}

func esc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// spread places labels at their desired ys (ascending), at least gap apart,
// within [lo, hi].
func spread(ys []float64, gap, lo, hi float64) []float64 {
	out := append([]float64(nil), ys...)
	for i := range out {
		if out[i] < lo {
			out[i] = lo
		}
		if i > 0 && out[i] < out[i-1]+gap {
			out[i] = out[i-1] + gap
		}
	}
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] > hi {
			out[i] = hi
		}
		if i < len(out)-1 && out[i] > out[i+1]-gap {
			out[i] = out[i+1] - gap
		}
	}
	return out
}

type layer struct {
	d          *Demo
	y, h       float64 // top, height
	bytes      int64
	labelY     float64
	center     float64
	labelLines string
}

// cake draws one two-bar cake at y0 and returns its height.
func (s *svg) cake(y0 float64, title, subtitle string, demos []*Demo, beforeLabel, afterLabel string, barMaxH float64) float64 {
	const (
		barW     = 150.0
		gapW     = 180.0
		labelGap = 15.0
	)
	bx := 400.0
	ax := bx + barW + gapW
	s.text(20, y0+22, "start", "h2", title)
	s.text(20, y0+42, "start", "sub", subtitle)

	top := y0 + 90 // top of the tallest bar
	bottom := top + barMaxH

	var sumB, sumA int64
	for _, d := range demos {
		sumB += d.Before.Focus
		sumA += d.After.Focus
	}
	maxTotal := math.Max(float64(sumB), float64(sumA))
	scale := barMaxH / maxTotal

	stack := func(side string, x float64, get func(*Demo) int64) []*layer {
		y := bottom
		var ls []*layer
		var hSum float64
		for _, d := range demos {
			b := get(d)
			h := float64(b) * scale
			l := &layer{d: d, y: y - h, h: h, bytes: b, center: y - h/2}
			check(near(l.y+l.h, y), "%s #%s layer is not contiguous", side, d.PR)
			y -= h
			hSum += h
			ls = append(ls, l)
		}
		var total int64
		for _, l := range ls {
			total += l.bytes
		}
		check(near(hSum, float64(total)*scale), "%s stack height %.3f != total %.3f", side, hSum, float64(total)*scale)
		check(near(bottom-hSum, ls[len(ls)-1].y), "%s stack top mismatch", side)
		check(hSum <= barMaxH+1e-6, "%s stack taller than the panel", side)
		return ls
	}
	bl := stack("before", bx, func(d *Demo) int64 { return d.Before.Focus })
	al := stack("after", ax, func(d *Demo) int64 { return d.After.Focus })

	// Connectors, then bars, so the bars' edges stay crisp.
	for i := range bl {
		b, a := bl[i], al[i]
		s.el(`<polygon points="%.2f,%.2f %.2f,%.2f %.2f,%.2f %.2f,%.2f" fill="%s" opacity="0.18"/>`,
			bx+barW, b.y, ax, a.y, ax, a.y+a.h, bx+barW, b.y+b.h, colorOf(b.d.PR))
	}
	for _, side := range []struct {
		x  float64
		ls []*layer
	}{{bx, bl}, {ax, al}} {
		for _, l := range side.ls {
			s.inBounds("layer #"+l.d.PR, side.x, l.y+l.h)
			s.el(`<rect x="%.2f" y="%.2f" width="%.1f" height="%.2f" fill="%s"><title>#%s %s: %s</title></rect>`,
				side.x, l.y, barW, l.h, colorOf(l.d.PR), l.d.PR, esc(l.d.Title), fmtMiB(l.bytes))
		}
	}

	// Labels, spread so they never overlap, with leader lines.
	place := func(ls []*layer) {
		ys := make([]float64, len(ls))
		// Ascending y: the stack's top layer first.
		idx := make([]int, len(ls))
		for i := range ls {
			idx[i] = len(ls) - 1 - i
			ys[i] = ls[idx[i]].center + 4
		}
		got := spread(ys, labelGap, y0+70, bottom+4)
		for i, y := range got {
			ls[idx[i]].labelY = y
			if i > 0 {
				check(y-got[i-1] >= labelGap-1e-6, "labels #%s and #%s overlap", ls[idx[i]].d.PR, ls[idx[i-1]].d.PR)
			}
		}
	}
	place(bl)
	place(al)
	for _, l := range bl {
		lx := bx - 40
		s.el(`<polyline points="%.1f,%.2f %.1f,%.2f %.1f,%.2f" class="leader"/>`, lx+4, l.labelY-4, bx-14, l.labelY-4, bx, l.center)
		s.text(lx, l.labelY, "end", "lbl", fmt.Sprintf("#%s %s · %s", l.d.PR, l.d.Short, fmtMiB(l.bytes)))
	}
	for i, l := range al {
		b := bl[i]
		lx := ax + barW + 40
		s.el(`<polyline points="%.1f,%.2f %.1f,%.2f %.1f,%.2f" class="leader"/>`, ax+barW, l.center, ax+barW+14, l.labelY-4, lx-4, l.labelY-4)
		s.text(lx, l.labelY, "start", "lbl", fmt.Sprintf("#%s · %s · Δ %s (%s)", l.d.PR, fmtMiB(l.bytes), fmtSigned(l.bytes-b.bytes), ratio(b.bytes, l.bytes)))
	}

	// Totals above, ref names below.
	s.text(bx+barW/2, bottom-float64(sumB)*scale-10, "middle", "total", fmtMiB(sumB))
	s.text(ax+barW/2, bottom-float64(sumA)*scale-10, "middle", "total", fmtMiB(sumA))
	s.el(`<line x1="%.1f" y1="%.2f" x2="%.1f" y2="%.2f" class="axis"/>`, bx-20, bottom, ax+barW+20, bottom)
	s.text(bx+barW/2, bottom+22, "middle", "bar", "BEFORE")
	s.text(bx+barW/2, bottom+38, "middle", "sub", beforeLabel)
	s.text(ax+barW/2, bottom+22, "middle", "bar", "AFTER")
	s.text(ax+barW/2, bottom+38, "middle", "sub", afterLabel)
	s.text((bx+barW+ax)/2, bottom+22, "middle", "delta", fmtSigned(sumA-sumB)+" ("+ratio(sumB, sumA)+")")
	return bottom + 50 - y0
}

func (s *svg) legend(y float64) float64 {
	x := 20.0
	for _, f := range families {
		s.el(`<rect x="%.1f" y="%.1f" width="14" height="14" fill="%s"/>`, x, y-11, f.colors[1])
		s.text(x+20, y, "start", "lbl", f.name)
		x += 30 + 8*float64(len(f.name))
	}
	return 24
}

// transient draws #15218/#15219's per-search cost, before vs after.
func (s *svg) transient(y0 float64, tr *Traces) float64 {
	s.text(20, y0+22, "start", "h2", "Transient: LLM trace tools per search (#15218 FindSpans, #15219 FindCalls)")
	s.text(20, y0+42, "start", "sub", "Engine heap, 20k cheap calls in the session. Nothing is retained after a search, so this is not part of the cakes.")
	if tr == nil || tr.Before == nil || tr.After == nil || tr.Before.Error != "" || tr.After.Error != "" {
		s.text(20, y0+70, "start", "lbl", "tracePeaks: not measured")
		return 90
	}
	metrics := []struct {
		name string
		get  func(*Tool) int64
	}{
		{"allocated per search", func(t *Tool) int64 { return t.AllocPerSearch }},
		{"live heap peak during searches", func(t *Tool) int64 { return t.Peak - t.HeapStart }},
	}
	tools := []struct{ name, pr string }{{"FindSpans", "15218"}, {"FindCalls", "15219"}}
	colW := 560.0
	y := y0 + 64
	for mi, m := range metrics {
		x0 := 20 + float64(mi)*(colW+60)
		var max int64
		for _, t := range tools {
			for _, side := range []*Trace{tr.Before, tr.After} {
				if tt := side.tool(t.name); tt != nil && m.get(tt) > max {
					max = m.get(tt)
				}
			}
		}
		s.text(x0, y+10, "start", "bar", m.name)
		barX := x0 + 150
		barMax := colW - 150 - 90
		ry := y + 24
		for _, t := range tools {
			s.text(x0, ry+20, "start", "lbl", t.name+" (#"+t.pr+")")
			for si, side := range []*Trace{tr.Before, tr.After} {
				tt := side.tool(t.name)
				if tt == nil {
					continue
				}
				v := m.get(tt)
				w := 0.0
				if max > 0 && v > 0 {
					w = float64(v) / float64(max) * barMax
				}
				check(w >= 0 && w <= barMax+1e-6, "transient bar %s %s out of range", t.name, m.name)
				fill := "#9a9a9a"
				lbl := "before"
				if si == 1 {
					fill = families[3].colors[1]
					lbl = "after"
				}
				by := ry + float64(si)*16
				s.inBounds("transient bar", barX+w, by+12)
				s.el(`<rect x="%.1f" y="%.1f" width="%.2f" height="12" fill="%s"/>`, barX, by, w, fill)
				val := fmtMiB(v)
				if mi == 1 {
					val = fmtSigned(v)
				}
				s.text(barX+w+6, by+10, "start", "small", lbl+" "+val)
			}
			ry += 44
		}
	}
	y += 24 + 2*44 + 8
	ls := func(t *Trace) string {
		if tt := t.tool("ListServices"); tt != nil {
			return fmt.Sprintf("%s/search, peak %s", fmtMiB(tt.AllocPerSearch), fmtSigned(tt.Peak-tt.HeapStart))
		}
		return "n/a"
	}
	s.text(20, y+8, "start", "small", "Baseline (the LLM's own overhead, a ListServices call with no trace search): before "+ls(tr.Before)+"; after "+ls(tr.After)+".")
	return y + 24 - y0
}

func render(doc *Doc) (string, []string) {
	var engine, cli []*Demo
	var skipped []string
	for _, d := range doc.Demos {
		if d.Error != "" || d.Before == nil || d.After == nil {
			skipped = append(skipped, d.PR)
			continue
		}
		if d.CLI {
			cli = append(cli, d)
		} else {
			engine = append(engine, d)
		}
	}
	s := &svg{}
	y := 10.0
	s.text(20, y+24, "start", "h1", "The memory cake: what each memory PR frees")
	s.text(20, y+44, "start", "sub", fmt.Sprintf("Focus bytes of the live Go heap (inuse_space, gc=1), BEFORE = %s (main) vs AFTER = %s @ %s (main + all memory PRs).", short(doc.BeforeSha), doc.After, short(doc.AfterSha)))
	s.text(20, y+60, "start", "sub", "Sum of per-PR targeted workloads, each measured in its own session on a fresh engine; not one combined workload.")
	y += 80
	y += s.legend(y + 6)
	beforeLabel := short(doc.BeforeSha) + " (main)"
	afterLabel := doc.After + " (" + short(doc.AfterSha) + ")"
	if len(engine) > 0 {
		y += s.cake(y, "Engine cake: the engine's retained heap, per PR",
			"Each layer: one PR's focus bytes at the end of its workload, session still open.",
			engine, beforeLabel, afterLabel, 520) + 20
	}
	if len(cli) > 0 {
		y += s.cake(y, "CLI cake: the `dagger` CLI process heap, per PR",
			"Each layer: one PR's focus bytes in the CLI (headless pretty TUI) once its workload's telemetry is ingested.",
			cli, beforeLabel, afterLabel, 300) + 20
	}
	y += s.transient(y, doc.TracePeaks)
	for i, n := range svgNotes {
		s.text(20, y+16+float64(i)*15, "start", "small", n)
	}
	y += 16 + float64(len(svgNotes))*15
	height := math.Ceil(math.Max(y, s.maxY) + 10)

	var out strings.Builder
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" font-family="system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif">`+"\n", width, height, width, height)
	out.WriteString(`<style>
.h1{font-size:22px;font-weight:700;fill:#111}
.h2{font-size:17px;font-weight:700;fill:#111}
.sub{font-size:12px;fill:#555}
.lbl{font-size:12px;fill:#222}
.small{font-size:11px;fill:#333}
.bar{font-size:13px;font-weight:700;fill:#111}
.total{font-size:13px;font-weight:700;fill:#111}
.delta{font-size:14px;font-weight:700;fill:#1e7b3a}
.leader{fill:none;stroke:#999;stroke-width:0.8}
.axis{stroke:#333;stroke-width:1}
</style>
<rect width="100%" height="100%" fill="#fff"/>
`)
	out.Write(s.Bytes())
	out.WriteString("</svg>\n")
	return out.String(), skipped
}

func checkXML(svgText string) {
	dec := xml.NewDecoder(strings.NewReader(svgText))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			passed++
			return
		}
		if err != nil {
			failures = append(failures, "cake.svg does not parse: "+err.Error())
			return
		}
	}
}

// ---- table -----------------------------------------------------------------

func markdown(doc *Doc) string {
	var b strings.Builder
	b.WriteString("| PR | family | workload | live before | live after | focus before | focus after | Δ focus | ratio |\n")
	b.WriteString("|---|---|---|---:|---:|---:|---:|---:|---:|\n")
	for _, d := range doc.Demos {
		name := "#" + d.PR + " " + d.Title
		if d.CLI {
			name += " (CLI heap)"
		}
		if d.Error != "" || d.Before == nil || d.After == nil {
			fmt.Fprintf(&b, "| %s | %s | error: %s | | | | | | |\n", name, d.Family, strings.ReplaceAll(strings.ReplaceAll(d.Error, "\n", " "), "|", "/"))
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", name, d.Family, d.Workload,
			fmtMiB(d.Before.Live), fmtMiB(d.After.Live), fmtMiB(d.Before.Focus), fmtMiB(d.After.Focus),
			fmtSigned(d.After.Focus-d.Before.Focus), ratio(d.Before.Focus, d.After.Focus))
		if d.FocusAlloc != "" {
			fmt.Fprintf(&b, "| #%s alloc focus (churn, alloc_space) | %s | same run | %s | %s | %s | %s | %s | %s |\n", d.PR, d.Family,
				fmtMiB(d.Before.Alloc), fmtMiB(d.After.Alloc), fmtMiB(d.Before.FocusAlloc), fmtMiB(d.After.FocusAlloc),
				fmtSigned(d.After.FocusAlloc-d.Before.FocusAlloc), ratio(d.Before.FocusAlloc, d.After.FocusAlloc))
		}
	}
	if tr := doc.TracePeaks; tr != nil && tr.Before != nil && tr.After != nil {
		b.WriteString("\n| trace tool | alloc/search before | alloc/search after | live peak before | live peak after |\n|---|---:|---:|---:|---:|\n")
		for _, name := range []string{"ListServices", "FindSpans", "FindCalls"} {
			tb, ta := tr.Before.tool(name), tr.After.tool(name)
			if tb == nil || ta == nil {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", name, fmtMiB(tb.AllocPerSearch), fmtMiB(ta.AllocPerSearch), fmtSigned(tb.Peak-tb.HeapStart), fmtSigned(ta.Peak-ta.HeapStart))
		}
	}
	return b.String()
}

// The footnotes, short, for the SVG on its own.
var svgNotes = []string{
	"Notes: each cake sums per-PR targeted workloads, each measured in its own session on a fresh engine; not one combined workload.",
	"#15212 and #15213 share one focus (serialized contenthash trees); #15212 alone, against its own branch: 360 → 53 MiB.",
	"#15226 frees memory only for Dang and dang-entrypoint Go modules; legacy Go/Python/TS runtimes keep receiver JSON via",
	"session-owned currentFunctionCall / FunctionCall.parent results. #15218's idle-LoadTrace release is not measured (needs Cloud).",
}

var footnotes = []string{
	"Each layer is one PR's <em>focus bytes</em>: the live heap's inuse_space in samples with a stack frame matching the PR's focus regexp (table), taken at the end of that PR's own targeted workload with its session still open. The cakes are the <strong>sum of per-PR targeted workloads, each measured in its own session (a fresh engine per measurement); not one combined workload</strong>. Live totals in the table are the whole process heap at that point, for context; they do not add up.",
	"#15212 and #15213 share the same focus (the serialized contenthash trees SetExternal keeps). On AFTER, #15213 already keeps those trees on disk, so #15212's layer is 0 there; #15212 alone, measured against its own branch, takes it from 360 to 53 MiB.",
	"#15226 only frees memory for Dang modules and Go modules with a dang entrypoint; the legacy Go/Python/TypeScript runtimes keep the receiver JSON alive through session-owned currentFunctionCall / FunctionCall.parent results.",
	"#15218's release of idle LoadTrace data is not measured (it needs Dagger Cloud); only its per-search cost is (transient panel).",
	"The CLI cake is the <code>dagger</code> CLI process heap (headless pretty TUI under <code>dagger run</code>), not the engine's. #15227 also stops idle log batch processors' buffer churn: its alloc_space focus row.",
}

func htmlPage(doc *Doc, svgText, md string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>The memory cake</title>
<style>
body{font-family:system-ui,-apple-system,Segoe UI,Helvetica,Arial,sans-serif;margin:24px auto;max-width:1280px;color:#111;padding:0 12px}
table{border-collapse:collapse;font-size:13px;margin:12px 0}
th,td{border:1px solid #ddd;padding:4px 6px;vertical-align:top}
th{background:#f4f4f4}
td.n{text-align:right;white-space:nowrap}
code{font-size:11px;word-break:break-all}
.sw{display:inline-block;width:10px;height:10px;margin-right:4px}
.svg svg{max-width:100%;height:auto}
ol{font-size:13px}
</style></head><body>
`)
	fmt.Fprintf(&b, "<h1>The memory cake</h1>\n<p>BEFORE = <code>%s</code> (main), AFTER = <code>%s</code> @ <code>%s</code> (that main plus all memory PRs). Sum of per-PR targeted workloads, each measured in its own session; not one combined workload. Raw numbers: <a href=\"data.json\">data.json</a>.</p>\n",
		html.EscapeString(doc.BeforeSha), html.EscapeString(doc.After), html.EscapeString(doc.AfterSha))
	b.WriteString(`<div class="svg">` + "\n")
	b.WriteString(svgText)
	b.WriteString("</div>\n<h2>Every demo</h2>\n<table><tr><th>PR</th><th>workload</th><th>focus regexp</th><th>live before</th><th>live after</th><th>focus before</th><th>focus after</th><th>Δ focus</th><th>ratio</th></tr>\n")
	for _, d := range doc.Demos {
		name := fmt.Sprintf(`<span class="sw" style="background:%s"></span><a href="https://github.com/dagger/dagger/pull/%s">#%s</a> %s`, colorOf(d.PR), d.PR, d.PR, html.EscapeString(d.Title))
		if d.CLI {
			name += " <em>(CLI heap)</em>"
		}
		if d.Error != "" || d.Before == nil || d.After == nil {
			fmt.Fprintf(&b, "<tr><td>%s</td><td colspan=8>error: %s</td></tr>\n", name, html.EscapeString(d.Error))
			continue
		}
		fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td><td><code>%s</code></td><td class=n>%s</td><td class=n>%s</td><td class=n>%s</td><td class=n>%s</td><td class=n>%s</td><td class=n>%s</td></tr>\n",
			name, html.EscapeString(d.Workload), html.EscapeString(d.Focus),
			fmtMiB(d.Before.Live), fmtMiB(d.After.Live), fmtMiB(d.Before.Focus), fmtMiB(d.After.Focus),
			fmtSigned(d.After.Focus-d.Before.Focus), ratio(d.Before.Focus, d.After.Focus))
		if d.FocusAlloc != "" {
			fmt.Fprintf(&b, "<tr><td>#%s alloc focus (churn: alloc_space, garbage included)</td><td>same run</td><td><code>%s</code></td><td class=n>%s</td><td class=n>%s</td><td class=n>%s</td><td class=n>%s</td><td class=n>%s</td><td class=n>%s</td></tr>\n",
				d.PR, html.EscapeString(d.FocusAlloc), fmtMiB(d.Before.Alloc), fmtMiB(d.After.Alloc),
				fmtMiB(d.Before.FocusAlloc), fmtMiB(d.After.FocusAlloc), fmtSigned(d.After.FocusAlloc-d.Before.FocusAlloc), ratio(d.Before.FocusAlloc, d.After.FocusAlloc))
		}
	}
	b.WriteString("</table>\n")
	if tr := doc.TracePeaks; tr != nil && tr.Before != nil && tr.After != nil && tr.Before.Error == "" && tr.After.Error == "" {
		b.WriteString("<h2>Transient: trace tools per search</h2>\n<table><tr><th>tool</th><th>alloc/search before</th><th>alloc/search after</th><th>live peak before</th><th>live peak after</th></tr>\n")
		for _, name := range []string{"ListServices", "FindSpans", "FindCalls"} {
			tb, ta := tr.Before.tool(name), tr.After.tool(name)
			if tb == nil || ta == nil {
				continue
			}
			label := name
			switch name {
			case "ListServices":
				label += " (baseline: the LLM's own overhead)"
			case "FindSpans":
				label += " (#15218)"
			case "FindCalls":
				label += " (#15219)"
			}
			fmt.Fprintf(&b, "<tr><td>%s</td><td class=n>%s</td><td class=n>%s</td><td class=n>%s</td><td class=n>%s</td></tr>\n", label,
				fmtMiB(tb.AllocPerSearch), fmtMiB(ta.AllocPerSearch), fmtSigned(tb.Peak-tb.HeapStart), fmtSigned(ta.Peak-ta.HeapStart))
		}
		b.WriteString("</table>\n<p style=\"font-size:13px\">Live peak: the highest HeapAlloc right after forced GCs while searches ran, over the live heap just before them. 20k cheap uncached calls in the session; query <code>withEnvVariable</code>.</p>\n")
	}
	b.WriteString("<h2>Notes</h2>\n<ol>\n")
	for _, f := range footnotes {
		b.WriteString("<li>" + f + "</li>\n")
	}
	for _, n := range doc.Notes {
		b.WriteString("<li>" + html.EscapeString(n) + "</li>\n")
	}
	b.WriteString("</ol>\n<p style=\"font-size:12px;color:#666\">Measured with the memory-cake Dang module (<code>.dagger/modules/memory-cake</code>, <code>cakeJson</code> and <code>tracePeaksJson</code>); drawn by its <code>render/</code>.</p>\n</body></html>\n")
	return b.String()
}

// ---- main ------------------------------------------------------------------

func main() {
	outDir := flag.String("out", ".", "directory to write data.json, cake.svg, index.html and cake.md into")
	var notes multi
	flag.Var(&notes, "note", "a note to add to the page and data.json (repeatable)")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: render -out DIR doc.json...")
		os.Exit(2)
	}

	merged := &Doc{}
	byPR := map[string]*Demo{}
	for _, path := range flag.Args() {
		raw, err := os.ReadFile(path)
		if err != nil {
			fatal(err)
		}
		var doc Doc
		if err := json.Unmarshal(raw, &doc); err != nil {
			fatal(fmt.Errorf("%s: %w", path, err))
		}
		if merged.Before == "" {
			merged.Before, merged.After = doc.Before, doc.After
		}
		if doc.Before != merged.Before || doc.After != merged.After {
			fatal(fmt.Errorf("%s compares %s..%s, not %s..%s", path, doc.Before, doc.After, merged.Before, merged.After))
		}
		for _, d := range doc.Demos {
			// A failed rerun does not replace a good measurement.
			if old, ok := byPR[d.PR]; ok && old.Error == "" && d.Error != "" {
				continue
			}
			byPR[d.PR] = d
		}
		if tp := doc.TracePeaks; tp != nil {
			if merged.TracePeaks == nil {
				merged.TracePeaks = &Traces{}
			}
			if tp.Before != nil && (merged.TracePeaks.Before == nil || tp.Before.Error == "") {
				merged.TracePeaks.Before = tp.Before
			}
			if tp.After != nil && (merged.TracePeaks.After == nil || tp.After.Error == "") {
				merged.TracePeaks.After = tp.After
			}
		}
		merged.Notes = append(merged.Notes, doc.Notes...)
	}
	merged.Notes = append(merged.Notes, notes...)
	merged.Notes = dedupe(merged.Notes)

	// Cake order first, then anything unknown.
	var prs []string
	for pr := range byPR {
		prs = append(prs, pr)
	}
	pos := func(pr string) int {
		for i, p := range order {
			if p == pr {
				return i
			}
		}
		return len(order)
	}
	sort.Slice(prs, func(i, j int) bool {
		if pos(prs[i]) != pos(prs[j]) {
			return pos(prs[i]) < pos(prs[j])
		}
		return prs[i] < prs[j]
	})
	for _, pr := range prs {
		d := byPR[pr]
		m, ok := metas[pr]
		if !ok {
			m = meta{family: "other", short: d.Title}
		}
		d.Family, d.Short, d.Workload = m.family, m.short, m.workload
		merged.Demos = append(merged.Demos, d)
		if d.Before != nil && merged.BeforeSha == "" {
			merged.BeforeSha = d.Before.Sha
		}
		if d.After != nil && merged.AfterSha == "" {
			merged.AfterSha = d.After.Sha
		}
	}
	for _, d := range merged.Demos {
		if d.Before != nil {
			check(d.Before.Sha == merged.BeforeSha, "#%s BEFORE measured %s, not %s", d.PR, d.Before.Sha, merged.BeforeSha)
		}
		if d.After != nil {
			check(d.After.Sha == merged.AfterSha, "#%s AFTER measured %s, not %s", d.PR, d.After.Sha, merged.AfterSha)
		}
	}

	md := markdown(merged)
	merged.Markdown = md
	svgText, skipped := render(merged)
	checkXML(svgText)
	page := htmlPage(merged, svgText, md)
	checkXML(page[strings.Index(page, "<svg") : strings.Index(page, "</svg>")+len("</svg>")])

	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal(err)
	}
	for name, content := range map[string]string{
		"data.json":  string(data) + "\n",
		"cake.svg":   svgText,
		"index.html": page,
		"cake.md":    md,
	} {
		if err := os.WriteFile(filepath.Join(*outDir, name), []byte(content), 0o644); err != nil {
			fatal(err)
		}
	}

	fmt.Print(md)
	if len(skipped) > 0 {
		fmt.Printf("\nnot drawn (errors): %s\n", strings.Join(skipped, ", "))
	}
	fmt.Printf("\nchecks: %d passed, %d failed\n", passed, len(failures))
	for _, f := range failures {
		fmt.Println("FAIL:", f)
	}
	if len(failures) > 0 {
		os.Exit(1)
	}
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, "; ") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "render:", err)
	os.Exit(1)
}
