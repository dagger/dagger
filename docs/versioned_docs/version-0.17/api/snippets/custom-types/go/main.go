package main

import "dagger/my-module/internal/dagger"

type MyModule struct{}

func (module *MyModule) DaggerOrganization() *Organization {
	url := "https://github.com/dagger"
	return &Organization{
		URL:          url,
		Repositories: []*dagger.GitRepository{dag.Git(url + "/dagger")},
		Members: []*Account{
			{"jane", "jane@example.com"},
			{"john", "john@example.com"},
		},
	}
}

type Organization struct {
	URL          string
	Repositories []*dagger.GitRepository
	Members      []*Account
}

type Account struct {
	Username string
	Email    string
}

func (account *Account) URL() string {
	return "https://github.com/" + account.Username
}
