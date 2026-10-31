package core

import (
	"context"
	"crypto/rand"
	"time"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A module loaded from Git at a commit, and its dependency beside it in the
// same repository, are the same commit: loading both checks the repository
// out once.
func (ModuleSuite) TestGitModuleLocalDependencySharesCheckout(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)

	content := core.NewQuery(c).Directory().
		WithNewFile("app/dagger-module.toml", `name = "app"

[runtime]
source = "dang"

[[dependencies]]
name = "lib"
source = "../lib"
`).
		WithNewFile("app/main.dang", `type App {
  pub hello: String! {
    lib.hello
  }
}
`).
		WithNewFile("lib/dagger-module.toml", `name = "lib"

[runtime]
source = "dang"
`).
		WithNewFile("lib/main.dang", `type Lib {
  pub hello: String! {
    "hello"
  }
}
`).
		WithNewFile("nonce.txt", rand.Text())
	gitDaemon, repoURL := gitService(ctx, t, c, content)
	_, err := gitDaemon.Start(ctx)
	require.NoError(t, err)
	sha, err := core.NewQuery(c).Git(repoURL).Branch("main").CommitSHA(ctx)
	require.NoError(t, err)

	deps, err := core.NewQuery(c).ModuleSource(repoURL + "#" + sha + ":app").Dependencies(ctx)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	name, err := deps[0].ModuleName(ctx)
	require.NoError(t, err)
	require.Equal(t, "lib", name)
	require.NoError(t, c.Close())

	// A checkout holds the repository's mirror lock to copy its objects,
	// then finishes beside the lock.
	holdName := "holding git mirror lock: " + repoURL
	require.EventuallyWithT(t, func(ct *assert.CollectT) {
		sink.read(func(db *dagui.DB) {
			checkouts := 0
			for _, span := range db.Spans.Order {
				var holds, finishes bool
				for _, child := range span.ChildSpans.Spans() {
					switch child.Name {
					case holdName:
						holds = true
					case "finishing git checkout (mirror released)":
						finishes = true
					}
				}
				if holds && finishes {
					checkouts++
				}
			}
			assert.Equal(ct, 1, checkouts, "checkouts of %s", repoURL)
		})
	}, time.Minute, 100*time.Millisecond)
}
