package cloud

import "context"

// GitLabSource is a namespace the organization has already been granted access
// to. Connecting a personal account alone does not add an organization source.
type GitLabSource struct {
	ID            string `json:"id"`
	InstanceURL   string `json:"instanceURL"`
	NamespacePath string `json:"namespacePath"`
}

const getGitLabOrganizationSourcesOperation = `
query GetGitLabOrganizationSources($org: ID!) {
	gitlabOrganizationSources(org: $org) {
		id
		instanceURL
		namespacePath
	}
}
`

func (c *Client) GitLabOrganizationSources(ctx context.Context, orgID string) ([]GitLabSource, error) {
	var data struct {
		Sources []GitLabSource `json:"gitlabOrganizationSources"`
	}
	if err := c.doGraphQL(ctx, "GetGitLabOrganizationSources", getGitLabOrganizationSourcesOperation, map[string]any{"org": orgID}, &data); err != nil {
		return nil, err
	}
	return data.Sources, nil
}
