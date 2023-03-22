package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
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
func LoadRootLabels(ctx context.Context, workdir string) {
	loadOnce.Do(func() {
		defer close(loadDoneCh)
		defaultLabels = loadRootLabels(ctx, workdir)
	})
}

func loadRootLabels(ctx context.Context, workdir string) []Label {
	labels := []Label{}

	if gitLabels, err := loadGitLabels(workdir); err == nil {
		labels = append(labels, gitLabels...)
	} else {
		logrus.Warnf("failed to collect git labels: %s", err)
	}

	if githubLabels, err := loadGitHubLabels(ctx); err == nil {
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

func loadGitHubLabels(ctx context.Context) ([]Label, error) {
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

	client := github.NewTokenClient(ctx, os.Getenv("GITHUB_TOKEN"))

	job, err := getGitHubJob(ctx, client)
	if err != nil {
		logrus.Warnf("failed to determine current job: %s", err)
	} else {
		labels = append(labels, Label{
			Name:  "github.com/workflow.url",
			Value: job.GetHTMLURL(),
		})
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

// GitHub doesn't expose the job ID to actions runs for some reason, so we need
// to find the current job by name instead.
func getGitHubJob(ctx context.Context, client *github.Client) (*github.WorkflowJob, error) {
	jobName := os.Getenv("GITHUB_JOB")
	ownerAndRepo := os.Getenv("GITHUB_REPOSITORY")
	workflowRunID := os.Getenv("GITHUB_RUN_ID")

	owner, repo, ok := strings.Cut(ownerAndRepo, "/")
	if !ok {
		return nil, fmt.Errorf("invalid $GITHUB_REPOSITORY: %q", ownerAndRepo)
	}

	workflowID, err := strconv.Atoi(workflowRunID)
	if err != nil {
		return nil, fmt.Errorf("invalid $GITHUB_RUN_ID: %q", workflowRunID)
	}

	jobs, err := allPages(func(github.ListOptions) ([]*github.WorkflowJob, *github.Response, error) {
		res, resp, err := client.Actions.ListWorkflowJobs(ctx, owner, repo, int64(workflowID), nil)
		return res.Jobs, resp, err
	})
	if err != nil {
		return nil, fmt.Errorf("list workflow jobs: %w", err)
	}

	for _, job := range jobs {
		if job.GetName() == jobName {
			return job, nil
		}
	}

	return nil, fmt.Errorf("job not found")
}

func allPages[T any](fn func(github.ListOptions) ([]T, *github.Response, error)) ([]T, error) {
	var all []T
	opt := github.ListOptions{PerPage: 100}
	for {
		page, resp, err := fn(opt)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return all, nil
}
