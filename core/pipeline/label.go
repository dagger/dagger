package pipeline

import (
	"fmt"
	"os"
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

		var action *string
		var pr *github.PullRequest
		var repoURL, repoFullName string
		switch x := event.(type) {
		case *github.PushEvent:
			action = x.Action
			repoURL = x.GetRepo().GetHTMLURL()
			repoFullName = x.GetRepo().GetFullName()
		case *github.PullRequestEvent:
			action = x.Action
			pr = x.GetPullRequest()
			repoURL = x.GetRepo().GetHTMLURL()
			repoFullName = x.GetRepo().GetFullName()
		}

		if action != nil {
			labels = append(labels, Label{
				Name:  "github.com/event.action",
				Value: *action,
			})
		}

		if repoURL != "" && repoFullName != "" {
			labels = append(labels, Label{
				Name:  "github.com/repo.full_name",
				Value: repoFullName,
			})

			labels = append(labels, Label{
				Name:  "github.com/repo.url",
				Value: repoURL,
			})
		}

		if pr != nil {
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

type GitHubEventPayload struct {
	// set on many events
	Action *string `json:"action,omitempty"`

	// set on push events
	After *string `json:"after,omitempty"`

	// set on check_suite events
	CheckSuite *github.CheckSuite `json:"check_suite,omitempty"`

	// set on check_run events
	CheckRun *github.CheckRun `json:"check_run,omitempty"`

	// set on pull_request events
	PullRequest *github.PullRequest `json:"pull_request,omitempty"`

	// set on all events
	Repo         *github.Repository   `json:"repository,omitempty"`
	Sender       *github.User         `json:"sender,omitempty"`
	Installation *github.Installation `json:"installation,omitempty"`
}
