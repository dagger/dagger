package core

// These tests create commits through the generated Go SDK and inspect their
// trees, metadata, and history through the public Git APIs.

import (
	"context"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// Native transactions must produce the same commit object as the general
// reconciliation path, not merely equivalent checked-out bytes. The oracle
// deliberately obscures Git-tree provenance without changing the source tree.
func (GitSuite) TestGitRefWithCommitNative(ctx context.Context, t *testctx.T) {
	if runWithPrivateTraceSession(ctx, t) {
		return
	}
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	const date = "2026-09-05T12:00:00Z"
	fixture := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/repo").
		WithExec([]string{"sh", "-ec", `
		git init -b main
		git config user.name Author
		git config user.email author@example.com
		mkdir -p nested
		printf 'old\n' > file.txt
		printf 'remove\n' > delete.txt
		printf 'old nested\n' > nested/a.txt
		printf '*.txt text eol=lf\n*.id ident\n' > .gitattributes
		printf '$Id$\n' > identity.id
		printf 'ignored*\n' > .gitignore
		git add .
		git commit -m base
		git branch side
		git tag baseline
		git gc --prune=now
		touch -t 200101010000 .git/objects/pack/*
		`}).Directory("/repo")
	packStat := func(stage string) string {
		t.Helper()
		out, err := c.Container().From(alpineImage).
			WithMountedDirectory("/repo", fixture).
			WithEnvVariable("INSPECTION_STAGE", stage).
			WithExec([]string{"sh", "-ec", "stat -c '%n %Y %a' /repo/.git/objects/pack/*"}).Stdout(ctx)
		require.NoError(t, err)
		return out
	}
	packsBefore := packStat("before")
	base := fixture.AsGit().Head()
	before := base.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	oracleBefore := before.WithNewFile(".oracle-provenance", "").WithoutFile(".oracle-provenance")
	commit := func(changes *dagger.Changeset) *dagger.GitRef {
		return base.WithCommit(changes, "native test\n\nKeep the body.\n", date, "Author", "author@example.com", dagger.GitRefWithCommitOpts{
			Signoff: true, CommitterName: "Committer", CommitterEmail: "committer@example.com",
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*dagger.Directory) *dagger.Directory
	}{
		{"ordinary", func(d *dagger.Directory) *dagger.Directory {
			return d.WithNewFile("file.txt", "new\n")
		}},
		{"add delete and replace directory", func(d *dagger.Directory) *dagger.Directory {
			return d.WithoutFile("delete.txt").WithoutDirectory("nested").WithNewFile("nested", "now a file\n").WithNewFile("added", "new\n")
		}},
		{"executable", func(d *dagger.Directory) *dagger.Directory {
			return d.WithNewFile("run", "#!/bin/sh\n", dagger.DirectoryWithNewFileOpts{Permissions: 0o755})
		}},
		{"eol and ident", func(d *dagger.Directory) *dagger.Directory {
			return d.WithNewFile("nested/a.txt", "new\r\n").WithNewFile("identity.id", "$Id$\nnew\n")
		}},
		{"changed nested attributes", func(d *dagger.Directory) *dagger.Directory {
			return d.WithNewFile("nested/.gitattributes", "*.txt -text\n").WithNewFile("nested/a.txt", "new\r\n")
		}},
	} {
		t.Logf("native case: %s", tc.name)
		func() {
			changes := tc.edit(before).Changes(before)
			// Match the agent workflow: reviewing pending paths evaluates the
			// edit before committing. Raw lazy Directory diffs may still need
			// their source tree materialized while preparing changed content.
			_, err := changes.ModifiedPaths(ctx)
			require.NoError(t, err)
			fast := commit(changes)
			legacy := commit(tc.edit(oracleBefore).Changes(oracleBefore))
			got, err := fast.CommitSHA(ctx)
			require.NoError(t, err)
			want, err := legacy.CommitSHA(ctx)
			require.NoError(t, err)
			require.Equal(t, want, got, "native and legacy commit objects differ")
			branch, err := fast.AsRepository().Branch("main").CommitSHA(ctx)
			require.NoError(t, err)
			require.Equal(t, got, branch)
			baseSHA, err := base.CommitSHA(ctx)
			require.NoError(t, err)
			// Native storage retains unrelated refs instead of pruning them as
			// a fresh single-ref fetch would. The source remains immutable.
			side, err := fast.AsRepository().Branch("side").CommitSHA(ctx)
			require.NoError(t, err)
			require.Equal(t, baseSHA, side)
			tag, err := fast.AsRepository().Tag("baseline").CommitSHA(ctx)
			require.NoError(t, err)
			require.Equal(t, baseSHA, tag)
			// A second transaction must read the first one's durable objects,
			// after its temporary index/worktree and parent mount are gone.
			nextBefore := fast.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
			nextChanges := nextBefore.WithNewFile("followup", tc.name).Changes(nextBefore)
			_, err = nextChanges.AddedPaths(ctx)
			require.NoError(t, err)
			next := fast.WithCommit(nextChanges, "followup", date, "Author", "author@example.com")
			contents, err := next.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}).File("followup").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.name, contents)
			parents, err := next.TargetCommit().ParentShas(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{got}, parents)
		}()
	}
	// Host snapshots select refs by exact SHA. Commit concurrently from that
	// representation as well as the named branch exercised above: neither
	// transaction may mutate the shared ref, index, objects or source branch.
	baseSHA, err := base.CommitSHA(ctx)
	require.NoError(t, err)
	pinned := base.AsRepository().Ref(baseSHA)
	pinnedTree := pinned.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	forks := make([]*dagger.GitRef, 2)
	for i, name := range []string{"fork-a", "fork-b"} {
		changes := pinnedTree.WithNewFile(name, name).Changes(pinnedTree)
		_, err := changes.AddedPaths(ctx)
		require.NoError(t, err)
		forks[i] = pinned.WithCommit(changes, name, date, "Author", "author@example.com")
	}
	var group errgroup.Group
	shas := make([]string, len(forks))
	for i, fork := range forks {
		group.Go(func() error {
			sha, err := fork.CommitSHA(ctx)
			shas[i] = sha
			return err
		})
	}
	require.NoError(t, group.Wait())
	require.NotEqual(t, shas[0], shas[1])
	for i, fork := range forks {
		parents, err := fork.TargetCommit().ParentShas(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{baseSHA}, parents)
		branch, err := fork.AsRepository().Branch("main").CommitSHA(ctx)
		require.NoError(t, err)
		require.Equal(t, baseSHA, branch, "a detached transaction must not advance the source branch")
		entries, err := fork.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}).Entries(ctx)
		require.NoError(t, err)
		if i == 0 {
			require.Contains(t, entries, "fork-a")
			require.NotContains(t, entries, "fork-b")
		} else {
			require.Contains(t, entries, "fork-b")
			require.NotContains(t, entries, "fork-a")
		}
	}
	original, err := before.File("file.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "old\n", original)
	require.Equal(t, packsBefore, packStat("after"), "native transactions must not freshen source packs")
	require.NoError(t, c.Close())

	traces, _ := sink.capture()
	parents, names := map[string]string{}, map[string]string{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					id := string(span.TraceId) + string(span.SpanId)
					parents[id], names[id] = string(span.TraceId)+string(span.ParentSpanId), span.Name
				}
			}
		}
	}
	var transactions, writes int
	for id, name := range names {
		if name == "git native commit transaction" {
			transactions++
		}
		for parent := parents[id]; parent != ""; parent = parents[parent] {
			if names[parent] != "git native commit transaction" {
				continue
			}
			require.False(t, strings.HasPrefix(name, "git fetch") || strings.HasPrefix(name, "fetching "), "native transaction fetched: %s", name)
			require.NotEqual(t, "materialize local git checkout", name)
			if name == "git commit-tree" {
				writes++
			}
			break
		}
	}
	require.GreaterOrEqual(t, transactions, 5, "must execute native transactions, not only the fallback")
	require.GreaterOrEqual(t, writes, 5, "must actually write native commits")
}

// The original storage deliberately has dirty tracked and untracked files.
// It is not a valid seed for a committed checkout, even when its HEAD matches.
func gitIncrementalCheckoutFixture(c *dagger.Client) (*dagger.Directory, *dagger.Container) {
	inspector := c.Container().From(alpineImage).WithExec([]string{"apk", "add", "git", "python3"})
	fixture := inspector.WithWorkdir("/repo").
		WithEnvVariable("GIT_AUTHOR_DATE", workspaceCommitDate).
		WithEnvVariable("GIT_COMMITTER_DATE", workspaceCommitDate).
		WithExec([]string{"sh", "-ec", `
git init -b main
git config user.name Oracle
git config user.email oracle@example.com
mkdir -p deep/dir nested
printf 'base\n' > selected.txt
printf 'base pending\n' > pending.txt
printf 'delete\n' > delete.txt
printf 'leaf\n' > deep/dir/leaf
printf 'file\n' > deep/file
printf 'file\n' > deep/to-link
printf '#!/bin/sh\n' > run
printf '*.txt text eol=lf\n*.id ident\n' > .gitattributes
printf '*.txt text eol=crlf\n*.id ident\n' > nested/.gitattributes
printf 'unchanged\n' > nested/unchanged.txt
printf 'nested\n' > nested/edit.txt
printf '$Id$\n' > nested/identity.id
ln -s ../selected.txt deep/link
git add .
git commit -m base
git tag baseline
printf 'dirty tracked\n' > pending.txt
printf 'dirty untracked\n' > untracked.txt
`}).Directory("/repo")
	return fixture, inspector
}

// Reopen plain metadata instead of obscuring changes.Before: withCommit can
// attach checkout provenance to both native and legacy transaction results.
func gitFullCheckoutOracle(ref *dagger.GitRef) *dagger.Directory {
	return ref.AsWorkspace().Git().Directory().AsGit().Head().Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
}

// Manifests intentionally exclude times. Source-only Git checkouts normalize
// every path to 1s. Container mounting/copying replaces the root's mtime even
// for the legacy oracle, so inspect its descendants here; backend tests check
// the snapshot root directly, before this consumer wrapper changes it.
func requireGitCheckoutTimes(ctx context.Context, t *testctx.T, inspector *dagger.Container, dir *dagger.Directory) {
	t.Helper()
	out, err := inspector.WithDirectory("/inspect", dir).
		WithExec([]string{"python3", "-c", `
import os, stat
bad = []
def visit(path):
    s = os.lstat(path)
    if path != '/inspect' and s.st_mtime_ns != 1000000000:
        bad.append((path, s.st_mtime_ns))
    if stat.S_ISDIR(s.st_mode):
        for name in sorted(os.listdir(path)):
            visit(os.path.join(path, name))
visit('/inspect')
assert not bad, bad
print('all paths normalized')
`}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "all paths normalized\n", out)
}

func (GitSuite) TestGitRefIncrementalCheckoutOracle(ctx context.Context, t *testctx.T) {
	if runWithPrivateTraceSession(ctx, t) {
		return
	}
	c := connect(ctx, t, dagger.WithLogOutput(io.Discard))
	fixture, inspector := gitIncrementalCheckoutFixture(c)
	original := workspaceCommitManifest(ctx, t, inspector, fixture)
	base := fixture.AsGit().Branch("main")
	before := base.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	baseManifest := workspaceCommitManifest(ctx, t, inspector, before) // Warm canonical parent.
	require.NotContains(t, baseManifest, "untracked.txt")
	require.Equal(t, hex.EncodeToString([]byte("base pending\n")), baseManifest["pending.txt"].Contents)
	for _, tc := range []struct {
		name    string
		edit    func(*dagger.Directory) *dagger.Directory
		include []string
		check   func(map[string]workspaceCommitManifestEntry)
	}{
		{name: "selected edit excludes pending", edit: func(d *dagger.Directory) *dagger.Directory {
			return d.WithNewFile("selected.txt", "selected\n").WithNewFile("pending.txt", "must not leak\n").WithNewFile("pending-add", "must not leak\n")
		}, include: []string{"selected.txt"}, check: func(m map[string]workspaceCommitManifestEntry) {
			require.Equal(t, hex.EncodeToString([]byte("selected\n")), m["selected.txt"].Contents)
			require.Equal(t, baseManifest["pending.txt"], m["pending.txt"])
			require.NotContains(t, m, "pending-add")
		}},
		{name: "adds and deletes", edit: func(d *dagger.Directory) *dagger.Directory {
			return d.WithoutFile("delete.txt").WithNewFile("new/deep/added.txt", "added\n")
		}},
		{name: "deep file directory and symlink replacements", edit: func(d *dagger.Directory) *dagger.Directory {
			return d.WithoutDirectory("deep/dir").WithNewFile("deep/dir", "regular now\n").
				WithoutFile("deep/file").WithNewFile("deep/file/sub/leaf", "replacement\n").
				WithoutFile("deep/link").WithNewFile("deep/link/sub/leaf", "directory now\n").
				WithoutFile("deep/to-link").WithSymlink("../selected.txt", "deep/to-link")
		}},
		{name: "executable bits", edit: func(d *dagger.Directory) *dagger.Directory {
			return d.WithNewFile("run", "#!/bin/sh\n", dagger.DirectoryWithNewFileOpts{Permissions: 0o755}).
				WithNewFile("new-run", "#!/bin/sh\n", dagger.DirectoryWithNewFileOpts{Permissions: 0o755})
		}},
		{name: "static nested eol and ident", edit: func(d *dagger.Directory) *dagger.Directory {
			return d.WithNewFile("nested/edit.txt", "new\n").WithNewFile("nested/identity.id", "$Id$\nnew\n")
		}, check: func(m map[string]workspaceCommitManifestEntry) {
			require.Equal(t, hex.EncodeToString([]byte("new\r\n")), m["nested/edit.txt"].Contents)
			contents, err := hex.DecodeString(m["nested/identity.id"].Contents)
			require.NoError(t, err)
			require.Contains(t, string(contents), "$Id: ")
		}},
		{name: "changed attributes resmudge unchanged blob", edit: func(d *dagger.Directory) *dagger.Directory {
			return d.WithNewFile("nested/.gitattributes", "*.txt text eol=lf\n*.id ident\n")
		}, check: func(m map[string]workspaceCommitManifestEntry) {
			require.Equal(t, hex.EncodeToString([]byte("unchanged\r\n")), baseManifest["nested/unchanged.txt"].Contents)
			require.Equal(t, hex.EncodeToString([]byte("unchanged\n")), m["nested/unchanged.txt"].Contents)
		}},
		{name: "same tree allow empty", edit: func(d *dagger.Directory) *dagger.Directory { return d }},
	} {
		// Inline: testctx subtests run in parallel, which would race telemetry
		// draining and client closure in related trace tests.
		t.Logf("incremental checkout oracle: %s", tc.name)
		changes := tc.edit(before).Changes(before)
		if len(tc.include) > 0 {
			changes = changes.Filter(dagger.ChangesetFilterOpts{Include: tc.include})
		}
		_, err := changes.ModifiedPaths(ctx)
		require.NoError(t, err)
		committed := base.WithCommit(changes, tc.name, workspaceCommitDate, "Oracle", "oracle@example.com", dagger.GitRefWithCommitOpts{AllowEmpty: true})
		fast := committed.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
		legacy := gitFullCheckoutOracle(committed)
		got := workspaceCommitManifest(ctx, t, inspector, fast)
		require.Equal(t, workspaceCommitManifest(ctx, t, inspector, legacy), got, tc.name)
		require.NotContains(t, got, "untracked.txt")
		require.NotContains(t, got, ".git")
		requireGitCheckoutTimes(ctx, t, inspector, fast)
		requireGitCheckoutTimes(ctx, t, inspector, legacy)
		if tc.check != nil {
			tc.check(got)
		}
		if tc.name == "same tree allow empty" {
			require.Equal(t, baseManifest, got)
			baseSHA, err := base.CommitSHA(ctx)
			require.NoError(t, err)
			sha, err := committed.CommitSHA(ctx)
			require.NoError(t, err)
			require.NotEqual(t, baseSHA, sha, "allow-empty must create a new commit")
		}
		if tc.name == "changed attributes resmudge unchanged blob" {
			metadata := committed.AsWorkspace().Git().Directory()
			out, err := inspector.WithMountedDirectory("/repo/.git", metadata).WithWorkdir("/repo").
				WithExec([]string{"sh", "-ec", `test "$(git rev-parse HEAD:nested/unchanged.txt)" = "$(git rev-parse HEAD^:nested/unchanged.txt)"`}).Stdout(ctx)
			require.NoError(t, err, out)
		}
		if tc.name == "adds and deletes" {
			baseSHA, err := base.CommitSHA(ctx)
			require.NoError(t, err)
			// Legacy commit storage may prune unrelated branches; the current
			// branch must use the new tip, while a reachable tag and parent SHA
			// must not inherit that tip's incremental checkout.
			require.Equal(t, got, workspaceCommitManifest(ctx, t, inspector, committed.AsRepository().Branch("main").Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})))
			for _, ref := range []*dagger.GitRef{
				committed.AsRepository().Tag("baseline"),
				committed.AsRepository().Ref("refs/tags/baseline"),
				committed.AsRepository().Ref(baseSHA),
			} {
				// Repository-level provenance must not seed another tip's tree.
				require.Equal(t, baseManifest, workspaceCommitManifest(ctx, t, inspector, ref.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})))
			}
		}
	}
	finalInspector := inspector.WithEnvVariable("INSPECTION_STAGE", "after commits")
	require.Equal(t, baseManifest, workspaceCommitManifest(ctx, t, finalInspector, before), "canonical parent mutated")
	require.Equal(t, original, workspaceCommitManifest(ctx, t, finalInspector, fixture), "original repository storage mutated")
}

func (GitSuite) TestGitRefIncrementalCheckoutTrace(ctx context.Context, t *testctx.T) {
	if runWithPrivateTraceSession(ctx, t) {
		return
	}
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture, inspector := gitIncrementalCheckoutFixture(c)
	base := fixture.AsGit().Head()
	before := base.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	workspaceCommitManifest(ctx, t, inspector, before) // Materialize outside optimized span.
	changes := before.WithNewFile("selected.txt", "trace selected\n").Changes(before)
	_, err := changes.ModifiedPaths(ctx)
	require.NoError(t, err)
	committed := base.WithCommit(changes, "incremental trace", workspaceCommitDate, "Oracle", "oracle@example.com")
	fast := committed.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	require.Equal(t, workspaceCommitManifest(ctx, t, inspector, gitFullCheckoutOracle(committed)), workspaceCommitManifest(ctx, t, inspector, fast))
	requireGitCheckoutTimes(ctx, t, inspector, fast)
	require.NoError(t, c.Close()) // All cases above are inline; drain finished spans.

	traces, _ := sink.capture()
	parents, names, supported, changed := map[string]string{}, map[string]string{}, map[string]bool{}, map[string]int64{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					parents[id], names[id] = string(span.TraceId)+string(span.ParentSpanId), span.Name
					if span.Name != "materialize incremental git checkout" {
						continue
					}
					for _, attr := range span.Attributes {
						switch attr.Key {
						case "dagger.git.checkout.incremental.supported":
							supported[id] = attr.Value.GetBoolValue()
						case "dagger.git.checkout.incremental.changed_paths":
							changed[id] = attr.Value.GetIntValue()
						}
					}
				}
			}
		}
	}
	var materializations, checkouts, fullCheckouts int
	for id, name := range names {
		if supported[id] {
			materializations++
			require.Equal(t, int64(1), changed[id], "must check out just the selected path")
		}
		if name == "materialize local git checkout" {
			fullCheckouts++
		}
		for parent := parents[id]; parent != ""; parent = parents[parent] {
			if !supported[parent] {
				continue
			}
			require.False(t, strings.HasPrefix(name, "git fetch") || strings.HasPrefix(name, "fetching "), "incremental checkout fetched: %s", name)
			require.NotEqual(t, "materialize local git checkout", name, "warmed parent must not be checked out again")
			require.False(t, name == "git checkout" || strings.HasPrefix(name, "git checkout "), "must not run a full checkout")
			if name == "git checkout-index" || strings.HasPrefix(name, "git checkout-index ") {
				checkouts++
			}
			break
		}
	}
	require.Positive(t, fullCheckouts, "unannotated oracle must exercise full materialization")
	require.Positive(t, materializations, "must execute supported incremental materialization")
	require.Positive(t, checkouts, "must actually check out changed paths, not merely report eligibility")
}

func (GitSuite) TestGitRefRetainedCheckoutSurvivesSourceScope(ctx context.Context, t *testctx.T) {
	if runWithPrivateTraceSession(ctx, t) {
		return
	}
	c := connect(ctx, t)
	fixture, _ := gitIncrementalCheckoutFixture(c)
	base := fixture.AsGit().Head()
	before := base.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	committed := base.WithCommit(before.WithNewFile("selected.txt", "retained\n").Changes(before), "retained", workspaceCommitDate, "Oracle", "oracle@example.com")
	sha, err := committed.CommitSHA(ctx)
	require.NoError(t, err)
	baseSHA, err := base.CommitSHA(ctx)
	require.NoError(t, err)
	id, err := committed.ID(ctx)
	require.NoError(t, err)
	var result struct {
		Node struct {
			Tree struct{ ID dagger.ID }
		}
	}
	// Explicit depth zero requests full retained history; the SDK omits zero.
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on GitRef { tree(depth: 0, discardGitDir: false) { id } } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &result}))
	exported := filepath.Join(t.TempDir(), "retained")
	_, err = dagger.Ref[*dagger.Directory](c, result.Node.Tree.ID).Export(ctx, exported)
	require.NoError(t, err)
	require.NoError(t, c.Close())

	// A fresh client has only the exported filesystem, not the source scope's
	// mounts. This catches borrowed alternates, gitfiles and temporary indexes.
	consumer := connect(ctx, t)
	out, err := consumer.Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).
		WithMountedDirectory("/repo", consumer.Host().Directory(exported)).WithWorkdir("/repo").
		WithExec([]string{"sh", "-ec", `
test -d .git
test ! -s .git/objects/info/alternates
git fsck --full --no-dangling >&2
test -z "$(git status --porcelain)"
test "$(cat selected.txt)" = retained
git log --format=%H
`}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{sha, baseSHA}, strings.Fields(out))
}

func (GitSuite) TestGitRefWithCommit(ctx context.Context, t *testctx.T) {
	if runWithPrivateTraceSession(ctx, t) {
		return
	}
	c := connect(ctx, t, dagger.WithLogOutput(io.Discard))
	const date = "2026-09-05T12:00:00Z"
	const baseText = "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("file.txt", baseText).WithNewFile("delete.txt", "delete"))
	base := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Head()
	before := base.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	commit := func(ref *dagger.GitRef, changes *dagger.Changeset, opts ...dagger.GitRefWithCommitOpts) *dagger.GitRef {
		return ref.WithCommit(changes, "edit", date, "Author", "author@example.com", opts...)
	}
	oursText := strings.Replace(baseText, "one", "OURS", 1)
	ours := commit(base, before.WithNewFile("file.txt", oursText).WithNewFile("ours.txt", "keep").Changes(before))
	theirsText := strings.Replace(baseText, "ten", "THEIRS", 1)
	changes := before.WithNewFile("file.txt", theirsText).WithoutFile("delete.txt").WithNewFile("added.txt", "new").Changes(before)
	result := commit(ours, changes, dagger.GitRefWithCommitOpts{
		CommitterName: "Committer", CommitterEmail: "committer@example.com", CommitterDate: "2026-09-06T12:00:00Z",
	})

	t.Run("merges independent edits and preserves inputs", func(ctx context.Context, t *testctx.T) {
		for file, want := range map[string]string{
			"file.txt": strings.Replace(theirsText, "one", "OURS", 1),
			"ours.txt": "keep", "added.txt": "new",
		} {
			got, err := result.Tree().File(file).Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, want, got, file)
		}
		entries, err := result.Tree().Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, entries, "delete.txt")
		log, err := result.Log(ctx)
		require.NoError(t, err)
		require.Len(t, log, 3)
		original, err := base.Tree().File("file.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, baseText, original)
		parent, err := ours.Tree().File("file.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, oursText, parent)
	})

	t.Run("records explicit identity dates and a single parent", func(ctx context.Context, t *testctx.T) {
		meta := result.TargetCommit()
		for _, field := range []struct {
			name string
			read func(context.Context) (string, error)
			want string
		}{
			{"author name", meta.AuthorName, "Author"},
			{"author email", meta.AuthorEmail, "author@example.com"},
			{"committer name", meta.CommitterName, "Committer"},
			{"committer email", meta.CommitterEmail, "committer@example.com"},
			{"author date", meta.AuthoredDate, date},
			{"committer date", meta.CommittedDate, "2026-09-06T12:00:00Z"},
		} {
			got, err := field.read(ctx)
			require.NoError(t, err, field.name)
			require.Equal(t, field.want, got, field.name)
		}
		parentSHA, err := ours.CommitSHA(ctx)
		require.NoError(t, err)
		parents, err := meta.ParentShas(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{parentSHA}, parents)
	})

	t.Run("does not sign off by default", func(ctx context.Context, t *testctx.T) {
		message, err := result.TargetCommit().Message(ctx)
		require.NoError(t, err)
		require.Equal(t, "edit", strings.TrimSpace(message))
	})

	t.Run("signs off as author not committer", func(ctx context.Context, t *testctx.T) {
		signed := commit(ours, changes, dagger.GitRefWithCommitOpts{
			Signoff: true, CommitterName: "Committer", CommitterEmail: "committer@example.com",
		})
		message, err := signed.TargetCommit().Message(ctx)
		require.NoError(t, err)
		require.Equal(t, "edit\n\nSigned-off-by: Author <author@example.com>", strings.TrimSpace(message))
		committer, err := signed.TargetCommit().CommitterName(ctx)
		require.NoError(t, err)
		require.Equal(t, "Committer", committer)
	})

	t.Run("rejects conflicting edits", func(ctx context.Context, t *testctx.T) {
		conflicting := before.WithNewFile("file.txt", strings.Replace(baseText, "one", "CONFLICT", 1)).Changes(before)
		_, err := commit(ours, conflicting).CommitSHA(ctx)
		require.ErrorContains(t, err, "CONFLICT")
	})

	// A nonempty changeset can still produce an empty commit when its edits are
	// already present in the parent tree.
	alreadyApplied := before.WithNewFile("file.txt", oursText).Changes(before)
	t.Run("rejects an unchanged result by default", func(ctx context.Context, t *testctx.T) {
		_, err := commit(ours, alreadyApplied).CommitSHA(ctx)
		require.ErrorContains(t, err, "nothing to commit")
	})
	t.Run("allows an explicitly empty commit", func(ctx context.Context, t *testctx.T) {
		empty := commit(ours, alreadyApplied, dagger.GitRefWithCommitOpts{AllowEmpty: true})
		emptySHA, err := empty.CommitSHA(ctx)
		require.NoError(t, err)
		parentSHA, err := ours.CommitSHA(ctx)
		require.NoError(t, err)
		require.NotEqual(t, parentSHA, emptySHA)
		unchanged, err := empty.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}).Changes(ours.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})).IsEmpty(ctx)
		require.NoError(t, err)
		require.True(t, unchanged)
	})
}
