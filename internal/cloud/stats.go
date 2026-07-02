package cloud

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// clientStats accumulates how much data a Client has fetched from Cloud, broken
// down by GraphQL operation, for --debug diagnostics. All streaming fetches
// funnel through streamGraphQL, so counting there captures every span/log pull.
type clientStats struct {
	mu  sync.Mutex
	ops map[string]*opStat
}

type opStat struct {
	requests int   // SSE round trips
	events   int   // non-empty "next" events
	bytes    int64 // raw event payload bytes
	records  int   // decoded spans / log messages
}

func newClientStats() *clientStats {
	return &clientStats{ops: map[string]*opStat{}}
}

// op returns the stat bucket for name; caller must hold mu.
func (s *clientStats) op(name string) *opStat {
	st := s.ops[name]
	if st == nil {
		st = &opStat{}
		s.ops[name] = st
	}
	return st
}

func (s *clientStats) addRequest(name string) {
	s.mu.Lock()
	s.op(name).requests++
	s.mu.Unlock()
}

func (s *clientStats) addEvent(name string, nbytes int) {
	s.mu.Lock()
	st := s.op(name)
	st.events++
	st.bytes += int64(nbytes)
	s.mu.Unlock()
}

func (s *clientStats) addRecords(name string, n int) {
	s.mu.Lock()
	s.op(name).records += n
	s.mu.Unlock()
}

// Summary returns a human-readable breakdown of data fetched from Cloud.
func (s *clientStats) Summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.ops))
	var total opStat
	for name, st := range s.ops {
		names = append(names, name)
		total.requests += st.requests
		total.events += st.events
		total.bytes += st.bytes
		total.records += st.records
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "cloud fetch: %d requests, %d records, %s",
		total.requests, total.records, humanBytes(total.bytes))
	for _, name := range names {
		st := s.ops[name]
		fmt.Fprintf(&b, "\n  %-16s %d req, %d records, %s",
			name, st.requests, st.records, humanBytes(st.bytes))
	}
	return b.String()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
