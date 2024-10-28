// Manage Github Actions configurations with Dagger
//
// Daggerizing your CI makes your YAML configurations smaller, but they still exist,
// and they're still a pain to maintain by hand.
//
// This module aims to finish the job, by letting you generate your remaining
// YAML configuration from a Dagger pipeline, written in your favorite language.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dagger/dagger/modules/gha/internal/dagger"
	"golang.org/x/mod/semver"
	"mvdan.cc/sh/shell"
)

func New(
	// Disable sending traces to Dagger Cloud
	// +optional
	noTraces bool,
	// Public Dagger Cloud token, for open-source projects. DO NOT PASS YOUR PRIVATE DAGGER CLOUD TOKEN!
	// This is for a special "public" token which can safely be shared publicly.
	// To get one, contact support@dagger.io
	// +optional
	publicToken string,
	// Dagger version to run in the Github Actions pipelines
	// +optional
	// +default="latest"
	daggerVersion string,
	// Explicitly stop the Dagger Engine after completing the pipeline
	// +optional
	stopEngine bool,
	// Encode all files as JSON (which is also valid YAML)
	// +optional
	asJSON bool,
	// Configure a default runner for all workflows
	// See https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/using-self-hosted-runners-in-a-workflow
	// +optional
	runner []string,
	// File extension to use for generated workflow files
	// +optional
	// +default=".gen.yml"
	fileExtension string,
	// Existing repository root, to merge existing content
	// +optional
	// +ignore=["!.github"]
	repository *dagger.Directory,
	// Default timeout for CI jobs, in minutes
	// +optional
	timeoutMinutes int,
) *Gha {
	if runner == nil {
		runner = []string{"ubuntu-latest"}
	}

	return &Gha{Settings: Settings{
		PublicToken:    publicToken,
		NoTraces:       noTraces,
		DaggerVersion:  daggerVersion,
		StopEngine:     stopEngine,
		AsJSON:         asJSON,
		Runner:         runner,
		FileExtension:  fileExtension,
		Repository:     repository,
		TimeoutMinutes: timeoutMinutes,
	}}
}

type Gha struct {
	// +private
	Pipelines []*Pipeline
	// Settings for this Github Actions project
	Settings Settings
}

type Settings struct {
	PublicToken            string
	DaggerVersion          string
	NoTraces               bool
	StopEngine             bool
	AsJSON                 bool
	Runner                 []string
	PullRequestConcurrency string
	Debug                  bool
	FileExtension          string
	Repository             *dagger.Directory
	TimeoutMinutes         int
	Permissions            Permissions
}

// Validate a Github Actions configuration (best effort)
func (m *Gha) Validate(ctx context.Context, repo *dagger.Directory) (*Gha, error) {
	for _, p := range m.Pipelines {
		if err := p.Check(ctx, repo); err != nil {
			return m, err
		}
	}
	return m, nil
}

// Export the configuration to a .github directory
func (m *Gha) Config(ctx context.Context) *dagger.Directory {
	return m.
		otherWorkflows(ctx).
		WithDirectory(".", m.generatedWorkflows()).
		WithDirectory(".", m.gitAttributes(ctx))
}

func (m *Gha) otherWorkflows(ctx context.Context) *dagger.Directory {
	dir := dag.Directory()
	if repo := m.Settings.Repository; repo != nil {
		if filenames, err := repo.Directory(".github/workflows").Entries(ctx); err == nil {
			for _, filename := range filenames {
				workflow := repo.File(".github/workflows/" + filename)
				if contents, err := repo.File(".github/workflows/" + filename).Contents(ctx); err == nil {
					if !strings.HasPrefix(contents, "# This file was generated.") {
						dir = dir.WithFile(".github/workflows/"+filename, workflow)
					}
				}
			}
		}
	}
	return dir
}

func (m *Gha) generatedWorkflows() *dagger.Directory {
	dir := dag.Directory()
	for _, p := range m.Pipelines {
		dir = dir.WithDirectory(".", p.Config())
	}
	return dir
}

func (m *Gha) gitAttributes(ctx context.Context) *dagger.Directory {
	// Need a custom file extension to match generated files in .gitattributes
	if ext := m.Settings.FileExtension; ext == ".yml" || ext == ".yaml" {
		return dag.Directory()
	}
	repo := m.Settings.Repository
	// Need access to the existing .gitattributes, to avoid appending the same line multiple times
	if repo == nil {
		return dag.Directory()
	}
	attributes, err := repo.File(".github/.gitattributes").Contents(ctx)
	// Need access to the existing .gitattributes, to avoid appending the same line multiple times
	if err != nil {
		// FIXME: differentiate between file not found and other errors. I can never remember how
		return dag.Directory()
	}
	return dag.
		Directory().
		WithNewFile(
			".github/.gitattributes",
			appendOnce(attributes, "**"+m.Settings.FileExtension+" linguist-generated"),
		)
}

// Append a line to a string, only if it doesn't already exist
func appendOnce(s, line string) string {
	if !lineMatch(s, line) {
		return s + "\n" + line + "\n"
	}
	return s
}

// Check if a string contains a line
func lineMatch(s, line string) bool {
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == line {
			return true
		}
	}
	return false
}

// Add a pipeline
//
//nolint:gocyclo
func (m *Gha) WithPipeline(
	// Pipeline name
	name string,
	// The Dagger command to execute
	// Example 'build --source=.'
	command string,
	// The Dagger module to load
	// +optional
	module string,
	// Dispatch jobs to the given runner
	// Example: ["ubuntu-latest"]
	// +optional
	runner []string,
	// Github secrets to inject into the pipeline environment.
	// For each secret, an env variable with the same name is created.
	// Example: ["PROD_DEPLOY_TOKEN", "PRIVATE_SSH_KEY"]
	// +optional
	secrets []string,
	// Use a sparse git checkout, only including the given paths
	// Example: ["src", "tests", "Dockerfile"]
	// +optional
	sparseCheckout []string,
	// (DEPRECATED) allow this pipeline to be manually "dispatched"
	// +optional
	// +deprecated
	//nolint: unparam
	dispatch bool,
	// Disable manual "dispatch" of this pipeline
	// +optional
	noDispatch bool,
	// Enable lfs on git checkout
	// +optional
	lfs bool,
	// Run the pipeline in debug mode
	// +optional
	debug bool,
	// Dagger version to run this pipeline
	// +optional
	daggerVersion string,
	// The maximum number of minutes to run the pipeline before killing the process
	// +optional
	timeoutMinutes int,
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
	// Configure this pipeline's concurrency for each PR.
	// This is triggered when the pipeline is scheduled concurrently on the same PR.
	//   - allow: all instances are allowed to run concurrently
	//   - queue: new instances are queued, and run sequentially
	//   - preempt: new instances run immediately, older ones are canceled
	// Possible values: "allow", "preempt", "queue"
	// +optional
	// +default="allow"
	pullRequestConcurrency string,
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
) *Gha {
	p := &Pipeline{
		Name:           name,
		Command:        command,
		Module:         module,
		Secrets:        secrets,
		SparseCheckout: sparseCheckout,
		LFS:            lfs,
		Settings:       m.Settings,
	}
	if !noDispatch {
		p.Triggers.WorkflowDispatch = &WorkflowDispatchEvent{}
	}
	if pullRequestConcurrency != "" {
		p.Settings.PullRequestConcurrency = pullRequestConcurrency
	}
	if permissions != nil {
		p.Settings.Permissions = permissions
	}
	if debug {
		p.Settings.Debug = debug
	}
	if daggerVersion != "" {
		p.Settings.DaggerVersion = daggerVersion
	}
	if runner != nil {
		p.Settings.Runner = runner
	}
	if timeoutMinutes != 0 {
		p.Settings.TimeoutMinutes = timeoutMinutes
	}
	if onIssueComment {
		p = p.OnIssueComment(nil)
	}
	if onIssueCommentCreated {
		p = p.OnIssueComment([]string{"created"})
	}
	if onIssueCommentDeleted {
		p = p.OnIssueComment([]string{"deleted"})
	}
	if onIssueCommentEdited {
		p = p.OnIssueComment([]string{"edited"})
	}
	if onPullRequest {
		p = p.OnPullRequest(nil, nil, nil)
	}
	if onPullRequestBranches != nil {
		p = p.OnPullRequest(nil, onPullRequestBranches, nil)
	}
	if onPullRequestPaths != nil {
		p = p.OnPullRequest([]string{"paths"}, nil, onPullRequestPaths)
	}
	if onPullRequestAssigned {
		p = p.OnPullRequest([]string{"assigned"}, nil, nil)
	}
	if onPullRequestUnassigned {
		p = p.OnPullRequest([]string{"unassigned"}, nil, nil)
	}
	if onPullRequestLabeled {
		p = p.OnPullRequest([]string{"labeled"}, nil, nil)
	}
	if onPullRequestUnlabeled {
		p = p.OnPullRequest([]string{"unlabeled"}, nil, nil)
	}
	if onPullRequestOpened {
		p = p.OnPullRequest([]string{"opened"}, nil, nil)
	}
	if onPullRequestEdited {
		p = p.OnPullRequest([]string{"edited"}, nil, nil)
	}
	if onPullRequestClosed {
		p = p.OnPullRequest([]string{"closed"}, nil, nil)
	}
	if onPullRequestReopened {
		p = p.OnPullRequest([]string{"reopened"}, nil, nil)
	}
	if onPullRequestSynchronize {
		p = p.OnPullRequest([]string{"synchronize"}, nil, nil)
	}
	if onPullRequestConvertedToDraft {
		p = p.OnPullRequest([]string{"converted_to_draft"}, nil, nil)
	}
	if onPullRequestLocked {
		p = p.OnPullRequest([]string{"locked"}, nil, nil)
	}
	if onPullRequestUnlocked {
		p = p.OnPullRequest([]string{"unlocked"}, nil, nil)
	}
	if onPullRequestEnqueued {
		p = p.OnPullRequest([]string{"enqueued"}, nil, nil)
	}
	if onPullRequestDequeued {
		p = p.OnPullRequest([]string{"dequeued"}, nil, nil)
	}
	if onPullRequestMilestoned {
		p = p.OnPullRequest([]string{"milestoned"}, nil, nil)
	}
	if onPullRequestDemilestoned {
		p = p.OnPullRequest([]string{"demilestoned"}, nil, nil)
	}
	if onPullRequestReadyForReview {
		p = p.OnPullRequest([]string{"ready_for_review"}, nil, nil)
	}
	if onPullRequestReviewRequested {
		p = p.OnPullRequest([]string{"review_requested"}, nil, nil)
	}
	if onPullRequestReviewRequestRemoved {
		p = p.OnPullRequest([]string{"review_request_removed"}, nil, nil)
	}
	if onPullRequestAutoMergeEnabled {
		p = p.OnPullRequest([]string{"auto_merge_enabled"}, nil, nil)
	}
	if onPullRequestAutoMergeDisabled {
		p = p.OnPullRequest([]string{"auto_merge_disabled"}, nil, nil)
	}
	if onPush {
		p = p.OnPush(nil, nil)
	}
	if onPushBranches != nil {
		p = p.OnPush(onPushBranches, nil)
	}
	if onPushTags != nil {
		p = p.OnPush(nil, onPushTags)
	}
	if onSchedule != nil {
		p = p.OnSchedule(onSchedule)
	}
	m.Pipelines = append(m.Pipelines, p)
	return m
}

func (p *Pipeline) OnIssueComment(
	// Run only for certain types of issue comment events
	// See https://docs.github.com/en/actions/writing-workflows/choosing-when-your-workflow-runs/events-that-trigger-workflows#issue_comment
	// +optional
	types []string,
) *Pipeline {
	if p.Triggers.IssueComment == nil {
		p.Triggers.IssueComment = &IssueCommentEvent{}
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
		p.Triggers.PullRequest = &PullRequestEvent{}
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
		p.Triggers.Push = &PushEvent{}
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
		p.Triggers.Schedule = make([]ScheduledEvent, len(expressions))
		for i, expression := range expressions {
			p.Triggers.Schedule[i] = ScheduledEvent{Cron: expression}
		}
	}
	return p
}

// A Dagger pipeline to be called from a Github Actions configuration
type Pipeline struct {
	// +private
	Name string
	// +private
	Module string
	// +private
	Command string
	// +private
	Secrets []string
	// +private
	SparseCheckout []string
	// +private
	LFS bool
	// +private
	Settings Settings
	// +private
	Triggers WorkflowTriggers
}

func (p *Pipeline) Config() *dagger.Directory {
	return p.asWorkflow().Config(p.workflowFilename(), p.Settings.AsJSON)
}

func (p *Pipeline) concurrency() *WorkflowConcurrency {
	setting := p.Settings.PullRequestConcurrency
	if setting == "" || setting == "allow" {
		return nil
	}
	if (setting != "queue") && (setting != "preempt") {
		panic("Unsupported value for 'pullRequestConcurrency': " + setting)
	}
	concurrency := &WorkflowConcurrency{
		// If in a pull request: concurrency group is unique to workflow + head branch
		// If NOT in a pull request: concurrency group is unique to run ID -> no grouping
		Group: "${{ github.workflow }}-${{ github.head_ref || github.run_id }}",
	}
	if setting == "preempt" {
		concurrency.CancelInProgress = true
	}
	return concurrency
}

func (p *Pipeline) checkSecretNames() error {
	// check if the secret name contains only alphanumeric characters and underscores.
	validName := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	for _, secretName := range p.Secrets {
		if !validName.MatchString(secretName) {
			return errors.New("invalid secret name: '" + secretName + "' must contain only alphanumeric characters and underscores")
		}
	}
	return nil
}

func (p *Pipeline) checkCommandAndModule(ctx context.Context, repo *dagger.Directory) error {
	script := "dagger call"
	if p.Module != "" {
		script = script + " -m '" + p.Module + "' "
	}
	script = script + p.Command + " --help"
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
	if err := p.checkSecretNames(); err != nil {
		return err
	}
	if err := p.checkCommandAndModule(ctx, repo); err != nil {
		return err
	}
	return nil
}

// Generate a GHA workflow from a Dagger pipeline definition.
// The workflow will have no triggers, they should be filled separately.
func (p *Pipeline) asWorkflow() Workflow {
	var steps []JobStep
	// FIXME: make checkout configurable
	steps = append(steps, p.checkoutStep())
	steps = append(steps, p.installDaggerSteps()...)
	steps = append(steps, p.warmEngineStep(), p.callDaggerStep())
	if p.Settings.StopEngine {
		steps = append(steps, p.stopEngineStep())
	}
	return Workflow{
		Name:        p.Name,
		On:          p.Triggers,
		Concurrency: p.concurrency(),
		Jobs: map[string]Job{
			p.jobID(): {
				// The job name is used by the "required checks feature" in branch protection rules
				Name:           p.Name,
				RunsOn:         p.Settings.Runner,
				Permissions:    p.JobPermissions(),
				Steps:          steps,
				TimeoutMinutes: p.Settings.TimeoutMinutes,
				Outputs: map[string]string{
					"stdout": "${{ steps.exec.outputs.stdout }}",
					"stderr": "${{ steps.exec.outputs.stderr }}",
				},
			},
		},
	}
}

func (p *Pipeline) JobPermissions() *JobPermissions {
	return p.Settings.Permissions.JobPermissions()
}

func (p *Pipeline) workflowFilename() string {
	var name string
	// Convert to lowercase
	name = strings.ToLower(p.Name)
	// Replace spaces and special characters with hyphens
	re := regexp.MustCompile(`[^a-z0-9]+`)
	name = re.ReplaceAllString(name, "-")
	// Trim leading and trailing hyphens
	name = strings.Trim(name, "-")
	// Add the file extension
	return name + p.Settings.FileExtension
}

func (p *Pipeline) jobID() string {
	return "dagger"
}

func (p *Pipeline) checkoutStep() JobStep {
	step := JobStep{
		Name: "Checkout",
		Uses: "actions/checkout@v4",
		With: map[string]string{},
	}
	if p.SparseCheckout != nil {
		// Include common dagger paths in the checkout, to make
		// sure local modules work by default
		// FIXME: this is only a guess, we need the 'source' field of dagger.json
		//  to be sure.
		sparseCheckout := append([]string{}, p.SparseCheckout...)
		sparseCheckout = append(sparseCheckout, "dagger.json", ".dagger", "dagger", "ci")
		step.With["sparse-checkout"] = strings.Join(sparseCheckout, "\n")
	}
	if p.LFS {
		step.With["lfs"] = "true"
	}
	return step
}

func (p *Pipeline) warmEngineStep() JobStep {
	return p.bashStep("warm-engine", nil)
}

func (p *Pipeline) installDaggerSteps() []JobStep {
	if v := p.Settings.DaggerVersion; (v == "latest") || (semver.IsValid(v)) {
		return []JobStep{
			p.bashStep("install-dagger", map[string]string{"DAGGER_VERSION": v}),
		}
	}
	// Interpret dagger version as a local source, and build it (dev engine)
	return []JobStep{
		// Install latest dagger to bootstrap dev dagger
		// FIXME: let's daggerize this, using dagger in dagger :)
		p.bashStep("install-dagger", map[string]string{"DAGGER_VERSION": "latest"}),
		{
			Name: "Install go",
			Uses: "actions/setup-go@v5",
			With: map[string]string{
				"go-version":            "1.23",
				"cache-dependency-path": ".dagger/go.sum",
			},
		},
		p.bashStep("start-dev-dagger", map[string]string{
			"DAGGER_SOURCE": p.Settings.DaggerVersion,
			// create separate outputs and containers for each job run (to prevent
			// collisions with shared docker containers).
			"_EXPERIMENTAL_DAGGER_DEV_OUTPUT":    "./bin/dev-${{ github.run_id }}",
			"_EXPERIMENTAL_DAGGER_DEV_CONTAINER": "dagger-engine.dev-${{ github.run_id }}di",
		}),
	}
}

// Analyze the pipeline command, and return a list of env variables it references
func (p *Pipeline) envLookups() []string {
	var lookups = make(map[string]interface{})
	_, err := shell.Expand(p.Command, func(name string) string {
		lookups[name] = nil
		return name
	})
	if err != nil {
		// An error might mean an invalid command OR a bug or incomatibility in our parser,
		// let's not surface it for now.
		return nil
	}
	result := make([]string, 0, len(lookups))
	for name := range lookups {
		if name == "IFS" {
			continue
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (p *Pipeline) callDaggerStep() JobStep {
	env := map[string]string{}
	// Debug mode
	if p.Settings.Debug {
		env["DEBUG"] = "1"
	}
	// Inject dagger command
	env["COMMAND"] = "dagger call -q " + p.Command
	// Inject user-defined secrets
	for _, secretName := range p.Secrets {
		env[secretName] = fmt.Sprintf("${{ secrets.%s }}", secretName)
	}
	// Inject module name
	if p.Module != "" {
		env["DAGGER_MODULE"] = p.Module
	}
	// Inject Dagger Cloud token
	if !p.Settings.NoTraces {
		if p.Settings.PublicToken != "" {
			env["DAGGER_CLOUD_TOKEN"] = p.Settings.PublicToken
			// For backwards compatibility with older engines
			env["_EXPERIMENTAL_DAGGER_CLOUD_TOKEN"] = p.Settings.PublicToken
		} else {
			env["DAGGER_CLOUD_TOKEN"] = "${{ secrets.DAGGER_CLOUD_TOKEN }}"
			// For backwards compatibility with older engines
			env["_EXPERIMENTAL_DAGGER_CLOUD_TOKEN"] = "${{ secrets.DAGGER_CLOUD_TOKEN }}"
		}
	}
	for _, key := range p.envLookups() {
		if strings.HasPrefix(key, "GITHUB_") {
			// Inject Github context keys
			// github.ref becomes $GITHUB_REF, etc.
			env[key] = fmt.Sprintf("${{ github.%s }}", strings.ToLower(key))
		} else if strings.HasPrefix(key, "RUNNER_") {
			// Inject Runner context keys
			// runner.ref becomes $RUNNER_REF, etc.
			env[key] = fmt.Sprintf("${{ runner.%s }}", strings.ToLower(key))
		}
	}
	return p.bashStep("exec", env)
}

func (p *Pipeline) stopEngineStep() JobStep {
	return p.bashStep("scripts/stop-engine.sh", nil)
}

// Return a github actions step which executes the script embedded at scripts/<filename>.sh
// The script must be checked in with the module source code.
func (p *Pipeline) bashStep(id string, env map[string]string) JobStep {
	filename := "scripts/" + id + ".sh"
	script, err := dag.
		CurrentModule().
		Source().
		File(filename).
		Contents(context.Background())
	if err != nil {
		// We skip error checking for simplicity
		// (don't want to plumb error checking everywhere)
		panic(err)
	}
	return JobStep{
		Name:  filename,
		ID:    id,
		Shell: "bash",
		Run:   script,
		Env:   env,
	}
}
