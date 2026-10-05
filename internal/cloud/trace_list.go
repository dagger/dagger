package cloud

import (
	"context"
	"time"
)

// TraceListFilter narrows Org.traces. Zero fields do not filter.
type TraceListFilter struct {
	Repos       []string   `json:"repos,omitempty"`
	Branch      string     `json:"branch,omitempty"`
	Commit      string     `json:"commit,omitempty"`
	Tag         string     `json:"tag,omitempty"`
	Change      string     `json:"change,omitempty"`
	Status      string     `json:"status,omitempty"` // PASSED, FAILED or RUNNING
	Local       *bool      `json:"local,omitempty"`
	Mine        bool       `json:"mine,omitempty"`
	User        string     `json:"user,omitempty"`
	Token       string     `json:"token,omitempty"`
	Author      string     `json:"author,omitempty"`
	Name        string     `json:"name,omitempty"`
	Provider    string     `json:"provider,omitempty"`
	Since       *time.Time `json:"since,omitempty"`
	Until       *time.Time `json:"until,omitempty"`
	MinDuration float64    `json:"minDuration,omitempty"` // seconds
	MaxDuration float64    `json:"maxDuration,omitempty"` // seconds
}

// Sort orders for Org.traces.
const (
	TraceListSortStart    = "START"
	TraceListSortDuration = "DURATION"
)

// traceListPageSize is how many traces each request asks for while paging to
// the caller's limit. The server caps a page at 1000.
const traceListPageSize = 100

// TraceSummary is one row of Org.traces.
type TraceSummary struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Status    *TraceStatus `json:"status"`
	Timestamp time.Time    `json:"timestamp"`
	EndTime   *time.Time   `json:"endTime"`
	Local     bool         `json:"local"`
	Sender    *TraceSender `json:"sender"`
	TraceMetadata
}

type TraceStatus struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type TraceSender struct {
	Name string `json:"name"`
}

// Trace states as the CLI shows them.
const (
	TraceStatePassed  = "passed"
	TraceStateFailed  = "failed"
	TraceStateRunning = "running"
)

// State is the trace's state: running until it has an end time, then failed
// or passed by its status code.
func (t *TraceSummary) State() string {
	switch {
	case t.EndTime == nil:
		return TraceStateRunning
	case t.Status != nil && t.Status.Code == "STATUS_CODE_ERROR":
		return TraceStateFailed
	default:
		return TraceStatePassed
	}
}

// Duration is the trace's run time so far, measured to now while it runs.
func (t *TraceSummary) Duration(now time.Time) time.Duration {
	if t.EndTime == nil {
		return now.Sub(t.Timestamp)
	}
	return t.EndTime.Sub(t.Timestamp)
}

const traceListOperation = `
query TraceList($org: String!, $filter: TraceFilter, $sort: TraceSort!, $first: Int!, $after: Time) {
	org(name: $org) {
		traces(filter: $filter, sort: $sort, first: $first, after: $after) {
			pageInfo { endCursor hasNextPage }
			nodes {
				id
				name
				status { code message }
				timestamp
				endTime
				local
				sender { name }
				git {
					remote
					title
					ref
					tag
					branch
					author { name email }
				}
				ci {
					provider
					repository
					change { id title branch headSHA }
				}
			}
		}
	}
}
`

// TraceList lists an org's traces, local and CI, that match filter.
//
// Org.traces is a cursor-paginated connection, so this pages until it has the
// caller's limit or the server runs out. The cursor is the raw endCursor value
// echoed back untouched: it is a nanosecond timestamp, and reformatting it (for
// example through time.RFC3339) would truncate it to whole seconds and skip
// every trace sharing that second.
//
// DURATION order is unrelated to the time cursors, so the server rejects a
// cursor with that sort. A duration-sorted listing is therefore a single page,
// which is why the caller bounds it with a since window.
func (c *Client) TraceList(ctx context.Context, orgName string, filter TraceListFilter, sort string, first int) ([]TraceSummary, error) {
	filter.Repos = expandRepoForms(filter.Repos)

	var (
		traces []TraceSummary
		after  *string
	)
	for len(traces) < first {
		page := min(traceListPageSize, first-len(traces))

		vars := map[string]any{
			"org":    orgName,
			"filter": filter,
			"sort":   sort,
			"first":  page,
		}
		if after != nil {
			vars["after"] = *after
		}

		var data struct {
			Org *struct {
				Traces *struct {
					PageInfo struct {
						EndCursor   *string `json:"endCursor"`
						HasNextPage bool    `json:"hasNextPage"`
					} `json:"pageInfo"`
					Nodes []TraceSummary `json:"nodes"`
				} `json:"traces"`
			} `json:"org"`
		}
		if err := c.doGraphQL(ctx, "TraceList", traceListOperation, vars, &data); err != nil {
			return nil, err
		}
		if data.Org == nil || data.Org.Traces == nil {
			return traces, nil
		}

		traces = append(traces, data.Org.Traces.Nodes...)

		info := data.Org.Traces.PageInfo
		// Stop on a short page too: without it an empty last page would loop.
		if !info.HasNextPage || info.EndCursor == nil || len(data.Org.Traces.Nodes) == 0 {
			break
		}
		// Duration order cannot be paged; the server would reject the cursor.
		if sort == TraceListSortDuration {
			break
		}
		after = info.EndCursor
	}

	if len(traces) > first {
		traces = traces[:first]
	}
	return traces, nil
}
