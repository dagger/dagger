package core

// These tests cover how a check's result is cached: like any other function,
// as its @cache policy says, across sessions and engines. A failure is never
// kept, so a failing check runs again in the next session. Each check's body
// records its runs, so the tests count how often it really ran.
//
// See also:
// - checks_test.go: running and listing checks.
// - engine_persistence_test.go: a cached check across an engine restart.
//
// From a Git workspace, a check run's planning is reused across sessions too
// (TestCheckPlanningFromGitAcrossSessions).

import (
	"context"
	"crypto/rand"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// checkCachingModule is the fixture module at dir, made unique to this test,
// so neither its checks' results nor its record of runs are shared with
// another test.
func checkCachingModule(env *core.Container, dir string) *core.Container {
	return env.
		WithWorkdir(dir).
		WithNewFile("nonce.go", "package main\n\nconst nonce = \""+rand.Text()+"\"\n")
}

// checkCachingRun runs the dagger CLI in a new session, so only the cache
// connects one run to the next.
func checkCachingRun(ctx context.Context, t *testctx.T, mod *core.Container, expect core.ReturnType, args ...string) string {
	t.Helper()
	out, err := mod.
		WithEnvVariable("CACHE_BUST", rand.Text()).
		WithExec(append([]string{"dagger", "--progress=report"}, args...), core.ContainerWithExecOpts{
			Expect:                   expect,
			DaggerInDaggerNewSession: true,
		}).
		CombinedOutput(ctx)
	require.NoError(t, err)
	return out
}

// checkCachingRuns returns how often the body of check name has run.
func checkCachingRuns(ctx context.Context, t *testctx.T, mod *core.Container, args ...string) string {
	t.Helper()
	out, err := mod.
		WithEnvVariable("CACHE_BUST", rand.Text()).
		WithExec(append([]string{"dagger", "call", "runs"}, args...), core.ContainerWithExecOpts{
			DaggerInDaggerNewSession: true,
		}).
		Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

func (ChecksSuite) TestChecksCachedAcrossSessions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	env, err := checksTestEnv(t, c)
	require.NoError(t, err)

	t.Run("a passing check is reused in a new session", func(ctx context.Context, t *testctx.T) {
		mod := checkCachingModule(env, "check-caching")
		checkCachingRun(ctx, t, mod, core.ReturnTypeSuccess, "check", "pass")
		checkCachingRun(ctx, t, mod, core.ReturnTypeSuccess, "check", "pass")
		require.Equal(t, "1", checkCachingRuns(ctx, t, mod, "--name", "pass"))
	})

	// The check reads a file at the repository root, outside the module, so
	// changing it changes only the check's input.
	t.Run("a check runs again when its input changes", func(ctx context.Context, t *testctx.T) {
		mod := checkCachingModule(env, "check-caching").
			WithNewFile("/work/input/value.txt", "a")
		checkCachingRun(ctx, t, mod, core.ReturnTypeSuccess, "check", "input")
		checkCachingRun(ctx, t, mod, core.ReturnTypeSuccess, "check", "input")
		require.Equal(t, "1", checkCachingRuns(ctx, t, mod, "--name", "input"),
			"the same input must reuse the check's result")

		changed := mod.WithNewFile("/work/input/value.txt", "b")
		checkCachingRun(ctx, t, changed, core.ReturnTypeSuccess, "check", "input")
		require.Equal(t, "2", checkCachingRuns(ctx, t, changed, "--name", "input"),
			"a changed input must run the check again")
	})

	t.Run("a failing check runs again in the next session", func(ctx context.Context, t *testctx.T) {
		mod := checkCachingModule(env, "check-caching")
		checkCachingRun(ctx, t, mod, core.ReturnTypeFailure, "check", "fail")
		checkCachingRun(ctx, t, mod, core.ReturnTypeFailure, "check", "fail")
		require.Equal(t, "2", checkCachingRuns(ctx, t, mod, "--name", "fail"))
	})

	// Asking for the same check twice in one session runs it once, unless it
	// is never cached.
	t.Run("within a session", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []struct {
			check string
			want  string
		}{
			{"fail", "1"},
			{"per-session", "1"},
			{"never", "2"},
		} {
			t.Run(tc.check, func(ctx context.Context, t *testctx.T) {
				mod := checkCachingModule(env, "check-caching")
				checkCachingRun(ctx, t, mod, core.ReturnTypeAny, "-c", tc.check+" | pass; "+tc.check+" | pass")
				require.Equal(t, tc.want, checkCachingRuns(ctx, t, mod, "--name", tc.check))
			})
		}
	})

	t.Run("a per-session check runs once in each session", func(ctx context.Context, t *testctx.T) {
		mod := checkCachingModule(env, "check-caching")
		checkCachingRun(ctx, t, mod, core.ReturnTypeSuccess, "check", "per-session")
		checkCachingRun(ctx, t, mod, core.ReturnTypeSuccess, "check", "per-session")
		require.Equal(t, "2", checkCachingRuns(ctx, t, mod, "--name", "per-session"))
	})

	// A check from a module written before v1.0 may return a container,
	// which the check then runs. When the container fails, the next session
	// runs the check's body again, not only the container.
	t.Run("a failing returned container reruns the check's body", func(ctx context.Context, t *testctx.T) {
		mod := checkCachingModule(env, "check-caching-legacy")
		checkCachingRun(ctx, t, mod, core.ReturnTypeFailure, "check", "failing-container")
		checkCachingRun(ctx, t, mod, core.ReturnTypeFailure, "check", "failing-container")
		require.Equal(t, "2", checkCachingRuns(ctx, t, mod))
	})
}

// checkCachingRemote serves the check-caching module, made unique to this
// test, as the main module of a Git workspace, and returns its address.
func checkCachingRemote(ctx context.Context, t *testctx.T, c *dagger.Client) string {
	t.Helper()
	return workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.check-caching]\nsource = \".dagger/modules/check-caching\"\nmain = true\n").
		WithDirectory(".dagger/modules/check-caching", core.NewQuery(c).Host().Directory(testDataPath(t, "checks", "check-caching"))).
		WithNewFile(".dagger/modules/check-caching/nonce.go", "package main\n\nconst nonce = \""+rand.Text()+"\"\n"))
}

// planningCollectionModule is the main module of a Git workspace whose
// collection gets a new key each time its producer runs, cached as policy
// says, or by default when it's empty. Each item's check records its key, so
// a test can tell which items a run checked.
func planningCollectionModule(c *dagger.Client, policy string) *core.Directory {
	cache := ""
	if policy != "" {
		cache = "// +cache=\"" + policy + "\"\n"
	}
	return core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"./probe\"\nmain = true\n").
		WithNewFile("probe/dagger.json", `{"name":"probe","engineVersion":"latest","sdk":{"source":"go"},"source":"."}`).
		WithNewFile("probe/main.go", `package main

import (
	"context"
	"crypto/rand"
	"strings"

	"dagger/probe/internal/dagger"
)

const nonce = "`+rand.Text()+`"

type Probe struct{}

`+cache+`func (*Probe) Batches() *Batches { return &Batches{Names: []string{rand.Text()}} }

// +collection
type Batches struct {
	// +keys
	Names []string
}

// +get
func (*Batches) Batch(name string) *Batch { return &Batch{Name: name} }

type Batch struct{ Name string }

// +check
func (b *Batch) Verify(ctx context.Context) error {
	_, err := runLog().
		WithEnvVariable("NONCE", rand.Text()).
		WithExec([]string{"sh", "-c", "echo '" + b.Name + "' >> /runs/log"}).
		Sync(ctx)
	return err
}

// Checked lists the keys of the items whose check ran, once each.
// +cache="never"
func (*Probe) Checked(ctx context.Context) (string, error) {
	out, err := runLog().
		WithEnvVariable("NONCE", rand.Text()).
		WithExec([]string{"sh", "-c", "sort -u /runs/log"}).
		Stdout(ctx)
	return strings.TrimSpace(out), err
}

func runLog() *dagger.Container {
	return dag.Container().
		From("alpine:3").
		WithMountedCache("/runs", dag.CacheVolume("planning-collection-"+nonce))
}
`)
}

// planningImageCollectionModule is the main module of a Git workspace whose
// constructor takes an image from a module setting set to a tag, and whose
// collection is cached by default.
func planningImageCollectionModule(c *dagger.Client) *core.Directory {
	return core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"./probe\"\nmain = true\nsettings.base = \"alpine:latest\"\n").
		WithNewFile("probe/dagger.json", `{"name":"probe","engineVersion":"latest","sdk":{"source":"go"},"source":"."}`).
		WithNewFile("probe/main.go", `package main

import "dagger/probe/internal/dagger"

type Probe struct {
	// +private
	Base *dagger.Container
}

func New(base *dagger.Container) *Probe { return &Probe{Base: base} }

func (*Probe) Batches() *Batches { return &Batches{Names: []string{"a", "b"}} }

// +collection
type Batches struct {
	// +keys
	Names []string
}

// +get
func (*Batches) Batch(name string) *Batch { return &Batch{Name: name} }

type Batch struct{ Name string }

// +check
func (*Batch) Verify() error { return nil }
`)
}

// planningDirectoryCollectionModule is the main module of a Git workspace
// whose constructor takes a directory from a module setting set to a remote
// Git branch, and whose collection is cached by default.
func planningDirectoryCollectionModule(c *dagger.Client, branch string) *core.Directory {
	return core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"./probe\"\nmain = true\nsettings.src = \""+branch+"\"\n").
		WithNewFile("probe/dagger.json", `{"name":"probe","engineVersion":"latest","sdk":{"source":"go"},"source":"."}`).
		WithNewFile("probe/main.go", `package main

import "dagger/probe/internal/dagger"

type Probe struct {
	// +private
	Src *dagger.Directory
}

func New(src *dagger.Directory) *Probe { return &Probe{Src: src} }

func (*Probe) Batches() *Batches { return &Batches{Names: []string{"a", "b"}} }

// +collection
type Batches struct {
	// +keys
	Names []string
}

// +get
func (*Batches) Batch(name string) *Batch { return &Batch{Name: name} }

type Batch struct{ Name string }

// +check
func (*Batch) Verify() error { return nil }
`)
}

// planningListingOutcomes lists the checks of the Git workspace at remoteRef
// in two sessions of one engine, and returns the cache outcome of each
// listing of their items, as the engine reports it to Cloud.
func planningListingOutcomes(ctx context.Context, t *testctx.T, c *dagger.Client, remoteRef string) []string {
	t.Helper()
	cloud := newTelemetrySplitCloud(t, c)
	engine, err := devEngineContainerAsService(telemetrySplitEngineWithoutCloud(c, devEngineContainer(c))).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = engine.Stop(ctx) })
	for range 2 {
		_, err := telemetrySplitClient(ctx, t, c, daggerCliFile(t, c), engine, cloud).
			WithNewFile("/query.graphql", `{currentWorkspace{artifacts{__evaluationItems{uri}}}}`).
			WithEnvVariable("CACHEBUSTER", rand.Text()).
			WithExec([]string{"/bin/dagger", "query", "--doc", "/query.graphql", "-W", remoteRef}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
			Stdout(ctx)
		require.NoError(t, err)
	}
	type span struct {
		Name  string            `json:"name"`
		Attrs map[string]string `json:"attrs"`
	}
	var outcomes []string
	for _, s := range readTelemetrySplitLines[span](ctx, t, cloud, "v1/traces.json.spans") {
		if strings.HasSuffix(s.Name, ".__evaluationItems") {
			outcomes = append(outcomes, s.Attrs["dagger.io/cache.outcome"])
		}
	}
	t.Logf("listing outcomes: %v", outcomes)
	return outcomes
}

// From a Git workspace, a check run's planning is reused across sessions:
// the workspace's artifacts, the listed items and each check's run. These
// tests cover what must still run again in the next session.
func (ChecksSuite) TestCheckPlanningFromGitAcrossSessions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := core.NewQuery(c).Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/empty")

	runs := func(ctx context.Context, t *testctx.T, remoteRef, name string) string {
		return workspaceIdentityCall(ctx, t, ctr, "-W", remoteRef, "runs", "--name", name)
	}

	t.Run("a failing check runs again in the next session", func(ctx context.Context, t *testctx.T) {
		remoteRef := checkCachingRemote(ctx, t, c)
		checkCachingRun(ctx, t, ctr, core.ReturnTypeFailure, "check", "-W", remoteRef, "fail")
		checkCachingRun(ctx, t, ctr, core.ReturnTypeFailure, "check", "-W", remoteRef, "fail")
		require.Equal(t, "2", runs(ctx, t, remoteRef, "fail"))
	})

	t.Run("a failing check runs again in a session open beside the one that ran it", func(ctx context.Context, t *testctx.T) {
		remoteRef := checkCachingRemote(ctx, t, c)
		// This client joins the test's own session, which stays open.
		_, err := ctr.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			WithExec([]string{"dagger", "--progress=report", "check", "-W", remoteRef, "fail"}, core.ContainerWithExecOpts{
				Expect: core.ReturnTypeFailure,
			}).
			CombinedOutput(ctx)
		require.NoError(t, err)
		checkCachingRun(ctx, t, ctr, core.ReturnTypeFailure, "check", "-W", remoteRef, "fail")
		require.Equal(t, "2", runs(ctx, t, remoteRef, "fail"))
	})

	t.Run("a passing check is reused in the next session", func(ctx context.Context, t *testctx.T) {
		remoteRef := checkCachingRemote(ctx, t, c)
		checkCachingRun(ctx, t, ctr, core.ReturnTypeSuccess, "check", "-W", remoteRef, "pass")
		checkCachingRun(ctx, t, ctr, core.ReturnTypeSuccess, "check", "-W", remoteRef, "pass")
		require.Equal(t, "1", runs(ctx, t, remoteRef, "pass"))
	})

	t.Run("a per-session check runs once in each session", func(ctx context.Context, t *testctx.T) {
		remoteRef := checkCachingRemote(ctx, t, c)
		checkCachingRun(ctx, t, ctr, core.ReturnTypeSuccess, "check", "-W", remoteRef, "per-session")
		checkCachingRun(ctx, t, ctr, core.ReturnTypeSuccess, "check", "-W", remoteRef, "per-session")
		require.Equal(t, "2", runs(ctx, t, remoteRef, "per-session"))
	})

	// Listing a collection's items calls its producer. A producer cached
	// across sessions gives the same items, so the next session's listing is
	// a cache hit. The engine reports each call's cache outcome to Cloud.
	t.Run("items of a collection cached by default are listed once across sessions", func(ctx context.Context, t *testctx.T) {
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, planningCollectionModule(c, ""))
		require.Contains(t, planningListingOutcomes(ctx, t, c, remoteRef), "hit", "the second session must reuse the listing")
	})

	// A module setting set to an image tag resolves while listing. The
	// workspace lock can pin the tag, so the listing is reused; an unpinned
	// tag may move, so the next session lists the items again.
	t.Run("a listing over an image tag", func(ctx context.Context, t *testctx.T) {
		imageRef, err := core.NewQuery(c).Container().From("alpine:latest").ImageRef(ctx)
		require.NoError(t, err)
		_, pin, found := strings.Cut(imageRef, "@")
		require.True(t, found, "expected an image ref with a digest: %q", imageRef)

		t.Run("pinned by the lock is reused in the next session", func(ctx context.Context, t *testctx.T) {
			remoteRef := workspaceSelectionRemoteRef(ctx, t, c, planningImageCollectionModule(c).
				WithNewFile(workspace.LockFileName, mustMarshalOCISHALock(t, pin)))
			require.Contains(t, planningListingOutcomes(ctx, t, c, remoteRef), "hit", "a pinned tag must not keep the listing from being reused")
		})
		t.Run("without a pin is listed again", func(ctx context.Context, t *testctx.T) {
			remoteRef := workspaceSelectionRemoteRef(ctx, t, c, planningImageCollectionModule(c))
			outcomes := planningListingOutcomes(ctx, t, c, remoteRef)
			require.NotEmpty(t, outcomes)
			require.NotContains(t, outcomes, "hit", "an unpinned tag may move, so the listing must not be reused")
		})
	})

	// A module setting set to a remote Git branch resolves while listing. The
	// branch may move, so the next session lists the items again.
	t.Run("a listing over a remote branch is listed again", func(ctx context.Context, t *testctx.T) {
		branchRepo := workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("README", "branch"))
		branch := strings.TrimSuffix(branchRepo, "@main") + "#main"
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, planningDirectoryCollectionModule(c, branch))
		outcomes := planningListingOutcomes(ctx, t, c, remoteRef)
		require.NotEmpty(t, outcomes)
		require.NotContains(t, outcomes, "hit", "a branch may move, so the listing must not be reused")
	})

	// A collection's items come from its producer. When the producer isn't
	// cached across sessions, the next session lists the items it returns
	// then, not the ones listed before.
	for _, policy := range []string{"session", "never"} {
		t.Run("items of a collection cached per "+policy+" are listed again", func(ctx context.Context, t *testctx.T) {
			remoteRef := workspaceSelectionRemoteRef(ctx, t, c, planningCollectionModule(c, policy))
			first := workspaceIdentityExec(ctx, t, ctr, true, "check", "-W", remoteRef, "-l", "--all", "-f=cli")
			second := workspaceIdentityExec(ctx, t, ctr, true, "check", "-W", remoteRef, "-l", "--all", "-f=cli")
			require.Contains(t, first, "--probe-batch=")
			require.NotEqual(t, first, second, "the second session must list the producer's new item")
		})
	}

	// Checking an item evaluates its collection again, so only a producer
	// cached for the session can be checked: one never cached returns other
	// keys.
	t.Run("items of a collection cached per session are checked again", func(ctx context.Context, t *testctx.T) {
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, planningCollectionModule(c, "session"))
		checkCachingRun(ctx, t, ctr, core.ReturnTypeSuccess, "check", "-W", remoteRef)
		checkCachingRun(ctx, t, ctr, core.ReturnTypeSuccess, "check", "-W", remoteRef)
		out, err := ctr.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			WithExec([]string{"dagger", "call", "-W", remoteRef, "checked"}, core.ContainerWithExecOpts{
				DaggerInDaggerNewSession: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Len(t, strings.Fields(out), 2, "each session must check the item its producer returned then")
	})

	// A listing of a producer cached per session belongs to its session,
	// even while that session is still open: another session checks the
	// items its own producer returns.
	t.Run("items of a collection cached per session are not planned from an open session's listing", func(ctx context.Context, t *testctx.T) {
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, planningCollectionModule(c, "session"))
		// This client joins the test's own session, which stays open.
		workspaceIdentityExec(ctx, t, ctr, false, "check", "-W", remoteRef)
		checkCachingRun(ctx, t, ctr, core.ReturnTypeSuccess, "check", "-W", remoteRef)
	})
}
