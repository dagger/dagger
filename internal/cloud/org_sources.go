package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type Source struct {
	Name         string  `json:"name"`
	ID           string  `json:"id"`
	Type         string  `json:"type"`
	ConfiguredAt string  `json:"configuredAt"`
	OrgName      *string `json:"orgName"`
	ConfigURL    string  `json:"configUrl"`
}

type SourceRepository struct {
	Repository string `json:"repository"`
	Selected   bool   `json:"selected"`
}

type MappedSource struct {
	SourceName     string   `json:"sourceName"`
	InstallationID string   `json:"installationId"`
	Mode           string   `json:"mode"`
	Repositories   []string `json:"repositories"`
}

// Repository is a repo-scoped view of Cloud configuration, reachable through
// the user-scoped User.repositories(refs:) field. It only surfaces repos that
// are currently auto-checked (selected under a mapped source).
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

type PlanItem struct {
	ID           string `json:"id"`
	ExternalName string `json:"external_name"`
}

type PlanPrice struct {
	ID           string `json:"id"`
	ItemID       string `json:"item_id"`
	ExternalName string `json:"external_name"`
	Unit         string `json:"unit"`
	Price        uint   `json:"price"`
	PeriodUnit   string `json:"period_unit"`
}

type Plan struct {
	Item  PlanItem    `json:"item"`
	Price []PlanPrice `json:"price"`
}

type PlansResponse struct {
	Plans []Plan `json:"plans"`
}

const getSourcesOperation = `
query GetSources {
	sources {
		name
		id
		type
		configuredAt
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
			}
			mappedSource {
				sourceName
				installationId
				mode
				repositories
			}
		}
	}
}
`

// UserRepositories returns the repo-scoped Cloud configuration for the given
// refs, scoped to the authenticated user. Only repos that are currently
// auto-checked (selected) are returned.
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

const getOrgMappedSourcesOperation = `
query GetOrgMappedSources($org: String!) {
	org(name: $org) {
		mappedSources {
			sourceName
			installationId
			mode
			repositories
		}
	}
}
`

func (c *Client) OrgMappedSources(ctx context.Context, orgName string) ([]MappedSource, error) {
	var data struct {
		Org struct {
			MappedSources []MappedSource `json:"mappedSources"`
		} `json:"org"`
	}
	if err := c.doGraphQL(ctx, "GetOrgMappedSources", getOrgMappedSourcesOperation, map[string]any{
		"org": orgName,
	}, &data); err != nil {
		return nil, err
	}
	return data.Org.MappedSources, nil
}

const getSourceRepositoriesOperation = `
query GetSourceRepositories($installationId: ID!, $org: ID) {
	sourceRepositories(installationId: $installationId, org: $org) {
		repository
		selected
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

const configureSourceOperation = `
mutation ConfigureSource($installationId: ID!, $mode: SourceMode!, $repositories: [String!]!) {
	configureSource(source: { installationId: $installationId, mode: $mode, repositories: $repositories }) {
		sourceName
		installationId
		mode
	}
}
`

func (c *Client) ConfigureSource(ctx context.Context, installationID, mode string, repositories []string) (*MappedSource, error) {
	var data struct {
		ConfigureSource MappedSource `json:"configureSource"`
	}
	if err := c.doGraphQL(ctx, "ConfigureSource", configureSourceOperation, map[string]any{
		"installationId": installationID,
		"mode":           mode,
		"repositories":   repositories,
	}, &data); err != nil {
		return nil, err
	}
	return &data.ConfigureSource, nil
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

func (c *Client) Plans(ctx context.Context) (*PlansResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.u.JoinPath("/plans").String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.h.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list plans: %s", resp.Status)
	}
	var plans PlansResponse
	if err := json.NewDecoder(resp.Body).Decode(&plans); err != nil {
		return nil, err
	}
	return &plans, nil
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
