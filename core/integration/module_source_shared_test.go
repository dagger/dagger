package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"strings"

	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// sharedSourceDependency is a public module on a known code host, written as
// a module's dependency is: the commit as the ref's version, with no pin.
const sharedSourceDependency = "github.com/dagger/dagger-test-modules/contextual-git-bug@d730fb3af8757e1ca293e01aa4fcfd510a6e40e5"

// A module source read by module code is shared with a later session when
// nothing it reads is its caller's, here a source whose one dependency is
// written as users write one on github.com. The dependency's ref has no
// scheme, so on its own it could name a local directory on the caller's
// host, and a ref to such a host was once taken for a vanity ref resolved
// through the session's workspace lock. Either kept the dependency's read,
// and the source read that loads it, to its session. The dependency is
// read as Git, which its parent's files say it is, and a known code host
// has no vanity URL.
//
// A function that is never cached reads the source in each of two sessions,
// one after the other, and the second session's read must be the first's.
func (ModuleLoadingSuite) TestModuleSourceReadWithAGitHubDependencyIsShared(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	nonce := rand.Text()
	// The workspace is a Git checkout, as a workspace is found from one.
	workdir := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithWorkdir("/work").
		WithNewFile("dagger.toml", "[modules.reader]\nsource = \".dagger/modules/reader\"\n").
		WithNewFile(".dagger/modules/reader/dagger.json", `{"name":"reader","engineVersion":"latest","sdk":{"source":"dang"}}`).
		WithNewFile(".dagger/modules/reader/main.dang", `type Reader {
  pub read(n: Int!): String! @cache(policy: FunctionCachePolicy.NEVER) {
    directory
      .withNewFile("consumer/dagger.json", "{\"name\":\"consumer-`+strings.ToLower(nonce)+`\",\"engineVersion\":\"latest\",\"dependencies\":[{\"name\":\"dep\",\"source\":\"`+sharedSourceDependency+`\"}]}")
      .asModuleSource(sourceRootPath: "consumer")
      .moduleName
  }
}
`).
		WithExec([]string{"git", "init", "-q"}).
		WithExec([]string{"git", "add", "-A"}).
		WithExec([]string{"git", "commit", "-qm", "init"}).
		Directory("/work")

	cloud := newTelemetrySplitCloud(t, c)
	engine, err := devEngineContainerAsService(telemetrySplitEngineWithoutCloud(c, devEngineContainer(c))).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = engine.Stop(ctx) })
	client := telemetrySplitClient(ctx, t, c, daggerCliFile(t, c), engine, cloud).
		WithMountedDirectory("/work", workdir).
		WithWorkdir("/work")

	read := func(n int) {
		t.Helper()
		out, err := client.
			WithNewFile("/query.graphql", fmt.Sprintf("{ reader { read(n: %d) } }", n)).
			WithEnvVariable("CACHEBUSTER", rand.Text()).
			WithExec([]string{"/bin/dagger", "query", "--doc", "/query.graphql"}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "consumer-", "session %d reads the source: %s", n, out)
	}
	read(1)
	read(2)

	type span struct {
		Session string            `json:"session"`
		SpanID  string            `json:"spanID"`
		Name    string            `json:"name"`
		Attrs   map[string]string `json:"attrs"`
	}
	// The consumer's read, by session: every export with an outcome of the one
	// call of Directory.asModuleSource with its source root. A session that
	// executed it at all counts as reading it.
	outcomes := map[string][]string{}
	for _, s := range readTelemetrySplitLines[span](ctx, t, cloud, "v1/traces.json.spans") {
		outcome := s.Attrs["dagger.io/cache.outcome"]
		payload := s.Attrs["dagger.io/dag.call"]
		if s.Name != "Directory.asModuleSource" || outcome == "" || payload == "" {
			continue
		}
		var frame callpbv1.Call
		require.NoError(t, frame.Decode(payload))
		for _, arg := range frame.GetArgs() {
			if arg.GetName() == "sourceRootPath" && arg.GetValue().GetString_() == "consumer" {
				outcomes[s.Session] = append(outcomes[s.Session], outcome)
			}
		}
	}
	t.Logf("the consumer's read by session: %v", outcomes)
	require.Len(t, outcomes, 2, "each session reports the consumer's read")
	var executed, served int
	for _, got := range outcomes {
		switch {
		case slices.Contains(got, "executed"):
			executed++
		case slices.Contains(got, "hit"):
			served++
		}
	}
	require.Equal(t, 1, executed, "the first session reads the source")
	require.Equal(t, 1, served, "the later session is served the first one's read")
}
