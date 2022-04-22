package mod

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/spf13/viper"
)

type HTTPRepoSource struct {
	repo          string
	authorization string
}

func parseHTTPRepoName(repoName, versionConstraint string) (*Require, error) {
	u, err := url.Parse(repoName)

	if err != nil {
		return nil, fmt.Errorf("issue when parsing HTTP repo")
	}

	clone, _ := url.Parse(repoName)
	clone.Path = path.Join(clone.Path, strings.TrimPrefix(clone.Fragment, "/"))
	clone.Fragment = ""

	return &Require{
		repo:              u.Host + u.Path,
		path:              "",
		version:           "",
		versionConstraint: versionConstraint,

		sourcePath: "",
		source: &HTTPRepoSource{
			repo:          clone.String(),
			authorization: viper.GetString("authorization-header"),
		},
	}, nil
}

func (repo HTTPRepoSource) download(ctx context.Context, req *Require, tmpPath string) error {
	return download(ctx, repo.repo, tmpPath, repo.authorization, false)
}
