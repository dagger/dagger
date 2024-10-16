// Manage Github Actions configurations with Dagger
//
// Daggerizing your CI makes your YAML configurations smaller, but they still exist,
// and they're still a pain to maintain by hand.
//
// This module aims to finish the job, by letting you generate your remaining
// YAML configuration from a Dagger pipeline, written in your favorite language.
package main

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/dagger/dagger/modules/gha/api"
	"github.com/dagger/dagger/modules/gha/internal/dagger"
	"github.com/iancoleman/strcase"
)

type Gha struct{}

type Settings struct{}

type Pipeline struct {
	Name string
	Jobs []Job
	// +private
	Triggers               api.WorkflowTriggers
	PullRequestConcurrency string
	// +private
	Permissions Permissions
}

func (m *Gha) Generate(pipelines []*Pipeline,
	// +optional
	asJSON bool,
	// +optional
	// +default=".gen.yml"
	fileExtension string,
) *dagger.Directory {
	dir := dag.Directory()
	for _, p := range pipelines {
		dir = dir.WithDirectory(".", p.Config(asJSON, fileExtension))
	}
	return dir.WithFile(".github/workflows/.gitattributes", m.gitAttributes(fileExtension))
}

func (m *Gha) gitAttributes(fileExtension string) *dagger.File {
	// Need a custom file extension to match generated files in .gitattributes
	if ext := fileExtension; ext == ".yml" || ext == ".yaml" {
		return nil
	}

	return dag.
		Directory().
		WithNewFile(
			".gitattributes",
			"**"+fileExtension+" linguist-generated").
		File(".gitattributes")
}

type Job struct {
	Name    string
	Command string

	// The maximum number of minutes to run the pipeline before killing the process
	TimeoutMinutes int
	// Run the pipeline in debug mode
	Debug bool
	// Use a sparse git checkout, only including the given paths
	// Example: ["src", "tests", "Dockerfile"]
	SparseCheckout []string
	// Enable lfs on git checkout
	LFS bool
	// Github secrets to inject into the pipeline environment.
	// For each secret, an env variable with the same name is created.
	// Example: ["PROD_DEPLOY_TOKEN", "PRIVATE_SSH_KEY"]
	Secrets []string
	// Dispatch jobs to the given runner
	// Example: ["ubuntu-latest"]
	Runner []string
	// The Dagger module to load
	Module string
	// Dagger version to run this pipeline
	DaggerVersion string
	// Public Dagger Cloud token, for open-source projects. DO NOT PASS YOUR PRIVATE DAGGER CLOUD TOKEN!
	// This is for a special "public" token which can safely be shared publicly.
	// To get one, contact support@dagger.io
	PublicToken string
	// Explicitly stop the dagger engine after completing the pipeline.
	StopEngine bool
}

func (p *Pipeline) WithJob(
	name string,
	command string,

	// Public Dagger Cloud token, for open-source projects. DO NOT PASS YOUR PRIVATE DAGGER CLOUD TOKEN!
	// This is for a special "public" token which can safely be shared publicly.
	// To get one, contact support@dagger.io
	// +optional
	publicToken string,
	// Explicitly stop the dagger engine after completing the pipeline.
	// +optional
	stopEngine bool,
	// The maximum number of minutes to run the pipeline before killing the process
	// +optional
	timeoutMinutes int,
	// Run the pipeline in debug mode
	// +optional
	debug bool,
	// Use a sparse git checkout, only including the given paths
	// Example: ["src", "tests", "Dockerfile"]
	// +optional
	sparseCheckout []string,
	// Enable lfs on git checkout
	// +optional
	lfs bool,
	// Github secrets to inject into the pipeline environment.
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
	// Dagger version to run this pipeline
	// +optional
	daggerVersion string,
) *Pipeline {
	p.Jobs = append(p.Jobs, Job{
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
	})
	return p
}

// Add a pipeline
//
//nolint:gocyclo
func (m *Gha) Pipeline(
	// Pipeline name
	name string,
	// Configure this pipeline's concurrency for each PR.
	// This is triggered when the pipeline is scheduled concurrently on the same PR.
	//   - allow: all instances are allowed to run concurrently
	//   - queue: new instances are queued, and run sequentially
	//   - preempt: new instances run immediately, older ones are canceled
	// Possible values: "allow", "preempt", "queue"
	// +optional
	// +default="allow"
	pullRequestConcurrency string,
	// Disable manual "dispatch" of this pipeline
	// +optional
	noDispatch bool,
	// Permissions to grant the pipeline
	// +optional
	permissions Permissions,
	// Run the pipeline on any issue comment activity
	// +optional
	onIssueComment bool,
	// +optional
	onIssueCommentCreated bool,
	// +optional
	onIssueCommentEdited bool,
	// +optional
	onIssueCommentDeleted bool,
	// Run the pipeline on any pull request activity
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
	// Run the pipeline on any git push
	// +optional
	onPush bool,
	// Run the pipeline on git push to the specified tags
	// +optional
	onPushTags []string,
	// Run the pipeline on git push to the specified branches
	// +optional
	onPushBranches []string,
	// Run the pipeline at a schedule time
	// +optional
	onSchedule []string,
) *Pipeline {
	p := &Pipeline{
		Name: name,
		Jobs: []Job{},
	}

	if !noDispatch {
		p.Triggers.WorkflowDispatch = &api.WorkflowDispatchEvent{}
	}
	if pullRequestConcurrency != "" {
		p.PullRequestConcurrency = pullRequestConcurrency
	}
	if permissions != nil {
		p.Permissions = permissions
	}
	if onIssueComment {
		p.OnIssueComment(nil)
	}
	if onIssueCommentCreated {
		p.OnIssueComment([]string{"created"})
	}
	if onIssueCommentDeleted {
		p.OnIssueComment([]string{"deleted"})
	}
	if onIssueCommentEdited {
		p.OnIssueComment([]string{"edited"})
	}
	if onPullRequest {
		p.OnPullRequest(nil, nil, nil)
	}
	if onPullRequestBranches != nil {
		p.OnPullRequest(nil, onPullRequestBranches, nil)
	}
	if onPullRequestPaths != nil {
		p.OnPullRequest([]string{"paths"}, nil, onPullRequestPaths)
	}
	if onPullRequestAssigned {
		p.OnPullRequest([]string{"assigned"}, nil, nil)
	}
	if onPullRequestUnassigned {
		p.OnPullRequest([]string{"unassigned"}, nil, nil)
	}
	if onPullRequestLabeled {
		p.OnPullRequest([]string{"labeled"}, nil, nil)
	}
	if onPullRequestUnlabeled {
		p.OnPullRequest([]string{"unlabeled"}, nil, nil)
	}
	if onPullRequestOpened {
		p.OnPullRequest([]string{"opened"}, nil, nil)
	}
	if onPullRequestEdited {
		p.OnPullRequest([]string{"edited"}, nil, nil)
	}
	if onPullRequestClosed {
		p.OnPullRequest([]string{"closed"}, nil, nil)
	}
	if onPullRequestReopened {
		p.OnPullRequest([]string{"reopened"}, nil, nil)
	}
	if onPullRequestSynchronize {
		p.OnPullRequest([]string{"synchronize"}, nil, nil)
	}
	if onPullRequestConvertedToDraft {
		p.OnPullRequest([]string{"converted_to_draft"}, nil, nil)
	}
	if onPullRequestLocked {
		p.OnPullRequest([]string{"locked"}, nil, nil)
	}
	if onPullRequestUnlocked {
		p.OnPullRequest([]string{"unlocked"}, nil, nil)
	}
	if onPullRequestEnqueued {
		p.OnPullRequest([]string{"enqueued"}, nil, nil)
	}
	if onPullRequestDequeued {
		p.OnPullRequest([]string{"dequeued"}, nil, nil)
	}
	if onPullRequestMilestoned {
		p.OnPullRequest([]string{"milestoned"}, nil, nil)
	}
	if onPullRequestDemilestoned {
		p.OnPullRequest([]string{"demilestoned"}, nil, nil)
	}
	if onPullRequestReadyForReview {
		p.OnPullRequest([]string{"ready_for_review"}, nil, nil)
	}
	if onPullRequestReviewRequested {
		p.OnPullRequest([]string{"review_requested"}, nil, nil)
	}
	if onPullRequestReviewRequestRemoved {
		p.OnPullRequest([]string{"review_request_removed"}, nil, nil)
	}
	if onPullRequestAutoMergeEnabled {
		p.OnPullRequest([]string{"auto_merge_enabled"}, nil, nil)
	}
	if onPullRequestAutoMergeDisabled {
		p.OnPullRequest([]string{"auto_merge_disabled"}, nil, nil)
	}
	if onPush {
		p.OnPush(nil, nil)
	}
	if onPushBranches != nil {
		p.OnPush(onPushBranches, nil)
	}
	if onPushTags != nil {
		p.OnPush(nil, onPushTags)
	}
	if onSchedule != nil {
		p.OnSchedule(onSchedule)
	}
	return p
}

func (p *Pipeline) OnIssueComment(
	// Run only for certain types of issue comment events
	// See https://docs.github.com/en/actions/writing-workflows/choosing-when-your-workflow-runs/events-that-trigger-workflows#issue_comment
	// +optional
	types []string,
) *Pipeline {
	if p.Triggers.IssueComment == nil {
		p.Triggers.IssueComment = &api.IssueCommentEvent{}
	}
	p.Triggers.IssueComment.Types = append(p.Triggers.IssueComment.Types, types...)
	return p
}

// Add a trigger to execute a Dagger pipeline on a pull request
func (p *Pipeline) OnPullRequest(
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
) *Pipeline {
	if p.Triggers.PullRequest == nil {
		p.Triggers.PullRequest = &api.PullRequestEvent{}
	}
	p.Triggers.PullRequest.Types = append(p.Triggers.PullRequest.Types, types...)
	p.Triggers.PullRequest.Branches = append(p.Triggers.PullRequest.Branches, branches...)
	p.Triggers.PullRequest.Paths = append(p.Triggers.PullRequest.Paths, paths...)
	return p
}

// Add a trigger to execute a Dagger pipeline on a git push
func (p *Pipeline) OnPush(
	// Run only on push to specific branches
	// +optional
	branches []string,
	// Run only on push to specific tags
	// +optional
	tags []string,
) *Pipeline {
	if p.Triggers.Push == nil {
		p.Triggers.Push = &api.PushEvent{}
	}
	p.Triggers.Push.Branches = append(p.Triggers.Push.Branches, branches...)
	p.Triggers.Push.Tags = append(p.Triggers.Push.Tags, tags...)
	return p
}

// Add a trigger to execute a Dagger pipeline on a schedule time
func (p *Pipeline) OnSchedule(
	// Cron exressions from https://pubs.opengroup.org/onlinepubs/9699919799/utilities/crontab.html#tag_20_25_07.
	// +optional
	expressions []string,
) *Pipeline {
	if p.Triggers.Schedule == nil {
		p.Triggers.Schedule = make([]api.ScheduledEvent, len(expressions))
		for i, expression := range expressions {
			p.Triggers.Schedule[i] = api.ScheduledEvent{Cron: expression}
		}
	}
	return p
}

// A Dagger pipeline to be called from a Github Actions configuration
func (p *Pipeline) Config(
	// Encode all files as JSON (which is also valid YAML)
	// +optional
	asJSON bool,
	// File extension to use for generated workflow files
	// +optional
	// +default=".gen.yml"
	fileExtension string,
) *dagger.Directory {
	return workflowConfig(p.asWorkflow(), p.workflowFilename(fileExtension), asJSON)
}

func (p *Pipeline) concurrency() *api.WorkflowConcurrency {
	setting := p.PullRequestConcurrency
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

func (j *Job) checkSecretNames() error {
	// check if the secret name contains only alphanumeric characters and underscores.
	validName := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	for _, secretName := range j.Secrets {
		if !validName.MatchString(secretName) {
			return errors.New("invalid secret name: '" + secretName + "' must contain only alphanumeric characters and underscores")
		}
	}
	return nil
}

func (j *Job) checkCommandAndModule(ctx context.Context, repo *dagger.Directory) error {
	script := "dagger call"
	if j.Module != "" {
		script = script + " -m '" + j.Module + "' "
	}
	script = script + j.Command + " --help"
	_, err := dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: []string{"dagger", "bash"},
		}).
		WithMountedDirectory("/src", repo).
		WithWorkdir("/src").
		WithExec(
			[]string{"bash", "-c", script},
			dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
		).
		Sync(ctx)
	return err
}

// Check that the pipeline is valid, in a best effort way
func (p *Pipeline) Check(
	ctx context.Context,
	// +defaultPath="/"
	repo *dagger.Directory,
) error {
	for _, job := range p.Jobs {
		if err := job.checkSecretNames(); err != nil {
			return err
		}
		if err := job.checkCommandAndModule(ctx, repo); err != nil {
			return err
		}
	}
	return nil
}

// Generate a GHA workflow from a Dagger pipeline definition.
// The workflow will have no triggers, they should be filled separately.
func (p *Pipeline) asWorkflow() api.Workflow {
	jobs := map[string]api.Job{}
	for _, job := range p.Jobs {
		steps := []api.JobStep{}
		// FIXME: make checkout configurable
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
		Name:        p.Name,
		On:          p.Triggers,
		Concurrency: p.concurrency(),
		Jobs:        jobs,
		Permissions: p.Permissions.Permissions(),
	}
}

func (p *Pipeline) workflowFilename(fileExtension string) string {
	return idify(p.Name) + fileExtension
}

func idify(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	name = strings.ToLower(re.ReplaceAllString(name, "-"))
	return strcase.ToKebab(name)
}
