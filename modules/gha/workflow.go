package main

import (
	"context"
	"encoding/json"

	"github.com/dagger/dagger/modules/gha/api"
	"github.com/dagger/dagger/modules/gha/internal/dagger"
	"gopkg.in/yaml.v3"
)

type Workflow struct {
	Name string
	Jobs []*Job

	// +private
	Triggers api.WorkflowTriggers

	// +private
	PullRequestConcurrency string
	// +private
	Permissions Permissions
}

// HACK: dagger.gen.go needs this (for some reason)
type WorkflowTriggers = api.WorkflowTriggers

func (w *Workflow) WithJob(job *Job) *Workflow {
	w.Jobs = append(w.Jobs, job)
	return w
}

// Check that the workflow is valid, in a best effort way
func (w *Workflow) Check(
	ctx context.Context,
	// +defaultPath="/"
	repo *dagger.Directory,
) error {
	for _, job := range w.Jobs {
		if err := job.checkSecretNames(); err != nil {
			return err
		}
		if err := job.checkCommandAndModule(ctx, repo); err != nil {
			return err
		}
	}
	return nil
}

func (gha *Gha) WithWorkflow(workflow *Workflow) *Gha {
	workflow = workflow.applyDefaults(gha.WorkflowDefaults)
	for i, job := range workflow.Jobs {
		// TODO: move this to WithWorkflow (requires propagating
		// gha.JobDefaults onto the workflow)
		workflow.Jobs[i] = job.applyDefaults(gha.JobDefaults)
	}

	gha.Workflows = append(gha.Workflows, workflow)
	return gha
}

// Add a workflow
//
//nolint:gocyclo
func (gha *Gha) Workflow(
	// Workflow name
	name string,
	// Configure this workflow's concurrency for each PR.
	// This is triggered when the workflow is scheduled concurrently on the same PR.
	//   - allow: all instances are allowed to run concurrently
	//   - queue: new instances are queued, and run sequentially
	//   - preempt: new instances run immediately, older ones are canceled
	// Possible values: "allow", "preempt", "queue"
	// +optional
	pullRequestConcurrency string,
	// Disable manual "dispatch" of this workflow
	// +optional
	noDispatch bool,
	// Permissions to grant the workflow
	// +optional
	permissions Permissions,
	// Run the workflow on any issue comment activity
	// +optional
	onIssueComment bool,
	// +optional
	onIssueCommentCreated bool,
	// +optional
	onIssueCommentEdited bool,
	// +optional
	onIssueCommentDeleted bool,
	// Run the workflow on any pull request activity
	// +optional
	onPullRequest bool,
	// +optional
	onPullRequestBranches []string,
	// +optional
	onPullRequestPaths []string,
	// +optional
	onPullRequestAssigned bool,
	// +optional
	onPullRequestUnassigned bool,
	// +optional
	onPullRequestLabeled bool,
	// +optional
	onPullRequestUnlabeled bool,
	// +optional
	onPullRequestOpened bool,
	// +optional
	onPullRequestEdited bool,
	// +optional
	onPullRequestClosed bool,
	// +optional
	onPullRequestReopened bool,
	// +optional
	onPullRequestSynchronize bool,
	// +optional
	onPullRequestConvertedToDraft bool,
	// +optional
	onPullRequestLocked bool,
	// +optional
	onPullRequestUnlocked bool,
	// +optional
	onPullRequestEnqueued bool,
	// +optional
	onPullRequestDequeued bool,
	// +optional
	onPullRequestMilestoned bool,
	// +optional
	onPullRequestDemilestoned bool,
	// +optional
	onPullRequestReadyForReview bool,
	// +optional
	onPullRequestReviewRequested bool,
	// +optional
	onPullRequestReviewRequestRemoved bool,
	// +optional
	onPullRequestAutoMergeEnabled bool,
	// +optional
	onPullRequestAutoMergeDisabled bool,
	// Run the workflow on any git push
	// +optional
	onPush bool,
	// Run the workflow on git push to the specified tags
	// +optional
	onPushTags []string,
	// Run the workflow on git push to the specified branches
	// +optional
	onPushBranches []string,
	// Run the workflow only if the paths match
	// +optional
	onPushPaths []string,
	// Run the workflow at a schedule time
	// +optional
	onSchedule []string,
) *Workflow {
	w := &Workflow{Name: name}

	if !noDispatch {
		w.Triggers.WorkflowDispatch = &api.WorkflowDispatchEvent{}
	}
	if pullRequestConcurrency != "" {
		w.PullRequestConcurrency = pullRequestConcurrency
	}
	if permissions != nil {
		w.Permissions = permissions
	}
	if onIssueComment {
		w = w.onIssueComment(nil)
	}
	if onIssueCommentCreated {
		w = w.onIssueComment([]string{"created"})
	}
	if onIssueCommentDeleted {
		w = w.onIssueComment([]string{"deleted"})
	}
	if onIssueCommentEdited {
		w = w.onIssueComment([]string{"edited"})
	}
	if onPullRequest {
		w = w.onPullRequest(nil, nil, nil)
	}
	if onPullRequestBranches != nil {
		w = w.onPullRequest(nil, onPullRequestBranches, nil)
	}
	if onPullRequestPaths != nil {
		w = w.onPullRequest(nil, nil, onPullRequestPaths)
	}
	if onPullRequestAssigned {
		w = w.onPullRequest([]string{"assigned"}, nil, nil)
	}
	if onPullRequestUnassigned {
		w = w.onPullRequest([]string{"unassigned"}, nil, nil)
	}
	if onPullRequestLabeled {
		w = w.onPullRequest([]string{"labeled"}, nil, nil)
	}
	if onPullRequestUnlabeled {
		w = w.onPullRequest([]string{"unlabeled"}, nil, nil)
	}
	if onPullRequestOpened {
		w = w.onPullRequest([]string{"opened"}, nil, nil)
	}
	if onPullRequestEdited {
		w = w.onPullRequest([]string{"edited"}, nil, nil)
	}
	if onPullRequestClosed {
		w = w.onPullRequest([]string{"closed"}, nil, nil)
	}
	if onPullRequestReopened {
		w = w.onPullRequest([]string{"reopened"}, nil, nil)
	}
	if onPullRequestSynchronize {
		w = w.onPullRequest([]string{"synchronize"}, nil, nil)
	}
	if onPullRequestConvertedToDraft {
		w = w.onPullRequest([]string{"converted_to_draft"}, nil, nil)
	}
	if onPullRequestLocked {
		w = w.onPullRequest([]string{"locked"}, nil, nil)
	}
	if onPullRequestUnlocked {
		w = w.onPullRequest([]string{"unlocked"}, nil, nil)
	}
	if onPullRequestEnqueued {
		w = w.onPullRequest([]string{"enqueued"}, nil, nil)
	}
	if onPullRequestDequeued {
		w = w.onPullRequest([]string{"dequeued"}, nil, nil)
	}
	if onPullRequestMilestoned {
		w = w.onPullRequest([]string{"milestoned"}, nil, nil)
	}
	if onPullRequestDemilestoned {
		w = w.onPullRequest([]string{"demilestoned"}, nil, nil)
	}
	if onPullRequestReadyForReview {
		w = w.onPullRequest([]string{"ready_for_review"}, nil, nil)
	}
	if onPullRequestReviewRequested {
		w = w.onPullRequest([]string{"review_requested"}, nil, nil)
	}
	if onPullRequestReviewRequestRemoved {
		w = w.onPullRequest([]string{"review_request_removed"}, nil, nil)
	}
	if onPullRequestAutoMergeEnabled {
		w = w.onPullRequest([]string{"auto_merge_enabled"}, nil, nil)
	}
	if onPullRequestAutoMergeDisabled {
		w = w.onPullRequest([]string{"auto_merge_disabled"}, nil, nil)
	}
	if onPush {
		w = w.onPush(nil, nil, nil)
	}
	if onPushBranches != nil {
		w = w.onPush(onPushBranches, nil, nil)
	}
	if onPushTags != nil {
		w = w.onPush(nil, onPushTags, nil)
	}
	if onPushPaths != nil {
		w = w.onPush(nil, nil, onPushPaths)
	}
	if onSchedule != nil {
		w = w.onSchedule(onSchedule)
	}
	w.applyDefaults(gha.WorkflowDefaults)
	return w
}

func (w *Workflow) onIssueComment(
	// Run only for certain types of issue comment events
	// See https://docs.github.com/en/actions/writing-workflows/choosing-when-your-workflow-runs/events-that-trigger-workflows#issue_comment
	// +optional
	types []string,
) *Workflow {
	if w.Triggers.IssueComment == nil {
		w.Triggers.IssueComment = &api.IssueCommentEvent{}
	}
	w.Triggers.IssueComment.Types = append(w.Triggers.IssueComment.Types, types...)
	return w
}

// Add a trigger to execute a Dagger workflow on a pull request
func (w *Workflow) onPullRequest(
	// Run only for certain types of pull request events
	// See https://docs.github.com/en/actions/writing-workflows/choosing-when-your-workflow-runs/events-that-trigger-workflows#pull_request
	// +optional
	types []string,
	// Run only for pull requests that target specific branches
	// +optional
	branches []string,
	// Run only for pull requests that target specific paths
	// +optional
	paths []string,
) *Workflow {
	if w.Triggers.PullRequest == nil {
		w.Triggers.PullRequest = &api.PullRequestEvent{}
	}
	w.Triggers.PullRequest.Types = append(w.Triggers.PullRequest.Types, types...)
	w.Triggers.PullRequest.Branches = append(w.Triggers.PullRequest.Branches, branches...)
	w.Triggers.PullRequest.Paths = append(w.Triggers.PullRequest.Paths, paths...)
	return w
}

// Add a trigger to execute a Dagger workflow on a git push
func (w *Workflow) onPush(
	// Run only on push to specific branches
	// +optional
	branches []string,
	// Run only on push to specific tags
	// +optional
	tags []string,
	// Run only if the paths match
	// +optional
	paths []string,
) *Workflow {
	if w.Triggers.Push == nil {
		w.Triggers.Push = &api.PushEvent{}
	}
	w.Triggers.Push.Branches = append(w.Triggers.Push.Branches, branches...)
	w.Triggers.Push.Tags = append(w.Triggers.Push.Tags, tags...)
	w.Triggers.Push.Paths = append(w.Triggers.Push.Paths, paths...)
	return w
}

// Add a trigger to execute a Dagger workflow on a schedule time
func (w *Workflow) onSchedule(
	// Cron expressions from https://pubs.opengroup.org/onlinepubs/9699919799/utilities/crontab.html#tag_20_25_07.
	// +optional
	expressions []string,
) *Workflow {
	if w.Triggers.Schedule == nil {
		w.Triggers.Schedule = make([]api.ScheduledEvent, len(expressions))
		for i, expression := range expressions {
			w.Triggers.Schedule[i] = api.ScheduledEvent{Cron: expression}
		}
	}
	return w
}

const configHeader = "# This file was generated. See https://daggerverse.dev/mod/github.com/dagger/dagger/modules/gha"

// A Dagger workflow to be called from a Github Actions configuration
func (w *Workflow) config(
	// Encode all files as JSON (which is also valid YAML)
	// +optional
	asJSON bool,
	// File extension to use for generated workflow files
	// +optional
	// +default=".gen.yml"
	fileExtension string,
) *dagger.Directory {
	var contents []byte
	var err error
	if asJSON {
		contents, err = json.MarshalIndent(w.asWorkflow(), "", " ")
	} else {
		contents, err = yaml.Marshal(w.asWorkflow())
	}
	if err != nil {
		panic(err) // this generally only happens with some internal issue
	}

	return dag.
		Directory().
		WithNewFile(
			".github/workflows/"+w.workflowFilename(fileExtension),
			configHeader+"\n"+string(contents),
		)
}

func (w *Workflow) concurrency() *api.WorkflowConcurrency {
	setting := w.PullRequestConcurrency
	if setting == "" || setting == "allow" {
		return nil
	}
	if (setting != "queue") && (setting != "preempt") {
		panic("Unsupported value for 'pullRequestConcurrency': " + setting)
	}
	concurrency := &api.WorkflowConcurrency{
		// If in a pull request: concurrency group is unique to workflow + head branch
		// If NOT in a pull request: concurrency group is unique to run ID -> no grouping
		Group: "${{ github.workflow }}-${{ github.head_ref || github.run_id }}",
	}
	if setting == "preempt" {
		concurrency.CancelInProgress = true
	}
	return concurrency
}

// Generate a GHA workflow from a Dagger workflow definition.
// The workflow will have no triggers, they should be filled separately.
func (w *Workflow) asWorkflow() api.Workflow {
	jobs := map[string]api.Job{}
	for _, job := range w.Jobs {
		steps := []api.JobStep{}
		for _, cmd := range job.SetupCommands {
			steps = append(steps, api.JobStep{
				Name:  cmd,
				Shell: "bash",
				Run:   cmd,
			})
		}
		// HACK: this *isn't* required! we load the module using DAGGER_MODULE
		// directly from git, *but* this is currently required so that we can
		// get the right labels :(
		steps = append(steps, job.checkoutStep())
		callStep := job.callDaggerStep()
		steps = append(steps, callStep)
		if job.StopEngine {
			steps = append(steps, job.stopEngineStep())
		}
		for _, cmd := range job.TeardownCommands {
			steps = append(steps, api.JobStep{
				Name:  cmd,
				Shell: "bash",
				Run:   cmd,
			})
		}

		jobs[idify(job.Name)] = api.Job{
			// The job name is used by the "required checks feature" in branch protection rules
			Name:           job.Name,
			Condition:      job.Condition,
			RunsOn:         job.Runner,
			Steps:          steps,
			TimeoutMinutes: job.TimeoutMinutes,
		}
	}
	return api.Workflow{
		Name:        w.Name,
		On:          w.Triggers,
		Concurrency: w.concurrency(),
		Jobs:        jobs,
		Permissions: w.Permissions.Permissions(),
	}
}

func (w *Workflow) workflowFilename(fileExtension string) string {
	return idify(w.Name) + fileExtension
}

func (w *Workflow) applyDefaults(other *Workflow) *Workflow {
	if other == nil {
		return w
	}

	mergeDefault(&w.Triggers, other.Triggers)
	setDefault(&w.PullRequestConcurrency, other.PullRequestConcurrency)
	mergeDefault(&w.Permissions, other.Permissions)
	return w
}
