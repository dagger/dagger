package main

import (
	"context"
	"dagger/github/internal/dagger"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v59/github"
)

type Github struct{}

// NewProgressReport creates a new progress report for tracking tasks on a GitHub issue
func (gh Github) NewProgressReport(
	ctx context.Context,
	// A unique identifier for this progress report in the given issue.
	// Using the same key on the same issue will overwrite the same comment in the issue
	key string,
	// GitHub authentication token
	token *dagger.Secret,
	// Github repository to send updates
	repo string,
	// Issue number to report progress on
	issue int,
) ProgressReport {
	ghIssue, err := loadGithubIssue(ctx, token, repo, issue)
	if err != nil {
		ghIssue = &GithubIssue{
			IssueNumber: issue,
			Title:       "",
			Body:        "",
		}
	}
	return ProgressReport{
		Token: token,
		Repo:  repo,
		Issue: ghIssue,
		Key:   key,
	}
}

// A system for reporting on the progress of a task on a github issue
type ProgressReport struct {
	Token   *dagger.Secret
	Repo    string // +private
	Key     string // +private
	Issue   *GithubIssue
	Title   string
	Summary string
	Tasks   []Task
}

type Task struct {
	Key         string
	Description string
	Status      string
}

// Send a notification as an in-place update to a Github issue comment
func (r ProgressReport) Notify(ctx context.Context, message string) (ProgressReport, error) {
	r = r.StartTask(message, message, "✅")
	err := r.Publish(ctx)
	return r, err
}

// Write a new summary for the progress report.
// Any previous summary is overwritten.
// This function only stages the change. Call publish to actually apply it.
func (r ProgressReport) WriteSummary(
	ctx context.Context,
	// The text of the summary, markdown-formatted
	// It will be formatted as-is in the comment, after the title and before the task list
	summary string,
) (ProgressReport, error) {
	r.Summary = summary
	return r, nil
}

// Append new text to the summary, without overwriting it
// This function only stages the change. Call publish to actually apply it.
func (r ProgressReport) AppendSummary(
	ctx context.Context,
	// The text of the summary, markdown-formatted
	// It will be formatted as-is in the comment, after the title and before the task list
	summary string,
) (ProgressReport, error) {
	if r.Summary == "" {
		r.Summary = summary
		return r, nil
	}
	sep := "\n"
	// If the current summary already ends with a newline,
	// don't add another one to avoid double newlines
	if strings.HasSuffix(r.Summary, "\n") {
		sep = ""
	}
	// Trim whitespace from current summary, add separator and new summary
	r.Summary = strings.TrimSpace(r.Summary) + sep + summary
	return r, nil
}

// Report the starting of a new task
// This function only stages the change. Call publish to actually apply it.
func (r ProgressReport) StartTask(
	// A unique key for the task. Not sent in the comment. Use to update the task status later.
	key string,
	// The task description. It will be formatted as a cell in the first column of a markdown table
	description string,
	// The task status. It will be formatted as a cell in the second column of a markdown table
	status string,
) ProgressReport {
	r.Tasks = append(r.Tasks, Task{
		Key:         key,
		Description: description,
		Status:      status,
	})
	return r
}

// Write a new title for the progress report.
// Any previous title is overwritten.
// This function only stages the change. Call publish to actually apply it.
func (r ProgressReport) WriteTitle(
	ctx context.Context,
	// The summary. It should be a single line of unformatted text.
	// It will be formatted as a H2 title in the markdown body of the comment
	title string,
) (ProgressReport, error) {
	r.Title = strings.ToTitle(title)
	return r, nil
}

// Update the status of a task
// This function only stages the change. Call publish to actually apply it.
func (r ProgressReport) UpdateTask(
	ctx context.Context,
	// A unique key for the task. Use to update the task status later.
	key string,
	// The task status. It will be formatted as a cell in the second column of a markdown table
	status string,
) (ProgressReport, error) {
	for i := range r.Tasks {
		if r.Tasks[i].Key == key {
			r.Tasks[i].Status = status
			return r, nil
		}
	}
	return r, fmt.Errorf("no task at key %s", key)
}

// Publish all staged changes to the status update.
// This will cause a single comment on the target issue to be either
// created, or updated in-place.
func (r ProgressReport) Publish(ctx context.Context) error {
	var contents string
	if r.Title != "" {
		contents = "## " + r.Title + "\n\n"
	}
	if r.Summary != "" {
		contents += r.Summary + "\n\n"
	}
	if len(r.Tasks) > 0 {
		contents += "### Tasks\n\n"
		contents += "<table>\n<tr><th>Description</th><th>Status</th></tr>\n"
		for _, task := range r.Tasks {
			contents += fmt.Sprintf("<tr><td>%s</td><td>%s</td></tr>\n", task.Description, task.Status)
		}
		contents += "</table>\n"
	}
	contents += fmt.Sprintf("\n<sub>*Last update: %s*<sub>\n", time.Now().Local().Format("2006-01-02 15:04:05 MST"))
	comment := dag.GithubComment(r.Token, r.Repo, dagger.GithubCommentOpts{Issue: r.Issue.IssueNumber, MessageID: r.Key})
	_, err := comment.Create(ctx, contents)
	return err
}

type GithubIssue struct {
	IssueNumber int
	Title       string
	Body        string
}

func loadGithubIssue(ctx context.Context, token *dagger.Secret, repo string, id int) (*GithubIssue, error) {
	// Strip .git suffix if present
	repo = strings.TrimSuffix(repo, ".git")

	// Remove https:// or http:// prefix if present
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")

	// Remove github.com/ prefix if present
	repo = strings.TrimPrefix(repo, "github.com/")

	// Split remaining string into owner/repo
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repository format: %s", repo)
	}

	owner := parts[0]
	repo = parts[1]

	ghClient, err := githubClient(ctx, token)
	if err != nil {
		return nil, err
	}

	issue, _, err := ghClient.Issues.Get(ctx, owner, repo, id)
	if err != nil {
		return nil, err
	}

	ghi := &GithubIssue{IssueNumber: id}
	if issue.Title != nil {
		ghi.Title = *issue.Title
	}
	if issue.Body != nil {
		ghi.Body = *issue.Body
	}

	return ghi, nil
}

func githubClient(ctx context.Context, token *dagger.Secret) (*github.Client, error) {
	plaintoken, err := token.Plaintext(ctx)
	if err != nil {
		return nil, err
	}
	return github.NewClient(nil).WithAuthToken(plaintoken), nil
}
