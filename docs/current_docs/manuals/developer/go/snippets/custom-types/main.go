package main

type Github struct{}

func (module *Github) DaggerOrganization() *Organization {
	return &Organization{
		URL:          "https://github.com/dagger",
		Repositories: []*GitRepository{dag.Git(`${organisation.url}/dagger`)},
		Members: []*Account{
			{"jane", "jane@example.com"},
			{"john", "john@example.com"},
		},
	}
}

type Account struct {
	Username string
	Email    string
}

func (account *Account) URL() string {
	return "https://github.com/" + account.Username
}

type Organization struct {
	URL          string
	Repositories []*GitRepository
	Members      []*Account
}
