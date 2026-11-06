package core

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/stretchr/testify/require"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// sinkSlowest is how many of a trace sink's slowest exports its timing keeps.
const sinkSlowest = 8

// sinkExport is the timing of one export request a trace sink served.
type sinkExport struct {
	kind    string // "traces" or "logs"
	arrived time.Time
	// lockWait is the time the request waited for the sink's mutex, which
	// ingest and the test's reads share; read is the time to read its body,
	// and total runs from its arrival to its answer.
	lockWait, read, total time.Duration
	bytes, items          int
}

// sinkTiming is how fast a trace sink answered the exports sent to it, kept
// so a test that waits for telemetry can say, when it fails, whether the sink
// was slow to answer or the exports stopped arriving. The sink's mutex guards
// it. Its zero value is ready to record, timed from its first export.
type sinkTiming struct {
	start    time.Time
	count    map[string]int
	total    map[string]time.Duration
	last     map[string]time.Time
	maxGap   map[string]time.Duration
	maxGapAt map[string]time.Time
	slowest  []sinkExport
}

func (st *sinkTiming) record(ex sinkExport) {
	if st.count == nil {
		if st.start.IsZero() {
			st.start = ex.arrived
		}
		st.count = map[string]int{}
		st.total = map[string]time.Duration{}
		st.last = map[string]time.Time{}
		st.maxGap = map[string]time.Duration{}
		st.maxGapAt = map[string]time.Time{}
	}
	st.count[ex.kind]++
	st.total[ex.kind] += ex.total
	if last, ok := st.last[ex.kind]; ok {
		if gap := ex.arrived.Sub(last); gap > st.maxGap[ex.kind] {
			st.maxGap[ex.kind] = gap
			st.maxGapAt[ex.kind] = ex.arrived
		}
	}
	st.last[ex.kind] = ex.arrived
	i, _ := slices.BinarySearchFunc(st.slowest, ex, func(a, b sinkExport) int {
		return cmp.Compare(b.total, a.total)
	})
	if i < sinkSlowest {
		st.slowest = slices.Insert(st.slowest, i, ex)
		if len(st.slowest) > sinkSlowest {
			st.slowest = st.slowest[:sinkSlowest]
		}
	}
}

func (st *sinkTiming) report() string {
	at := func(t time.Time) string { return "+" + t.Sub(st.start).Round(time.Millisecond).String() }
	var sb strings.Builder
	fmt.Fprintf(&sb, "trace sink timing, %s after it started:\n", time.Since(st.start).Round(time.Millisecond))
	for _, kind := range []string{"traces", "logs"} {
		if st.count[kind] == 0 {
			fmt.Fprintf(&sb, "  %s: no exports\n", kind)
			continue
		}
		fmt.Fprintf(&sb, "  %s: %d exports answered in %s in all, the last arriving at %s; longest gap between arrivals %s, ending at %s\n",
			kind, st.count[kind], st.total[kind].Round(time.Millisecond), at(st.last[kind]),
			st.maxGap[kind].Round(time.Millisecond), at(st.maxGapAt[kind]))
	}
	for _, ex := range st.slowest {
		fmt.Fprintf(&sb, "  slow: %s arrived at %s, %d items, %d bytes: answered in %s (waited %s for the lock, read the body in %s)\n",
			ex.kind, at(ex.arrived), ex.items, ex.bytes, ex.total.Round(time.Millisecond),
			ex.lockWait.Round(time.Millisecond), ex.read.Round(time.Millisecond))
	}
	return sb.String()
}

// TestTraceSinkTiming checks that a trace sink's timing report says which
// exports it was slow to answer and why, and how long exports stopped arriving.
func TestTraceSinkTiming(t *testing.T) {
	t.Parallel()
	db := dagui.NewDB()
	sink := &agentTraceSink{db: db, logExp: db.LogExporter(), changed: make(chan struct{}), timing: sinkTiming{start: time.Now()}}
	next := uint64(0)
	// request builds an export of n ended spans; send sends it and returns
	// the sink's answer.
	request := func(n int) *http.Request {
		var spans []*tracepb.Span
		for range n {
			next++
			id := make([]byte, 8)
			binary.BigEndian.PutUint64(id, next)
			now := uint64(time.Now().UnixNano())
			spans = append(spans, &tracepb.Span{TraceId: bytes.Repeat([]byte{1}, 16), SpanId: id, Name: "span", StartTimeUnixNano: now, EndTimeUnixNano: now})
		}
		body, err := proto.Marshal(&coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: &resourcepb.Resource{}, ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}}}}})
		require.NoError(t, err)
		return httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	}
	send := func(r *http.Request) int {
		response := httptest.NewRecorder()
		sink.tracesHandler(response, r)
		return response.Code
	}
	const held, quiet = 300 * time.Millisecond, 500 * time.Millisecond

	for range 9 {
		require.Equal(t, http.StatusCreated, send(request(1)))
	}
	// One export arrives while the test's reads hold the sink. The hold is
	// timed from just before the export reaches the sink, so it waits for
	// most of it however late its goroutine runs.
	sink.mu.Lock()
	blocked := request(3)
	sending := make(chan struct{})
	answered := make(chan int)
	go func() {
		close(sending)
		answered <- send(blocked)
	}()
	<-sending
	time.Sleep(held)
	sink.mu.Unlock()
	require.Equal(t, http.StatusCreated, <-answered)
	// Then none arrives for a while.
	time.Sleep(quiet)
	require.Equal(t, http.StatusCreated, send(request(1)))

	sink.mu.Lock()
	report := sink.timing.report()
	sink.mu.Unlock()
	duration := func(pattern, in string) time.Duration {
		t.Helper()
		m := regexp.MustCompile(pattern).FindStringSubmatch(in)
		require.Len(t, m, 2, "%q in %q", pattern, in)
		d, err := time.ParseDuration(m[1])
		require.NoError(t, err)
		return d
	}
	require.Contains(t, report, "traces: 11 exports answered in ")
	require.Contains(t, report, "logs: no exports")
	require.GreaterOrEqual(t, duration(`longest gap between arrivals (\S+),`, report), quiet)
	// Only sinkSlowest exports are kept, among them the one that waited.
	var slow []string
	waited := ""
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, "  slow: ") {
			slow = append(slow, line)
			if strings.Contains(line, ", 3 items, ") {
				waited = line
			}
		}
	}
	require.Len(t, slow, sinkSlowest)
	require.NotEmpty(t, waited, report)
	require.GreaterOrEqual(t, duration(`waited (\S+) for the lock`, waited), held/2)
}
