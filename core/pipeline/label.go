package pipeline

import (
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

// RootLabels returns default labels for Pipelines.
//
// `LoadRootLabels` *must* be called before invoking this function.
// `RootLabels` will wait until `LoadRootLabels` has completed.
func RootLabels() []Label {
	<-loadDoneCh
	return defaultLabels
}

// LoadRootLabels loads default Pipeline labels from a workdir.
func LoadRootLabels(workdir string) {
	loadOnce.Do(func() {
		defer close(loadDoneCh)
		defaultLabels = loadRootLabels(workdir)
	})
}

func loadRootLabels(workdir string) []Label {
	labels := []Label{}

	if gitLabels, err := loadGitLabels(workdir); err == nil {
		labels = append(labels, gitLabels...)
	} else {
		logrus.Warnf("failed to collect git labels: %s", err)
	}

	if githubLabels, err := loadGitHubLabels(); err == nil {
		labels = append(labels, githubLabels...)
	} else {
		logrus.Warnf("failed to collect GitHub labels: %s", err)
	}

	return labels
}

func loadGitLabels(workdir string) ([]Label, error) {
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

	return []Label{
		{
			Name:  "dagger.io/git.remote",
			Value: endpoint,
		},
		{
			Name:  "dagger.io/git.branch",
			Value: head.Name().Short(),
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
	}, nil
}

func loadGitHubLabels() ([]Label, error) {
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

		if event, ok := event.(interface{ GetAction() string }); ok {
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

		if event, ok := event.(interface{ GetPullRequest() *github.PullRequest }); ok {
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
