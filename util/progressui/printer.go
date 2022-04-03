package progressui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	digest "github.com/opencontainers/go-digest"
	"github.com/tonistiigi/units"
)

const (
	antiFlicker      = 5 * time.Second
	maxDelay         = 10 * time.Second
	minTimeDelta     = 5 * time.Second
	minProgressDelta = 0.05 // %
)

type lastStatus struct {
	Current   int64
	Timestamp time.Time
}

type textMux struct {
	vertexPrintCb VertexPrintFunc
	statusPrintCb StatusPrintFunc
	logPrintCb    LogPrintFunc

	current  digest.Digest
	last     map[string]lastStatus
	notFirst bool
}

func (p *textMux) printVtx(t *trace, dgst digest.Digest) {
	if p.last == nil {
		p.last = make(map[string]lastStatus)
	}

	v, ok := t.byDigest[dgst]
	if !ok {
		return
	}

	if dgst != p.current {
		if p.current != "" {
			old := t.byDigest[p.current]
			if old.logsPartial {
				p.statusPrintCb(v.Vertex, "")
			}
			old.logsOffset = 0
			old.count = 0
			p.statusPrintCb(v.Vertex, "#%d ...", old.index)
		}

		if p.notFirst {
			p.statusPrintCb(v.Vertex, "")
		} else {
			p.notFirst = true
		}

		p.vertexPrintCb(v.Vertex, v.index)
	}

	if len(v.events) != 0 {
		v.logsOffset = 0
	}
	for _, ev := range v.events {
		p.statusPrintCb(v.Vertex, "#%d %s", v.index, ev)
	}
	v.events = v.events[:0]

	for _, s := range v.statuses {
		if _, ok := v.statusUpdates[s.ID]; ok {
			doPrint := true

			if last, ok := p.last[s.ID]; ok && s.Completed == nil {
				var progressDelta float64
				if s.Total > 0 {
					progressDelta = float64(s.Current-last.Current) / float64(s.Total)
				}
				timeDelta := s.Timestamp.Sub(last.Timestamp)
				if progressDelta < minProgressDelta && timeDelta < minTimeDelta {
					doPrint = false
				}
			}

			if !doPrint {
				continue
			}

			p.last[s.ID] = lastStatus{
				Timestamp: s.Timestamp,
				Current:   s.Current,
			}

			var bytes string
			if s.Total != 0 {
				bytes = fmt.Sprintf(" %.2f / %.2f", units.Bytes(s.Current), units.Bytes(s.Total))
			} else if s.Current != 0 {
				bytes = fmt.Sprintf(" %.2f", units.Bytes(s.Current))
			}
			var tm string
			endTime := s.Timestamp
			if s.Completed != nil {
				endTime = *s.Completed
			}
			if s.Started != nil {
				diff := endTime.Sub(*s.Started).Seconds()
				if diff > 0.01 {
					tm = fmt.Sprintf(" %.1fs", diff)
				}
			}
			if s.Completed != nil {
				tm += " done"
			}
			p.statusPrintCb(v.Vertex, "#%d %s%s%s", v.index, s.ID, bytes, tm)
		}
	}
	v.statusUpdates = map[string]struct{}{}

	for i, l := range v.logs {
		line := l.line
		if i == 0 {
			line = line[v.logsOffset:]
		}
		complete := i != len(v.logs)-1 || !v.logsPartial
		p.logPrintCb(v.Vertex, l.stream, !complete, "%s", line)
	}

	if len(v.logs) > 0 {
		if v.logsPartial {
			v.logs = v.logs[len(v.logs)-1:]
			v.logsOffset = len(v.logs[0].line)
		} else {
			v.logs = nil
			v.logsOffset = 0
		}
	}

	p.current = dgst
	if v.Completed != nil {
		p.current = ""
		v.count = 0

		if v.Error != "" {
			if v.logsPartial {
				p.statusPrintCb(v.Vertex, "")
			}
			if strings.HasSuffix(v.Error, context.Canceled.Error()) {
				p.statusPrintCb(v.Vertex, "#%d CANCELED", v.index)
			} else {
				p.statusPrintCb(v.Vertex, "#%d ERROR: %s", v.index, v.Error)
			}
		} else if v.Cached {
			p.statusPrintCb(v.Vertex, "#%d CACHED", v.index)
		} else {
			tm := ""
			if v.Started != nil {
				tm = fmt.Sprintf(" %.1fs", v.Completed.Sub(*v.Started).Seconds())
			}
			p.statusPrintCb(v.Vertex, "#%d DONE%s", v.index, tm)
		}
	}

	delete(t.updates, dgst)
}

func sortCompleted(t *trace, m map[digest.Digest]struct{}) []digest.Digest {
	out := make([]digest.Digest, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		return t.byDigest[out[i]].Completed.Before(*t.byDigest[out[j]].Completed)
	})
	return out
}

func (p *textMux) print(t *trace) {
	completed := map[digest.Digest]struct{}{}
	rest := map[digest.Digest]struct{}{}

	for dgst := range t.updates {
		v, ok := t.byDigest[dgst]
		if !ok {
			continue
		}
		if v.Vertex.Completed != nil {
			completed[dgst] = struct{}{}
		} else {
			rest[dgst] = struct{}{}
		}
	}

	current := p.current

	// items that have completed need to be printed first
	if _, ok := completed[current]; ok {
		p.printVtx(t, current)
	}

	for _, dgst := range sortCompleted(t, completed) {
		if dgst != current {
			p.printVtx(t, dgst)
		}
	}

	if len(rest) == 0 {
		if current != "" {
			if v := t.byDigest[current]; v.Started != nil && v.Completed == nil {
				return
			}
		}
		// make any open vertex active
		for dgst, v := range t.byDigest {
			if v.Started != nil && v.Completed == nil {
				p.printVtx(t, dgst)
				return
			}
		}
		return
	}

	// now print the active one
	if _, ok := rest[current]; ok {
		p.printVtx(t, current)
	}

	stats := map[digest.Digest]*vtxStat{}
	now := time.Now()
	sum := 0.0
	var max digest.Digest
	if current != "" {
		rest[current] = struct{}{}
	}
	for dgst := range rest {
		v, ok := t.byDigest[dgst]
		if !ok {
			continue
		}
		tm := now.Sub(*v.lastBlockTime)
		speed := float64(v.count) / tm.Seconds()
		overLimit := tm > maxDelay && dgst != current
		stats[dgst] = &vtxStat{blockTime: tm, speed: speed, overLimit: overLimit}
		sum += speed
		if overLimit || max == "" || stats[max].speed < speed {
			max = dgst
		}
	}
	for dgst := range stats {
		stats[dgst].share = stats[dgst].speed / sum
	}

	if _, ok := completed[current]; ok || current == "" {
		p.printVtx(t, max)
		return
	}

	// show items that were hidden
	for dgst := range rest {
		if stats[dgst].overLimit {
			p.printVtx(t, dgst)
			return
		}
	}

	// fair split between vertexes
	if 1.0/(1.0-stats[current].share)*antiFlicker.Seconds() < stats[current].blockTime.Seconds() {
		p.printVtx(t, max)
		return
	}
}

type vtxStat struct {
	blockTime time.Duration
	speed     float64
	share     float64
	overLimit bool
}

func limitString(s string, l int) string {
	if len(s) > l {
		return s[:l] + "..."
	}
	return s
}
