package cloud

import (
	"context"
	"strings"
	"time"
)

// TraceQuestions holds the high-level answers about a trace, computed server-side
// in ClickHouse without loading the whole trace. It mirrors the Trace.summary
// GraphQL type and backs `dagger cloud analyze`.
type TraceQuestions struct {
	Outcome         *TraceOutcome `json:"outcome"`
	FailingCommands []Command     `json:"failingCommands"`
	Checks          CheckSummary  `json:"checks"`
	FailedTests     []FailedTest  `json:"failedTests"`
}

// SpanRef references a span and carries its timing.
type SpanRef struct {
	ID        string     `json:"id"`
	StartedAt *time.Time `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt"`
}

type TraceOutcome struct {
	Span    SpanRef `json:"span"`
	Command string  `json:"command"`
	// Status is normalized to lowercase (passed | failed | running | unknown) on
	// fetch; the API enum is uppercase but the renderer keys off lowercase.
	Status string `json:"status"`
	Error  string `json:"error"`
}

// Command is a span whose decoded call args read as a command line, plus the
// error it failed with. Used for both failing commands and a test's cause.
type Command struct {
	Span    SpanRef `json:"span"`
	Command string  `json:"command"`
	Error   string  `json:"error"`
}

// CheckSummary is the pass/fail tally plus the per-check status of a trace.
type CheckSummary struct {
	Passed int          `json:"passed"`
	Failed int          `json:"failed"`
	Total  int          `json:"total"`
	Items  []TraceCheck `json:"items"`
}

type TraceCheck struct {
	Name string  `json:"name"`
	Span SpanRef `json:"span"`
	// Status is normalized to lowercase on fetch (see TraceOutcome.Status).
	Status string `json:"status"`
	Error  string `json:"error"`
}

type FailedTest struct {
	Name          string  `json:"name"`
	Suite         string  `json:"suite"`
	DisplayName   string  `json:"displayName"`
	Span          SpanRef `json:"span"`
	FailureStatus string  `json:"failureStatus"`
	Error         string  `json:"error"`
	// Cause is the command that actually failed inside the test, nil if there is
	// no distinct origin.
	Cause *Command `json:"cause"`
}

const traceQuestionsQuery = `
query Analyze($orgID: ID!, $traceID: ID!) {
	trace(id: $traceID, org: $orgID) {
		summary {
			outcome { span { id startedAt endedAt } command status error }
			failingCommands { span { id startedAt } command error }
			checks { passed failed total items { name span { id startedAt endedAt } status error } }
			failedTests { name suite displayName span { id startedAt } failureStatus error cause { span { id startedAt } command error } }
		}
	}
}
`

// TraceQuestions fetches the high-level analysis of a trace.
func (c *Client) TraceQuestions(ctx context.Context, orgID, traceID string) (*TraceQuestions, error) {
	var out struct {
		Trace *struct {
			Summary *TraceQuestions `json:"summary"`
		} `json:"trace"`
	}
	if err := c.doGraphQL(ctx, "Analyze", traceQuestionsQuery, map[string]any{
		"orgID":   orgID,
		"traceID": traceID,
	}, &out); err != nil {
		return nil, err
	}
	if out.Trace == nil || out.Trace.Summary == nil {
		return nil, nil
	}
	tq := out.Trace.Summary
	// The API returns the Outcome enum uppercase; the renderer keys off
	// lowercase status words, so normalize at the boundary.
	if tq.Outcome != nil {
		tq.Outcome.Status = strings.ToLower(tq.Outcome.Status)
	}
	for i := range tq.Checks.Items {
		tq.Checks.Items[i].Status = strings.ToLower(tq.Checks.Items[i].Status)
	}
	return tq, nil
}
