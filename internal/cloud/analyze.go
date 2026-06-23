package cloud

import (
	"context"
	"time"
)

// TraceQuestions holds the high-level answers about a trace, computed server-side
// in ClickHouse without loading the whole trace. It mirrors the traceQuestions
// GraphQL type and backs `dagger cloud analyze`.
type TraceQuestions struct {
	OverallStatus   *TraceOverallStatus `json:"overallStatus"`
	FailingCommands []FailingCommand    `json:"failingCommands"`
	Checks          []TraceCheckStatus  `json:"checks"`
	FailedTests     []FailedTest        `json:"failedTests"`
}

type TraceOverallStatus struct {
	TraceID    string     `json:"traceId"`
	SpanID     string     `json:"spanId"`
	Command    string     `json:"command"`
	StatusCode string     `json:"statusCode"`
	Outcome    string     `json:"outcome"` // passed | failed | running
	Error      string     `json:"error"`
	StartedAt  *time.Time `json:"startedAt"`
	EndedAt    *time.Time `json:"endedAt"`
}

type FailingCommand struct {
	SpanID    string     `json:"spanId"`
	Command   string     `json:"command"`
	Error     string     `json:"error"`
	StartedAt *time.Time `json:"startedAt"`
}

type TraceCheckStatus struct {
	Name      string     `json:"name"`
	SpanID    string     `json:"spanId"`
	Status    string     `json:"status"` // passed | failed | running | unknown
	Error     string     `json:"error"`
	StartedAt *time.Time `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt"`
}

type FailedTest struct {
	Name          string     `json:"name"`
	Suite         string     `json:"suite"`
	DisplayName   string     `json:"displayName"`
	SpanID        string     `json:"spanId"`
	FailureStatus string     `json:"failureStatus"`
	Error         string     `json:"error"`
	OriginCommand string     `json:"originCommand"`
	OriginError   string     `json:"originError"`
	StartedAt     *time.Time `json:"startedAt"`
}

const traceQuestionsQuery = `
query Analyze($orgID: ID!, $traceID: ID!) {
	traceQuestions(id: $traceID, org: $orgID) {
		overallStatus { traceId spanId command statusCode outcome error startedAt endedAt }
		failingCommands { spanId command error startedAt }
		checks { name spanId status error startedAt endedAt }
		failedTests { name suite displayName spanId failureStatus error originCommand originError startedAt }
	}
}
`

// TraceQuestions fetches the high-level analysis of a trace.
func (c *Client) TraceQuestions(ctx context.Context, orgID, traceID string) (*TraceQuestions, error) {
	var out struct {
		TraceQuestions *TraceQuestions `json:"traceQuestions"`
	}
	if err := c.doGraphQL(ctx, "Analyze", traceQuestionsQuery, map[string]any{
		"orgID":   orgID,
		"traceID": traceID,
	}, &out); err != nil {
		return nil, err
	}
	return out.TraceQuestions, nil
}
