package main

import (
	"context"
	"dagger/releaser/internal/dagger"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
)

type Team struct {
	Slug       string `json:"slug"`
	MembersURL string `json:"members_url"`
}

type User struct {
	Login string `json:"login"`
}

// +cache="session"
func (r *Releaser) GetMaintainers(
	ctx context.Context,

	githubOrgName string,
	githubToken *dagger.Secret, // +optional
) ([]string, error) {
	token, err := githubToken.Plaintext(ctx)
	if err != nil {
		return nil, err
	}
	// HACK: just workaround the lack of https://github.com/dagger/dagger/pull/10836
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("github token is required")
	}
	return getMaintainers(ctx, token, githubOrgName)
}

func getMaintainers(ctx context.Context, token, org string) ([]string, error) {
	teams, err := getAllTeams(ctx, token, org)
	if err != nil {
		return nil, err
	}
	var filtered []Team
	for _, t := range teams {
		if t.Slug == "maintainers" || t.Slug == "team" || strings.HasPrefix(t.Slug, "sdk-") {
			filtered = append(filtered, t)
		}
	}
	var allLogins []string
	for _, t := range filtered {
		logins, err := t.members(ctx, token)
		if err != nil {
			return nil, err
		}
		allLogins = append(allLogins, logins...)
	}
	slices.SortFunc(allLogins, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	allLogins = slices.Compact(allLogins)
	return allLogins, nil
}

func getAllTeams(ctx context.Context, token, org string) ([]Team, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/orgs/"+org+"/teams", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("failed to fetch teams: " + resp.Status)
	}
	var teams []Team
	if err := json.NewDecoder(resp.Body).Decode(&teams); err != nil {
		return nil, err
	}
	return teams, nil
}

func (team *Team) members(ctx context.Context, token string) ([]string, error) {
	url := strings.Replace(team.MembersURL, "{/member}", "", 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("failed to fetch team members: " + resp.Status)
	}
	var users []User
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, err
	}
	logins := make([]string, 0, len(users))
	for _, u := range users {
		logins = append(logins, u.Login)
	}
	return logins, nil
}
