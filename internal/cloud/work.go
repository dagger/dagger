package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Work classes of a run.
const (
	WorkNew    = "new"
	WorkRepeat = "repeat"
)

// TraceWork is the work of a completed trace, as Dagger Cloud computes it:
// each run with work -- a call that ran, or a lazy evaluation, with
// executions or helper commands below it -- is new work, or repeat work when
// the organization already ran the same work (same content-preferred digest)
// within the repeat window before it started. Cached work is the calls whose
// result came from the cache. The history covers Cloud Engines.
type TraceWork struct {
	TraceID          string      `json:"traceId"`
	RepeatWindowDays int         `json:"repeatWindowDays"`
	Summary          WorkSummary `json:"summary"`
	// Runs are the runs with work, in start order.
	Runs []WorkRun `json:"runs"`
}

type WorkSummary struct {
	New    WorkTotals  `json:"new"`
	Repeat WorkTotals  `json:"repeat"`
	Cached CachedTotal `json:"cached"`
}

type WorkTotals struct {
	Runs       int     `json:"runs"`
	Executions int     `json:"executions"`
	Helpers    int     `json:"helpers"`
	CPUSeconds float64 `json:"cpuSeconds"`
}

type CachedTotal struct {
	Calls int `json:"calls"`
}

type WorkRun struct {
	// SpanID is the run's span: the call that ran, or the lazy evaluation.
	SpanID string `json:"spanId"`
	// CallSpanID is, for a lazy evaluation, the call that returned the
	// evaluated result: the span a trace view shows for it.
	CallSpanID string `json:"callSpanId,omitempty"`
	// Name is the name of the span a trace view shows for the run: the call
	// for a lazy evaluation, else the run's own span, named SpanName.
	Name       string  `json:"name"`
	SpanName   string  `json:"spanName"`
	Digest     string  `json:"digest"`
	Work       string  `json:"work"`
	Executions int     `json:"executions"`
	Helpers    int     `json:"helpers"`
	CPUSeconds float64 `json:"cpuSeconds"`
	// Evidence is the latest earlier run of the same work, for repeat work.
	Evidence  *WorkEvidence `json:"evidence,omitempty"`
	StartTime time.Time     `json:"startTime"`
	EndTime   time.Time     `json:"endTime"`
}

type WorkEvidence struct {
	TraceID string    `json:"traceId"`
	SpanID  string    `json:"spanId"`
	EndTime time.Time `json:"endTime"`
}

// otlpWork is the stats bucket of work requests.
const otlpWork = "work"

// FetchWork asks Dagger Cloud for the work of a completed trace. Cloud
// computes it on request, so it can take several seconds for a large trace.
func (c *OTLPClient) FetchWork(ctx context.Context, traceID string) (*TraceWork, error) {
	endpoint := c.u.JoinPath("/v1/traces", traceID, "work").String()
	resp, err := c.openRequest(ctx, otlpWork, endpoint, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fetch %s: %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
	}
	var work TraceWork
	if err := json.NewDecoder(resp.Body).Decode(&work); err != nil {
		return nil, fmt.Errorf("decode the work of trace %s: %w", traceID, err)
	}
	return &work, nil
}
