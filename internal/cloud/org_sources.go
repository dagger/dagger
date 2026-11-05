package cloud

import (
	"context"
	"errors"
	"fmt"
)

type Source struct {
	Name         string  `json:"name"`
	ID           string  `json:"id"`
	Type         string  `json:"type"`
	ConfiguredAt string  `json:"configuredAt"`
	OrgID        *string `json:"orgId"`
	OrgName      *string `json:"orgName"`
	ConfigURL    string  `json:"configUrl"`
}

// SourceRepository is a repository that an installed source can reach, as
// seen by one organization.
type SourceRepository struct {
	Repository string `json:"repository"`
	Selected   bool   `json:"selected"`
	// Eligible is false when another organization has claimed the repository.
	Eligible         bool    `json:"eligible"`
	ClaimedByOrgName *string `json:"claimedByOrgName"`
}

// MappedSource is the installed source that claims a repository for an
// organization.
type MappedSource struct {
	Provider       string `json:"provider"`
	SourceName     string `json:"sourceName"`
	InstallationID string `json:"installationId"`
	ConfigURL      string `json:"configUrl"`
	Org            *Org   `json:"org"`
}

// Org identifies a Dagger Cloud organization.
type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Repository is a repo-scoped view of Cloud configuration, reachable through
// the user-scoped User.repositories(refs:) field. It only surfaces repos that
// one of the user's organizations has claimed for checks.
type Repository struct {
	Ref          string        `json:"ref"`
	Settings     *RepoSetting  `json:"settings"`
	MappedSource *MappedSource `json:"mappedSource"`
}

type SubscriptionInfo struct {
	Status         string  `json:"status"`
	TrialStart     *string `json:"trialStart,omitempty"`
	TrialEnd       *string `json:"trialEnd,omitempty"`
	SubscriptionID string  `json:"subscriptionID"`
	PlanID         string  `json:"planID"`
	HasCaching     bool    `json:"hasCaching"`
}

type Feature struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	TrialStart *string `json:"trialStart,omitempty"`
	TrialEnd   *string `json:"trialEnd,omitempty"`
}

type OrgDetails struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	CreatedAt    string           `json:"createdAt"`
	Subscription SubscriptionInfo `json:"subscription"`
	Features     []Feature        `json:"features"`
}

const getSourcesOperation = `
query GetSources {
	sources {
		name
		id
		type
		configuredAt
		orgId
		orgName
		configUrl
	}
}
`

func (c *Client) Sources(ctx context.Context) ([]Source, error) {
	var data struct {
		Sources []Source `json:"sources"`
	}
	if err := c.doGraphQL(ctx, "GetSources", getSourcesOperation, nil, &data); err != nil {
		return nil, err
	}
	return data.Sources, nil
}

const getUserRepositoriesOperation = `
query GetUserRepositories($refs: [String!]) {
	user {
		repositories(refs: $refs) {
			ref
			settings {
				repo
				isPublic
				effectiveRunChecksOnDefaultBranch
				effectiveRunChecksOnPullRequest
			}
			mappedSource {
				provider
				sourceName
				installationId
				configUrl
				org {
					id
					name
				}
			}
		}
	}
}
`

// UserRepositories returns the repo-scoped Cloud configuration for the given
// refs, scoped to the authenticated user. Only repos that one of the user's
// organizations has claimed are returned.
func (c *Client) UserRepositories(ctx context.Context, refs ...string) ([]Repository, error) {
	var data struct {
		User struct {
			Repositories []Repository `json:"repositories"`
		} `json:"user"`
	}
	if err := c.doGraphQL(ctx, "GetUserRepositories", getUserRepositoriesOperation, map[string]any{
		"refs": refs,
	}, &data); err != nil {
		return nil, err
	}
	return data.User.Repositories, nil
}

const getSourceRepositoriesOperation = `
query GetSourceRepositories($installationId: ID!, $org: ID) {
	sourceRepositories(installationId: $installationId, org: $org) {
		repository
		selected
		eligible
		claimedByOrgName
	}
}
`

func (c *Client) SourceRepositories(ctx context.Context, installationID, orgID string) ([]SourceRepository, error) {
	var data struct {
		SourceRepositories []SourceRepository `json:"sourceRepositories"`
	}
	if err := c.doGraphQL(ctx, "GetSourceRepositories", getSourceRepositoriesOperation, map[string]any{
		"installationId": installationID,
		"org":            orgID,
	}, &data); err != nil {
		return nil, err
	}
	return data.SourceRepositories, nil
}

const createQuickstartOrgOperation = `
mutation CreateQuickstartOrg($name: String!) {
	createQuickstartOrg(name: $name) {
		id
		name
	}
}
`

// CreateQuickstartOrg creates a free "quickstart" org for the authenticated
// user without any browser interaction, returning the new org's id and name.
// The requested name may be adjusted server-side (e.g. to resolve collisions),
// so callers must use the returned name rather than the requested one.
func (c *Client) CreateQuickstartOrg(ctx context.Context, name string) (*OrgResponse, error) {
	if c.g == nil {
		return nil, errors.New("no user logged in")
	}
	var data struct {
		CreateQuickstartOrg OrgResponse `json:"createQuickstartOrg"`
	}
	if err := c.doGraphQL(ctx, "CreateQuickstartOrg", createQuickstartOrgOperation, map[string]any{
		"name": name,
	}, &data); err != nil {
		return nil, err
	}
	return &data.CreateQuickstartOrg, nil
}

// GitHubConnection is a user's linked GitHub identity in Dagger Cloud.
type GitHubConnection struct {
	GitHubLogin string `json:"githubLogin"`
	ConnectedAt string `json:"connectedAt"`
}

const getGithubConnectionOperation = `
query GetGithubConnection {
	githubConnection {
		githubLogin
		connectedAt
	}
}
`

// GitHubConnection returns the authenticated user's GitHub connection, or nil
// when no GitHub account is connected yet.
func (c *Client) GitHubConnection(ctx context.Context) (*GitHubConnection, error) {
	var data struct {
		GitHubConnection *GitHubConnection `json:"githubConnection"`
	}
	if err := c.doGraphQL(ctx, "GetGithubConnection", getGithubConnectionOperation, nil, &data); err != nil {
		return nil, err
	}
	return data.GitHubConnection, nil
}

const getGithubOAuthURLOperation = `
query GetGithubOAuthURL($redirectURI: String!) {
	githubOAuthURL(redirectURI: $redirectURI)
}
`

func (c *Client) GitHubOAuthURL(ctx context.Context, redirectURI string) (string, error) {
	var data struct {
		GitHubOAuthURL string `json:"githubOAuthURL"`
	}
	if err := c.doGraphQL(ctx, "GetGithubOAuthURL", getGithubOAuthURLOperation, map[string]any{
		"redirectURI": redirectURI,
	}, &data); err != nil {
		return "", err
	}
	return data.GitHubOAuthURL, nil
}

const getOrgDetailsOperation = `
query GetOrgDetails($org: String!) {
	org(name: $org) {
		id
		name
		createdAt
		subscription {
			status
			trialStart
			trialEnd
			subscriptionID
			planID
			hasCaching
		}
		features {
			name
			status
			trialStart
			trialEnd
		}
	}
}
`

func (c *Client) OrgDetails(ctx context.Context, orgName string) (*OrgDetails, error) {
	var data struct {
		Org *OrgDetails `json:"org"`
	}
	if err := c.doGraphQL(ctx, "GetOrgDetails", getOrgDetailsOperation, map[string]any{
		"org": orgName,
	}, &data); err != nil {
		return nil, err
	}
	if data.Org == nil {
		return nil, fmt.Errorf("org %q not found", orgName)
	}
	return data.Org, nil
}

const getOrgCLINoticeOperation = `
query GetOrgCLINotice($org: String!) {
	org(name: $org) {
		cliNotice {
			level
			title
			body
		}
	}
}
`

// CLINoticeLevel is how prominently the CLI shows a CLINotice.
type CLINoticeLevel string

const (
	CLINoticeLevelInfo    CLINoticeLevel = "INFO"
	CLINoticeLevelWarning CLINoticeLevel = "WARNING"
)

// CLINotice is a message Cloud has for the CLI's user about an org, such as
// a trial reminder. Cloud chooses it from the org's current state.
type CLINotice struct {
	Level CLINoticeLevel `json:"level"`
	Title string         `json:"title"`
	Body  string         `json:"body"`
}

// OrgCLINotice returns the notice Cloud has for the org, or nil when it has
// none.
func (c *Client) OrgCLINotice(ctx context.Context, orgName string) (*CLINotice, error) {
	var data struct {
		Org *struct {
			CLINotice *CLINotice `json:"cliNotice"`
		} `json:"org"`
	}
	if err := c.doGraphQL(ctx, "GetOrgCLINotice", getOrgCLINoticeOperation, map[string]any{
		"org": orgName,
	}, &data); err != nil {
		return nil, err
	}
	if data.Org == nil {
		return nil, fmt.Errorf("org %q not found", orgName)
	}
	return data.Org.CLINotice, nil
}

const createPortalSessionOperation = `
mutation CreatePortalSession($org: ID!) {
	createPortalSession(org: $org)
}
`

func (c *Client) CreatePortalSession(ctx context.Context, orgID string) (string, error) {
	var data struct {
		CreatePortalSession string `json:"createPortalSession"`
	}
	if err := c.doGraphQL(ctx, "CreatePortalSession", createPortalSessionOperation, map[string]any{
		"org": orgID,
	}, &data); err != nil {
		return "", err
	}
	return data.CreatePortalSession, nil
}
