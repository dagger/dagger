package main

type MyModule struct{}

func (module *MyModule) DaggerOrganization() *GitHubOrganization {
	return &GitHubOrganization{
		URL:          "https://github.com/dagger",
		Repositories: []*GitRepository{dag.Git(`${organisation.url}/dagger`)},
		Members: []*GitHubAccount{
			{"jane", "jane@example.com"},
			{"john", "john@example.com"},
		},
	}
}

type GitHubAccount struct {
	Username string
	Email    string
}

func (account *GitHubAccount) URL() string {
	return "https://github.com/" + account.Username
}

type GitHubOrganization struct {
	URL          string
	Repositories []*GitRepository
	Members      []*GitHubAccount
}
