// A generated module for GoCoder functions

package main

import (
	"context"
	"dagger/go-coder/internal/dagger"
	"fmt"
)

const (
	DEFAULT_CODER_MODEL = "qwen2.5-coder:32b"
	DEFAULT_CHAT_MODEL  = "llama3.3"
)

type GoCoder struct{}

// Ask a go-coder to complete a task and get the Container with the completed task
func (m *GoCoder) Assignment(
	// The task to complete
	task string,
) *dagger.Container {
	// Create a workspace for building Go code
	ws := dag.Workspace(dagger.WorkspaceOpts{
		BaseImage: "golang",
		Context:   dag.Directory(),
		Checker:   "go build ./...",
	})

	// Give the workspace to the LLM
	coder := dag.Llm().
		WithWorkspace(ws).
		WithPromptFile(dag.CurrentModule().Source().File("system.txt")).
		WithPromptVar("assignment", task).
		WithPrompt(`
<assignment>
$assignment
</assignment>
		`).
		Sync()

	// Return the container
	return coder.Workspace().Container()
}

// Returns a container that echoes whatever string argument is provided
func (m *GoCoder) SolveIssue(
	ctx context.Context,
	// Github authentication token
	githubToken *dagger.Secret,
	// Github repository with an issue to solve
	repo string,
	// Issue number to solve
	issueId int,
	// LLM Model
	// +optional
	model string,
) (string, error) {
	coderModel := DEFAULT_CODER_MODEL
	chatModel := DEFAULT_CHAT_MODEL
	if model != "" {
		coderModel = model
		chatModel = model
	}
	// Read assignment from the github issue
	issue := dag.GithubIssue(githubToken).Read(repo, issueId)

	task, err := issue.Body(ctx)
	if err != nil {
		return "", err
	}

	// Complete the assignment
	ws := dag.Workspace(dagger.WorkspaceOpts{
		BaseImage: "golang",
		Context:   dag.Git(repo).Head().Tree(), // Start with the head of the repo
		Checker:   "go build ./...",
	})

	coder := dag.Llm(dagger.LlmOpts{Model: coderModel}).
		WithWorkspace(ws).
		WithPromptFile(dag.CurrentModule().Source().File("system.txt")).
		WithPromptVar("assignment", task).
		WithPrompt(`
<assignment>
$assignment
</assignment>
		`).
		Sync()

	completedWork := coder.Workspace().Container().Directory(".")

	// Create a pull request with the completed assignment
	branchName, err := askAnLLM(ctx, task, "Choose a git branch name apprpriate for this assignment. A git branch name should be no more than 20 alphanumeric characters.", chatModel)
	if err != nil {
		return "", err
	}
	title, err := askAnLLM(ctx, task, "Choose a pull request title that describes the changes made in this assignment.", chatModel)
	if err != nil {
		return "", err
	}

	featureBranch := dag.FeatureBranch(githubToken, repo, branchName).
		WithChanges(completedWork)

	// Make sure changes have been made to the workspace
	diff, err := featureBranch.Diff(ctx)
	if err != nil {
		return "", err
	}

	if diff == "" {
		return "", fmt.Errorf("Got empty diff for prompt: %s", task)
	}

	return featureBranch.PullRequest(ctx, title, task)
}

func (m *GoCoder) PrFeedback(
	ctx context.Context,
	// Github authentication token
	githubToken *dagger.Secret,
	// Github repository with an issue to solve
	repo string,
	// PR number to iterate on
	prNumber int,
	// PR feedback
	feedback string,
	// LLM Model
	// +optional
	model string,
) (string, error) {
	coderModel := DEFAULT_CODER_MODEL
	chatModel := DEFAULT_CHAT_MODEL
	if model != "" {
		coderModel = model
		chatModel = model
	}
	// Read original assignment from the github issue
	issue := dag.GithubIssue(githubToken).Read(repo, prNumber)

	title, err := askAnLLM(ctx, feedback, "Choose a git commit message that describes the changes made in this assignment.", chatModel)
	if err != nil {
		return "", err
	}

	task, err := issue.Body(ctx)
	if err != nil {
		return "", err
	}

	baseRef, err := issue.BaseRef(ctx)
	if err != nil {
		return "", err
	}
	headRef, err := issue.HeadRef(ctx)
	if err != nil {
		return "", err
	}

	base := dag.Git(repo).Ref(baseRef).Tree()
	head := dag.Git(repo).Ref(headRef).Tree()
	// Create a workspace to implement the feedback
	ws := dag.Workspace(dagger.WorkspaceOpts{
		BaseImage: "golang",
		Context:   base, // Set starting point as base so that diff shows full diff
		Checker:   "go build ./...",
	}).WriteDirectory(".", head.WithoutDirectory(".git")) // Layer on changes already made in the PR

	coder := dag.Llm(dagger.LlmOpts{Model: coderModel}).
		WithWorkspace(ws).
		WithPromptFile(dag.CurrentModule().Source().File("system.txt")).
		WithPromptVar("assignment", task).
		WithPromptVar("feedback", feedback).
		WithPrompt(`
You have already started solving an assignment.
You have received feedback on your progress so far.
Use the 'diff' tool to review the progress so far.
Implement the feedback provided to complete the assignment.
<assignment>
$assignment
</assignment>
<feedback>
$feedback
</feedback>
		`).
		Sync()

	completedWork := coder.Workspace().Container().Directory(".")

	// Git checkout has weird git info. Fix it.
	gitEnv := dag.Container().
		From("alpine/git").
		WithWorkdir("/src").
		WithDirectory("/src", head).
		WithExec([]string{
			"git", "tag", "-d", headRef,
		}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny}).
		WithExec([]string{
			"git", "checkout", headRef,
		})

	head = gitEnv.Directory("/src")

	// Setup feature branch for changes
	featureBranch := dag.FeatureBranch(githubToken, repo, "WeDontUseThis").
		// Set branch to our head
		WithBranch(head).
		WithBranchName(headRef).
		WithChanges(completedWork)

	// Make sure changes have been made to the workspace
	diff, err := featureBranch.Diff(ctx)
	if err != nil {
		return "", err
	}

	if diff == "" {
		return "", fmt.Errorf("Got empty diff for prompt: %s", task)
	}

	return featureBranch.Push(ctx, title)
}

func askAnLLM(ctx context.Context, queryContext, query string, model string) (string, error) {
	return dag.Llm(dagger.LlmOpts{Model: model}).
		WithPromptVar("query", query).
		WithPromptVar("context", queryContext).
		WithPrompt(`
You will be given a query and a context. Answer the query using the context provided.
Be brief in your responses.
Do not include punctuation or quotes.
Do not explain the response, simply return the answer.

<query>$query</query>

<context>$context</context>`).
		LastReply(ctx)
}
