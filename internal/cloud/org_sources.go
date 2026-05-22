package cloud

import "context"

type SourceMode string

const (
	SourceModeAll      SourceMode = "ALL"
	SourceModeSelected SourceMode = "SELECTED"
)

type SourceSelectionInput struct {
	InstallationID string     `json:"installationId"`
	Mode           SourceMode `json:"mode"`
	Repositories   []string   `json:"repositories"`
}

type Source struct {
	Name         string  `json:"name"`
	ID           string  `json:"id"`
	Type         string  `json:"type"`
	ConfiguredAt string  `json:"configuredAt"`
	Owner        string  `json:"owner"`
	OrgID        *string `json:"orgId"`
	OrgName      *string `json:"orgName"`
	AvatarURL    string  `json:"avatarUrl"`
	ConfigURL    string  `json:"configUrl"`
}

type SourceRepository struct {
	Name             string  `json:"name"`
	FullName         string  `json:"fullName"`
	Repository       string  `json:"repository"`
	Private          *bool   `json:"private"`
	HTMLURL          *string `json:"htmlURL"`
	Selected         bool    `json:"selected"`
	Eligible         bool    `json:"eligible"`
	ClaimedByOrgID   *string `json:"claimedByOrgId"`
	ClaimedByOrgName *string `json:"claimedByOrgName"`
}

type MappedSource struct {
	SourceName     string     `json:"sourceName"`
	InstallationID string     `json:"installationId"`
	ConfigURL      string     `json:"configUrl"`
	Owner          string     `json:"owner"`
	AvatarURL      string     `json:"avatarUrl"`
	MappedAt       string     `json:"mappedAt"`
	Type           string     `json:"type"`
	Mode           SourceMode `json:"mode"`
}

type GitSourcesResponse struct {
	Org struct {
		ID                   string         `json:"id"`
		Name                 string         `json:"name"`
		MappedSources        []MappedSource `json:"mappedSources"`
		ModuleIgnorePatterns []string       `json:"moduleIgnorePatterns"`
	} `json:"org"`
}

type GitHubConnection struct {
	GitHubLogin string `json:"githubLogin"`
	ConnectedAt string `json:"connectedAt"`
}

type Integration struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	IconURL     string  `json:"iconUrl"`
	Category    string  `json:"category"`
	EnabledAt   *string `json:"enabledAt"`
}

const getSourcesOperation = `
query GetSources {
	sources {
		name
		id
		type
		configuredAt
		owner
		orgId
		orgName
		avatarUrl
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

const getSourceRepositoriesOperation = `
query GetSourceRepositories($installationId: ID!, $org: ID) {
	sourceRepositories(installationId: $installationId, org: $org) {
		name
		fullName
		repository
		private
		htmlURL
		selected
		eligible
		claimedByOrgId
		claimedByOrgName
	}
}
`

func (c *Client) SourceRepositories(ctx context.Context, installationID string, orgID string) ([]SourceRepository, error) {
	var org any
	if orgID != "" {
		org = orgID
	}
	var data struct {
		SourceRepositories []SourceRepository `json:"sourceRepositories"`
	}
	if err := c.doGraphQL(ctx, "GetSourceRepositories", getSourceRepositoriesOperation, map[string]any{
		"installationId": installationID,
		"org":            org,
	}, &data); err != nil {
		return nil, err
	}
	return data.SourceRepositories, nil
}

const getGitSourcesOperation = `
query GetGitSources($org: String!) {
	org(name: $org) {
		id
		name
		mappedSources {
			sourceName
			installationId
			configUrl
			owner
			avatarUrl
			mappedAt
			type
			mode
		}
		moduleIgnorePatterns
	}
}
`

func (c *Client) GitSources(ctx context.Context, orgName string) (*GitSourcesResponse, error) {
	var data GitSourcesResponse
	if err := c.doGraphQL(ctx, "GetGitSources", getGitSourcesOperation, map[string]any{
		"org": orgName,
	}, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

const configureOrgSourceOperation = `
mutation ConfigureOrgSource($org: ID!, $source: SourceSelectionInput!) {
	configureOrgSource(org: $org, source: $source) {
		sourceName
		installationId
		configUrl
		owner
		avatarUrl
		mappedAt
		type
		mode
	}
}
`

func (c *Client) ConfigureOrgSource(ctx context.Context, orgID string, source SourceSelectionInput) (*MappedSource, error) {
	var data struct {
		ConfigureOrgSource MappedSource `json:"configureOrgSource"`
	}
	if err := c.doGraphQL(ctx, "ConfigureOrgSource", configureOrgSourceOperation, map[string]any{
		"org":    orgID,
		"source": source,
	}, &data); err != nil {
		return nil, err
	}
	return &data.ConfigureOrgSource, nil
}

const unmapSourceOperation = `
mutation UnmapSource($org: ID!, $installationId: ID!) {
	unmapSourceFromOrg(org: $org, installationId: $installationId)
}
`

func (c *Client) UnmapSource(ctx context.Context, orgID string, installationID string) (bool, error) {
	var data struct {
		UnmapSourceFromOrg bool `json:"unmapSourceFromOrg"`
	}
	if err := c.doGraphQL(ctx, "UnmapSource", unmapSourceOperation, map[string]any{
		"org":            orgID,
		"installationId": installationID,
	}, &data); err != nil {
		return false, err
	}
	return data.UnmapSourceFromOrg, nil
}

const setModuleIgnorePatternsOperation = `
mutation SetModuleIgnorePatterns($org: ID!, $patterns: [String!]!) {
	moduleIgnorePatterns(org: $org, patterns: $patterns)
}
`

func (c *Client) SetModuleIgnorePatterns(ctx context.Context, orgID string, patterns []string) (bool, error) {
	var data struct {
		ModuleIgnorePatterns bool `json:"moduleIgnorePatterns"`
	}
	if err := c.doGraphQL(ctx, "SetModuleIgnorePatterns", setModuleIgnorePatternsOperation, map[string]any{
		"org":      orgID,
		"patterns": patterns,
	}, &data); err != nil {
		return false, err
	}
	return data.ModuleIgnorePatterns, nil
}

const refreshModulesOperation = `
mutation RefreshModules($org: ID!, $sourceNames: [String!]) {
	refreshModules(org: $org, sourceNames: $sourceNames)
}
`

func (c *Client) RefreshModules(ctx context.Context, orgID string, sourceNames []string) (bool, error) {
	var sourceNamesVar any
	if sourceNames != nil {
		sourceNamesVar = sourceNames
	}
	var data struct {
		RefreshModules bool `json:"refreshModules"`
	}
	if err := c.doGraphQL(ctx, "RefreshModules", refreshModulesOperation, map[string]any{
		"org":         orgID,
		"sourceNames": sourceNamesVar,
	}, &data); err != nil {
		return false, err
	}
	return data.RefreshModules, nil
}

const getGithubConnectionOperation = `
query GetGithubConnection {
	githubConnection {
		githubLogin
		connectedAt
	}
}
`

func (c *Client) GitHubConnection(ctx context.Context) (*GitHubConnection, error) {
	var data struct {
		GitHubConnection *GitHubConnection `json:"githubConnection"`
	}
	if err := c.doGraphQL(ctx, "GetGithubConnection", getGithubConnectionOperation, nil, &data); err != nil {
		return nil, err
	}
	return data.GitHubConnection, nil
}

const getIntegrationsOperation = `
query GetIntegrations($org: ID!) {
	orgIntegrations(org: $org) {
		id
		name
		description
		iconUrl
		category
		enabledAt
	}
}
`

func (c *Client) Integrations(ctx context.Context, orgID string) ([]Integration, error) {
	var data struct {
		OrgIntegrations []Integration `json:"orgIntegrations"`
	}
	if err := c.doGraphQL(ctx, "GetIntegrations", getIntegrationsOperation, map[string]any{
		"org": orgID,
	}, &data); err != nil {
		return nil, err
	}
	return data.OrgIntegrations, nil
}
