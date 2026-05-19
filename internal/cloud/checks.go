package cloud

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// expandRepoForms duplicates each repo into both bare and "https://"-prefixed
// forms (deduplicated) so the cloud's commits.repository filter matches
// regardless of whether the caller passed the scheme.
func expandRepoForms(repos []string) []string {
	if len(repos) == 0 {
		return repos
	}
	seen := make(map[string]struct{}, len(repos)*2)
	out := make([]string, 0, len(repos)*2)
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, r := range repos {
		add(r)
		switch {
		case strings.HasPrefix(r, "https://"), strings.HasPrefix(r, "http://"):
			// already a URL — also try the bare form
			add(strings.TrimPrefix(strings.TrimPrefix(r, "https://"), "http://"))
		default:
			add("https://" + r)
		}
	}
	return out
}

// GraphQL queries against an org's checks. GetOrgChecks scans recent commits
// (used for SHA resolution); GetModuleChecks targets a specific (moduleRef,
// moduleVersion) pair (used for cheap refetches once we know the answer).
// Both mirror the operations used by the Dagger Cloud UI.

const getOrgChecksOperation = `
query GetOrgChecks ($org: String!, $repos: [String!], $first: Int) {
    org(name: $org) {
        checks(repos: $repos, first: $first) {
            nodes {
                ... CheckCommitProps
            }
        }
    }
}
` + checkCommitFragments

const getModuleChecksOperation = `
query GetModuleChecks ($org: String!, $moduleRef: String!, $moduleVersion: String!) {
    org(name: $org) {
        moduleChecks(moduleRef: $moduleRef, moduleVersion: $moduleVersion) {
            ... CheckCommitProps
        }
    }
}
` + checkCommitFragments

const checkCommitFragments = `
fragment CheckCommitProps on CheckCommit {
    repo
    commitSHA
    commitMessage
    timestamp
    authorName
    authorEmail
    events {
        provider
        type
        timestamp
        actor {
            id
            login
            name
            avatarUrl
        }
    }
    refs {
        __typename
        ... on CheckCommitTagRef {
            name
            url
        }
        ... on CheckCommitBranchRef {
            name
            url
        }
        ... on CheckCommitPullRequestRef {
            number
            url
            title
            state
            integrationCommit
        }
    }
    checks {
        ... CheckProps
    }
}
fragment CheckProps on Check {
    id
    name
    status
    startedAt
    endTime
    duration
    traceId
    spanId
    moduleRef
    moduleVersion
    internal
}
`

// CheckCommit groups the checks recorded for a single commit, together with
// the commit metadata and any refs (branches, tags, PRs) pointing at it.
type CheckCommit struct {
	Repo          string           `json:"repo"`
	CommitSHA     string           `json:"commitSHA"`
	CommitMessage string           `json:"commitMessage"`
	Timestamp     time.Time        `json:"timestamp"`
	AuthorName    string           `json:"authorName"`
	AuthorEmail   string           `json:"authorEmail"`
	Events        []CheckEvent     `json:"events"`
	Refs          []CheckCommitRef `json:"refs"`
	Checks        []Check          `json:"checks"`
}

// CheckEvent is a provider event (e.g. GitHub push, PR sync) associated with
// the commit.
type CheckEvent struct {
	Provider  string     `json:"provider"`
	Type      string     `json:"type"`
	Timestamp time.Time  `json:"timestamp"`
	Actor     CheckActor `json:"actor"`
}

// CheckActor identifies the user (or bot) that triggered a CheckEvent.
type CheckActor struct {
	ID        string `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl"`
}

// CheckCommitRef is a union of tag, branch and pull-request refs. The
// concrete type is identified by Typename; only the matching subset of fields
// is populated.
type CheckCommitRef struct {
	Typename string `json:"__typename"`

	// CheckCommitTagRef / CheckCommitBranchRef
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`

	// CheckCommitPullRequestRef
	Number            int    `json:"number,omitempty"`
	Title             string `json:"title,omitempty"`
	State             string `json:"state,omitempty"`
	IntegrationCommit string `json:"integrationCommit,omitempty"`
}

// Check is a single check execution for a module ref + version. Duration is
// in seconds (see schema: `Check.duration: Int  # in seconds`); nil means the
// check hasn't finished yet.
type Check struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	StartedAt     *time.Time `json:"startedAt"`
	EndTime       *time.Time `json:"endTime"`
	Duration      *int       `json:"duration"`
	TraceID       string     `json:"traceId"`
	SpanID        string     `json:"spanId"`
	ModuleRef     string     `json:"moduleRef"`
	ModuleVersion string     `json:"moduleVersion"`
	Internal      bool       `json:"internal"`
}

// DurationAsTime returns the check's elapsed time. Prefers the server-supplied
// Duration (in seconds), falling back to EndTime - StartedAt when Duration is
// absent (e.g. running checks).
func (c *Check) DurationAsTime() time.Duration {
	if c.Duration != nil {
		return time.Duration(*c.Duration) * time.Second
	}
	if c.StartedAt == nil || c.EndTime == nil {
		return 0
	}
	return c.EndTime.Sub(*c.StartedAt)
}

// OrgChecks fetches the most recent CheckCommits for an org, optionally
// filtered by repo. Accepts repos in the moduleRef form (e.g.
// "github.com/dagger/dagger"); the cloud stores full HTTPS URLs in its
// commits table, so we pass both forms to the filter to match either
// convention. first caps the number of returned commits; pass 0 to use the
// server's default.
func (c *Client) OrgChecks(
	ctx context.Context,
	org string,
	repos []string,
	first int,
) ([]CheckCommit, error) {
	vars := map[string]any{
		"org":   org,
		"repos": expandRepoForms(repos),
	}
	if first > 0 {
		vars["first"] = first
	}
	var resp struct {
		Org *struct {
			Checks struct {
				Nodes []CheckCommit `json:"nodes"`
			} `json:"checks"`
		} `json:"org"`
	}
	err := c.queryGraphQL(ctx, &graphqlRequest{
		OpName:    "GetOrgChecks",
		Query:     getOrgChecksOperation,
		Variables: vars,
	}, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Org == nil {
		return nil, fmt.Errorf("org %q not found", org)
	}
	return resp.Org.Checks.Nodes, nil
}

// ModuleChecks fetches the checks recorded for an exact (moduleRef,
// moduleVersion) pair. Cheaper than OrgChecks for repeated polling once the
// SHA has been resolved.
func (c *Client) ModuleChecks(
	ctx context.Context,
	org string,
	moduleRef string,
	moduleVersion string,
) ([]CheckCommit, error) {
	var resp struct {
		Org *struct {
			ModuleChecks []CheckCommit `json:"moduleChecks"`
		} `json:"org"`
	}
	err := c.queryGraphQL(ctx, &graphqlRequest{
		OpName: "GetModuleChecks",
		Query:  getModuleChecksOperation,
		Variables: map[string]any{
			"org":           org,
			"moduleRef":     moduleRef,
			"moduleVersion": moduleVersion,
		},
	}, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Org == nil {
		return nil, fmt.Errorf("org %q not found", org)
	}
	return resp.Org.ModuleChecks, nil
}
