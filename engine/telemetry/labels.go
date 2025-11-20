package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
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

	"github.com/dagger/dagger/engine/slog"
)

type Labels struct {
	src  map[string]string
	eg   EnvGetter
	ghep []byte
}

type EnvGetter interface {
	Getenv(key string) string
}

type OSEnvGetter struct{}

func (e OSEnvGetter) Getenv(key string) string {
	return os.Getenv(key)
}

func NewLabels(labels map[string]string, eg EnvGetter, ghEventPayload []byte) Labels {
	if labels == nil {
		labels = map[string]string{}
	}
	if eg == nil {
		eg = OSEnvGetter{}
	}
	return Labels{src: labels, eg: eg, ghep: ghEventPayload}
}

var (
	defaultLabels Labels
	labelsOnce    sync.Once
)

func LoadDefaultLabels(workdir, clientEngineVersion string) Labels {
	labelsOnce.Do(func() {
		defaultLabels = NewLabels(nil, nil, nil).
			WithCILabels().
			WithClientLabels(clientEngineVersion).
			WithVCSLabels(workdir)
	})
	return defaultLabels
}

func (labels *Labels) UnmarshalJSON(dt []byte) error {
	// HACK: this custom Unmarshaller allows for old clients to pass labels in
	// the legacy pre-v0.11 format without immediately erroring out (we can
	// easily convert them) ...but we should eventually remove this.

	var err error

	current := map[string]string{}
	if err = json.Unmarshal(dt, &current); err == nil {
		if labels.src == nil {
			labels.src = current
		} else {
			maps.Copy(labels.src, current)
		}
		return nil
	}

	legacy := []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{}
	if err = json.Unmarshal(dt, &legacy); err == nil {
		if labels.src == nil {
			labels.src = map[string]string{}
		}
		for _, label := range legacy {
			labels.src[label.Name] = label.Value
		}
		return nil
	}

	return err
}

func (labels Labels) Get(key string) (string, bool) {
	v, ok := labels.src[key]
	return v, ok
}

func (labels Labels) AsMap() map[string]string {
	return labels.src
}

func (labels Labels) UserAgent() string {
	out := []string{}
	for k, v := range labels.src {
		out = append(out, fmt.Sprintf("%s:%s", k, v))
	}
	return strings.Join(out, ",")
}

func (labels Labels) WithEngineLabel(engineName string) Labels {
	labels.src["dagger.io/engine"] = engineName
	return labels
}

func (labels Labels) WithServerLabels(engineVersion, os, arch string, cacheEnabled bool) Labels {
	labels.src["dagger.io/server.os"] = os
	labels.src["dagger.io/server.arch"] = arch
	labels.src["dagger.io/server.version"] = engineVersion
	labels.src["dagger.io/server.cache.enabled"] = strconv.FormatBool(cacheEnabled)
	return labels
}

func (labels Labels) WithClientLabels(engineVersion string) Labels {
	labels.src["dagger.io/client.os"] = runtime.GOOS
	labels.src["dagger.io/client.arch"] = runtime.GOARCH
	labels.src["dagger.io/client.version"] = engineVersion

	machineID, err := machineid.ProtectedID("dagger")
	if err == nil {
		labels.src["dagger.io/client.machine_id"] = machineID
	}

	return labels
}

func (labels Labels) WithVCSLabels(workdir string) Labels {
	return labels.
		WithGitLabels(workdir).
		WithGitHubLabels().
		WithGitLabLabels().
		WithCircleCILabels().
		WithJenkinsLabels().
		WithHarnessLabels()
}

func (labels Labels) WithGitLabels(workdir string) Labels {
	repo, err := git.PlainOpenWithOptions(workdir, &git.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
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

		labels.src["dagger.io/git.remote"] = endpoint
	}

	head, err := repo.Head()
	if err != nil {
		slog.Debug("failed to get repo HEAD", "err", err)
		return labels
	}

	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		slog.Warn("failed to get commit object", "err", err)
		return labels
	}

	// Checks if the commit is a merge commit in the context of pull request
	// For now, only GitHub needs to be handled, as GitLab doesn't create this
	// weird /merge ref in a merge-request
	if ref, ok := os.LookupEnv("GITHUB_REF"); ok {
		if strings.HasPrefix(ref, "refs/pull/") {
			ref = strings.Replace(ref, "/merge", "/head", 1)
			refCommit, err := fetchRef(repo, workdir, "origin", ref)
			if err != nil {
				slog.Warn("failed to fetch branch", "err", err)
			} else {
				commit = refCommit
			}
		}
	}

	title, _, _ := strings.Cut(commit.Message, "\n")

	labels.src["dagger.io/git.ref"] = commit.Hash.String()
	labels.src["dagger.io/git.author.name"] = commit.Author.Name
	labels.src["dagger.io/git.author.email"] = commit.Author.Email
	labels.src["dagger.io/git.committer.name"] = commit.Committer.Name
	labels.src["dagger.io/git.committer.email"] = commit.Committer.Email
	labels.src["dagger.io/git.title"] = title // first line from commit message

	// check if ref is a tag or branch
	refs, err := repo.References()
	if err != nil {
		slog.Warn("failed to get refs", "err", err)
		return labels
	}

	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Hash() == commit.Hash {
			if ref.Name().IsTag() {
				labels.src["dagger.io/git.tag"] = ref.Name().Short()
			}
			if ref.Name().IsBranch() {
				labels.src["dagger.io/git.branch"] = ref.Name().Short()
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
	if labels.eg.Getenv("GITHUB_ACTIONS") != "true" { //nolint:goconst
		return labels
	}

	eventType := labels.eg.Getenv("GITHUB_EVENT_NAME")

	gitLabels := Labels{src: map[string]string{}}

	labels.src["dagger.io/vcs.event.type"] = eventType
	labels.src["dagger.io/vcs.job.name"] = labels.eg.Getenv("GITHUB_JOB")
	labels.src["dagger.io/vcs.triggerer.login"] = labels.eg.Getenv("GITHUB_ACTOR")
	labels.src["dagger.io/vcs.workflow.name"] = labels.eg.Getenv("GITHUB_WORKFLOW")

	payload := labels.ghep
	if payload == nil {
		eventPath := labels.eg.Getenv("GITHUB_EVENT_PATH")
		if eventPath == "" {
			return labels
		}
		var err error
		payload, err = os.ReadFile(eventPath)
		if err != nil {
			slog.Warn("failed to read $GITHUB_EVENT_PATH", "err", err)
			return labels
		}
	}

	event, err := github.ParseWebHook(eventType, payload)
	if err != nil {
		slog.Warn("failed to parse github payload", "err", err)
		return labels
	}

	if event, ok := event.(interface {
		GetAction() string
	}); ok && event.GetAction() != "" {
		labels.src["github.com/event.action"] = event.GetAction()
	}

	if repo, ok := getRepoIsh(event); ok {
		labels.src["dagger.io/vcs.repo.full_name"] = repo.GetFullName()
		labels.src["dagger.io/vcs.repo.url"] = repo.GetHTMLURL()

		endpoint, err := parseGitURL(repo.GetCloneURL())
		if err != nil {
			slog.Warn("failed to parse git remote URL", "err", err)
			return labels
		}
		gitLabels.src["dagger.io/git.remote"] = endpoint
	}

	// pr-like events
	if event, ok := event.(interface {
		GetPullRequest() *github.PullRequest
	}); ok && event.GetPullRequest() != nil {
		pr := event.GetPullRequest()

		labels.src["dagger.io/vcs.change.number"] = fmt.Sprintf("%d", pr.GetNumber())
		labels.src["dagger.io/vcs.change.title"] = pr.GetTitle()
		labels.src["dagger.io/vcs.change.url"] = pr.GetHTMLURL()
		labels.src["dagger.io/vcs.change.branch"] = pr.GetHead().GetRef()
		labels.src["dagger.io/vcs.change.head_sha"] = pr.GetHead().GetSHA()
		labels.src["dagger.io/vcs.change.label"] = pr.GetHead().GetLabel()

		gitLabels.src["dagger.io/git.ref"] = pr.GetHead().GetSHA()
		gitLabels.src["dagger.io/git.branch"] = pr.GetHead().GetRef() // all prs are branches
	}

	// push-like events
	if event, ok := event.(interface {
		GetRef() string
		GetHeadCommit() *github.HeadCommit
	}); ok {
		headCommit := event.GetHeadCommit()
		gitLabels.src["dagger.io/git.ref"] = headCommit.GetID()
		gitLabels.src["dagger.io/git.author.name"] = headCommit.GetAuthor().GetName()
		gitLabels.src["dagger.io/git.author.email"] = headCommit.GetAuthor().GetEmail()
		gitLabels.src["dagger.io/git.committer.name"] = headCommit.GetCommitter().GetName()
		gitLabels.src["dagger.io/git.committer.email"] = headCommit.GetCommitter().GetEmail()

		title, _, _ := strings.Cut(headCommit.GetMessage(), "\n")
		gitLabels.src["dagger.io/git.title"] = title

		ref := event.GetRef()
		if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			gitLabels.src["dagger.io/git.branch"] = branch
		} else if tag, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
			gitLabels.src["dagger.io/git.tag"] = tag
		}
	}

	// git labels from GitHub take precedence over local git ones
	maps.Copy(labels.src, gitLabels.src)

	return labels
}

func (labels Labels) WithGitLabLabels() Labels {
	if labels.eg.Getenv("GITLAB_CI") != "true" { //nolint:goconst
		return labels
	}

	branchName := labels.eg.Getenv("CI_MERGE_REQUEST_SOURCE_BRANCH_NAME")
	if branchName == "" {
		// for a branch job, CI_MERGE_REQUEST_SOURCE_BRANCH_NAME is empty
		branchName = labels.eg.Getenv("CI_COMMIT_BRANCH")
	}

	changeTitle := labels.eg.Getenv("CI_MERGE_REQUEST_TITLE")
	if changeTitle == "" {
		changeTitle = labels.eg.Getenv("CI_COMMIT_TITLE")
	}

	labels.src["dagger.io/vcs.repo.url"] = labels.eg.Getenv("CI_PROJECT_URL")
	labels.src["dagger.io/vcs.repo.full_name"] = labels.eg.Getenv("CI_PROJECT_PATH")
	labels.src["dagger.io/vcs.change.branch"] = branchName
	labels.src["dagger.io/vcs.change.title"] = changeTitle
	labels.src["dagger.io/vcs.change.head_sha"] = labels.eg.Getenv("CI_COMMIT_SHA")
	labels.src["dagger.io/vcs.triggerer.login"] = labels.eg.Getenv("GITLAB_USER_LOGIN")
	labels.src["dagger.io/vcs.event.type"] = labels.eg.Getenv("CI_PIPELINE_SOURCE")
	labels.src["dagger.io/vcs.job.name"] = labels.eg.Getenv("CI_JOB_NAME")
	labels.src["dagger.io/vcs.workflow.name"] = labels.eg.Getenv("CI_PIPELINE_NAME")
	labels.src["dagger.io/vcs.change.label"] = labels.eg.Getenv("CI_MERGE_REQUEST_LABELS")
	labels.src["gitlab.com/job.id"] = labels.eg.Getenv("CI_JOB_ID")
	labels.src["gitlab.com/triggerer.id"] = labels.eg.Getenv("GITLAB_USER_ID")
	labels.src["gitlab.com/triggerer.email"] = labels.eg.Getenv("GITLAB_USER_EMAIL")
	labels.src["gitlab.com/triggerer.name"] = labels.eg.Getenv("GITLAB_USER_NAME")

	projectURL := labels.eg.Getenv("CI_MERGE_REQUEST_PROJECT_URL")
	mrIID := labels.eg.Getenv("CI_MERGE_REQUEST_IID")
	if projectURL != "" && mrIID != "" {
		labels.src["dagger.io/vcs.change.url"] = fmt.Sprintf("%s/-/merge_requests/%s", projectURL, mrIID)
		labels.src["dagger.io/vcs.change.number"] = mrIID
	}

	return labels
}

func (labels Labels) WithCircleCILabels() Labels {
	if labels.eg.Getenv("CIRCLECI") != "true" { //nolint:goconst
		return labels
	}

	labels.src["dagger.io/vcs.change.branch"] = labels.eg.Getenv("CIRCLE_BRANCH")
	labels.src["dagger.io/vcs.change.head_sha"] = labels.eg.Getenv("CIRCLE_SHA1")
	labels.src["dagger.io/vcs.job.name"] = labels.eg.Getenv("CIRCLE_JOB")

	firstEnvLabel := func(label string, envVar []string) {
		for _, envVar := range envVar {
			triggererLogin := labels.eg.Getenv(envVar)
			if triggererLogin != "" {
				labels.src[label] = triggererLogin
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

	pipelineRepoURL := labels.eg.Getenv("CIRCLE_PIPELINE_REPO_URL")
	repositoryURL := labels.eg.Getenv("CIRCLE_REPOSITORY_URL")
	if pipelineRepoURL != "" { // gitlab
		labels.src["dagger.io/vcs.repo.url"] = pipelineRepoURL
	} else if repositoryURL != "" { // github / bitbucket (returns the remote)
		transformedURL := repositoryURL
		if strings.Contains(repositoryURL, "@") { // from ssh to https
			re := regexp.MustCompile(`git@(.*?):(.*?)/(.*)\.git`)
			transformedURL = re.ReplaceAllString(repositoryURL, "https://$1/$2/$3")
		}
		labels.src["dagger.io/vcs.repo.url"] = transformedURL
	}

	return labels
}

func (labels Labels) WithJenkinsLabels() Labels {
	if len(labels.eg.Getenv("JENKINS_HOME")) == 0 {
		return labels
	}
	// in Jenkins, vcs labels take precedence over provider env variables
	_, ok := labels.src["dagger.io/git.branch"]

	if !ok {
		remoteBranch := labels.eg.Getenv("GIT_BRANCH")
		if remoteBranch != "" {
			if _, branch, ok := strings.Cut(remoteBranch, "/"); ok {
				labels.src["dagger.io/git.branch"] = branch
			}
		}
		labels.src["dagger.io/git.ref"] = labels.eg.Getenv("GIT_COMMIT")
	}
	return labels
}

func (labels Labels) WithHarnessLabels() Labels {
	if len(labels.eg.Getenv("HARNESS_ACCOUNT_ID")) == 0 {
		return labels
	}
	// in Harness, vcs labels take precedence over provider env variables
	_, ok := labels.src["dagger.io/git.branch"]

	if !ok {
		remoteBranch := labels.eg.Getenv("GIT_BRANCH")
		if remoteBranch != "" {
			if _, branch, ok := strings.Cut(remoteBranch, "/"); ok {
				labels.src["dagger.io/git.branch"] = branch
			}
		}
		labels.src["dagger.io/git.ref"] = labels.eg.Getenv("GIT_COMMIT")
	}
	return labels
}

type repoIsh interface {
	GetFullName() string
	GetCloneURL() string
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
	if labels.isCI() {
		isCIValue = "true"
	}
	labels.src["dagger.io/ci"] = isCIValue

	vendor := ""
	switch {
	case labels.eg.Getenv("GITHUB_ACTIONS") == "true": //nolint:goconst
		vendor = "GitHub"
	case labels.eg.Getenv("CIRCLECI") == "true": //nolint:goconst
		vendor = "CircleCI"
	case labels.eg.Getenv("GITLAB_CI") == "true": //nolint:goconst
		vendor = "GitLab"
	case labels.eg.Getenv("JENKINS_HOME") != "":
		vendor = "Jenkins"
	case labels.eg.Getenv("HARNESS_ACCOUNT_ID") != "":
		vendor = "Harness"
	case labels.eg.Getenv("BUILDKITE") == "true":
		vendor = "Buildkite"
	case labels.eg.Getenv("TEAMCITY_VERSION") != "":
		vendor = "TeamCity"
	case labels.eg.Getenv("TF_BUILD") != "":
		vendor = "Azure"
	}
	if vendor != "" {
		labels.src["dagger.io/ci.vendor"] = vendor
	}

	provider := ""
	switch {
	case labels.eg.Getenv("DEPOT_PROJECT_ID") != "":
		provider = "Depot"
	case labels.eg.Getenv("NAMESPACE_GITHUB_RUNTIME") != "":
		provider = "Namespace"
	}
	if provider != "" {
		labels.src["dagger.io/ci.provider"] = provider
	}

	return labels
}

func (labels Labels) isCI() bool {
	return labels.eg.Getenv("CI") != "" || // GitHub Actions, Travis CI, CircleCI, Cirrus CI, GitLab CI, AppVeyor, CodeShip, dsari
		labels.eg.Getenv("BUILD_NUMBER") != "" || // Jenkins, TeamCity
		labels.eg.Getenv("RUN_ID") != "" || // TaskCluster, dsari
		labels.eg.Getenv("TF_BUILD") != "" // Azure Pipelines
}

func fetchRef(repo *git.Repository, workdir string, remote string, target string) (*object.Commit, error) {
	target = strings.TrimPrefix(target, "refs/")
	src := fmt.Sprintf("refs/%s", target)
	dest := fmt.Sprintf("refs/dagger/%s", target)

	// Fetch from the origin remote
	cmd := exec.Command("git", "fetch", "--depth", "1", remote, fmt.Sprintf("+%s:%s", src, dest))
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("error fetching branch from origin: %w\n%s", err, string(out))
	}

	// Get the reference of the fetched branch
	ref, err := repo.Reference(plumbing.ReferenceName(dest), true)
	if err != nil {
		return nil, fmt.Errorf("error getting reference %q: %w", dest, err)
	}

	// Get the commit object of the fetched branch
	branchCommit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("error getting commit %q: %w", ref.Hash(), err)
	}

	// Cleanup the temp ref
	cmd = exec.Command("git", "update-ref", "-d", dest, ref.Hash().String())
	cmd.Dir = workdir
	out, err = cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("error deleting ref %q: %w\n%s", dest, err, out)
		slog.Warn("failed to cleanup temp ref", "err", err)
	}

	return branchCommit, nil
}

type LabelFlag struct {
	Labels
}

func NewLabelFlag() LabelFlag {
	return LabelFlag{Labels: Labels{src: map[string]string{}}}
}

func (flag LabelFlag) Set(s string) error {
	name, val, ok := strings.Cut(s, ":")
	if !ok {
		return errors.New("invalid label format (must be name:value)")
	}
	if flag.Labels.src == nil {
		flag.Labels.src = map[string]string{}
	}
	flag.Labels.src[name] = val
	return nil
}

func (flag LabelFlag) Type() string {
	return "labels"
}

func (flag LabelFlag) String() string {
	return flag.Labels.UserAgent() // it's fine
}

var (
	urlSchemeRegExp  = regexp.MustCompile(`^[^:]+://`)
	scpLikeURLRegExp = regexp.MustCompile(`^(?:(?P<user>[^@]+)@)?(?P<host>[^:\s]+):(?:(?P<port>[0-9]{1,5})(?:\/|:))?(?P<path>[^\\].*\/[^\\].*)$`)
)

func parseGitURL(endpoint string) (string, error) {
	if e, ok := parseSCPLike(endpoint); ok {
		return e, nil
	}

	return parseURL(endpoint)
}

func parseURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}

	if !u.IsAbs() {
		return "", fmt.Errorf(
			"invalid endpoint: %s", endpoint,
		)
	}

	return fmt.Sprintf("%s%s", u.Hostname(), strings.TrimSuffix(u.Path, ".git")), nil
}

func parseSCPLike(endpoint string) (string, bool) {
	if matchesURLScheme(endpoint) || !matchesScpLike(endpoint) {
		return "", false
	}

	_, host, _, path := findScpLikeComponents(endpoint)

	return fmt.Sprintf("%s/%s", host, strings.TrimSuffix(path, ".git")), true
}

// matchesURLScheme returns true if the given string matches a URL-like
// format scheme.
func matchesURLScheme(url string) bool {
	return urlSchemeRegExp.MatchString(url)
}

// matchesScpLike returns true if the given string matches an SCP-like
// format scheme.
func matchesScpLike(url string) bool {
	return scpLikeURLRegExp.MatchString(url)
}

// findScpLikeComponents returns the user, host, port and path of the
// given SCP-like URL.
func findScpLikeComponents(url string) (user, host, port, path string) {
	m := scpLikeURLRegExp.FindStringSubmatch(url)
	return m[1], m[2], m[3], m[4]
}
