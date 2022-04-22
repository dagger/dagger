package mod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/spf13/viper"
	"go.dagger.io/dagger/pkg"
)

type GithubTagResponse struct {
	Name string `json:"name"`
}

type GithubRepoSource struct {
	owner         string
	repo          string
	ref           string
	authorization string
}

func tryParseGithubRepoName(repoMatches []string, versionConstraint string) *Require {
	if !strings.HasPrefix(repoMatches[1], "github.com/") ||
		viper.GetString("private-key-password") != "" ||
		repoMatches[3] == "" {
		return nil
	}

	module := strings.TrimSuffix(repoMatches[1], ".git")
	parts := strings.Split(module, "/")
	owner := parts[1]
	repo := parts[2]
	path := repoMatches[2]
	version := repoMatches[3]

	return &Require{
		repo:              module,
		path:              path,
		version:           version,
		versionConstraint: versionConstraint,

		sourcePath: "",
		source: &GithubRepoSource{
			owner:         owner,
			repo:          repo,
			ref:           version,
			authorization: viper.GetString("authorization-header"),
		},
	}
}

var daggerRepoNameRegex = regexp.MustCompile(pkg.UniverseModule + `([a-zA-Z0-9/_.-]*)@?([0-9a-zA-Z.-]*)`)

func parseDaggerRepoName(repoName, versionConstraint string) (*Require, error) {
	repoMatches := daggerRepoNameRegex.FindStringSubmatch(repoName)

	if len(repoMatches) < 3 {
		return nil, fmt.Errorf("issue when parsing dagger repo")
	}

	return &Require{
		repo:              pkg.UniverseModule,
		path:              repoMatches[1],
		version:           repoMatches[2],
		versionConstraint: versionConstraint,

		sourcePath: path.Join("pkg/universe.dagger.io", repoMatches[1]),
		source: &GithubRepoSource{
			owner:         "dagger",
			repo:          "dagger",
			authorization: viper.GetString("authorization-header"),
		},
	}, nil
}

func (repo GithubRepoSource) download(ctx context.Context, req *Require, tmpPath string) error {
	versions, err := repo.getVersions(ctx)
	if err != nil {
		return err
	}

	version, err := upgradeToLatestVersion(ctx, repo.repo, versions, req.version, req.versionConstraint)
	if err != nil {
		return err
	}

	req.version = version
	u := url.URL{
		Scheme: "https",
		Host:   "github.com",
		Path:   fmt.Sprintf("%s/%s/archive/%s.tar.gz", repo.owner, repo.repo, repo.ref),
	}

	return download(ctx, u.String(), tmpPath, repo.authorization, true)
}

func (repo GithubRepoSource) getVersions(ctx context.Context) ([]string, error) {
	n := 0
	result := make([]string, 0)

	for {
		n++
		ver := url.URL{
			Scheme:   "https",
			Host:     "api.github.com",
			Path:     fmt.Sprintf("repos/%s/%s/tags", repo.owner, repo.repo),
			RawQuery: fmt.Sprintf("per-page=100&page=%d", n),
		}

		req, err := http.NewRequestWithContext(ctx, "GET", ver.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to download tags for %s: %e", repo.repo, err)
		}

		if repo.authorization != "" {
			req.Header.Set("Authorization", repo.authorization)
		}

		response, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to download tags for %s: %e", repo.repo, err)
		}
		defer response.Body.Close()

		if response.StatusCode != 200 {
			return nil, fmt.Errorf("failed to download tags for %s: status code %d", repo.repo, response.StatusCode)
		}

		tags := make([]GithubTagResponse, 0)
		json.NewDecoder(response.Body).Decode(&tags)
		if len(tags) == 0 {
			break
		}

		for _, tag := range tags {
			result = append(result, tag.Name)
		}
	}

	return result, nil
}
