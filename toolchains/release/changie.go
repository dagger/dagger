package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"toolchains/release/internal/dagger"
)

type Team struct {
	Slug       string `json:"slug"`
	MembersURL string `json:"members_url"`
}

type User struct {
	Login string `json:"login"`
}

// TODO: Decouple Changie-specific release-note generation from release publishing.
//
// Desired shape:
//   - Move the generic Changie wrapper out of this repo to github.com/dagger/changie.
//   - Keep generic Changie behavior thin and explicit:
//   - batch(version) -> Changeset
//   - merge() -> Changeset @generate
//   - do not mark generic batch as @generate; changie batch is a stateful
//     release-prep transition, not a normal dev-loop generator.
//   - Move maintainer lookup out of Release.GetMaintainers and into the generic
//     Changie wrapper, or into a small GitHub helper used by that wrapper.
//   - Add one release-note @generate function per released component in this
//     release module, for example:
//   - EngineReleaseNotes
//   - GoSdkReleaseNotes
//   - PythonSdkReleaseNotes
//   - TypescriptSdkReleaseNotes
//   - ElixirSdkReleaseNotes
//   - PhpSdkReleaseNotes
//   - RustSdkReleaseNotes
//   - HelmReleaseNotes
//   - Each release-note generator should use Release.TargetVersion, know its own
//     Changie config path, no-op if its target .changes/<version>.md file already
//     exists, and otherwise call changie.batch(version) for that component.
//   - The release-note generators should set CHANGIE_ENGINE_VERSION and
//     CHANGIE_MAINTAINERS internally so humans do not have to run
//     get-maintainers or changie batch manually during release prep.
//   - Release.Publish should consume already-generated release-note files
//     directly from workspace paths instead of calling Changelog.LookupEntry.
//   - RELEASING.md should keep dagger generate as the release-prep entrypoint for
//     target-version files and per-component release notes, and should no longer
//     instruct humans to export CHANGIE_MAINTAINERS or run changie batch manually.
//   - Keep changie merge as-is until we decide whether release should wrap it too.
//
// +cache="session"
func (r *Release) GetMaintainers(
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
