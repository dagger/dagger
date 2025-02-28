package main

import (
	"context"
	"dagger/melvin/internal/dagger"
	"fmt"
	"strconv"
)

// An instance of Melvin, a modular coding agent
type Melvin struct{}

// Ask Melvin to perform a Go programming task
func (m *Melvin) GoProgram(
	// A description of the Go programming task to perform
	assignment string,
	// A github repository to work from
	// +optional
	githubRepo string,
	// A github issue number to use for communication
	// +optional
	githubIssue int,
	// A github API token, to allow interacting in issues
	// +optional
	githubToken *dagger.Secret,
) GoProgrammingTask {
	githubProgress := dag.Github().NewProgressReport(assignment, githubToken, githubRepo, githubIssue)
	var opts dagger.WorkspaceOpts
	opts.Checkers = append(opts.Checkers, dag.GoChecker().AsWorkspaceChecker())
	opts.OnSave = append(opts.OnSave, githubProgress.AsWorkspaceNotifier())
	if githubRepo != "" {
		opts.Start = dag.Git(githubRepo).Head().Tree()
	}
	workspace := dag.Workspace(opts)
	return GoProgrammingTask{
		Assignment: assignment,
		Workspace:  workspace,
		Progress:   githubProgress,
	}
}

// A Go programming task to be accomplished by Melvin
type GoProgrammingTask struct {
	Assignment string
	Workspace  *dagger.Workspace            // +private
	Progress   *dagger.GithubProgressReport // +private
	Reviews    []*Review
}

func (task *GoProgrammingTask) coderAgent() *dagger.Llm {
	coder := dag.Llm().
		WithWorkspace(task.Workspace).
		WithPromptVar("assignment", task.Assignment).
		WithPromptFile(dag.CurrentModule().Source().File("coder.prompt"))
	if n := len(task.Reviews); n > 0 {
		lastReview := task.Reviews[n-1]
		coder = coder.
			WithPromptVar("score", fmt.Sprintf("%d", lastReview.Score)).
			WithPromptVar("suggestions", lastReview.Suggestions).
			WithPrompt(
				`At the last review, you received the score: ${score}/10,
				and the suggestions for improvement - apply them:
				$suggestions`)
	}
	return coder
}

func (task *GoProgrammingTask) withDevLoop() *GoProgrammingTask {
	task.Workspace = task.coderAgent().Workspace()
	return task
}

func (task *GoProgrammingTask) sendReviewProgress(ctx context.Context) (*GoProgrammingTask, error) {
	if task.Progress == nil {
		return task, nil
	}
	if len(task.Reviews) == 0 {
		return task, nil
	}
	n := len(task.Reviews)
	lastReview := task.Reviews[n-1]
	status := "❗"
	if lastReview.Score >= 7 {
		status = "✅"
	}
	progress := task.Progress.StartTask(
		fmt.Sprintf("review-%d", n),
		fmt.Sprintf("Code review #%d", n),
		fmt.Sprintf("%s %d/10: %s", status, lastReview.Score, lastReview.Summary),
	)
	if err := progress.Publish(ctx); err != nil {
		return nil, err
	}
	task.Progress = progress
	return task, nil
}

type Review struct {
	Score       int
	Summary     string
	Suggestions string
}

func (task *GoProgrammingTask) Review(ctx context.Context) (*Review, error) {
	reviewer := dag.Llm().
		WithWorkspace(task.Workspace).
		WithPromptVar("assignment", task.Assignment).
		WithPromptFile(dag.CurrentModule().Source().File("reviewer.prompt"))
	reviewer = reviewer.WithPrompt("OUTPUT: a bullet list of detailed, actionable suggestions. No other output.")
	suggestions, err := reviewer.LastReply(ctx)
	if err != nil {
		return nil, err
	}
	reviewer = reviewer.WithPrompt("OUTPUT: a one-line summary of the review. No other output.")
	summary, err := reviewer.LastReply(ctx)
	if err != nil {
		return nil, err
	}
	reviewer = reviewer.WithPrompt(`OUTPUT: an integer review score between 0 and 10. 0 is worst, 10 is best.
		If the code doesn't meet requirements, keep under 5. No other output.`)
	scoreStr, err := reviewer.LastReply(ctx)
	if err != nil {
		return nil, err
	}
	score, err := strconv.Atoi(scoreStr)
	if err != nil {
		return nil, err
	}
	return &Review{
		Score:       score,
		Summary:     summary,
		Suggestions: suggestions,
	}, nil
}

func (task *GoProgrammingTask) withReviewLoop(ctx context.Context) (*GoProgrammingTask, error) {
	for i := 0; i < 5; i++ {
		task = task.withDevLoop()
		review, err := task.Review(ctx)
		if err != nil {
			return nil, err
		}
		task.Reviews = append(task.Reviews, review)
		task.sendReviewProgress(ctx)
		if review.Score >= 7 {
			break
		}
	}
	return task.sendFinalProgress(ctx)
}

func (task *GoProgrammingTask) sendFinalProgress(ctx context.Context) (*GoProgrammingTask, error) {
	diff, err := task.Workspace.Diff(ctx)
	if err != nil {
		return nil, err
	}
	progress := task.Progress.AppendSummary(fmt.Sprintf("\n### Result\n\n```\n%s\n```\n", diff))
	if err := progress.Publish(ctx); err != nil {
		return nil, err
	}
	task.Progress = progress
	return task, nil
}

// Return the result of the task, as a source code directory
func (task *GoProgrammingTask) Source(ctx context.Context) (*dagger.Directory, error) {
	task, err := task.withReviewLoop(ctx)
	if err != nil {
		return nil, err
	}
	return task.Workspace.Dir(), nil
}

// Return the result of the task wrapped in a Go dev environment
func (task *GoProgrammingTask) Container(ctx context.Context) (*dagger.Container, error) {
	source, err := task.Source(ctx)
	if err != nil {
		return nil, err
	}
	return dag.Container().From("golang").WithDirectory(".", source), nil
}

// Send the first progress update over github, to acknowledge the task and say that work is starting
func (task *GoProgrammingTask) firstProgressUpdate(ctx context.Context) (*GoProgrammingTask, error) {
	progress := task.reporterAgent().
		WithPrompt(`Send an initial progress update with a concise title and summary,
			to indicate that the assignment has been received and is being processed.
			- Don't capitalize the title, use regular title casing.
			- Don't start any tasks.
			`).
		GithubProgressReport()
	if err := progress.Publish(ctx); err != nil {
		return nil, err
	}
	task.Progress = progress
	return task, nil
}

// A "reporter" agent who can send progress updates over github
func (task *GoProgrammingTask) reporterAgent() *dagger.Llm {
	return dag.Llm().
		WithPromptVar("assignment", task.Assignment).
		WithPrompt("You are an expert software engineer tasked with sending progress updates to your team").
		WithPrompt(`your writing style:
			- follows open-source engineering conventions
			- precise
			- informative
			- pragmatic
			- use simple words`).
		WithPrompt(`your assignment is: $assignment`).
		WithGithubProgressReport(task.Progress)
}
