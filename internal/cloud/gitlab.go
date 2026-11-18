package cloud

import (
	"context"
	"fmt"
)

// GitLabSource is a namespace the organization has already been granted access
// to. Connecting a personal account alone does not add an organization source.
type GitLabSource struct {
	ID            string              `json:"id"`
	InstanceURL   string              `json:"instanceURL"`
	NamespacePath string              `json:"namespacePath"`
	Access        *GitLabSourceAccess `json:"access"`
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

// GitLabSourceAccess indicates that an organization grant exists. It contains
// no provider credential; tokens remain in Cloud.
type GitLabSourceAccess struct {
	ConnectionID string `json:"connectionID"`
}

// GitLabSources also lists namespaces the caller can manage personally. This
// query is admin-only and read-only; choosing one still requires a grant.
func (c *Client) GitLabSources(ctx context.Context, orgID string) ([]GitLabSource, error) {
	var data struct {
		Sources []GitLabSource `json:"gitlabSources"`
	}
	const query = `query GetGitLabSources($org: ID!) { gitlabSources(org:$org) { id instanceURL namespacePath access(org:$org) { connectionID } } }`
	if err := c.doGraphQL(ctx, "GetGitLabSources", query, map[string]any{"org": orgID}, &data); err != nil {
		return nil, err
	}
	return data.Sources, nil
}

// UseGitLabAccount explicitly grants this organization access through the
// caller's own connected account. The API verifies current admin and GitLab access.
func (c *Client) UseGitLabAccount(ctx context.Context, orgID, sourceID string) error {
	const query = `mutation UseGitLabAccount($org: ID!, $source: ID!) { useGitLabAccount(org:$org,source:$source) { connectionID } }`
	var data struct {
		Access *GitLabSourceAccess `json:"useGitLabAccount"`
	}
	if err := c.doGraphQL(ctx, "UseGitLabAccount", query, map[string]any{"org": orgID, "source": sourceID}, &data); err != nil {
		return err
	}
	if data.Access == nil {
		return fmt.Errorf("missing GitLab organization access confirmation")
	}
	return nil
}
