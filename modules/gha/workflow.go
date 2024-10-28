package main

import (
	"encoding/json"

	"github.com/dagger/dagger/modules/gha/internal/dagger"
	"gopkg.in/yaml.v3"
)

const (
	genHeader = "# This file was generated. See https://daggerverse.dev/mod/github.com/dagger/dagger/modules/gha"
)

type Workflow struct {
	Name        string               `json:"name,omitempty" yaml:"name,omitempty"`
	On          WorkflowTriggers     `json:"on" yaml:"on"`
	Concurrency *WorkflowConcurrency `json:"concurrency,omitempty" yaml:"concurrency,omitempty"`
	Jobs        map[string]Job       `json:"jobs" yaml:"jobs"`
	Env         map[string]string    `json:"env,omitempty" yaml:"env,omitempty"`
}

// Generate an overlay config directory for this workflow
func (w Workflow) Config(
	// Filename of the workflow file under .github/workflows/
	filename string,
	// Encode the workflow as JSON, which is valid YAML
	asJSON bool,
) *dagger.Directory {
	var (
		contents []byte
		err      error
	)
	if asJSON {
		contents, err = json.MarshalIndent(w, "", " ")
	} else {
		contents, err = yaml.Marshal(w)
	}
	if err != nil {
		panic(err)
	}
	return dag.
		Directory().
		WithNewFile(".github/workflows/"+filename, genHeader+"\n"+string(contents))
}

type WorkflowConcurrency struct {
	Group            string `json:"group,omitempty" yaml:"group,omitempty"`
	CancelInProgress bool   `json:"cancel-in-progress,omitempty" yaml:"cancel-in-progress,omitempty"`
}

type WorkflowTriggers struct {
	Push             *PushEvent             `json:"push,omitempty" yaml:"push,omitempty"`
	PullRequest      *PullRequestEvent      `json:"pull_request,omitempty" yaml:"pull_request,omitempty"`
	Schedule         []ScheduledEvent       `json:"schedule,omitempty" yaml:"schedule,omitempty"`
	WorkflowDispatch *WorkflowDispatchEvent `json:"workflow_dispatch,omitempty" yaml:"workflow_dispatch,omitempty"`
	IssueComment     *IssueCommentEvent     `json:"issue_comment,omitempty" yaml:"issue_comment,omitempty"`
}

type PushEvent struct {
	Branches []string `json:"branches,omitempty" yaml:"branches,omitempty"`
	Tags     []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Paths    []string `json:"paths,omitempty" yaml:"paths,omitempty"`
}

type PullRequestEvent struct {
	Types    []string `json:"types,omitempty" yaml:"types,omitempty"`
	Branches []string `json:"branches,omitempty" yaml:"branches,omitempty"`
	Paths    []string `json:"paths,omitempty" yaml:"paths,omitempty"`
}

type ScheduledEvent struct {
	Cron string `json:"cron" yaml:"cron"`
}

type WorkflowDispatchEvent struct {
	// FIXME: The Dagger API can't serialize maps
	// Inputs map[string]DispatchInput `json:"inputs,omitempty" yaml:"inputs,omitempty"`
}

type IssueCommentEvent struct {
	Types []string `json:"types,omitempty" yaml:"types,omitempty"`
}

type DispatchInput struct {
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool   `json:"required,omitempty" yaml:"required,omitempty"`
	Default     string `json:"default,omitempty" yaml:"default,omitempty"`
}

type Job struct {
	RunsOn         []string          `json:"runs-on" yaml:"runs-on"`
	Permissions    *JobPermissions   `json:"permissions,omitempty" yaml:"permissions,omitempty"`
	Name           string            `json:"name" yaml:"name"`
	Needs          []string          `json:"needs,omitempty" yaml:"needs,omitempty"`
	Steps          []JobStep         `json:"steps" yaml:"steps"`
	Env            map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Strategy       *Strategy         `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	TimeoutMinutes int               `json:"timeout-minutes,omitempty" yaml:"timeout-minutes,omitempty"`
	Outputs        map[string]string `json:"outputs,omitempty" yaml:"outputs,omitempty"`
}

type JobStep struct {
	Name           string            `json:"name,omitempty" yaml:"name,omitempty"`
	ID             string            `json:"id,omitempty" yaml:"id,omitempty"`
	Uses           string            `json:"uses,omitempty" yaml:"uses,omitempty"`
	Run            string            `json:"run,omitempty" yaml:"run,omitempty"`
	With           map[string]string `json:"with,omitempty" yaml:"with,omitempty"`
	Env            map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	TimeoutMinutes int               `json:"timeout-minutes,omitempty" yaml:"timeout-minutes,omitempty"`
	Shell          string            `json:"shell,omitempty" yaml:"shell,omitempty"`
	// Other step-specific fields can be added here...
}

type Strategy struct {
	Matrix      map[string][]string `json:"matrix,omitempty" yaml:"matrix,omitempty"`
	MaxParallel int                 `json:"max-parallel,omitempty" yaml:"max-parallel,omitempty"`
	FailFast    bool                `json:"fail-fast,omitempty" yaml:"fail-fast,omitempty"`
}

// PermissionLevel represents the possible levels of permissions in a job.
type PermissionLevel string

const (
	PermissionRead  PermissionLevel = "read"
	PermissionWrite PermissionLevel = "write"
	PermissionNone  PermissionLevel = "none"
)

// Permissions defines the permission levels for various scopes in a job.
type JobPermissions struct {
	Contents           PermissionLevel `json:"contents,omitempty" yaml:"contents,omitempty"`
	Issues             PermissionLevel `json:"issues,omitempty" yaml:"issues,omitempty"`
	Actions            PermissionLevel `json:"actions,omitempty" yaml:"actions,omitempty"`
	Packages           PermissionLevel `json:"packages,omitempty" yaml:"packages,omitempty"`
	Deployments        PermissionLevel `json:"deployments,omitempty" yaml:"deployments,omitempty"`
	PullRequests       PermissionLevel `json:"pull-requests,omitempty" yaml:"pull-requests,omitempty"`
	Pages              PermissionLevel `json:"pages,omitempty" yaml:"pages,omitempty"`
	IDToken            PermissionLevel `json:"id-token,omitempty" yaml:"id-token,omitempty"`
	RepositoryProjects PermissionLevel `json:"repository-projects,omitempty" yaml:"repository-projects,omitempty"`
	Statuses           PermissionLevel `json:"statuses,omitempty" yaml:"statuses,omitempty"`
	Metadata           PermissionLevel `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Checks             PermissionLevel `json:"checks,omitempty" yaml:"checks,omitempty"`
	Discussions        PermissionLevel `json:"discussions,omitempty" yaml:"discussions,omitempty"`
}
