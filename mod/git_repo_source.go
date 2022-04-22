package mod

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

type GitRepoSource struct {
	repo               string
	privateKeyFile     string
	privateKeyPassword string
	contents           *git.Repository
}

var gitRepoNameRegex = regexp.MustCompile(`([a-zA-Z0-9_.-]+(?::\d*)?/[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+)([a-zA-Z0-9/_.-]*)@?([0-9a-zA-Z.-]*)`)

func parseGitRepoName(repoName, versionConstraint string) (*Require, error) {
	repoMatches := gitRepoNameRegex.FindStringSubmatch(repoName)

	if len(repoMatches) < 4 {
		return nil, fmt.Errorf("issue when parsing github repo")
	}

	if github := tryParseGithubRepoName(repoMatches, versionConstraint); github != nil {
		return github, nil
	}

	return &Require{
		repo:              strings.TrimSuffix(repoMatches[1], ".git"),
		path:              repoMatches[2],
		version:           repoMatches[3],
		versionConstraint: versionConstraint,

		sourcePath: "",
		source: &GitRepoSource{
			repo:               repoMatches[1],
			privateKeyFile:     viper.GetString("private-key-file"),
			privateKeyPassword: viper.GetString("private-key-password"),
		},
	}, nil
}

func (repo *GitRepoSource) download(ctx context.Context, req *Require, tmpPath string) error {
	// clone to a tmp directory
	err := repo.clone(ctx, req, tmpPath)

	if err != nil {
		return fmt.Errorf("error downloading package %s: %w", req.version, err)
	}

	versions, err := repo.listTagVersions()
	if err != nil {
		return err
	}

	version, err := upgradeToLatestVersion(ctx, repo.repo, versions, req.version, req.versionConstraint)
	if err != nil {
		return err
	}

	req.version = version
	if err = repo.checkout(ctx, req.version); err != nil {
		return err
	}

	return nil
}

func (repo *GitRepoSource) clone(ctx context.Context, req *Require, dir string) error {
	gitRepo, ok := req.source.(*GitRepoSource)
	if !ok {
		return fmt.Errorf("require must be a git repo to be cloned")
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("error cleaning up tmp directory")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating tmp dir for cloning package")
	}

	o := git.CloneOptions{
		URL: fmt.Sprintf("https://%s", gitRepo.repo),
	}

	if repo.privateKeyFile != "" {
		publicKeys, err := ssh.NewPublicKeysFromFile("git", repo.privateKeyFile, repo.privateKeyPassword)
		if err != nil {
			return err
		}

		o.Auth = publicKeys
		o.URL = fmt.Sprintf("git@%s", strings.Replace(gitRepo.repo, "/", ":", 1))
	}

	r, err := git.PlainClone(dir, false, &o)
	if err != nil {
		return err
	}

	repo.contents = r

	if req.version == "" {
		versions, err := repo.listTagVersions()
		if err != nil {
			return err
		}

		latestTag, err := latestVersion(ctx, req.repo, versions, req.versionConstraint)
		if err != nil {
			return err
		}

		req.version = latestTag
	}

	if err := repo.checkout(ctx, req.version); err != nil {
		return err
	}

	return nil
}

func (repo *GitRepoSource) checkout(ctx context.Context, version string) error {
	if repo.contents == nil {
		return errors.New("clone must be called before checkout")
	}

	lg := log.Ctx(ctx)

	h, err := repo.contents.ResolveRevision(plumbing.Revision(version))
	if err != nil {
		return err
	}

	lg.Debug().Str("repository", repo.repo).Str("version", version).Str("commit", h.String()).Msg("checkout repo")

	w, err := repo.contents.Worktree()
	if err != nil {
		return err
	}

	err = w.Checkout(&git.CheckoutOptions{
		Hash:  *h,
		Force: true,
	})
	if err != nil {
		return err
	}

	return nil
}

func (repo *GitRepoSource) listTagVersions() ([]string, error) {
	if repo.contents == nil {
		return nil, errors.New("clone must be called before listTagVersions")
	}

	iter, err := repo.contents.Tags()
	if err != nil {
		return nil, err
	}

	var tags []string
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		tags = append(tags, ref.Name().Short())
		return nil
	})

	if err != nil {
		return nil, err
	}

	return tags, nil
}
