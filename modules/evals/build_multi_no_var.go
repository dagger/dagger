package main

import (
	"context"
	"crypto/rand"
	"dagger/evals/internal/dagger"
	"testing"

	"github.com/vito/runt"
)

// BuildMultiNoVar is like BuildMulti but without explicitly referencing the
// relevant objects, leaving the LLM to figure it out.
func (m *Evals) BuildMultiNoVar() *BuildMultiNoVar {
	return &BuildMultiNoVar{}
}

type BuildMultiNoVar struct{}

func (e *BuildMultiNoVar) Name() string {
	return "BuildMultiNoVar"
}

func (e *BuildMultiNoVar) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithEnv(
			dag.Env().
				WithDirectoryInput("notRepo", dag.Directory(), "Bait - ignore this.").
				WithDirectoryInput("repo",
					dag.Git("https://github.com/vito/booklit").Head().Tree(),
					"The Booklit repository.").
				WithContainerInput("notCtr", dag.Container(), "Bait - ignore this.").
				WithContainerInput("ctr",
					dag.Container().
						From("golang").
						WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
						WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
						WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
						WithEnvVariable("GOCACHE", "/go/build-cache").
						WithEnvVariable("BUSTER", rand.Text()),
					"The Go container to use to build Booklit.").
				WithFileOutput("bin", "The /out/booklit binary."),
		).
		WithPrompt("Mount my repo into the container, set it as your workdir, and build ./cmd/booklit with the CGO_ENABLED env var set to 0, writing it to /out/booklit.").
		Loop()
}

func (e *BuildMultiNoVar) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		buildMultiAssert(ctx, t, prompt)
	})
}
