package pipeline

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/go-git/go-git/v5"
	"github.com/google/go-github/v50/github"
	"github.com/sirupsen/logrus"
)

var (
	loadOnce      sync.Once
	loadDoneCh    = make(chan struct{})
	defaultLabels []Label
)

type Label struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Labels []Label

// RootLabels returns default labels for Pipelines.
//
// `LoadRootLabels` *must* be called before invoking this function.
// `RootLabels` will wait until `LoadRootLabels` has completed.
func RootLabels() []Label {
	<-loadDoneCh
	return defaultLabels
}

// LoadRootLabels loads default Pipeline labels from a workdir.
func LoadRootLabels(workdir string, engineName string) {
	loadOnce.Do(func() {
		defer close(loadDoneCh)
		defaultLabels = loadRootLabels(workdir, engineName)
	})
}

func loadRootLabels(workdir, engineName string) []Label {
	labels := []Label{{
		Name:  "dagger.io/engine",
		Value: engineName,
	}}

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

	return labels
}

func LoadGitLabels(workdir string) ([]Label, error) {
	repo, err := git.PlainOpenWithOptions(workdir, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			return nil, nil
		}

		return nil, err
	}

	origin, err := repo.Remote("origin")
	if err != nil {
		return nil, err
	}

	urls := origin.Config().URLs
	if len(urls) == 0 {
		return []Label{}, nil
	}

	endpoint, err := parseGitURL(urls[0])
	if err != nil {
		return nil, err
	}

	head, err := repo.Head()
	if err != nil {
		return nil, err
	}

	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, err
	}

	title, _, _ := strings.Cut(commit.Message, "\n")

	labels := []Label{
		{
			Name:  "dagger.io/git.remote",
			Value: endpoint,
		},
		{
			Name:  "dagger.io/git.ref",
			Value: head.Hash().String(),
		},
		{
			Name:  "dagger.io/git.author.name",
			Value: commit.Author.Name,
		},
		{
			Name:  "dagger.io/git.author.email",
			Value: commit.Author.Email,
		},
		{
			Name:  "dagger.io/git.committer.name",
			Value: commit.Committer.Name,
		},
		{
			Name:  "dagger.io/git.committer.email",
			Value: commit.Committer.Email,
		},
		{
			Name:  "dagger.io/git.title",
			Value: title, // first line from commit message
		},
	}

	if head.Name().IsTag() {
		labels = append(labels, Label{
			Name:  "dagger.io/git.tag",
			Value: head.Name().Short(),
		})
	}

	if head.Name().IsBranch() {
		labels = append(labels, Label{
			Name:  "dagger.io/git.branch",
			Value: head.Name().Short(),
		})
	}

	return labels, nil
}

func LoadGitHubLabels() ([]Label, error) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return []Label{}, nil
	}

	eventType := os.Getenv("GITHUB_EVENT_NAME")

	labels := []Label{
		{
			Name:  "github.com/actor",
			Value: os.Getenv("GITHUB_ACTOR"),
		},
		{
			Name:  "github.com/event.type",
			Value: eventType,
		},
		{
			Name:  "github.com/workflow.name",
			Value: os.Getenv("GITHUB_WORKFLOW"),
		},
		{
			Name:  "github.com/workflow.job",
			Value: os.Getenv("GITHUB_JOB"),
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
				Name:  "github.com/repo.full_name",
				Value: repo.GetFullName(),
			})

			labels = append(labels, Label{
				Name:  "github.com/repo.url",
				Value: repo.GetHTMLURL(),
			})
		}

		if event, ok := event.(interface {
			GetPullRequest() *github.PullRequest
		}); ok && event.GetPullRequest() != nil {
			pr := event.GetPullRequest()

			labels = append(labels, Label{
				Name:  "github.com/pr.number",
				Value: fmt.Sprintf("%d", pr.GetNumber()),
			})

			labels = append(labels, Label{
				Name:  "github.com/pr.title",
				Value: pr.GetTitle(),
			})

			labels = append(labels, Label{
				Name:  "github.com/pr.url",
				Value: pr.GetHTMLURL(),
			})

			labels = append(labels, Label{
				Name:  "github.com/pr.branch",
				Value: pr.GetHead().GetRef(),
			})

			labels = append(labels, Label{
				Name:  "github.com/pr.label",
				Value: pr.GetHead().GetLabel(),
			})

			labels = append(labels, Label{
				Name:  "github.com/pr.head",
				Value: pr.GetHead().GetSHA(),
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
		if gitLabel.Name == "dagger.io/git.author.email" || gitLabel.Name == "dagger.io/git.remote" {
			labels.Add(gitLabel.Name, fmt.Sprintf("%x", base64.StdEncoding.EncodeToString([]byte(gitLabel.Value))))
		}
	}

	return labels
}
