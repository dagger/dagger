package main

import (
	"context"
	"crypto/rand"
	"dagger/evals/internal/dagger"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test the model's ability to pass objects around to one another and execute a
// series of operations given at once.
func (m *Evals) BuildMulti() *BuildMulti {
	return &BuildMulti{}
}

type BuildMulti struct{}

func (e *BuildMulti) Name() string {
	return "BuildMulti"
}

func (e *BuildMulti) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithEnv(
			dag.Env().
				WithDirectoryInput("repo",
					dag.Git("https://github.com/vito/booklit").Head().Tree(),
					"The Booklit repository.").
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
		WithPrompt("Mount $repo into $ctr at /src, set it as your workdir, and build ./cmd/booklit with the CGO_ENABLED env var set to 0, writing it to /out/booklit.")
}

func (e *BuildMulti) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		buildMultiAssert(ctx, t, prompt)
	})
}

// Extracted for reuse between BuildMulti tests
func buildMultiAssert(ctx context.Context, t testing.TB, llm *dagger.LLM) {
	f, err := llm.Env().Output("bin").AsFile().Sync(ctx)
	require.NoError(t, err)

	history, err := llm.History(ctx)
	require.NoError(t, err)
	if !strings.Contains(strings.Join(history, "\n"), "withEnvVariable") {
		t.Error("should have used Container.withEnvVariable - use the right tool for the job!")
	}

	ctr := dag.Container().
		From("alpine").
		WithFile("/bin/booklit", f).
		WithExec([]string{"chmod", "+x", "/bin/booklit"}).
		WithExec([]string{"/bin/booklit", "--version"})
	out, err := ctr.Stdout(ctx)
	require.NoError(t, err, "command failed - did you forget CGO_ENABLED=0?")

	out = strings.TrimSpace(out)
	require.Equal(t, "0.0.0-dev", out)
}
