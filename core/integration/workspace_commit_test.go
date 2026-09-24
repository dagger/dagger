package core

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const workspaceCommitDate = "2026-09-05T12:00:00Z"

type workspaceCommitState struct {
	ID  dagger.ID
	Git struct {
		Repository struct{ URL *string }
		Head       struct {
			Commit       string
			TargetCommit struct{ Message, AuthorName, AuthorEmail, AuthoredDate, CommittedDate string }
		}
		Uncommitted struct{ AddedPaths, ModifiedPaths, RemovedPaths []string }
	}
}

func commitWorkspace(ctx context.Context, c *dagger.Client, ws *dagger.Workspace, message string, include []string) (got workspaceCommitState, err error) {
	got.ID, err = ws.WithCommit(ws.Git().Uncommitted().Filter(dagger.ChangesetFilterOpts{Include: include}), message, workspaceCommitDate).ID(ctx)
	if err != nil {
		return got, err
	}
	// Resolve the result once before reading its fields: metadata reads must
	// not repeat the workspace's host capture or commit operation.
	committed := dagger.Ref[*dagger.Workspace](c, got.ID)
	head := committed.Git().Head()
	meta := head.TargetCommit()
	for _, field := range []struct {
		target *string
		read   func(context.Context) (string, error)
	}{
		{&got.Git.Head.Commit, head.CommitSHA},
		{&got.Git.Head.TargetCommit.Message, meta.Message},
		{&got.Git.Head.TargetCommit.AuthorName, meta.AuthorName},
		{&got.Git.Head.TargetCommit.AuthorEmail, meta.AuthorEmail},
		{&got.Git.Head.TargetCommit.AuthoredDate, meta.AuthoredDate},
		{&got.Git.Head.TargetCommit.CommittedDate, meta.CommittedDate},
	} {
		*field.target, err = field.read(ctx)
		if err != nil {
			return got, err
		}
	}
	url, err := head.AsRepository().URL(ctx)
	if err != nil {
		return got, err
	}
	if url != "" {
		got.Git.Repository.URL = &url
	}
	pending := committed.Git().Uncommitted()
	for _, field := range []struct {
		target *[]string
		read   func(context.Context) ([]string, error)
	}{
		{&got.Git.Uncommitted.AddedPaths, pending.AddedPaths},
		{&got.Git.Uncommitted.ModifiedPaths, pending.ModifiedPaths},
		{&got.Git.Uncommitted.RemovedPaths, pending.RemovedPaths},
	} {
		*field.target, err = field.read(ctx)
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

// A commit retains both repositories in the engine. Reading their histories
// must borrow those objects, not fetch them into another repository per log.
func (WorkspaceSuite) TestWorkspaceCommittedHistoryDoesNotFetch(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to inspect history call structure")
	}
	checkout, hostGit := workspaceExportCheckout(ctx, t)
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithWorkdir(checkout))...)
	base := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace())
	baseSHA, err := base.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	commit := func(message string) *dagger.Workspace {
		t.Helper()
		state, err := commitWorkspace(ctx, c, base.WithNewFile("base.txt", message).
			WithNewFile("pending.txt", "keep pending"), message, []string{"base.txt"})
		require.NoError(t, err)
		require.Equal(t, []string{"pending.txt"}, state.Git.Uncommitted.AddedPaths)
		return dagger.Ref[*dagger.Workspace](c, state.ID)
	}
	next, side := commit("next"), commit("side")
	for _, tc := range []struct {
		name string
		head *dagger.GitRef
		opts dagger.GitRefLogOpts
		want []string
	}{
		{name: "ahead", head: next.Git().Head(), opts: dagger.GitRefLogOpts{Base: base.Git().Head(), Limit: 101}, want: []string{"next"}},
		{name: "behind", head: base.Git().Head(), opts: dagger.GitRefLogOpts{Base: next.Git().Head(), Limit: 101}},
		{name: "divergent", head: next.Git().Head(), opts: dagger.GitRefLogOpts{Base: side.Git().Head()}, want: []string{"next"}},
		{name: "reverse divergent", head: side.Git().Head(), opts: dagger.GitRefLogOpts{Base: next.Git().Head()}, want: []string{"side"}},
		{name: "path filter", head: next.Git().Head(), opts: dagger.GitRefLogOpts{Base: base.Git().Head(), Paths: []string{"base.txt"}}, want: []string{"next"}},
		{name: "pending is not history", head: next.Git().Head(), opts: dagger.GitRefLogOpts{Paths: []string{"pending.txt"}}},
	} {
		commits, err := tc.head.Log(ctx, tc.opts)
		require.NoError(t, err, tc.name)
		var messages []string
		for _, commit := range commits {
			message, err := commit.Message(ctx)
			require.NoError(t, err, tc.name)
			messages = append(messages, strings.TrimSpace(message))
		}
		require.Equal(t, tc.want, messages, tc.name)
	}
	ancestor, err := next.Git().Head().CommonAncestor(side.Git().Head()).CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, baseSHA, ancestor)
	require.Equal(t, baseSHA, hostGit("rev-parse", "HEAD"), "history reads and commits must leave the host alone")
	require.Empty(t, hostGit("status", "--porcelain"))
	require.NoError(t, c.Close()) // Drain telemetry before asserting absence.

	// Inspect only history-query descendants: creating the fixture and the
	// commits may still materialize checkouts, independently of reading logs.
	traces, _ := sink.capture()
	parents, names := map[string]string{}, map[string]string{}
	discardedCheckouts := map[string]bool{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano < span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					parents[id], names[id] = string(span.TraceId)+string(span.ParentSpanId), span.Name
					if span.Name == "materialize local git checkout" {
						for _, attr := range span.Attributes {
							if attr.Key == "dagger.git.checkout.discard_git_dir" && attr.Value.GetBoolValue() {
								discardedCheckouts[id] = true
							}
						}
					}
				}
			}
		}
	}
	// Source-only trees created while committing also borrow local objects.
	// Retained full checkouts still fetch to own their history independently.
	require.NotEmpty(t, discardedCheckouts, "must exercise real local tree checkouts")
	for id, name := range names {
		for parent := parents[id]; parent != ""; parent = parents[parent] {
			if discardedCheckouts[parent] {
				require.False(t, strings.HasPrefix(name, "git fetch") || strings.HasPrefix(name, "fetching "), "local source tree fetched objects: %s", name)
				break
			}
		}
	}
	var walks int
	for id, name := range names {
		for parent := parents[id]; parent != ""; parent = parents[parent] {
			if names[parent] != "GitRef.log" && names[parent] != "GitRef.commonAncestor" {
				continue
			}
			require.False(t, strings.HasPrefix(name, "git fetch") || strings.HasPrefix(name, "fetching "), "history query fetched objects: %s", name)
			if strings.HasPrefix(name, "git rev-list") || strings.HasPrefix(name, "git merge-base") {
				walks++
			}
			break
		}
	}
	require.GreaterOrEqual(t, walks, 7, "must observe the six real log walks and merge-base, not an empty trace")
}

// TestWorkspaceScopedCommitPerformance is a deterministic, non-LLM latency
// harness. Run it alone with -v on a fresh engine for a cold capture followed by
// three sequential commits. Timings are observations, not performance gates.
// Fixture creation, connection and capture are reported separately; engine/CLI
// build time is outside this test. The fixture has no remote and uses packed
// objects, so fetch spans describe local copying rather than network traffic.
func (WorkspaceSuite) TestWorkspaceScopedCommitPerformance(ctx context.Context, t *testctx.T) {
	if runWithPrivateTraceSession(ctx, t) {
		return
	}
	const files, fileBytes, iterations = 12000, 8192, 3
	started := time.Now()
	checkout := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+workspaceCommitDate, "GIT_COMMITTER_DATE="+workspaceCommitDate)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Performance Fixture")
	git("config", "user.email", "performance@example.com")
	git("config", "commit.gpgsign", "false")
	git("config", "core.hooksPath", "/dev/null")
	git("config", "gc.auto", "0")
	// Fixed-seed unique text avoids an unrealistically tiny pack from repeated
	// identical blobs. These are logical fixture bytes, not measured I/O bytes.
	rng := rand.New(rand.NewSource(1))
	buf := make([]byte, fileBytes/2)
	for i := range files {
		path := filepath.Join(checkout, fmt.Sprintf("tree/%03d/%05d.txt", i/100, i))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		_, err := rng.Read(buf)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, []byte(hex.EncodeToString(buf)), 0o644))
	}
	for _, name := range []string{"selected.txt", "pending.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(checkout, name), []byte("base\n"), 0o644))
	}
	git("add", ".")
	git("commit", "-m", "fixture")
	git("gc", "--prune=now")
	hostSHA := git("rev-parse", "HEAD")
	t.Logf("PERF fixture files=%d payload_bytes=%d setup=%s git_objects=%q", files+2, files*fileBytes+10, time.Since(started), git("count-objects", "-v"))

	started = time.Now()
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithWorkdir(checkout), dagger.WithLogOutput(io.Discard))...)
	t.Logf("PERF connect=%s", time.Since(started))
	started = time.Now()
	ws := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace())
	baseSHA, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, hostSHA, baseSHA)
	t.Logf("PERF capture=%s", time.Since(started))
	ws = ws.WithNewFile("pending.txt", "keep pending\n").WithNewFile("selected.txt", "edit 1\n")

	for i := 1; i <= iterations; i++ {
		cycle := time.Now()
		started = time.Now()
		paths, err := ws.Git().Uncommitted().ModifiedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"selected.txt", "pending.txt"}, paths)
		selectedID, err := ws.Git().Uncommitted().Filter(dagger.ChangesetFilterOpts{Include: []string{"selected.txt"}}).ID(ctx)
		require.NoError(t, err)
		before := ws.Git().Head()
		beforeSHA, err := before.CommitSHA(ctx)
		require.NoError(t, err)
		preStatus := time.Since(started)

		started = time.Now()
		id, err := ws.WithCommit(dagger.Ref[*dagger.Changeset](c, selectedID), fmt.Sprintf("perf: edit %d", i), workspaceCommitDate,
			dagger.WorkspaceWithCommitOpts{AuthorName: "Performance Fixture", AuthorEmail: "performance@example.com"}).ID(ctx)
		require.NoError(t, err)
		commitTime := time.Since(started)
		next := dagger.Ref[*dagger.Workspace](c, id)

		started = time.Now()
		pending := next.Git().Uncommitted()
		paths, err = pending.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"pending.txt"}, paths)
		paths, err = pending.AddedPaths(ctx)
		require.NoError(t, err)
		require.Empty(t, paths)
		paths, err = pending.RemovedPaths(ctx)
		require.NoError(t, err)
		require.Empty(t, paths)
		postStatus := time.Since(started)

		started = time.Now()
		head := next.Git().Head()
		ahead, err := head.Log(ctx, dagger.GitRefLogOpts{Base: before, Limit: 101})
		require.NoError(t, err)
		require.Len(t, ahead, 1)
		message, err := ahead[0].Message(ctx)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("perf: edit %d", i), strings.TrimSpace(message))
		parents, err := ahead[0].ParentShas(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{beforeSHA}, parents)
		behind, err := before.Log(ctx, dagger.GitRefLogOpts{Base: head, Limit: 101})
		require.NoError(t, err)
		require.Empty(t, behind)
		recent, err := head.Log(ctx, dagger.GitRefLogOpts{Limit: 2})
		require.NoError(t, err)
		require.Len(t, recent, 2)
		history := time.Since(started)

		started = time.Now()
		// Consume the committed tree as well as the overlaid workspace: an ID
		// alone could hide deferred filesystem materialization.
		tree := head.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
		delta := tree.Changes(before.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}))
		paths, err = delta.ModifiedPaths(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"selected.txt"}, paths)
		paths, err = delta.AddedPaths(ctx)
		require.NoError(t, err)
		require.Empty(t, paths)
		paths, err = delta.RemovedPaths(ctx)
		require.NoError(t, err)
		require.Empty(t, paths)
		contents, err := tree.File("selected.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("edit %d\n", i), contents)
		contents, err = tree.File("pending.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "base\n", contents)
		contents, err = next.File("pending.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep pending\n", contents)
		consume := time.Since(started)

		started = time.Now()
		ws = next.WithNewFile("selected.txt", fmt.Sprintf("edit %d\n", i+1))
		contents, err = ws.File("selected.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("edit %d\n", i+1), contents)
		t.Logf("PERF iteration=%d pre_status=%s withCommit=%s post_status=%s history=%s consume=%s next_edit=%s cycle=%s", i, preStatus, commitTime, postStatus, history, consume, time.Since(started), time.Since(cycle))
	}
	require.Equal(t, hostSHA, git("rev-parse", "HEAD"), "engine commits must leave the host alone")
	require.Empty(t, git("status", "--porcelain"))
	require.NoError(t, c.Close()) // Drain live spans before counting or timing them.
	logWorkspaceCommitPerformanceTrace(t, sink, iterations)
}

// Report completed, deduplicated engine spans separately from client wall time.
// Durations are inclusive and overlap: do not add these rows together. No byte
// traffic, filesystem-write or object-copy counts are inferred from spans.
func logWorkspaceCommitPerformanceTrace(t *testctx.T, sink *agentTraceSink, iterations int) {
	t.Helper()
	type sample struct {
		id, parent, name string
		start, end       uint64
		discard          bool
	}
	byID := map[string]sample{}
	traces, _ := sink.capture()
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					s := sample{id: id, parent: string(span.TraceId) + string(span.ParentSpanId), name: span.Name, start: span.StartTimeUnixNano, end: span.EndTimeUnixNano}
					for _, attr := range span.Attributes {
						if attr.Key == "dagger.git.checkout.discard_git_dir" {
							s.discard = attr.Value.GetBoolValue()
						}
					}
					byID[id] = s
				}
			}
		}
	}
	var ordered []sample
	for _, s := range byID {
		ordered = append(ordered, s)
	}
	slices.SortFunc(ordered, func(a, b sample) int {
		if a.start < b.start {
			return -1
		}
		if a.start > b.start {
			return 1
		}
		return strings.Compare(a.id, b.id)
	})
	var commits, merges, fetches, walks, gitCommits int
	for _, s := range ordered {
		fetch := strings.HasPrefix(s.name, "git fetch") || strings.HasPrefix(s.name, "fetching ")
		if fetch {
			for parent, ok := byID[s.parent]; ok; parent, ok = byID[parent.parent] {
				require.NotEqual(t, "GitRef.log", parent.name, "history query fetched objects: %s", s.name)
				require.False(t, parent.name == "materialize local git checkout" && parent.discard, "source-only checkout fetched objects: %s", s.name)
			}
		}
		// API selection and execution may have distinct spans with the same
		// name. Keep only the outer one, in addition to deduplicating live
		// updates by span ID, so a log query is not counted twice.
		duplicate := false
		for parent, ok := byID[s.parent]; ok; parent, ok = byID[parent.parent] {
			if parent.name == s.name {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		if fetch {
			fetches++
		}
		interesting := fetch
		switch s.name {
		case "Workspace.withCommit":
			commits++
			interesting = true
		case "Changeset.__mergeWithChangeset":
			merges++
			interesting = true
		case "git commit":
			gitCommits++
			interesting = true
		case "GitRef.log":
			walks++
			interesting = true
		case "materialize local git checkout", "GitRef.asWorkspace":
			interesting = true
		}
		if interesting {
			t.Logf("PERF span=%q id=%x duration=%s discard_git_dir=%t", s.name, []byte(s.id)[16:], time.Duration(s.end-s.start), s.discard)
		}
	}
	t.Logf("PERF spans workspace_commits=%d merges=%d fetches=%d history_calls=%d git_commits=%d (inclusive, deduplicated; counts are not bytes)", commits, merges, fetches, walks, gitCommits)
	require.Equal(t, iterations, commits, "must observe actual commit calls, not an empty trace")
	require.GreaterOrEqual(t, walks, iterations*3)
}

func (WorkspaceSuite) TestWorkspaceWithCommitScopedHistory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().
		WithNewFile("src/a.txt", "old-a").WithNewFile("src/b.txt", "old-b").
		WithNewFile("keep.txt", "untouched"))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).
		Branch("main").AsWorkspace(dagger.GitRefAsWorkspaceOpts{Cwd: "src"}).
		WithNewFile("a.txt", "new-a").WithNewFile("b.txt", "new-b")
	baseSHA, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	first, err := commitWorkspace(ctx, c, ws, "same message", []string{"src/a.txt"})
	require.NoError(t, err)
	require.NotEqual(t, baseSHA, first.Git.Head.Commit)
	require.Equal(t, []string{"src/b.txt"}, first.Git.Uncommitted.ModifiedPaths)
	require.Equal(t, "Dagger", first.Git.Head.TargetCommit.AuthorName)
	require.Equal(t, "dagger@localhost", first.Git.Head.TargetCommit.AuthorEmail)
	require.Equal(t, workspaceCommitDate, first.Git.Head.TargetCommit.AuthoredDate)
	require.Equal(t, workspaceCommitDate, first.Git.Head.TargetCommit.CommittedDate)
	require.NotNil(t, first.Git.Repository.URL)
	require.Equal(t, url, *first.Git.Repository.URL)
	frozen := dagger.Ref[*dagger.Workspace](c, first.ID)
	for file, expected := range map[string]string{"a.txt": "new-a", "b.txt": "new-b", "/keep.txt": "untouched"} {
		contents, err := frozen.File(file).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, expected, contents)
	}
	repeated, err := commitWorkspace(ctx, c, ws, "same message", []string{"src/a.txt"})
	require.NoError(t, err)
	require.Equal(t, first.Git.Head.Commit, repeated.Git.Head.Commit)
	// Force a different recipe with the same resolved identity and tree, so
	// this checks Git determinism rather than just a cache hit.
	equivalent, err := commitWorkspace(ctx, c, ws.WithConfigEnvironment(""), "same message", []string{"src/a.txt"})
	require.NoError(t, err)
	require.Equal(t, first.Git.Head.Commit, equivalent.Git.Head.Commit)
	laterSHA, err := ws.WithCommit(ws.Git().Uncommitted().Filter(dagger.ChangesetFilterOpts{Include: []string{"src/a.txt"}}), "same message", "2026-09-05T12:00:01Z").Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEqual(t, first.Git.Head.Commit, laterSHA)
	second, err := commitWorkspace(ctx, c, frozen, "same message", nil)
	require.NoError(t, err)
	require.NotEqual(t, first.Git.Head.Commit, second.Git.Head.Commit)
	require.NotNil(t, second.Git.Repository.URL)
	require.Equal(t, url, *second.Git.Repository.URL)
	require.Empty(t, second.Git.Uncommitted.ModifiedPaths)
	require.Empty(t, second.Git.Uncommitted.AddedPaths)
	require.Empty(t, second.Git.Uncommitted.RemovedPaths)
	log, err := dagger.Ref[*dagger.Workspace](c, second.ID).Git().Head().Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 3)
	_, err = commitWorkspace(ctx, c, dagger.Ref[*dagger.Workspace](c, second.ID), "same message", nil)
	require.ErrorContains(t, err, "nothing to commit")
	// Input values are immutable, even after a path-scoped and a full commit.
	oldSHA, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, baseSHA, oldSHA)

	recipe, err := c.LLM().WithWorkspace(dagger.Ref[*dagger.Workspace](c, second.ID)).PortableID(ctx)
	require.NoError(t, err)
	id := new(call.ID)
	require.NoError(t, id.Decode(string(recipe)))
	dag, err := id.ToProto()
	require.NoError(t, err)
	for _, vertex := range dag.GetRecipe().CallsByDigest {
		require.NotContains(t, []string{"currentWorkspace", "checkpoint", "withCommit", "branch"}, vertex.Field)
	}
	restored := dagger.Ref[*dagger.LLM](c, recipe).Workspace()
	restoredSHA, err := restored.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, second.Git.Head.Commit, restoredSHA)
}

func (WorkspaceSuite) TestWorkspaceWithCommitFreezesHostAndAuthor(ctx context.Context, t *testctx.T) {
	checkout := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Original Author")
	git("config", "user.email", "original@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "a.txt"), []byte("old"), 0o644))
	git("add", ".")
	git("commit", "-m", "initial")
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "a.txt"), []byte("new"), 0o644))
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	ws := c.CurrentWorkspace()
	_, err := ws.ID(ctx)
	require.NoError(t, err)
	// Commit authorship is sampled at commit time, even if the workspace
	// was loaded before the client changed its Git config.
	git("config", "user.name", "Later Author")
	git("config", "user.email", "later@example.com")
	headBefore, statusBefore := git("rev-parse", "HEAD"), git("status", "--porcelain")
	committed, err := commitWorkspace(ctx, c, ws, "engine commit", nil)
	require.NoError(t, err)
	require.Contains(t, workspaceRecipeFields(ctx, t, c, string(committed.ID)), "__gitDir")
	require.NotEqual(t, headBefore, committed.Git.Head.Commit)
	require.Equal(t, "Later Author", committed.Git.Head.TargetCommit.AuthorName)
	require.Equal(t, "later@example.com", committed.Git.Head.TargetCommit.AuthorEmail)
	require.Empty(t, committed.Git.Uncommitted.ModifiedPaths)
	require.Equal(t, headBefore, git("rev-parse", "HEAD"))
	require.Equal(t, statusBefore, git("status", "--porcelain"))
	contents, err := os.ReadFile(filepath.Join(checkout, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "new", string(contents))

	require.NoError(t, os.WriteFile(filepath.Join(checkout, "untracked.txt"), []byte("not approved"), 0o644))
	_, err = commitWorkspace(ctx, c, ws, "must request approval", nil)
	require.ErrorContains(t, err, "untracked.txt")
	require.NotContains(t, err.Error(), "not approved")
	require.Equal(t, headBefore, git("rev-parse", "HEAD"))
}

func (WorkspaceSuite) TestWorkspaceWithCommitSignoff(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	git("config", "user.name", "Inherited Author")
	git("config", "user.email", "inherited@example.com")
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	ws := c.CurrentWorkspace().WithNewFile("base.txt", "changed")
	const message = "subject\n\nCommit body."

	t.Run("does not sign off by default", func(ctx context.Context, t *testctx.T) {
		got, err := ws.WithCommit(ws.Git().Uncommitted(), message, workspaceCommitDate).Git().Head().TargetCommit().Message(ctx)
		require.NoError(t, err)
		require.Equal(t, message, strings.TrimSpace(got))
	})

	for _, tc := range []struct {
		name        string
		opts        dagger.WorkspaceWithCommitOpts
		authorName  string
		authorEmail string
	}{
		{
			name:       "inherited identity",
			opts:       dagger.WorkspaceWithCommitOpts{Signoff: true},
			authorName: "Inherited Author", authorEmail: "inherited@example.com",
		},
		{
			name:       "explicit identity",
			opts:       dagger.WorkspaceWithCommitOpts{Signoff: true, AuthorName: "Explicit Author", AuthorEmail: "explicit@example.com"},
			authorName: "Explicit Author", authorEmail: "explicit@example.com",
		},
		{
			name:       "explicit name with inherited email",
			opts:       dagger.WorkspaceWithCommitOpts{Signoff: true, AuthorName: "Explicit Author"},
			authorName: "Explicit Author", authorEmail: "inherited@example.com",
		},
		{
			name:       "inherited name with explicit email",
			opts:       dagger.WorkspaceWithCommitOpts{Signoff: true, AuthorEmail: "explicit@example.com"},
			authorName: "Inherited Author", authorEmail: "explicit@example.com",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			id, err := ws.WithCommit(ws.Git().Uncommitted(), message, workspaceCommitDate, tc.opts).ID(ctx)
			require.NoError(t, err)
			meta := dagger.Ref[*dagger.Workspace](c, id).Git().Head().TargetCommit()
			got, err := meta.Message(ctx)
			require.NoError(t, err)
			require.Equal(t, message+"\n\nSigned-off-by: "+tc.authorName+" <"+tc.authorEmail+">", strings.TrimSpace(got))
			name, err := meta.AuthorName(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.authorName, name)
			email, err := meta.AuthorEmail(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.authorEmail, email)
		})
	}
}

func (WorkspaceSuite) TestWorkspaceWithResetAmendsHistory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("base.txt", "base"))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace()
	baseSHA, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)

	committed := ws.
		WithNewFile("feature.txt", "feature").
		WithNewFile("pending.txt", "pending").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted().Filter(dagger.ChangesetFilterOpts{Include: []string{"feature.txt"}}), "draft mesage", workspaceCommitDate)
	})
	draftSHA, err := committed.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEqual(t, baseSHA, draftSHA)

	// A mixed reset moves HEAD back while the reverted commit's changes and
	// the still-pending edit both stay uncommitted.
	reset := committed.WithReset(baseSHA)
	resetSHA, err := reset.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, baseSHA, resetSHA)
	added, err := reset.Git().Uncommitted().AddedPaths(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"feature.txt", "pending.txt"}, added)
	contents, err := reset.File("feature.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "feature", contents)
	// Reapplying the same paths with a corrected message is the amend flow.
	amended, err := commitWorkspace(ctx, c, reset, "draft message, amended", []string{"feature.txt"})
	require.NoError(t, err)
	require.NotEqual(t, draftSHA, amended.Git.Head.Commit)
	require.Equal(t, "draft message, amended", strings.TrimSpace(amended.Git.Head.TargetCommit.Message))
	require.Equal(t, "Dagger", amended.Git.Head.TargetCommit.AuthorName)
	require.Equal(t, []string{"pending.txt"}, amended.Git.Uncommitted.AddedPaths)
	require.NotNil(t, amended.Git.Repository.URL)
	require.Equal(t, url, *amended.Git.Repository.URL)
	log, err := dagger.Ref[*dagger.Workspace](c, amended.ID).Git().Head().Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 2)
	feature, err := dagger.Ref[*dagger.Workspace](c, amended.ID).Git().Head().Tree().File("feature.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "feature", feature)

	// A hard reset discards everything since the commit, pending edits included.
	hard := committed.WithReset(baseSHA, dagger.WorkspaceWithResetOpts{Hard: true})
	hardSHA, err := hard.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, baseSHA, hardSHA)
	hardAdded, err := hard.Git().Uncommitted().AddedPaths(ctx)
	require.NoError(t, err)
	require.Empty(t, hardAdded)
	_, err = hard.File("feature.txt").Contents(ctx)
	require.Error(t, err)

	// Input values are immutable.
	oldSHA, err := committed.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, draftSHA, oldSHA)

	// Resetting orphans the commits after the target: the frozen repository
	// keeps reachable history only, so the reverted draft cannot be returned
	// to. The amend flow recreates it from the uncommitted changes instead.
	_, err = reset.WithReset(draftSHA).Git().Head().CommitSHA(ctx)
	require.ErrorContains(t, err, "is not in this workspace's repository")

	_, err = committed.WithReset("main").Git().Head().CommitSHA(ctx)
	require.ErrorContains(t, err, "full lowercase commit hash")
	missing := strings.Repeat("ab", 20)
	_, err = committed.WithReset(missing).Git().Head().CommitSHA(ctx)
	require.ErrorContains(t, err, "is not in this workspace's repository")
}

func (WorkspaceSuite) TestWorkspaceWithResetPreservesTree(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().
		WithNewFile("keep.txt", "unchanged").
		WithNewFile("src/change.txt", "before").
		WithNewFile("removed.txt", "removed in draft"))
	base := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace()
	baseSHA, err := base.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	draft := base.WithNewFile("src/change.txt", "after").
		WithNewFile("added.txt", "added in draft").WithoutFile("removed.txt").
		With(func(ws *dagger.Workspace) *dagger.Workspace {
			return ws.WithCommit(ws.Git().Uncommitted(), "draft", workspaceCommitDate)
		}).Git().Head()

	for _, tc := range []struct {
		name string
		ws   *dagger.Workspace
	}{
		{"clean Git ref", draft.AsWorkspace()},
		{"directory with Git metadata", draft.AsWorkspace().Directory("/").WithDirectory(".git", draft.AsWorkspace().Git().Directory()).AsWorkspace()},
		{"pending overlay", draft.AsWorkspace().WithNewFile("pending.txt", "pending")},
		{"mounted directory", draft.AsWorkspace().WithMountedDirectory("/mounted", c.Directory().WithNewFile("data.txt", "read-only"))},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			// Use a nested cwd so preservation must address the boundary root.
			ws := tc.ws.WithWorkdir("src")
			id, err := ws.WithReset(baseSHA).ID(ctx)
			require.NoError(t, err)
			reset := dagger.Ref[*dagger.Workspace](c, id)
			sha, err := reset.Git().Head().CommitSHA(ctx)
			require.NoError(t, err)
			require.Equal(t, baseSHA, sha)
			cwd, err := reset.Cwd(ctx)
			require.NoError(t, err)
			require.Equal(t, "/src", cwd)
			before := ws.Directory("/").WithoutDirectory(".git")
			after := reset.Directory("/")
			changed, err := after.Changes(before).DiffStats(ctx)
			require.NoError(t, err)
			require.Empty(t, changed, "mixed reset must preserve the complete working tree without copying old Git metadata")
			contents, err := reset.File("change.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "after", contents)
			removed, err := reset.Git().Uncommitted().RemovedPaths(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"removed.txt"}, removed)
			added, err := reset.Git().Uncommitted().AddedPaths(ctx)
			require.NoError(t, err)
			expectedAdded := []string{"added.txt"}
			if tc.name == "pending overlay" {
				expectedAdded = append(expectedAdded, "pending.txt")
			}
			require.ElementsMatch(t, expectedAdded, added)
			modified, err := reset.Git().Uncommitted().ModifiedPaths(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"src/change.txt"}, modified)
		})
	}
}

func (WorkspaceSuite) TestWorkspaceWithCommitValidation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("old.txt", strings.Repeat("rename me\n", 20)))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace().
		WithoutFile("old.txt").WithNewFile("new.txt", strings.Repeat("rename me\n", 20))
	// A filtered changeset, not Workspace, decides which rename sides to keep.
	addition, err := commitWorkspace(ctx, c, ws, "new side only", []string{"new.txt"})
	require.NoError(t, err)
	require.Equal(t, []string{"old.txt"}, addition.Git.Uncommitted.RemovedPaths)
	additionTree := dagger.Ref[*dagger.Workspace](c, addition.ID).Git().Head().Tree()
	for _, path := range []string{"old.txt", "new.txt"} {
		exists, err := additionTree.Exists(ctx, path)
		require.NoError(t, err)
		require.True(t, exists)
	}
	committed, err := commitWorkspace(ctx, c, ws, "rename", []string{"old.txt", "new.txt"})
	require.NoError(t, err)
	require.Empty(t, committed.Git.Uncommitted.AddedPaths)
	require.Empty(t, committed.Git.Uncommitted.RemovedPaths)
	_, err = commitWorkspace(ctx, c, ws, "", nil)
	require.ErrorContains(t, err, "message must be nonempty")
	_, err = commitWorkspace(ctx, c, ws, "empty selection", []string{"missing"})
	require.ErrorContains(t, err, "nothing to commit")
	_, err = ws.WithCommit(ws.Git().Uncommitted(), "bad date", "now").ID(ctx)
	require.ErrorContains(t, err, "RFC3339")
	var response any
	for _, args := range []string{`message: "missing changes", date: "2026-09-05T12:00:00Z"`, `message: "old API", date: "2026-09-05T12:00:00Z", paths: ["new.txt"]`} {
		err := c.Do(ctx, &dagger.Request{Query: `{ currentWorkspace { withCommit(` + args + `) { id } } }`}, &dagger.Response{Data: &response})
		require.Error(t, err)
	}
	_, err = commitWorkspace(ctx, c, c.Directory().WithNewFile("a", "no Git").AsWorkspace(), "no repo", nil)
	require.ErrorContains(t, err, "not in a git repository")
}

func (WorkspaceSuite) TestWorkspaceWithCommitFilteredDirectoryDeletion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("src/a.txt", "a").WithNewFile("src/b.txt", "b"))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace().WithoutDirectory("src")
	partial, err := commitWorkspace(ctx, c, ws, "delete only a", []string{"src/a.txt"})
	require.NoError(t, err)
	committed := dagger.Ref[*dagger.Workspace](c, partial.ID)
	tree := committed.Git().Head().Tree()
	exists, err := tree.Exists(ctx, "src/a.txt")
	require.NoError(t, err)
	require.False(t, exists)
	contents, err := tree.File("src/b.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "b", contents, "the excluded deletion must not enter the commit")
	require.Equal(t, []string{"src/"}, partial.Git.Uncommitted.RemovedPaths)
	exists, err = committed.Directory("/").Exists(ctx, "src/b.txt")
	require.NoError(t, err)
	require.False(t, exists, "the excluded deletion must remain in the working tree")

	rest, err := commitWorkspace(ctx, c, committed, "delete b", nil)
	require.NoError(t, err)
	require.Empty(t, rest.Git.Uncommitted.RemovedPaths)
	exists, err = dagger.Ref[*dagger.Workspace](c, rest.ID).Git().Head().Tree().Exists(ctx, "src/b.txt")
	require.NoError(t, err)
	require.False(t, exists)
}

func (WorkspaceSuite) TestWorkspaceWithCommitRestoresWithoutClient(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := checkpointCheckoutBase(ctx, t, c)
	recipe, err := base.With(daggerShell(`llm | with-workspace --workspace $(current-workspace | with-commit --changes $(current-workspace | git | uncommitted) --message "frozen commit" --date "2026-09-05T12:00:00Z") | portable-id`)).Stdout(ctx)
	require.NoError(t, err)
	// The CLI's owning client and checkout are gone. Restore the recipe from
	// the outer client, then create another commit using a new client identity.
	restored := dagger.Ref[*dagger.LLM](c, dagger.ID(strings.TrimSpace(recipe))).Workspace()
	message, err := restored.Git().Head().TargetCommit().Message(ctx)
	require.NoError(t, err)
	require.Equal(t, "frozen commit", strings.TrimSpace(message))
	contents, err := restored.File("tracked.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "base\ndirty\n", contents)
	log, err := restored.Git().Head().Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 3)
	checkout, git := workspaceExportCheckout(ctx, t)
	git("config", "user.name", "Restoring Author")
	git("config", "user.email", "restoring@example.com")
	restoring := connect(ctx, t, dagger.WithWorkdir(checkout))
	restored = dagger.Ref[*dagger.LLM](restoring, dagger.ID(strings.TrimSpace(recipe))).Workspace()
	next, err := commitWorkspace(ctx, restoring, restored.WithNewFile("next.txt", "next"), "next commit", nil)
	require.NoError(t, err)
	require.Equal(t, "Restoring Author", next.Git.Head.TargetCommit.AuthorName)
	require.Equal(t, "restoring@example.com", next.Git.Head.TargetCommit.AuthorEmail)
}

func (WorkspaceSuite) TestWorkspaceWithCommitFileKindsAndMetadata(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().
		WithNewFile("delete/a", "a").WithNewFile("delete/b", "b").
		WithNewFile("binary", "before\x00binary"))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace().
		WithoutDirectory("delete").
		WithNewFile("binary", "after\x00binary").
		WithNewFile("run", "#!/bin/sh\n", dagger.WorkspaceWithNewFileOpts{Permissions: 0o755}).
		WithDirectory("/", c.Directory().WithSymlink("binary", "link")).
		WithNewFile(":literal", "pathspecs are literal").
		WithConfigEnvironment("testing").
		WithMountedDirectory("/mounted", c.Directory().WithNewFile("readme", "read-only"))
	partial, err := commitWorkspace(ctx, c, ws, "literal path", []string{":literal"})
	require.NoError(t, err)
	committed, err := commitWorkspace(ctx, c, dagger.Ref[*dagger.Workspace](c, partial.ID), "file kinds", nil)
	require.NoError(t, err)
	require.Equal(t, "Dagger", committed.Git.Head.TargetCommit.AuthorName)
	require.Empty(t, committed.Git.Uncommitted.AddedPaths)
	require.Empty(t, committed.Git.Uncommitted.ModifiedPaths)
	require.Empty(t, committed.Git.Uncommitted.RemovedPaths)
	frozen := dagger.Ref[*dagger.Workspace](c, committed.ID)
	tree := frozen.Git().Head().Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	out, err := c.Container().From(alpineImage).WithDirectory("/tree", tree).WithWorkdir("/tree").
		WithExec([]string{"sh", "-ec", "test -x run; test ! -e delete; test ! -e mounted; test -L link; readlink link"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "binary\n", out)
	data, err := frozen.File("binary").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "after\x00binary", data)
	mounted, err := frozen.File("/mounted/readme").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "read-only", mounted)
	next, err := commitWorkspace(ctx, c, frozen.WithNewFile("next", "next"), "next commit", nil)
	require.NoError(t, err)
	require.Equal(t, committed.Git.Head.TargetCommit.AuthorName, next.Git.Head.TargetCommit.AuthorName)
}

func (WorkspaceSuite) TestWorkspaceWithCommitDirectoryRepository(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("base.txt", "base"))
	directory := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).
		Branch("main").Tree().WithNewFile("base.txt", "changed")
	committed, err := commitWorkspace(ctx, c, directory.AsWorkspace(), "directory repo", nil)
	require.NoError(t, err)
	require.Empty(t, committed.Git.Uncommitted.ModifiedPaths)
	frozen := dagger.Ref[*dagger.Workspace](c, committed.ID)
	contents, err := frozen.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "changed", contents)
	log, err := frozen.Git().Head().Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 2)
}

func (WorkspaceSuite) TestWorkspaceWithCommitLoadsCommittedModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"modules/probe\"\n").
		WithNewFile("modules/probe/dagger.json", `{"name":"probe","engineVersion":"v1.0.0","sdk":"go"}`).
		WithNewFile("modules/probe/main.go", "package main\ntype Probe struct{}\n"))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace().
		WithNewFile("modules/probe/main.go", `package main
type Probe struct{}
// +check
func (*Probe) Committed() error { return nil }
`)
	committed, err := commitWorkspace(ctx, c, ws, "add module", nil)
	require.NoError(t, err)
	checks, err := dagger.Ref[*dagger.Workspace](c, committed.ID).Checks(dagger.WorkspaceChecksOpts{NoGenerate: true}).List(ctx)
	require.NoError(t, err)
	require.Len(t, checks, 1)
	name, err := checks[0].Name(ctx)
	require.NoError(t, err)
	require.Equal(t, "probe:committed", name)
}

func (WorkspaceSuite) TestWorkspaceWithCommitIncomingChanges(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	before := c.Directory().WithNewFile("base.txt", "base\n").WithNewFile("keep.txt", "keep\n")
	daemon, url := gitService(ctx, t, c, before)
	base := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Head().AsWorkspace()
	// This baseline is deliberately not HEAD: unchanged foreign content must
	// not be imported, while the actual incoming delta must survive in both trees.
	foreign := before.WithNewFile("foreign.txt", "not a change\n")
	incoming := foreign.WithNewFile("base.txt", "incoming\n").WithNewFile("new.txt", "new\n").Changes(foreign)
	for _, dirty := range []bool{false, true} {
		name := "clean receiver"
		ws := base
		if dirty {
			name = "retain unrelated dirt"
			ws = ws.WithNewFile("pending.txt", "pending\n").WithNewFile("keep.txt", "pending edit\n")
		}
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			id, err := ws.WithCommit(incoming, "incoming delta", workspaceCommitDate).ID(ctx)
			require.NoError(t, err)
			got := dagger.Ref[*dagger.Workspace](c, id)
			for _, tree := range []*dagger.Directory{got.Directory("/"), got.Git().Head().Tree()} {
				for file, want := range map[string]string{"base.txt": "incoming\n", "new.txt": "new\n"} {
					content, err := tree.File(file).Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, want, content)
				}
				exists, err := tree.Exists(ctx, "foreign.txt")
				require.NoError(t, err)
				require.False(t, exists)
			}
			added, err := got.Git().Uncommitted().AddedPaths(ctx)
			require.NoError(t, err)
			modified, err := got.Git().Uncommitted().ModifiedPaths(ctx)
			require.NoError(t, err)
			if dirty {
				require.Equal(t, []string{"pending.txt"}, added)
				require.Equal(t, []string{"keep.txt"}, modified)
				content, err := got.File("keep.txt").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "pending edit\n", content)
			} else {
				require.Empty(t, added)
				require.Empty(t, modified)
			}
			// External before/after trees are retained in a stable recipe too;
			// replay must neither sample host state nor rerun the commit boundary.
			recipe, err := c.LLM().WithWorkspace(got).PortableID(ctx)
			require.NoError(t, err)
			recipeID := new(call.ID)
			require.NoError(t, recipeID.Decode(string(recipe)))
			dag, err := recipeID.ToProto()
			require.NoError(t, err)
			for _, vertex := range dag.GetRecipe().CallsByDigest {
				require.NotContains(t, []string{"currentWorkspace", "checkpoint", "withCommit", "branch"}, vertex.Field)
			}
			restored := dagger.Ref[*dagger.LLM](c, recipe).Workspace()
			content, err := restored.File("base.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "incoming\n", content)
			sha, err := got.Git().Head().CommitSHA(ctx)
			require.NoError(t, err)
			restoredSHA, err := restored.Git().Head().CommitSHA(ctx)
			require.NoError(t, err)
			require.Equal(t, sha, restoredSHA)
			_, err = got.WithCommit(incoming, "already in HEAD", workspaceCommitDate).ID(ctx)
			require.ErrorContains(t, err, "nothing to commit")
			_, err = ws.WithCommit(before.Changes(before), "empty", workspaceCommitDate).ID(ctx)
			require.ErrorContains(t, err, "nothing to commit")
			content, err = ws.File("base.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "base\n", content, "the receiver is immutable")
		})
	}
}

func (WorkspaceSuite) TestWorkspaceWithCommitIgnoresGitMetadata(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	before := c.Directory().WithNewFile("base.txt", "base")
	daemon, url := gitService(ctx, t, c, before)
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Head().AsWorkspace()
	metadata := before.WithNewFile(".git/HEAD", "ref: refs/heads/injected\n").
		WithNewFile(".git/config", "[remote \"origin\"]\nurl = file:///injected\n")
	_, err := ws.WithCommit(metadata.Changes(before), "metadata only", workspaceCommitDate).ID(ctx)
	require.ErrorContains(t, err, "nothing to commit")
	id, err := ws.WithCommit(metadata.WithNewFile("base.txt", "changed").Changes(before), "mixed input", workspaceCommitDate).ID(ctx)
	require.NoError(t, err)
	got := dagger.Ref[*dagger.Workspace](c, id)
	content, err := got.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "changed", content)
	head, err := got.Git().Directory().File("HEAD").Contents(ctx)
	require.NoError(t, err)
	require.NotContains(t, head, "injected")
	config, err := got.Git().Directory().File("config").Contents(ctx)
	require.NoError(t, err)
	require.NotContains(t, config, "injected")
	origin, err := got.Git().Head().AsRepository().URL(ctx)
	require.NoError(t, err)
	require.Equal(t, url, origin)
	for _, tree := range []*dagger.Directory{got.Directory("/"), got.Git().Head().Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})} {
		exists, err := tree.Exists(ctx, ".git")
		require.NoError(t, err)
		require.False(t, exists)
	}
	pending, err := got.Git().Uncommitted().IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, pending)
}

func (WorkspaceSuite) TestWorkspaceWithCommitMergeConflicts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	before := c.Directory().WithNewFile("base.txt", "base\n")
	daemon, url := gitService(ctx, t, c, before)
	base := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Head().AsWorkspace()
	incoming := before.WithNewFile("base.txt", "incoming\n").Changes(before)
	t.Run("working tree conflict", func(ctx context.Context, t *testctx.T) {
		ws := base.WithNewFile("base.txt", "pending\n")
		_, err := ws.WithCommit(incoming, "conflict", workspaceCommitDate).ID(ctx)
		require.ErrorContains(t, err, "apply commit changes to working tree")
		require.ErrorContains(t, err, "base.txt")
		content, err := ws.File("base.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "pending\n", content)
	})
	t.Run("HEAD conflict even when working tree matches", func(ctx context.Context, t *testctx.T) {
		ours := base.WithNewFile("base.txt", "committed\n")
		ours = ours.WithCommit(ours.Git().Uncommitted(), "diverge", workspaceCommitDate)
		ws := ours.WithNewFile("base.txt", "incoming\n")
		_, err := ws.WithCommit(incoming, "conflict", workspaceCommitDate).ID(ctx)
		require.ErrorContains(t, err, "apply commit changes")
		require.NotContains(t, err.Error(), "apply commit changes to working tree")
		require.ErrorContains(t, err, "base.txt")
		content, err := ws.Git().Head().Tree().File("base.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "committed\n", content)
	})
	t.Run("compatible edits in the same file", func(ctx context.Context, t *testctx.T) {
		text := "first\n" + strings.Repeat("context\n", 10) + "last\n"
		ours := base.WithNewFile("base.txt", text)
		ours = ours.WithCommit(ours.Git().Uncommitted(), "multiline base", workspaceCommitDate)
		baseline := ours.Git().Head().Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
		selected := baseline.WithNewFile("base.txt", strings.Replace(text, "first", "selected", 1)).Changes(baseline)
		ws := ours.WithNewFile("base.txt", strings.Replace(text, "last", "pending", 1))
		committed := ws.WithCommit(selected, "partial file", workspaceCommitDate)
		content, err := committed.File("base.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "selected\n"+strings.Repeat("context\n", 10)+"pending\n", content)
		content, err = committed.Git().Head().Tree().File("base.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, strings.Replace(text, "first", "selected", 1), content)
	})
}

func (WorkspaceSuite) TestWorkspaceWithCommitLiteralPaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("a*.txt", "old").WithNewFile("abc.txt", "old").WithNewFile("dir[1]/file", "old"))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Head().AsWorkspace().
		WithNewFile("a*.txt", "literal").WithNewFile("abc.txt", "unselected").WithNewFile("dir[1]/file", "selected directory")
	result := ws.WithCommit(ws.Git().Uncommitted().Filter(dagger.ChangesetFilterOpts{Include: []string{`a\*.txt`, `dir\[1]`}}), "literal paths", workspaceCommitDate)
	for file, want := range map[string]string{"a*.txt": "literal", "abc.txt": "old", "dir[1]/file": "selected directory"} {
		got, err := result.Git().Head().Tree().File(file).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	modified, err := result.Git().Uncommitted().ModifiedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"abc.txt"}, modified)
}
