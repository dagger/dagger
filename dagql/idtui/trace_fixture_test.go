package idtui

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/cloud"
)

// TraceFixture is a recorded (or hand-built) trace, captured so the interactive
// pretty frontend can be driven in tests without a Cloud connection, a pty, or
// the CLI. It carries the full span tree (so lazy expand has children to serve)
// split into a priority set (what loadInitial returns) plus per-span logs in
// both the own-only and rolled-up forms, mirroring the two GetSpanLogs variants
// the real loader fetches.
//
// Capture one with TestRecordTraceFixture or build one by hand for a focused
// scenario, then drive it through a traceSession (pretty_harness_test.go).
type TraceFixture struct {
	TraceID string `json:"traceID"`
	// Spans is the entire tree. Priority lists the hex IDs that arrive in the
	// initial (root) load; everything else is served lazily when its parent is
	// listened to (expanded).
	Spans    []dagui.SpanSnapshot `json:"spans"`
	Priority []string             `json:"priority"`
	// Logs maps a span's hex ID to its recorded logs.
	Logs map[string]FixtureLogs `json:"logs,omitempty"`
}

// FixtureLogs holds a span's logs in the two forms the frontend fetches:
// Own (descendants=false, the span's own output) and Roll (descendants=true,
// the rolled-up subtree output a check/test surfaces).
type FixtureLogs struct {
	Own  []cloud.LogMessage `json:"own,omitempty"`
	Roll []cloud.LogMessage `json:"roll,omitempty"`
}

// LoadTraceFixture reads a fixture from testdata.
func LoadTraceFixture(t *testing.T, path string) *TraceFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var fix TraceFixture
	if err := json.Unmarshal(data, &fix); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", path, err)
	}
	return &fix
}

// Save writes the fixture as indented JSON (used by the recorder).
func (f *TraceFixture) Save(path string) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// prioritySet returns the priority hex IDs as a set.
func (f *TraceFixture) prioritySet() map[string]bool {
	set := make(map[string]bool, len(f.Priority))
	for _, hex := range f.Priority {
		set[hex] = true
	}
	return set
}

// fetchOp names a recorded fetch, matching the cloud client's --debug buckets.
type fetchOp string

const (
	opSpanUpdates fetchOp = "GetSpanUpdates"
	opSpanLogs    fetchOp = "GetSpanLogs"
)

// fetchStats accumulates per-op request/record/byte counts the same way the
// real cloud client's clientStats does (internal/cloud/stats.go), so a test can
// assert "expanding span X fetched N log requests / K bytes" -- the same numbers
// `dagger trace --debug` prints.
type fetchStats struct {
	ops map[fetchOp]*opCount
	// logRequests records the hex IDs whose logs were fetched, in order, so a
	// test can assert exactly which spans were (and weren't) fetched.
	logRequests []string
}

type opCount struct {
	Requests int
	Records  int
	Bytes    int64
}

func newFetchStats() *fetchStats {
	return &fetchStats{ops: map[fetchOp]*opCount{}}
}

func (s *fetchStats) add(op fetchOp, records int, bytes int64) {
	c := s.ops[op]
	if c == nil {
		c = &opCount{}
		s.ops[op] = c
	}
	c.Requests++
	c.Records += records
	c.Bytes += bytes
}

func (s *fetchStats) op(op fetchOp) opCount {
	if c := s.ops[op]; c != nil {
		return *c
	}
	return opCount{}
}

// fetchedLog reports whether a span's logs were ever requested.
func (s *fetchStats) fetchedLog(id dagui.SpanID) bool {
	hex := id.String()
	for _, h := range s.logRequests {
		if h == hex {
			return true
		}
	}
	return false
}
