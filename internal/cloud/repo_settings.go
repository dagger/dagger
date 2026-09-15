package cloud

import "context"

type RepoSetting struct {
	Repo     string `json:"repo"`
	IsPublic bool   `json:"isPublic"`
}

type Repo struct {
	Name     string `json:"name"`
	FullName string `json:"fullName"`
	Private  bool   `json:"private"`
}

const getReposOperation = `
query GetRepos {
	getRepos {
		name
		fullName
		private
	}
}
`

func (c *Client) Repos(ctx context.Context) ([]Repo, error) {
	var data struct {
		Repos []Repo `json:"getRepos"`
	}
	if err := c.doGraphQL(ctx, "GetRepos", getReposOperation, nil, &data); err != nil {
		return nil, err
	}
	return data.Repos, nil
}

const getOrgRepoSettingOperation = `
query GetOrgRepoSetting($org: String!, $repo: String!) {
	org(name: $org) {
		repoSettings(repo: $repo) {
			repo
			isPublic
		}
	}
}
`

func (c *Client) OrgRepoSetting(ctx context.Context, orgName, repo string) (*RepoSetting, error) {
	var data struct {
		Org struct {
			RepoSettings []RepoSetting `json:"repoSettings"`
		} `json:"org"`
	}
	if err := c.doGraphQL(ctx, "GetOrgRepoSetting", getOrgRepoSettingOperation, map[string]any{
		"org":  orgName,
		"repo": repo,
	}, &data); err != nil {
		return nil, err
	}
	if len(data.Org.RepoSettings) == 0 {
		return nil, nil
	}
	return &data.Org.RepoSettings[0], nil
}

const updateOrgRepoSettingOperation = `
mutation UpdateOrgRepoSetting($org: ID!, $repo: String!, $isPublic: Boolean!) {
	updateOrgRepoSetting(org: $org, setting: { repo: $repo, isPublic: $isPublic })
}
`

func (c *Client) UpdateOrgRepoSetting(ctx context.Context, orgID, repo string, isPublic bool) (bool, error) {
	var data struct {
		UpdateOrgRepoSetting bool `json:"updateOrgRepoSetting"`
	}
	if err := c.doGraphQL(ctx, "UpdateOrgRepoSetting", updateOrgRepoSettingOperation, map[string]any{
		"org":      orgID,
		"repo":     repo,
		"isPublic": isPublic,
	}, &data); err != nil {
		return false, err
	}
	return data.UpdateOrgRepoSetting, nil
}

const deleteOrgRepoSettingOperation = `
mutation DeleteOrgRepoSetting($org: ID!, $repo: String!) {
	deleteOrgRepoSetting(org: $org, repoName: $repo)
}
`

func (c *Client) DeleteOrgRepoSetting(ctx context.Context, orgID, repo string) (bool, error) {
	var data struct {
		DeleteOrgRepoSetting bool `json:"deleteOrgRepoSetting"`
	}
	if err := c.doGraphQL(ctx, "DeleteOrgRepoSetting", deleteOrgRepoSettingOperation, map[string]any{
		"org":  orgID,
		"repo": repo,
	}, &data); err != nil {
		return false, err
	}
	return data.DeleteOrgRepoSetting, nil
}
