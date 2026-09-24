package core

// These tests create commits through the generated Go SDK and inspect their
// trees, metadata, and history through the public Git APIs.

import (
	"context"
	"io"
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

func (GitSuite) TestGitRefWithCommit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
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
