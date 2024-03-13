package telemetry

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/denisbrodbeck/machineid"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/go-github/v59/github"
)

type Labels map[string]string

var defaultLabels Labels
var labelsOnce sync.Once

func LoadDefaultLabels(workdir, clientEngineVersion string) Labels {
	labelsOnce.Do(func() {
		defaultLabels = Labels{}.
			WithCILabels().
			WithClientLabels(clientEngineVersion).
			WithVCSLabels(workdir)
	})
	return defaultLabels
}

func (labels Labels) UserAgent() string {
	out := []string{}
	for k, v := range labels {
		out = append(out, fmt.Sprintf("%s:%s", k, v))
	}
	return strings.Join(out, ",")
}

func (labels Labels) WithEngineLabel(engineName string) Labels {
	labels["dagger.io/engine"] = engineName
	return labels
}

func (labels Labels) WithServerLabels(engineVersion, os, arch string, cacheEnabled bool) Labels {
	labels["dagger.io/server.os"] = os
	labels["dagger.io/server.arch"] = arch
	labels["dagger.io/server.version"] = engineVersion
	labels["dagger.io/server.cache.enabled"] = strconv.FormatBool(cacheEnabled)
	return labels
}

func (labels Labels) WithClientLabels(engineVersion string) Labels {
	labels["dagger.io/client.os"] = runtime.GOOS
	labels["dagger.io/client.arch"] = runtime.GOARCH
	labels["dagger.io/client.version"] = engineVersion

	machineID, err := machineid.ProtectedID("dagger")
	if err == nil {
		labels["dagger.io/client.machine_id"] = machineID
	}

	return labels
}

func (labels Labels) WithVCSLabels(workdir string) Labels {
	return labels.
		WithGitLabels(workdir).
		WithGitHubLabels().
		WithGitLabLabels().
		WithCircleCILabels()
}

func (labels Labels) WithGitLabels(workdir string) Labels {
	repo, err := git.PlainOpenWithOptions(workdir, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		if !errors.Is(err, git.ErrRepositoryNotExists) {
			slog.Warn("failed to open git repository", "err", err)
		}
		return labels
	}

	origin, err := repo.Remote("origin")
	if err == nil {
		urls := origin.Config().URLs
		if len(urls) == 0 {
			return labels
		}

		endpoint, err := parseGitURL(urls[0])
		if err != nil {
			slog.Warn("failed to parse git remote URL", "err", err)
			return labels
		}

		labels["dagger.io/git.remote"] = endpoint
	}

	head, err := repo.Head()
	if err != nil {
		slog.Warn("failed to get repo HEAD", "err", err)
		return labels
	}

	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		slog.Warn("failed to get commit object", "err", err)
		return labels
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
				slog.Warn("failed to fetch branch", "err", err)
			}
		}
	}

	title, _, _ := strings.Cut(commit.Message, "\n")

	labels["dagger.io/git.ref"] = commit.Hash.String()
	labels["dagger.io/git.author.name"] = commit.Author.Name
	labels["dagger.io/git.author.email"] = commit.Author.Email
	labels["dagger.io/git.committer.name"] = commit.Committer.Name
	labels["dagger.io/git.committer.email"] = commit.Committer.Email
	labels["dagger.io/git.title"] = title // first line from commit message

	// check if ref is a tag or branch
	refs, err := repo.References()
	if err != nil {
		slog.Warn("failed to get refs", "err", err)
		return labels
	}

	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Hash() == commit.Hash {
			if ref.Name().IsTag() {
				labels["dagger.io/git.tag"] = ref.Name().Short()
			}
			if ref.Name().IsBranch() {
				labels["dagger.io/git.branch"] = ref.Name().Short()
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("failed to get refs", "err", err)
		return labels
	}

	return labels
}

func (labels Labels) WithGitHubLabels() Labels {
	if os.Getenv("GITHUB_ACTIONS") != "true" { //nolint:goconst
		return labels
	}

	eventType := os.Getenv("GITHUB_EVENT_NAME")

	labels["dagger.io/vcs.event.type"] = eventType
	labels["dagger.io/vcs.job.name"] = os.Getenv("GITHUB_JOB")
	labels["dagger.io/vcs.triggerer.login"] = os.Getenv("GITHUB_ACTOR")
	labels["dagger.io/vcs.workflow.name"] = os.Getenv("GITHUB_WORKFLOW")

	eventPath := os.Getenv("GITHUB_EVENT_PATH")
	if eventPath == "" {
		return labels
	}

	payload, err := os.ReadFile(eventPath)
	if err != nil {
		slog.Warn("failed to read $GITHUB_EVENT_PATH", "err", err)
		return labels
	}

	event, err := github.ParseWebHook(eventType, payload)
	if err != nil {
		slog.Warn("failed to parse $GITHUB_EVENT_PATH", "err", err)
		return labels
	}

	if event, ok := event.(interface {
		GetAction() string
	}); ok && event.GetAction() != "" {
		labels["github.com/event.action"] = event.GetAction()
	}

	if repo, ok := getRepoIsh(event); ok {
		labels["dagger.io/vcs.repo.full_name"] = repo.GetFullName()
		labels["dagger.io/vcs.repo.url"] = repo.GetHTMLURL()
	}

	if event, ok := event.(interface {
		GetPullRequest() *github.PullRequest
	}); ok && event.GetPullRequest() != nil {
		pr := event.GetPullRequest()

		labels["dagger.io/vcs.change.number"] = fmt.Sprintf("%d", pr.GetNumber())
		labels["dagger.io/vcs.change.title"] = pr.GetTitle()
		labels["dagger.io/vcs.change.url"] = pr.GetHTMLURL()
		labels["dagger.io/vcs.change.branch"] = pr.GetHead().GetRef()
		labels["dagger.io/vcs.change.head_sha"] = pr.GetHead().GetSHA()
		labels["dagger.io/vcs.change.label"] = pr.GetHead().GetLabel()
	}

	return labels
}

func (labels Labels) WithGitLabLabels() Labels {
	if os.Getenv("GITLAB_CI") != "true" { //nolint:goconst
		return labels
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

	labels["dagger.io/vcs.repo.url"] = os.Getenv("CI_PROJECT_URL")
	labels["dagger.io/vcs.repo.full_name"] = os.Getenv("CI_PROJECT_PATH")
	labels["dagger.io/vcs.change.branch"] = branchName
	labels["dagger.io/vcs.change.title"] = changeTitle
	labels["dagger.io/vcs.change.head_sha"] = os.Getenv("CI_COMMIT_SHA")
	labels["dagger.io/vcs.triggerer.login"] = os.Getenv("GITLAB_USER_LOGIN")
	labels["dagger.io/vcs.event.type"] = os.Getenv("CI_PIPELINE_SOURCE")
	labels["dagger.io/vcs.job.name"] = os.Getenv("CI_JOB_NAME")
	labels["dagger.io/vcs.workflow.name"] = os.Getenv("CI_PIPELINE_NAME")
	labels["dagger.io/vcs.change.label"] = os.Getenv("CI_MERGE_REQUEST_LABELS")
	labels["gitlab.com/job.id"] = os.Getenv("CI_JOB_ID")
	labels["gitlab.com/triggerer.id"] = os.Getenv("GITLAB_USER_ID")
	labels["gitlab.com/triggerer.email"] = os.Getenv("GITLAB_USER_EMAIL")
	labels["gitlab.com/triggerer.name"] = os.Getenv("GITLAB_USER_NAME")

	projectURL := os.Getenv("CI_MERGE_REQUEST_PROJECT_URL")
	mrIID := os.Getenv("CI_MERGE_REQUEST_IID")
	if projectURL != "" && mrIID != "" {
		labels["dagger.io/vcs.change.url"] = fmt.Sprintf("%s/-/merge_requests/%s", projectURL, mrIID)
		labels["dagger.io/vcs.change.number"] = mrIID
	}

	return labels
}

func (labels Labels) WithCircleCILabels() Labels {
	if os.Getenv("CIRCLECI") != "true" { //nolint:goconst
		return labels
	}

	labels["dagger.io/vcs.change.branch"] = os.Getenv("CIRCLE_BRANCH")
	labels["dagger.io/vcs.change.head_sha"] = os.Getenv("CIRCLE_SHA1")
	labels["dagger.io/vcs.job.name"] = os.Getenv("CIRCLE_JOB")

	firstEnvLabel := func(label string, envVar []string) {
		for _, envVar := range envVar {
			triggererLogin := os.Getenv(envVar)
			if triggererLogin != "" {
				labels[label] = triggererLogin
				return
			}
		}
	}

	// environment variables beginning with "CIRCLE_PIPELINE_"  are set in `.circle-ci` pipeline config
	pipelineNumber := []string{
		"CIRCLE_PIPELINE_NUMBER",
	}
	firstEnvLabel("dagger.io/vcs.change.number", pipelineNumber)

	triggererLabels := []string{
		"CIRCLE_USERNAME",               // all, but account needs to exist on circleCI
		"CIRCLE_PROJECT_USERNAME",       // github / bitbucket
		"CIRCLE_PIPELINE_TRIGGER_LOGIN", // gitlab
	}
	firstEnvLabel("dagger.io/vcs.triggerer.login", triggererLabels)

	repoNameLabels := []string{
		"CIRCLE_PROJECT_REPONAME",        // github / bitbucket
		"CIRCLE_PIPELINE_REPO_FULL_NAME", // gitlab
	}
	firstEnvLabel("dagger.io/vcs.repo.full_name", repoNameLabels)

	vcsChangeURL := []string{
		"CIRCLE_PULL_REQUEST", // github / bitbucket, only from forks
	}
	firstEnvLabel("dagger.io/vcs.change.url", vcsChangeURL)

	pipelineRepoURL := os.Getenv("CIRCLE_PIPELINE_REPO_URL")
	repositoryURL := os.Getenv("CIRCLE_REPOSITORY_URL")
	if pipelineRepoURL != "" { // gitlab
		labels["dagger.io/vcs.repo.url"] = pipelineRepoURL
	} else if repositoryURL != "" { // github / bitbucket (returns the remote)
		transformedURL := repositoryURL
		if strings.Contains(repositoryURL, "@") { // from ssh to https
			re := regexp.MustCompile(`git@(.*?):(.*?)/(.*)\.git`)
			transformedURL = re.ReplaceAllString(repositoryURL, "https://$1/$2/$3")
		}
		labels["dagger.io/vcs.repo.url"] = transformedURL
	}

	return labels
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

func (labels Labels) WithCILabels() Labels {
	isCIValue := "false"
	if isCI() {
		isCIValue = "true"
	}
	labels["dagger.io/ci"] = isCIValue

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
		labels["dagger.io/ci.vendor"] = vendor
	}

	return labels
}

func isCI() bool {
	return os.Getenv("CI") != "" || // GitHub Actions, Travis CI, CircleCI, Cirrus CI, GitLab CI, AppVeyor, CodeShip, dsari
		os.Getenv("BUILD_NUMBER") != "" || // Jenkins, TeamCity
		os.Getenv("RUN_ID") != "" // TaskCluster, dsari
}

func (labels Labels) WithAnonymousGitLabels(workdir string) Labels {
	labels = labels.WithGitLabels(workdir)

	for name, value := range labels {
		if name == "dagger.io/git.author.email" {
			labels[name] = fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
		}
		if name == "dagger.io/git.remote" {
			labels[name] = base64.StdEncoding.EncodeToString([]byte(value))
		}
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

type LabelFlag struct {
	Labels
}

func NewLabelFlag() LabelFlag {
	return LabelFlag{Labels: Labels{}}
}

func (flag LabelFlag) Set(s string) error {
	name, val, ok := strings.Cut(s, ":")
	if !ok {
		return errors.New("invalid label format (must be name:value)")
	}
	if flag.Labels == nil {
		flag.Labels = Labels{}
	}
	flag.Labels[name] = val
	return nil
}

func (flag LabelFlag) Type() string {
	return "labels"
}

func (flag LabelFlag) String() string {
	return flag.Labels.UserAgent() // it's fine
}
