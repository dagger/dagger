package pipeline

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/denisbrodbeck/machineid"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/go-github/v50/github"
	"github.com/sirupsen/logrus"
)

type Label struct {
	Name  string `json:"name" field:"true" doc:"Label name."`
	Value string `json:"value" field:"true" doc:"Label value."`
}

func (Label) TypeName() string {
	return "PipelineLabel"
}

func (Label) TypeDescription() string {
	return "Key value object that represents a pipeline label."
}

type Labels []Label

func EngineLabel(engineName string) Label {
	return Label{
		Name:  "dagger.io/engine",
		Value: engineName,
	}
}

func LoadServerLabels(engineVersion, os, arch string, cacheEnabled bool) Labels {
	labels := []Label{
		{
			Name:  "dagger.io/server.os",
			Value: os,
		},
		{
			Name:  "dagger.io/server.arch",
			Value: arch,
		},
		{
			Name:  "dagger.io/server.version",
			Value: engineVersion,
		},

		{
			Name:  "dagger.io/server.cache.enabled",
			Value: strconv.FormatBool(cacheEnabled),
		},
	}

	return labels
}

func LoadClientLabels(engineVersion string) Labels {
	labels := []Label{
		{
			Name:  "dagger.io/client.os",
			Value: runtime.GOOS,
		},
		{
			Name:  "dagger.io/client.arch",
			Value: runtime.GOARCH,
		},
		{
			Name:  "dagger.io/client.version",
			Value: engineVersion,
		},
	}

	machineID, err := machineid.ProtectedID("dagger")
	if err == nil {
		labels = append(labels, Label{
			Name:  "dagger.io/client.machine_id",
			Value: machineID,
		})
	}

	return labels
}

func LoadVCSLabels(workdir string) Labels {
	labels := []Label{}

	if gitLabels, err := LoadGitLabels(workdir); err == nil {
		labels = append(labels, gitLabels...)
	} else {
		logrus.Warnf("failed to collect git labels: %s", err)
	}

	if githubLabels, err := LoadGitHubLabels(); err == nil {
		labels = append(labels, githubLabels...)
	} else {
		logrus.Warnf("failed to collect GitHub labels: %s", err)
	}

	if gitlabLabels, err := LoadGitLabLabels(); err == nil {
		labels = append(labels, gitlabLabels...)
	} else {
		logrus.Warnf("failed to collect Gitlab labels: %s", err)
	}

	if CircleCILabels, err := LoadCircleCILabels(); err == nil {
		labels = append(labels, CircleCILabels...)
	} else {
		logrus.Warnf("failed to collect CircleCI labels: %s", err)
	}

	return labels
}

// Define a type for functions that fetch a branch commit
type fetchFunc func(repo *git.Repository, branch string) (*object.Commit, error)

// Function to fetch from the origin remote
func fetchFromOrigin(repo *git.Repository, branch string) (*object.Commit, error) {
	// Fetch from the origin remote
	cmd := exec.Command("git", "fetch", "--depth", "1", "origin", branch)
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("error fetching branch from origin: %w", err)
	}

	// Get the reference of the fetched branch
	refName := plumbing.ReferenceName(fmt.Sprintf("refs/remotes/origin/%s", branch))
	ref, err := repo.Reference(refName, true)
	if err != nil {
		return nil, fmt.Errorf("error getting reference: %w", err)
	}

	// Get the commit object of the fetched branch
	branchCommit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("error getting commit: %w", err)
	}

	return branchCommit, nil
}

// Function to fetch from the fork remote
// GitHub forks are not added as remotes by default, so we need to guess the fork URL
// This is a heuristic approach, as the fork might not exist from the information we have
func fetchFromFork(repo *git.Repository, branch string) (*object.Commit, error) {
	// Get the username of the person who initiated the workflow run
	username := os.Getenv("GITHUB_ACTOR")

	// Get the repository name (owner/repo)
	repository := os.Getenv("GITHUB_REPOSITORY")
	parts := strings.Split(repository, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid repository format: %s", repository)
	}

	// Get the server URL: "https://github.com/" in general,
	// but can be different for GitHub Enterprise
	serverURL := os.Getenv("GITHUB_SERVER_URL")

	forkURL := fmt.Sprintf("%s/%s/%s", serverURL, username, parts[1])

	cmd := exec.Command("git", "remote", "add", "fork", forkURL)
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("error adding fork as remote: %w", err)
	}

	cmd = exec.Command("git", "fetch", "--depth", "1", "fork", branch)
	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("error fetching branch from fork: %w", err)
	}

	// Get the reference of the fetched branch
	refName := plumbing.ReferenceName(fmt.Sprintf("refs/remotes/fork/%s", branch))
	ref, err := repo.Reference(refName, true)
	if err != nil {
		return nil, fmt.Errorf("error getting reference: %w", err)
	}

	// Get the commit object of the fetched branch
	branchCommit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("error getting commit: %w", err)
	}

	return branchCommit, nil
}

func LoadGitLabels(workdir string) (Labels, error) {
	repo, err := git.PlainOpenWithOptions(workdir, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			return nil, nil
		}

		return nil, err
	}

	labels := []Label{}

	origin, err := repo.Remote("origin")
	if err == nil {
		urls := origin.Config().URLs
		if len(urls) == 0 {
			return []Label{}, nil
		}

		endpoint, err := parseGitURL(urls[0])
		if err != nil {
			return nil, err
		}

		labels = append(labels, Label{
			Name:  "dagger.io/git.remote",
			Value: endpoint,
		})
	}

	head, err := repo.Head()
	if err != nil {
		return nil, err
	}

	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, err
	}

	// Checks if the commit is a merge commit in the context of pull request
	// Only GitHub needs to be handled, as GitLab doesn't detach the head in MR context
	if os.Getenv("GITHUB_EVENT_NAME") == "pull_request" && commit.NumParents() > 1 {
		// Get the pull request's origin branch name
		branch := os.Getenv("GITHUB_HEAD_REF")

		// List of remotes function to try fetching from: origin and fork
		fetchFuncs := []fetchFunc{fetchFromOrigin, fetchFromFork}

		var branchCommit *object.Commit
		var err error

		for _, fetch := range fetchFuncs {
			branchCommit, err = fetch(repo, branch)
			if err == nil {
				commit = branchCommit
				break
			} else {
				fmt.Fprintf(os.Stderr, "Error fetching branch: %s", err.Error())
			}
		}
	}

	title, _, _ := strings.Cut(commit.Message, "\n")

	labels = append(labels,
		Label{
			Name:  "dagger.io/git.ref",
			Value: commit.Hash.String(),
		},
		Label{
			Name:  "dagger.io/git.author.name",
			Value: commit.Author.Name,
		},
		Label{
			Name:  "dagger.io/git.author.email",
			Value: commit.Author.Email,
		},
		Label{
			Name:  "dagger.io/git.committer.name",
			Value: commit.Committer.Name,
		},
		Label{
			Name:  "dagger.io/git.committer.email",
			Value: commit.Committer.Email,
		},
		Label{
			Name:  "dagger.io/git.title",
			Value: title, // first line from commit message
		},
	)

	// check if ref is a tag or branch
	refs, _ := repo.References()
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Hash() == commit.Hash {
			if ref.Name().IsTag() {
				labels = append(labels, Label{
					Name:  "dagger.io/git.tag",
					Value: ref.Name().Short(),
				})
			}
			if ref.Name().IsBranch() {
				labels = append(labels, Label{
					Name:  "dagger.io/git.branch",
					Value: ref.Name().Short(),
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return labels, nil
}

func LoadCircleCILabels() (Labels, error) {
	if os.Getenv("CIRCLECI") != "true" { //nolint:goconst
		return []Label{}, nil
	}

	labels := []Label{
		{
			Name:  "dagger.io/vcs.change.branch",
			Value: os.Getenv("CIRCLE_BRANCH"),
		},
		{
			Name:  "dagger.io/vcs.change.head_sha",
			Value: os.Getenv("CIRCLE_SHA1"),
		},
		{
			Name:  "dagger.io/vcs.job.name",
			Value: os.Getenv("CIRCLE_JOB"),
		},
	}

	firstEnvLabel := func(label string, envVar []string) (Label, bool) {
		for _, envVar := range envVar {
			triggererLogin := os.Getenv(envVar)
			if triggererLogin != "" {
				return Label{
					Name:  label,
					Value: triggererLogin,
				}, true
			}
		}
		return Label{}, false
	}

	// environment variables beginning with "CIRCLE_PIPELINE_"  are set in `.circle-ci` pipeline config
	pipelineNumber := []string{
		"CIRCLE_PIPELINE_NUMBER",
	}
	if label, found := firstEnvLabel("dagger.io/vcs.change.number", pipelineNumber); found {
		labels = append(labels, label)
	}

	triggererLabels := []string{
		"CIRCLE_USERNAME",               // all, but account needs to exist on circleCI
		"CIRCLE_PROJECT_USERNAME",       // github / bitbucket
		"CIRCLE_PIPELINE_TRIGGER_LOGIN", // gitlab
	}
	if label, found := firstEnvLabel("dagger.io/vcs.triggerer.login", triggererLabels); found {
		labels = append(labels, label)
	}

	repoNameLabels := []string{
		"CIRCLE_PROJECT_REPONAME",        // github / bitbucket
		"CIRCLE_PIPELINE_REPO_FULL_NAME", // gitlab
	}
	if label, found := firstEnvLabel("dagger.io/vcs.repo.full_name", repoNameLabels); found {
		labels = append(labels, label)
	}

	vcsChangeURL := []string{
		"CIRCLE_PULL_REQUEST", // github / bitbucket, only from forks
	}
	if label, found := firstEnvLabel("dagger.io/vcs.change.url", vcsChangeURL); found {
		labels = append(labels, label)
	}

	pipelineRepoURL := os.Getenv("CIRCLE_PIPELINE_REPO_URL")
	repositoryURL := os.Getenv("CIRCLE_REPOSITORY_URL")
	if pipelineRepoURL != "" { // gitlab
		labels = append(labels, Label{
			Name:  "dagger.io/vcs.repo.url",
			Value: pipelineRepoURL,
		})
	} else if repositoryURL != "" { // github / bitbucket (returns the remote)
		transformedURL := repositoryURL
		if strings.Contains(repositoryURL, "@") { // from ssh to https
			re := regexp.MustCompile(`git@(.*?):(.*?)/(.*)\.git`)
			transformedURL = re.ReplaceAllString(repositoryURL, "https://$1/$2/$3")
		}
		labels = append(labels, Label{
			Name:  "dagger.io/vcs.repo.url",
			Value: transformedURL,
		})
	}

	return labels, nil
}

func LoadGitLabLabels() (Labels, error) {
	if os.Getenv("GITLAB_CI") != "true" { //nolint:goconst
		return []Label{}, nil
	}

	branchName := os.Getenv("CI_MERGE_REQUEST_SOURCE_BRANCH_NAME")
	if branchName == "" {
		// for a branch job, CI_MERGE_REQUEST_SOURCE_BRANCH_NAME is empty
		branchName = os.Getenv("CI_COMMIT_BRANCH")
	}

	changeTitle := os.Getenv("CI_MERGE_REQUEST_TITLE")
	if changeTitle == "" {
		changeTitle = os.Getenv("CI_COMMIT_TITLE")
	}

	labels := []Label{
		{
			Name:  "dagger.io/vcs.repo.url",
			Value: os.Getenv("CI_PROJECT_URL"),
		},
		{
			Name:  "dagger.io/vcs.repo.full_name",
			Value: os.Getenv("CI_PROJECT_PATH"),
		},
		{
			Name:  "dagger.io/vcs.change.branch",
			Value: branchName,
		},
		{
			Name:  "dagger.io/vcs.change.title",
			Value: changeTitle,
		},
		{
			Name:  "dagger.io/vcs.change.head_sha",
			Value: os.Getenv("CI_COMMIT_SHA"),
		},
		{
			Name:  "dagger.io/vcs.triggerer.login",
			Value: os.Getenv("GITLAB_USER_LOGIN"),
		},
		{
			Name:  "dagger.io/vcs.event.type",
			Value: os.Getenv("CI_PIPELINE_SOURCE"),
		},
		{
			Name:  "dagger.io/vcs.job.name",
			Value: os.Getenv("CI_JOB_NAME"),
		},
		{
			Name:  "dagger.io/vcs.workflow.name",
			Value: os.Getenv("CI_PIPELINE_NAME"),
		},
		{
			Name:  "dagger.io/vcs.change.label",
			Value: os.Getenv("CI_MERGE_REQUEST_LABELS"),
		},
		{
			Name:  "gitlab.com/job.id",
			Value: os.Getenv("CI_JOB_ID"),
		},
		{
			Name:  "gitlab.com/triggerer.id",
			Value: os.Getenv("GITLAB_USER_ID"),
		},
		{
			Name:  "gitlab.com/triggerer.email",
			Value: os.Getenv("GITLAB_USER_EMAIL"),
		},
		{
			Name:  "gitlab.com/triggerer.name",
			Value: os.Getenv("GITLAB_USER_NAME"),
		},
	}

	projectURL := os.Getenv("CI_MERGE_REQUEST_PROJECT_URL")
	mrIID := os.Getenv("CI_MERGE_REQUEST_IID")
	if projectURL != "" && mrIID != "" {
		labels = append(labels, Label{
			Name:  "dagger.io/vcs.change.url",
			Value: fmt.Sprintf("%s/-/merge_requests/%s", projectURL, mrIID),
		})

		labels = append(labels, Label{
			Name:  "dagger.io/vcs.change.number",
			Value: mrIID,
		})
	}

	return labels, nil
}

func LoadGitHubLabels() (Labels, error) {
	if os.Getenv("GITHUB_ACTIONS") != "true" { //nolint:goconst
		return []Label{}, nil
	}

	eventType := os.Getenv("GITHUB_EVENT_NAME")

	labels := []Label{
		{
			Name:  "dagger.io/vcs.event.type",
			Value: eventType,
		},
		{
			Name:  "dagger.io/vcs.job.name",
			Value: os.Getenv("GITHUB_JOB"),
		},
		{
			Name:  "dagger.io/vcs.triggerer.login",
			Value: os.Getenv("GITHUB_ACTOR"),
		},
		{
			Name:  "dagger.io/vcs.workflow.name",
			Value: os.Getenv("GITHUB_WORKFLOW"),
		},
	}

	eventPath := os.Getenv("GITHUB_EVENT_PATH")
	if eventPath != "" {
		payload, err := os.ReadFile(eventPath)
		if err != nil {
			return nil, fmt.Errorf("read $GITHUB_EVENT_PATH: %w", err)
		}

		event, err := github.ParseWebHook(eventType, payload)
		if err != nil {
			return nil, fmt.Errorf("unmarshal $GITHUB_EVENT_PATH: %w", err)
		}

		if event, ok := event.(interface {
			GetAction() string
		}); ok && event.GetAction() != "" {
			labels = append(labels, Label{
				Name:  "github.com/event.action",
				Value: event.GetAction(),
			})
		}

		if repo, ok := getRepoIsh(event); ok {
			labels = append(labels, Label{
				Name:  "dagger.io/vcs.repo.full_name",
				Value: repo.GetFullName(),
			})

			labels = append(labels, Label{
				Name:  "dagger.io/vcs.repo.url",
				Value: repo.GetHTMLURL(),
			})
		}

		if event, ok := event.(interface {
			GetPullRequest() *github.PullRequest
		}); ok && event.GetPullRequest() != nil {
			pr := event.GetPullRequest()

			labels = append(labels, Label{
				Name:  "dagger.io/vcs.change.number",
				Value: fmt.Sprintf("%d", pr.GetNumber()),
			})

			labels = append(labels, Label{
				Name:  "dagger.io/vcs.change.title",
				Value: pr.GetTitle(),
			})

			labels = append(labels, Label{
				Name:  "dagger.io/vcs.change.url",
				Value: pr.GetHTMLURL(),
			})

			labels = append(labels, Label{
				Name:  "dagger.io/vcs.change.branch",
				Value: pr.GetHead().GetRef(),
			})

			labels = append(labels, Label{
				Name:  "dagger.io/vcs.change.head_sha",
				Value: pr.GetHead().GetSHA(),
			})

			labels = append(labels, Label{
				Name:  "dagger.io/vcs.change.label",
				Value: pr.GetHead().GetLabel(),
			})
		}
	}

	return labels, nil
}

type repoIsh interface {
	GetFullName() string
	GetHTMLURL() string
}

func getRepoIsh(event any) (repoIsh, bool) {
	switch x := event.(type) {
	case *github.PushEvent:
		// push event repositories aren't quite a *github.Repository for silly
		// legacy reasons
		return x.GetRepo(), true
	case interface{ GetRepo() *github.Repository }:
		return x.GetRepo(), true
	default:
		return nil, false
	}
}

func (labels *Labels) Type() string {
	return "labels"
}

func (labels *Labels) Set(s string) error {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return fmt.Errorf("bad format: '%s' (expected name:value)", s)
	}

	labels.Add(parts[0], parts[1])

	return nil
}

func (labels *Labels) Add(name string, value string) {
	*labels = append(*labels, Label{Name: name, Value: value})
}

func (labels *Labels) String() string {
	var ls string
	for _, l := range *labels {
		ls += fmt.Sprintf("%s:%s,", l.Name, l.Value)
	}
	return ls
}

func (labels *Labels) AppendCILabel() *Labels {
	isCIValue := "false"
	if isCI() {
		isCIValue = "true"
	}
	labels.Add("dagger.io/ci", isCIValue)

	vendor := ""
	switch {
	case os.Getenv("GITHUB_ACTIONS") == "true": //nolint:goconst
		vendor = "GitHub"
	case os.Getenv("CIRCLECI") == "true": //nolint:goconst
		vendor = "CircleCI"
	case os.Getenv("GITLAB_CI") == "true": //nolint:goconst
		vendor = "GitLab"
	}
	if vendor != "" {
		labels.Add("dagger.io/ci.vendor", vendor)
	}

	return labels
}

func isCI() bool {
	return os.Getenv("CI") != "" || // GitHub Actions, Travis CI, CircleCI, Cirrus CI, GitLab CI, AppVeyor, CodeShip, dsari
		os.Getenv("BUILD_NUMBER") != "" || // Jenkins, TeamCity
		os.Getenv("RUN_ID") != "" // TaskCluster, dsari
}

func (labels *Labels) AppendAnonymousGitLabels(workdir string) *Labels {
	gitLabels, err := LoadGitLabels(workdir)
	if err != nil {
		return labels
	}

	for _, gitLabel := range gitLabels {
		if gitLabel.Name == "dagger.io/git.author.email" {
			labels.Add(gitLabel.Name, fmt.Sprintf("%x", sha256.Sum256([]byte(gitLabel.Value))))
		}
		if gitLabel.Name == "dagger.io/git.remote" {
			labels.Add(gitLabel.Name, base64.StdEncoding.EncodeToString([]byte(gitLabel.Value)))
		}
	}

	return labels
}
