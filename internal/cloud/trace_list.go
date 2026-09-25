package cloud

import (
	"context"
	"time"
)

// TraceListFilter narrows Org.traceList. Zero fields do not filter.
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

// Sort orders for Org.traceList.
const (
	TraceListSortStart    = "START"
	TraceListSortDuration = "DURATION"
)

// TraceSummary is one row of Org.traceList.
type TraceSummary struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Status    *TraceStatus `json:"status"`
	Timestamp time.Time    `json:"timestamp"`
	EndTime   *time.Time   `json:"endTime"`
	Local     bool         `json:"local"`
	Sender    *TraceSender `json:"sender"`
	Git       *TraceGit    `json:"git"`
	CI        *TraceCI     `json:"ci"`
}

type TraceStatus struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type TraceSender struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TraceGit struct {
	Remote string          `json:"remote"`
	Title  string          `json:"title"`
	Ref    string          `json:"ref"`
	Tag    *string         `json:"tag"`
	Branch *string         `json:"branch"`
	Author *TraceGitAuthor `json:"author"`
}

type TraceGitAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type TraceCI struct {
	Provider   *string        `json:"provider"`
	Repository *string        `json:"repository"`
	Change     *TraceCIChange `json:"change"`
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
	case t.EndTime == nil || t.EndTime.IsZero():
		return TraceStateRunning
	case t.Status != nil && t.Status.Code == "STATUS_CODE_ERROR":
		return TraceStateFailed
	default:
		return TraceStatePassed
	}
}

// Duration is the trace's run time so far, measured to now while it runs.
func (t *TraceSummary) Duration(now time.Time) time.Duration {
	if t.EndTime == nil || t.EndTime.IsZero() {
		return now.Sub(t.Timestamp)
	}
	return t.EndTime.Sub(t.Timestamp)
}

const traceListOperation = `
query TraceList($org: String!, $filter: TraceListFilter, $sort: TraceListSort!, $first: Int!) {
	org(name: $org) {
		traceList(filter: $filter, sort: $sort, first: $first) {
			id
			name
			status { code message }
			timestamp
			endTime
			local
			sender { id name }
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
				change { id title url branch headSHA }
			}
		}
	}
}
`

// TraceList lists an org's traces, local and CI, that match filter.
func (c *Client) TraceList(ctx context.Context, orgName string, filter TraceListFilter, sort string, first int) ([]TraceSummary, error) {
	filter.Repos = expandRepoForms(filter.Repos)
	var data struct {
		Org *struct {
			TraceList []TraceSummary `json:"traceList"`
		} `json:"org"`
	}
	if err := c.doGraphQL(ctx, "TraceList", traceListOperation, map[string]any{
		"org":    orgName,
		"filter": filter,
		"sort":   sort,
		"first":  first,
	}, &data); err != nil {
		return nil, err
	}
	if data.Org == nil {
		return nil, nil
	}
	return data.Org.TraceList, nil
}

const lastUserTraceOperation = `
query LastUserTrace($org: String!) {
	org(name: $org) {
		lastUserTrace {
			id
		}
	}
}
`

// LastUserTraceID returns the ID of the logged-in user's most recent trace in
// the org, or "" when there is none.
func (c *Client) LastUserTraceID(ctx context.Context, orgName string) (string, error) {
	var data struct {
		Org *struct {
			LastUserTrace *struct {
				ID string `json:"id"`
			} `json:"lastUserTrace"`
		} `json:"org"`
	}
	if err := c.doGraphQL(ctx, "LastUserTrace", lastUserTraceOperation, map[string]any{
		"org": orgName,
	}, &data); err != nil {
		return "", err
	}
	if data.Org == nil || data.Org.LastUserTrace == nil {
		return "", nil
	}
	return data.Org.LastUserTrace.ID, nil
}
