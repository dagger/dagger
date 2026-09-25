package cloud

import (
	"context"
)

// The org-scoped GraphQL side of a trace: its source metadata. Span and log
// streaming moved to the binary OTLP endpoints (otlp.go), which are addressed
// by trace ID alone; this query stays on GraphQL because it IS org-scoped --
// the CI change it reports belongs to the org's source integration.

// graphqlRequest is the JSON body sent to the GraphQL endpoint.
type graphqlRequest struct {
	OpName    string         `json:"operationName"`
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

const getTraceMetadataOperation = `
query GetTraceMetadata ($org: ID!, $traceID: ID!) {
	trace(id: $traceID, org: $org) {
		git {
			ref
		}
		ci {
			isNativeCI
			change {
				headSHA
			}
		}
	}
}
`

// TraceMetadata is the trace's source git/CI context. A query fills only the
// fields it selects.
type TraceMetadata struct {
	Git *TraceGitMetadata `json:"git"`
	CI  *TraceCIMetadata  `json:"ci"`
}

type TraceGitMetadata struct {
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

type TraceCIMetadata struct {
	IsNativeCI bool           `json:"isNativeCI"`
	Provider   *string        `json:"provider"`
	Repository *string        `json:"repository"`
	Change     *TraceCIChange `json:"change"`
}

type TraceCIChange struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Branch  string `json:"branch"`
	HeadSHA string `json:"headSHA"`
}

// TraceMetadata fetches a trace's git/CI context. Returns nil (no error) when the
// trace has no metadata recorded.
func (c *Client) TraceMetadata(ctx context.Context, orgID, traceID string) (*TraceMetadata, error) {
	var data struct {
		Trace *TraceMetadata `json:"trace"`
	}
	if err := c.doGraphQL(ctx, "GetTraceMetadata", getTraceMetadataOperation, map[string]any{
		"org":     orgID,
		"traceID": traceID,
	}, &data); err != nil {
		return nil, err
	}
	return data.Trace, nil
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
