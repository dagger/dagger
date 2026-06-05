package cloud

import (
	"context"
	"fmt"
	"time"
)

const getCheckReportOperation = `
query GetCheckReport ($org: String!, $checkID: ID!, $input: CheckReportInput) {
	org(name: $org) {
		check(id: $checkID) {
			report(input: $input) {
				checkId
				checkName
				status
				summary
				traceId
				spanId
				traceUrl
				generatedAt
				partial
				notices
				failure {
					message
					roots {
						spanId
						traceId
						name
						message
						statusCode
						startedAt
						endedAt
						path
						traceUrl
						logs {
							lines
							truncated
							totalLineCount
						}
					}
				}
				tests {
					total
					passed
					failed
					skipped
					running
					failures {
						name
						suite
						status
						spanId
						traceId
						message
						startedAt
						endedAt
						traceUrl
						logs {
							lines
							truncated
							totalLineCount
						}
					}
					skippedTests {
						name
						suite
						status
						spanId
						traceId
						message
						startedAt
						endedAt
						traceUrl
					}
				}
			}
		}
	}
}
`

type CheckReportOptions struct {
	LogLines           int
	MaxFailedSpans     int
	MaxTests           int
	IncludeLogs        *bool
	IncludePassedTests *bool
}

func (opts CheckReportOptions) input() map[string]any {
	input := map[string]any{}
	if opts.LogLines > 0 {
		input["logLines"] = opts.LogLines
	}
	if opts.MaxFailedSpans > 0 {
		input["maxFailedSpans"] = opts.MaxFailedSpans
	}
	if opts.MaxTests > 0 {
		input["maxTests"] = opts.MaxTests
	}
	if opts.IncludeLogs != nil {
		input["includeLogs"] = *opts.IncludeLogs
	}
	if opts.IncludePassedTests != nil {
		input["includePassedTests"] = *opts.IncludePassedTests
	}
	if len(input) == 0 {
		return nil
	}
	return input
}

type CheckReport struct {
	CheckID     string              `json:"checkId"`
	CheckName   string              `json:"checkName"`
	Status      string              `json:"status"`
	Summary     string              `json:"summary"`
	TraceID     string              `json:"traceId,omitempty"`
	SpanID      string              `json:"spanId,omitempty"`
	TraceURL    string              `json:"traceUrl,omitempty"`
	GeneratedAt time.Time           `json:"generatedAt"`
	Partial     bool                `json:"partial"`
	Notices     []string            `json:"notices"`
	Failure     *CheckFailureReport `json:"failure,omitempty"`
	Tests       *CheckTestReport    `json:"tests,omitempty"`
}

type CheckFailureReport struct {
	Message string             `json:"message,omitempty"`
	Roots   []CheckFailureRoot `json:"roots"`
}

type CheckFailureRoot struct {
	SpanID     string           `json:"spanId"`
	TraceID    string           `json:"traceId"`
	Name       string           `json:"name"`
	Message    string           `json:"message,omitempty"`
	StatusCode string           `json:"statusCode,omitempty"`
	StartedAt  *time.Time       `json:"startedAt,omitempty"`
	EndedAt    *time.Time       `json:"endedAt,omitempty"`
	Path       []string         `json:"path,omitempty"`
	Logs       *CheckLogExcerpt `json:"logs,omitempty"`
	TraceURL   string           `json:"traceUrl,omitempty"`
}

type CheckLogExcerpt struct {
	Lines          []string `json:"lines"`
	Truncated      bool     `json:"truncated"`
	TotalLineCount *int     `json:"totalLineCount,omitempty"`
}

type CheckTestReport struct {
	Total        int             `json:"total"`
	Passed       int             `json:"passed"`
	Failed       int             `json:"failed"`
	Skipped      int             `json:"skipped"`
	Running      int             `json:"running"`
	Failures     []CheckTestCase `json:"failures"`
	SkippedTests []CheckTestCase `json:"skippedTests"`
}

type CheckTestCase struct {
	Name      string           `json:"name"`
	Suite     string           `json:"suite,omitempty"`
	Status    string           `json:"status"`
	SpanID    string           `json:"spanId"`
	TraceID   string           `json:"traceId"`
	Message   string           `json:"message,omitempty"`
	StartedAt *time.Time       `json:"startedAt,omitempty"`
	EndedAt   *time.Time       `json:"endedAt,omitempty"`
	Logs      *CheckLogExcerpt `json:"logs,omitempty"`
	TraceURL  string           `json:"traceUrl,omitempty"`
}

func (c *Client) CheckReport(ctx context.Context, org string, checkID string, opts CheckReportOptions) (*CheckReport, error) {
	var data struct {
		Org *struct {
			Check *struct {
				Report CheckReport `json:"report"`
			} `json:"check"`
		} `json:"org"`
	}
	if err := c.doGraphQL(ctx, "GetCheckReport", getCheckReportOperation, map[string]any{
		"org":     org,
		"checkID": checkID,
		"input":   opts.input(),
	}, &data); err != nil {
		return nil, err
	}
	if data.Org == nil {
		return nil, fmt.Errorf("org %q not found", org)
	}
	if data.Org.Check == nil {
		return nil, fmt.Errorf("check %q not found", checkID)
	}
	return &data.Org.Check.Report, nil
}
