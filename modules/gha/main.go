// Manage Github Actions configurations with Dagger
//
// Daggerizing your CI makes your YAML configurations smaller, but they still exist,
// and they're still a pain to maintain by hand.
//
// This module aims to finish the job, by letting you generate your remaining
// YAML configuration from a Dagger pipeline, written in your favorite language.
package main

import (
	"encoding/json"
	"regexp"
	"strings"

	"dario.cat/mergo"
	"github.com/iancoleman/strcase"
	"gopkg.in/yaml.v3"

	"github.com/dagger/dagger/modules/gha/api"
	"github.com/dagger/dagger/modules/gha/internal/dagger"
)

type Gha struct{}

type Repository struct {
	Workflows []*Workflow

	JobDefaults      *Job      // +private
	WorkflowDefaults *Workflow // +private
}

func (gha *Gha) Repository(
	jobDefaults *Job, // +optional
	workflowDefaults *Workflow, // +optional
) *Repository {
	return &Repository{
		JobDefaults:      jobDefaults,
		WorkflowDefaults: workflowDefaults,
	}
}

func (r *Repository) WithWorkflow(workflow *Workflow) *Repository {
	workflow = workflow.applyDefaults(r.WorkflowDefaults)
	for i, job := range workflow.Jobs {
		workflow.Jobs[i] = job.applyDefaults(r.JobDefaults)
	}

	r.Workflows = append(r.Workflows, workflow)
	return r
}

func (r *Repository) Generate(
	// +optional
	directory *dagger.Directory,
	// +optional
	asJSON bool,
	// +optional
	// +default=".gen.yml"
	fileExtension string,
) *dagger.Directory {
	if directory == nil {
		directory = dag.Directory()
	}
	directory = directory.With(deleteOldFiles(fileExtension))
	for _, p := range r.Workflows {
		directory = directory.WithDirectory(".", p.config(asJSON, fileExtension))
	}
	directory = directory.With(gitAttributes(fileExtension))
	return directory
}

func gitAttributes(fileExtension string) func(*dagger.Directory) *dagger.Directory {
	// Need a custom file extension to match generated files in .gitattributes
	if ext := fileExtension; ext == ".yml" || ext == ".yaml" {
		return nil
	}

	return func(d *dagger.Directory) *dagger.Directory {
		return d.WithNewFile(
			".github/workflows/.gitattributes",
			"*"+fileExtension+" linguist-generated")
	}
}

func deleteOldFiles(fileExtension string) func(*dagger.Directory) *dagger.Directory {
	// Need a custom file extension to delete old files
	if ext := fileExtension; ext == ".yml" || ext == ".yaml" {
		return nil
	}

	return func(d *dagger.Directory) *dagger.Directory {
		return dag.Directory().WithDirectory("", d, dagger.DirectoryWithDirectoryOpts{
			Exclude: []string{".github/workflows/*" + fileExtension},
		})
	}
}

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

func (w *Workflow) applyDefaults(other *Workflow) *Workflow {
	if other == nil {
		return w
	}

	mergeDefault(&w.Triggers, other.Triggers)
	setDefault(&w.PullRequestConcurrency, other.PullRequestConcurrency)
	mergeDefault(&w.Permissions, other.Permissions)
	return w
}

func (gha *Gha) Job(
	name string,
	command string,

	// Public Dagger Cloud token, for open-source projects. DO NOT PASS YOUR PRIVATE DAGGER CLOUD TOKEN!
	// This is for a special "public" token which can safely be shared publicly.
	// To get one, contact support@dagger.io
	// +optional
	publicToken string,
	// Explicitly stop the dagger engine after completing the workflow.
	// +optional
	stopEngine bool,
	// The maximum number of minutes to run the workflow before killing the process
	// +optional
	timeoutMinutes int,
	// Run the workflow in debug mode
	// +optional
	debug bool,
	// Use a sparse git checkout, only including the given paths
	// Example: ["src", "tests", "Dockerfile"]
	// +optional
	sparseCheckout []string,
	// Enable lfs on git checkout
	// +optional
	lfs bool,
	// Github secrets to inject into the workflow environment.
	// For each secret, an env variable with the same name is created.
	// Example: ["PROD_DEPLOY_TOKEN", "PRIVATE_SSH_KEY"]
	// +optional
	secrets []string,
	// Dispatch jobs to the given runner
	// Example: ["ubuntu-latest"]
	// +optional
	runner []string,
	// The Dagger module to load
	// +optional
	module string,
	// Dagger version to run this workflow
	// +optional
	daggerVersion string,
) *Job {
	return &Job{
		Name:           name,
		PublicToken:    publicToken,
		StopEngine:     stopEngine,
		Command:        command,
		TimeoutMinutes: timeoutMinutes,
		Debug:          debug,
		SparseCheckout: sparseCheckout,
		LFS:            lfs,
		Secrets:        secrets,
		Runner:         runner,
		Module:         module,
		DaggerVersion:  daggerVersion,
	}
}

func (j *Job) applyDefaults(other *Job) *Job {
	if other == nil {
		return j
	}
	setDefault(&j.PublicToken, other.PublicToken)
	setDefault(&j.StopEngine, other.StopEngine)
	setDefault(&j.TimeoutMinutes, other.TimeoutMinutes)
	setDefault(&j.Debug, other.Debug)
	mergeDefault(&j.Runner, other.Runner)
	setDefault(&j.Module, other.Module)
	setDefault(&j.DaggerVersion, other.DaggerVersion)
	return j
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
		w = w.OnIssueComment(nil)
	}
	if onIssueCommentCreated {
		w = w.OnIssueComment([]string{"created"})
	}
	if onIssueCommentDeleted {
		w = w.OnIssueComment([]string{"deleted"})
	}
	if onIssueCommentEdited {
		w = w.OnIssueComment([]string{"edited"})
	}
	if onPullRequest {
		w = w.OnPullRequest(nil, nil, nil)
	}
	if onPullRequestBranches != nil {
		w = w.OnPullRequest(nil, onPullRequestBranches, nil)
	}
	if onPullRequestPaths != nil {
		w = w.OnPullRequest([]string{"paths"}, nil, onPullRequestPaths)
	}
	if onPullRequestAssigned {
		w = w.OnPullRequest([]string{"assigned"}, nil, nil)
	}
	if onPullRequestUnassigned {
		w = w.OnPullRequest([]string{"unassigned"}, nil, nil)
	}
	if onPullRequestLabeled {
		w = w.OnPullRequest([]string{"labeled"}, nil, nil)
	}
	if onPullRequestUnlabeled {
		w = w.OnPullRequest([]string{"unlabeled"}, nil, nil)
	}
	if onPullRequestOpened {
		w = w.OnPullRequest([]string{"opened"}, nil, nil)
	}
	if onPullRequestEdited {
		w = w.OnPullRequest([]string{"edited"}, nil, nil)
	}
	if onPullRequestClosed {
		w = w.OnPullRequest([]string{"closed"}, nil, nil)
	}
	if onPullRequestReopened {
		w = w.OnPullRequest([]string{"reopened"}, nil, nil)
	}
	if onPullRequestSynchronize {
		w = w.OnPullRequest([]string{"synchronize"}, nil, nil)
	}
	if onPullRequestConvertedToDraft {
		w = w.OnPullRequest([]string{"converted_to_draft"}, nil, nil)
	}
	if onPullRequestLocked {
		w = w.OnPullRequest([]string{"locked"}, nil, nil)
	}
	if onPullRequestUnlocked {
		w = w.OnPullRequest([]string{"unlocked"}, nil, nil)
	}
	if onPullRequestEnqueued {
		w = w.OnPullRequest([]string{"enqueued"}, nil, nil)
	}
	if onPullRequestDequeued {
		w = w.OnPullRequest([]string{"dequeued"}, nil, nil)
	}
	if onPullRequestMilestoned {
		w = w.OnPullRequest([]string{"milestoned"}, nil, nil)
	}
	if onPullRequestDemilestoned {
		w = w.OnPullRequest([]string{"demilestoned"}, nil, nil)
	}
	if onPullRequestReadyForReview {
		w = w.OnPullRequest([]string{"ready_for_review"}, nil, nil)
	}
	if onPullRequestReviewRequested {
		w = w.OnPullRequest([]string{"review_requested"}, nil, nil)
	}
	if onPullRequestReviewRequestRemoved {
		w = w.OnPullRequest([]string{"review_request_removed"}, nil, nil)
	}
	if onPullRequestAutoMergeEnabled {
		w = w.OnPullRequest([]string{"auto_merge_enabled"}, nil, nil)
	}
	if onPullRequestAutoMergeDisabled {
		w = w.OnPullRequest([]string{"auto_merge_disabled"}, nil, nil)
	}
	if onPush {
		w = w.OnPush(nil, nil)
	}
	if onPushBranches != nil {
		w = w.OnPush(onPushBranches, nil)
	}
	if onPushTags != nil {
		w = w.OnPush(nil, onPushTags)
	}
	if onSchedule != nil {
		w = w.OnSchedule(onSchedule)
	}
	return w
}

type Job struct {
	Name    string
	Command string

	// The maximum number of minutes to run the workflow before killing the process
	TimeoutMinutes int
	// Run the workflow in debug mode
	Debug bool
	// Use a sparse git checkout, only including the given paths
	// Example: ["src", "tests", "Dockerfile"]
	SparseCheckout []string
	// Enable lfs on git checkout
	LFS bool
	// Github secrets to inject into the workflow environment.
	// For each secret, an env variable with the same name is created.
	// Example: ["PROD_DEPLOY_TOKEN", "PRIVATE_SSH_KEY"]
	Secrets []string
	// Dispatch jobs to the given runner
	// Example: ["ubuntu-latest"]
	Runner []string
	// The Dagger module to load
	Module string
	// Dagger version to run this workflow
	DaggerVersion string
	// Public Dagger Cloud token, for open-source projects. DO NOT PASS YOUR PRIVATE DAGGER CLOUD TOKEN!
	// This is for a special "public" token which can safely be shared publicly.
	// To get one, contact support@dagger.io
	PublicToken string
	// Explicitly stop the dagger engine after completing the workflow.
	StopEngine bool
}

func (w *Workflow) OnIssueComment(
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
func (w *Workflow) OnPullRequest(
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
func (w *Workflow) OnPush(
	// Run only on push to specific branches
	// +optional
	branches []string,
	// Run only on push to specific tags
	// +optional
	tags []string,
) *Workflow {
	if w.Triggers.Push == nil {
		w.Triggers.Push = &api.PushEvent{}
	}
	w.Triggers.Push.Branches = append(w.Triggers.Push.Branches, branches...)
	w.Triggers.Push.Tags = append(w.Triggers.Push.Tags, tags...)
	return w
}

// Add a trigger to execute a Dagger workflow on a schedule time
func (w *Workflow) OnSchedule(
	// Cron exressions from https://pubs.opengroup.org/onlinepubs/9699919799/utilities/crontab.html#tag_20_25_07.
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
		// TODO: make checkout configurable
		steps = append(steps, job.checkoutStep())
		steps = append(steps, job.installDaggerSteps()...)
		steps = append(steps, job.warmEngineStep(), job.callDaggerStep())
		if job.StopEngine {
			steps = append(steps, job.stopEngineStep())
		}

		jobs[idify(job.Name)] = api.Job{
			// The job name is used by the "required checks feature" in branch protection rules
			Name:           job.Name,
			RunsOn:         job.Runner,
			Steps:          steps,
			TimeoutMinutes: job.TimeoutMinutes,
			Outputs: map[string]string{
				"stdout": "${{ steps.exec.outputs.stdout }}",
				"stderr": "${{ steps.exec.outputs.stderr }}", // FIXME: max output size is 1MB
			},
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

func idify(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	name = strings.ToLower(re.ReplaceAllString(name, "-"))
	return strcase.ToKebab(name)
}

func setDefault[T comparable](value *T, defaultValue T) {
	var empty T
	if *value == empty {
		*value = defaultValue
	}
}
func mergeDefault[T any](value *T, defaultValue T) {
	err := mergo.Merge(value, defaultValue)
	if err != nil {
		panic(err)
	}
}
