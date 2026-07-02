package cloud

import (
	"context"
	"fmt"
	"strings"
	"time"
)

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
		withoutScheme := strings.TrimPrefix(strings.TrimPrefix(r, "https://"), "http://")
		add(withoutScheme)
		add("https://" + withoutScheme)

		parts := strings.Split(withoutScheme, "/")
		switch {
		case len(parts) == 2:
			add("github.com/" + withoutScheme)
			add("https://github.com/" + withoutScheme)
		case len(parts) >= 3 && parts[0] == "github.com":
			ownerRepo := strings.Join(parts[1:3], "/")
			add(ownerRepo)
			add("https://github.com/" + ownerRepo)
		}
	}
	return out
}

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

const getUserChecksOperation = `
query GetUserChecks ($repos: [String!], $first: Int) {
	user {
		checks(repos: $repos, first: $first) {
			nodes {
				... CheckCommitProps
				org {
					id
					name
				}
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
	// Org identifies the owning org of the commit. It is only populated by
	// user-scoped queries (e.g. UserChecks) where checks may span orgs; the
	// org-scoped queries leave it zero since the org is known from context.
	Org CheckCommitOrg `json:"org"`
}

type CheckCommitOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type CheckEvent struct {
	Provider  string     `json:"provider"`
	Type      string     `json:"type"`
	Timestamp time.Time  `json:"timestamp"`
	Actor     CheckActor `json:"actor"`
}

type CheckActor struct {
	ID        string `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl"`
}

type CheckCommitRef struct {
	Typename string `json:"__typename"`

	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`

	Number            int    `json:"number,omitempty"`
	Title             string `json:"title,omitempty"`
	State             string `json:"state,omitempty"`
	IntegrationCommit string `json:"integrationCommit,omitempty"`
}

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

func (c *Check) DurationAsTime() time.Duration {
	if c.Duration != nil {
		return time.Duration(*c.Duration) * time.Second
	}
	if c.StartedAt == nil || c.EndTime == nil {
		return 0
	}
	return c.EndTime.Sub(*c.StartedAt)
}

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
	var data struct {
		Org *struct {
			Checks struct {
				Nodes []CheckCommit `json:"nodes"`
			} `json:"checks"`
		} `json:"org"`
	}
	if err := c.doGraphQL(ctx, "GetOrgChecks", getOrgChecksOperation, vars, &data); err != nil {
		return nil, err
	}
	if data.Org == nil {
		return nil, fmt.Errorf("org %q not found", org)
	}
	return data.Org.Checks.Nodes, nil
}

func (c *Client) UserChecks(
	ctx context.Context,
	repos []string,
	first int,
) ([]CheckCommit, error) {
	vars := map[string]any{
		"repos": expandRepoForms(repos),
	}
	if first > 0 {
		vars["first"] = first
	}
	var data struct {
		User *struct {
			Checks struct {
				Nodes []CheckCommit `json:"nodes"`
			} `json:"checks"`
		} `json:"user"`
	}
	if err := c.doGraphQL(ctx, "GetUserChecks", getUserChecksOperation, vars, &data); err != nil {
		return nil, err
	}
	if data.User == nil {
		return nil, nil
	}
	return data.User.Checks.Nodes, nil
}

const rerunChecksOperation = `
mutation RerunChecks ($org: ID!, $checkIds: [ID!]!, $cleanSlate: Boolean) {
	rerunChecks(org: $org, checkIds: $checkIds, cleanSlate: $cleanSlate) {
		id
		name
		status
		moduleRef
		moduleVersion
	}
}
`

// RerunChecks re-runs the given checks (by ID) on Dagger Cloud, against the same
// commit they originally ran on, and returns the newly queued check runs. Checks
// that are already running or queued are skipped server-side and won't appear in
// the result. cleanSlate requests a no-cache-reuse run; it's an experimental,
// org-gated feature and is ignored when unavailable.
func (c *Client) RerunChecks(ctx context.Context, orgID string, checkIDs []string, cleanSlate bool) ([]Check, error) {
	vars := map[string]any{
		"org":      orgID,
		"checkIds": checkIDs,
	}
	if cleanSlate {
		vars["cleanSlate"] = true
	}
	var data struct {
		RerunChecks []Check `json:"rerunChecks"`
	}
	if err := c.doGraphQL(ctx, "RerunChecks", rerunChecksOperation, vars, &data); err != nil {
		return nil, err
	}
	return data.RerunChecks, nil
}

const rerunLoadOperation = `
mutation RerunLoad ($org: ID!, $checkId: ID!) {
	rerunLoad(org: $org, checkId: $checkId) {
		id
		name
		status
		moduleRef
		moduleVersion
	}
}
`

// RerunLoad re-runs a failed load check (the gate that discovers and runs a
// commit's checks) by ID, returning the newly queued load check. Load checks are
// internal and can't go through RerunChecks; the server requires the check to be
// a failed load check.
func (c *Client) RerunLoad(ctx context.Context, orgID, checkID string) (*Check, error) {
	var data struct {
		RerunLoad Check `json:"rerunLoad"`
	}
	if err := c.doGraphQL(ctx, "RerunLoad", rerunLoadOperation, map[string]any{
		"org":     orgID,
		"checkId": checkID,
	}, &data); err != nil {
		return nil, err
	}
	return &data.RerunLoad, nil
}

func (c *Client) ModuleChecks(
	ctx context.Context,
	org string,
	moduleRef string,
	moduleVersion string,
) ([]CheckCommit, error) {
	var data struct {
		Org *struct {
			ModuleChecks []CheckCommit `json:"moduleChecks"`
		} `json:"org"`
	}
	if err := c.doGraphQL(ctx, "GetModuleChecks", getModuleChecksOperation, map[string]any{
		"org":           org,
		"moduleRef":     moduleRef,
		"moduleVersion": moduleVersion,
	}, &data); err != nil {
		return nil, err
	}
	if data.Org == nil {
		return nil, fmt.Errorf("org %q not found", org)
	}
	return data.Org.ModuleChecks, nil
}
