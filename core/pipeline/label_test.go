package pipeline_test

import (
	"os"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/stretchr/testify/require"
)

func TestLoadGitHubLabels(t *testing.T) {
	type Example struct {
		Name   string
		Env    []string
		Labels []pipeline.Label
	}

	for _, example := range []Example{
		{
			Name: "workflow_dispatch",
			Env: []string{
				"GITHUB_ACTIONS=true",
				"GITHUB_ACTOR=vito",
				"GITHUB_WORKFLOW=some-workflow",
				"GITHUB_JOB=some-job",
				"GITHUB_EVENT_NAME=workflow_dispatch",
				"GITHUB_EVENT_PATH=testdata/workflow_dispatch.json",
			},
			Labels: []pipeline.Label{
				{
					Name:  "github.com/actor",
					Value: "vito",
				},
				{
					Name:  "github.com/event.type",
					Value: "workflow_dispatch",
				},
				{
					Name:  "github.com/workflow.name",
					Value: "some-workflow",
				},
				{
					Name:  "github.com/workflow.job",
					Value: "some-job",
				},
				{
					Name:  "github.com/repo.full_name",
					Value: "dagger/testdata",
				},
				{
					Name:  "github.com/repo.url",
					Value: "https://github.com/dagger/testdata",
				},
			},
		},
		{
			Name: "pull_request.synchronize",
			Env: []string{
				"GITHUB_ACTIONS=true",
				"GITHUB_ACTOR=vito",
				"GITHUB_WORKFLOW=some-workflow",
				"GITHUB_JOB=some-job",
				"GITHUB_EVENT_NAME=pull_request",
				"GITHUB_EVENT_PATH=testdata/pull_request.synchronize.json",
			},
			Labels: []pipeline.Label{
				{
					Name:  "github.com/actor",
					Value: "vito",
				},
				{
					Name:  "github.com/event.type",
					Value: "pull_request",
				},
				{
					Name:  "github.com/workflow.name",
					Value: "some-workflow",
				},
				{
					Name:  "github.com/workflow.job",
					Value: "some-job",
				},
				{
					Name:  "github.com/event.action",
					Value: "synchronize",
				},
				{
					Name:  "github.com/repo.full_name",
					Value: "dagger/testdata",
				},
				{
					Name:  "github.com/repo.url",
					Value: "https://github.com/dagger/testdata",
				},
				{
					Name:  "github.com/pr.number",
					Value: "2018",
				},
				{
					Name:  "github.com/pr.title",
					Value: "dump env, use session binary from submodule",
				},
				{
					Name:  "github.com/pr.url",
					Value: "https://github.com/dagger/testdata/pull/2018",
				},
				{
					Name:  "github.com/pr.head",
					Value: "81be07d3103b512159628bfa3aae2fbb5d255964",
				},
			},
		},
		{
			Name: "push",
			Env: []string{
				"GITHUB_ACTIONS=true",
				"GITHUB_ACTOR=vito",
				"GITHUB_WORKFLOW=some-workflow",
				"GITHUB_JOB=some-job",
				"GITHUB_EVENT_NAME=push",
				"GITHUB_EVENT_PATH=testdata/push.json",
			},
			Labels: []pipeline.Label{
				{
					Name:  "github.com/actor",
					Value: "vito",
				},
				{
					Name:  "github.com/event.type",
					Value: "push",
				},
				{
					Name:  "github.com/workflow.name",
					Value: "some-workflow",
				},
				{
					Name:  "github.com/workflow.job",
					Value: "some-job",
				},
				{
					Name:  "github.com/repo.full_name",
					Value: "vito/bass",
				},
				{
					Name:  "github.com/repo.url",
					Value: "https://github.com/vito/bass",
				},
			},
		},
	} {
		example := example
		t.Run(example.Name, func(t *testing.T) {
			for _, e := range example.Env {
				k, v, _ := strings.Cut(e, "=")
				os.Setenv(k, v)
			}

			labels, err := pipeline.LoadGitHubLabels()
			require.NoError(t, err)
			require.Equal(t, example.Labels, labels)
		})
	}
}
