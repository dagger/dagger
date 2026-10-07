package core

import (
	"cmp"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"dagger.io/dagger/engineconn"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

const workspaceCommitDate = "2026-09-05T12:00:00Z"

type workspaceCommitState struct {
	ID  core.ID
	Git struct {
		Repository struct{ URL *string }
		Head       struct {
			Commit       string
			TargetCommit struct{ Message, AuthorName, AuthorEmail, AuthoredDate, CommittedDate string }
		}
		Uncommitted struct{ AddedPaths, ModifiedPaths, RemovedPaths []string }
	}
}

func commitWorkspace(ctx context.Context, c *dagger.Client, ws *core.Workspace, message string, include []string) (got workspaceCommitState, err error) {
	got.ID, err = ws.WithCommit(ws.Git().Uncommitted().Filter(core.ChangesetFilterOpts{Include: include}), message, workspaceCommitDate).ID(ctx)
	if err != nil {
		return got, err
	}
	// Resolve the result once before reading its fields: metadata reads must
	// not repeat the workspace's host capture or commit operation.
	committed := core.Ref[*core.Workspace](core.NewQuery(c), got.ID)
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

// Reconciliation is a filesystem operation as well as a Git operation. Git's
// tree hash cannot detect dropped ownership, rich modes, xattrs or empty dirs.
// Inspect the tree as a consumer sees it, including the root directory. Times
// and inode numbers are fresh checkout artifacts, not stable between even two
// legacy runs; all xattrs except OverlayFS's origin bookkeeping are compared.
func workspaceCommitManifest(ctx context.Context, t *testctx.T, inspector *core.Container, dir *core.Directory) map[string]workspaceCommitManifestEntry {
	t.Helper()
	out, err := inspector.WithMountedDirectory("/inspect", dir).
		WithExec([]string{"python3", "-c", `
import json, os, stat
root = '/inspect'
entries = {}
def visit(rel):
    path = os.path.join(root, rel)
    s = os.lstat(path)
    item = dict(mode=s.st_mode, uid=s.st_uid, gid=s.st_gid,
        xattrs={name: os.getxattr(path, name, follow_symlinks=False).hex()
                for name in sorted(os.listxattr(path, follow_symlinks=False))
                if name != 'trusted.overlay.origin'})
    if stat.S_ISLNK(s.st_mode):
        item['target'] = os.readlink(path)
    elif stat.S_ISREG(s.st_mode):
        with open(path, 'rb') as f:
            item['contents'] = f.read().hex()
    entries[rel] = item
    if stat.S_ISDIR(s.st_mode):
        for name in sorted(os.listdir(path)):
            visit(name if rel == '.' else rel + '/' + name)
visit('.')
print(json.dumps(entries, sort_keys=True))
`}).Stdout(ctx)
	require.NoError(t, err)
	var manifest map[string]workspaceCommitManifestEntry
	require.NoError(t, json.Unmarshal([]byte(out), &manifest))
	require.Contains(t, manifest, ".", "must inspect directory root metadata")
	return manifest
}

type workspaceCommitManifestEntry struct {
	Mode     uint32            `json:"mode"`
	UID      uint32            `json:"uid"`
	GID      uint32            `json:"gid"`
	Xattrs   map[string]string `json:"xattrs"`
	Target   string            `json:"target"`
	Contents string            `json:"contents"`
}

// This is a complete local SHA-1 repository, not a git daemon or remote clone.
// Source workspaces use its direct Git tree; only the oracle obscures that
// provenance. Keep this small correctness fixture separate from the perf test.
func workspaceReconciliationFixture(c *dagger.Client) (*core.Container, *core.Container) {
	inspector := core.NewQuery(c).Container().From(alpineImage).WithExec([]string{"apk", "add", "git", "python3"})
	fixture := inspector.WithWorkdir("/repo").
		WithEnvVariable("GIT_AUTHOR_DATE", workspaceCommitDate).
		WithEnvVariable("GIT_COMMITTER_DATE", workspaceCommitDate).
		WithExec([]string{"sh", "-ec", `
git init --object-format=sha1 -b main
git config user.name Oracle
git config user.email oracle@example.com
git config commit.gpgsign false
git config core.hooksPath /dev/null
mkdir -p nested
printf 'one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n' > file.txt
printf 'base\n' > pending.txt
printf 'delete\n' > delete.txt
printf 'nested\n' > nested/a.txt
printf 'file\n' > file-to-dir
printf 'file\n' > file-to-link
printf '#!/bin/sh\n' > run
printf '$Id$\n' > identity.id
printf 'ignored*\n' > .gitignore
ln -s file.txt link
git add .
git commit -m base
git gc --prune=now
`})
	return fixture, inspector
}

type workspaceReconciliationCase struct {
	base        *core.Workspace
	name        string
	attributes  bool
	pending     func(*core.Directory) *core.Directory
	incoming    func(*core.Directory) *core.Directory
	include     []string
	conflict    bool
	wantFile    string
	checkInput  func(map[string]workspaceCommitManifestEntry)
	checkResult func(*core.Workspace)
}

func checkWorkspaceReconciliation(ctx context.Context, t *testctx.T, c *dagger.Client, fixture, inspector *core.Container, tc workspaceReconciliationCase) {
	t.Helper()
	t.Logf("reconciliation oracle: %s", tc.name)
	if tc.attributes {
		fixture = fixture.WithExec([]string{"sh", "-ec", `
printf '*.txt text eol=lf\n*.id ident\n' > .gitattributes
printf '*.txt text eol=crlf\n' > nested/.gitattributes
git add .
git commit -m attributes
`})
	}
	base := tc.base
	if base == nil {
		base = fixture.Directory("/repo").AsGit().Head().AsWorkspace()
	}
	before := base.Git().Head().Tree(core.GitRefTreeOpts{DiscardGitDir: true})
	pending := before.WithNewFile("pending.txt", "keep pending\n")
	if tc.pending != nil {
		pending = tc.pending(pending)
	}
	// Both commits must receive this exact frozen workspace, not separately
	// captured or reconstructed receivers whose inputs might differ.
	id, err := base.WithChanges(pending.Changes(before)).ID(ctx)
	require.NoError(t, err, tc.name)
	working := core.Ref[*core.Workspace](core.NewQuery(c), id)
	original := workspaceCommitManifest(ctx, t, inspector, working.Directory("/"))
	if tc.checkInput != nil {
		tc.checkInput(original)
	}
	changes := working.Git().Uncommitted().Filter(core.ChangesetFilterOpts{Include: tc.include})
	if tc.incoming != nil {
		changes = tc.incoming(before).Changes(before)
	}
	_, err = changes.ModifiedPaths(ctx) // Match review-before-commit workflow.
	require.NoError(t, err, tc.name)
	wrappedBefore := changes.Before().WithNewFile(".oracle-provenance", "").WithoutFile(".oracle-provenance")
	oracle := changes.After().Changes(wrappedBefore)
	commit := func(delta *core.Changeset) (*core.Workspace, error) {
		id, err := working.WithCommit(delta, "reconcile "+tc.name, workspaceCommitDate, core.WorkspaceWithCommitOpts{
			AuthorName: "Oracle", AuthorEmail: "oracle@example.com",
		}).ID(ctx)
		return core.Ref[*core.Workspace](core.NewQuery(c), id), err
	}
	fast, fastErr := commit(changes)
	legacy, legacyErr := commit(oracle)
	if tc.conflict {
		require.ErrorContains(t, fastErr, "conflict", tc.name)
		// The native merge's conflict is final: no legacy restage behind it.
		require.ErrorContains(t, fastErr, "merge conflict between workspace and incoming changes", tc.name)
		require.ErrorContains(t, legacyErr, "conflict", tc.name)
		require.Equal(t, original, workspaceCommitManifest(ctx, t, inspector, working.Directory("/")), "conflict mutated the receiver")
		return
	}
	require.NoError(t, fastErr, tc.name)
	require.NoError(t, legacyErr, tc.name)
	baseSHA, err := working.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	fastSHA, err := fast.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	legacySHA, err := legacy.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, legacySHA, fastSHA, "%s: exact commit objects", tc.name)
	require.NotEqual(t, baseSHA, fastSHA)
	for _, result := range []*core.Workspace{fast, legacy} {
		if tc.checkResult != nil {
			tc.checkResult(result)
		}
		parents, err := result.Git().Head().TargetCommit().ParentSHAs(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{baseSHA}, parents, tc.name)
		contents, err := result.File("pending.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep pending\n", contents, tc.name)
		if tc.wantFile != "" {
			contents, err := result.File("file.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.wantFile, contents, tc.name)
		}
	}
	fastPending, legacyPending := fast.Git().Uncommitted(), legacy.Git().Uncommitted()
	for _, pair := range []struct {
		name         string
		fast, legacy func(context.Context) ([]string, error)
	}{
		{"added", fastPending.AddedPaths, legacyPending.AddedPaths},
		{"modified", fastPending.ModifiedPaths, legacyPending.ModifiedPaths},
		{"removed", fastPending.RemovedPaths, legacyPending.RemovedPaths},
	} {
		got, err := pair.fast(ctx)
		require.NoError(t, err)
		want, err := pair.legacy(ctx)
		require.NoError(t, err)
		slices.Sort(got)
		slices.Sort(want)
		require.Equal(t, want, got, "%s: pending %s paths", tc.name, pair.name)
		if pair.name == "modified" {
			require.Contains(t, got, "pending.txt", tc.name)
		}
	}
	require.Equal(t, workspaceCommitManifest(ctx, t, inspector, legacy.Directory("/")), workspaceCommitManifest(ctx, t, inspector, fast.Directory("/")), "%s: consumer filesystem", tc.name)
	require.Equal(t, workspaceCommitManifest(ctx, t, inspector, legacy.Git().Head().Tree(core.GitRefTreeOpts{DiscardGitDir: true})), workspaceCommitManifest(ctx, t, inspector, fast.Git().Head().Tree(core.GitRefTreeOpts{DiscardGitDir: true})), "%s: committed checkout", tc.name)
	require.Equal(t, original, workspaceCommitManifest(ctx, t, inspector, working.Directory("/")), "%s: receiver must remain immutable", tc.name)
}

func (WorkspaceSuite) TestWorkspaceWithCommitReconciliationOracle(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture, inspector := workspaceReconciliationFixture(c)
	const text = "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"
	for _, tc := range []workspaceReconciliationCase{
		{name: "ordinary", pending: func(d *core.Directory) *core.Directory {
			return d.WithNewFile("file.txt", "selected\n")
		}, include: []string{"file.txt"}, wantFile: "selected\n"},
		{name: "identical selected overlap with raw metadata", pending: func(d *core.Directory) *core.Directory {
			return inspector.WithMountedDirectory("/work", d).WithExec([]string{"python3", "-c", `
import os
with open('/work/file.txt', 'w') as f: f.write('selected raw metadata\n')
os.chown('/work/file.txt', 123, 456)
os.chmod('/work/file.txt', 0o600)
os.setxattr('/work/file.txt', 'user.oracle', b'selected\x00metadata')
`}).Directory("/work")
		}, include: []string{"file.txt"}, wantFile: "selected raw metadata\n", checkInput: func(manifest map[string]workspaceCommitManifestEntry) {
			file := manifest["file.txt"]
			require.Equal(t, uint32(0o600), file.Mode&0o7777)
			require.Equal(t, uint32(123), file.UID)
			require.Equal(t, uint32(456), file.GID)
			require.Equal(t, hex.EncodeToString([]byte("selected\x00metadata")), file.Xattrs["user.oracle"])
		}},
		{name: "compatible overlapping same file", pending: func(d *core.Directory) *core.Directory {
			return d.WithNewFile("file.txt", strings.Replace(text, "one", "OURS", 1))
		}, incoming: func(d *core.Directory) *core.Directory {
			return d.WithNewFile("file.txt", strings.Replace(text, "ten", "THEIRS", 1))
		}, wantFile: strings.Replace(strings.Replace(text, "one", "OURS", 1), "ten", "THEIRS", 1)},
		{name: "conflicting same file", pending: func(d *core.Directory) *core.Directory {
			return d.WithNewFile("file.txt", "ours\n")
		}, incoming: func(d *core.Directory) *core.Directory {
			return d.WithNewFile("file.txt", "theirs\n")
		}, conflict: true},
		{name: "add delete type changes and symlinks", pending: func(d *core.Directory) *core.Directory {
			return d.WithNewFile("added.txt", "added\n").WithoutFile("delete.txt").
				WithoutDirectory("nested").WithNewFile("nested", "directory became file\n").
				WithoutFile("file-to-dir").WithNewFile("file-to-dir/sub/child", "file became nested directory\n").
				WithoutFile("file-to-link").WithSymlink("pending.txt", "file-to-link").
				WithoutFile("link").WithNewFile("link", "symlink became file\n").
				WithNewFile("pending-add", "not selected\n").WithoutFile("run")
		}, include: []string{"added.txt", "delete.txt", "nested", "nested/**", "file-to-dir", "file-to-dir/**", "file-to-link", "link"}},
		{name: "executable", pending: func(d *core.Directory) *core.Directory {
			return inspector.WithMountedDirectory("/work", d).
				WithExec([]string{"sh", "-ec", "chmod 755 /work/run; printf '#!/bin/sh\\necho new\\n' > /work/new-run; chmod 755 /work/new-run"}).Directory("/work")
		}, include: []string{"run", "new-run"}, checkInput: func(manifest map[string]workspaceCommitManifestEntry) {
			require.Equal(t, uint32(0o755), manifest["run"].Mode&0o7777)
			require.Equal(t, uint32(0o755), manifest["new-run"].Mode&0o7777)
		}},
		{name: "static root and nested attributes", attributes: true, pending: func(d *core.Directory) *core.Directory {
			return d.WithNewFile("file.txt", "selected\r\n").WithNewFile("nested/a.txt", "nested\r\nnew\r\n").WithNewFile("identity.id", "$Id$\nnew\n")
		}, include: []string{"file.txt", "nested/a.txt", "identity.id"}},
		{name: "changed root attributes", attributes: true, pending: func(d *core.Directory) *core.Directory {
			return d.WithNewFile(".gitattributes", "*.txt -text\n*.id -ident\n").WithNewFile("file.txt", "selected\r\n").WithNewFile("identity.id", "$Id$\nnew\n")
		}, include: []string{".gitattributes", "file.txt", "identity.id"}},
		{name: "changed nested attributes", attributes: true, pending: func(d *core.Directory) *core.Directory {
			return d.WithNewFile("nested/.gitattributes", "*.txt -text\n").WithNewFile("nested/a.txt", "nested\r\nnew\r\n")
		}, include: []string{"nested/**"}},
		{name: "unselected ignored file", pending: func(d *core.Directory) *core.Directory {
			return d.WithNewFile("file.txt", "selected\n").WithNewFile("ignored-pending", "ignored but visible\n")
		}, include: []string{"file.txt"}},
		{name: "introduced directory modes", pending: func(d *core.Directory) *core.Directory {
			return inspector.WithMountedDirectory("/work", d).WithExec([]string{"sh", "-ec", `
mkdir -p /work/fresh/deep /work/nested/sub
printf 'fresh\n' > /work/fresh/deep/file
printf 'sub\n' > /work/nested/sub/file
chmod 0777 /work/fresh /work/nested/sub
chmod 0700 /work/fresh/deep
`}).Directory("/work")
		}, include: []string{"fresh/**", "nested/sub/**"}, checkInput: func(manifest map[string]workspaceCommitManifestEntry) {
			require.Equal(t, uint32(0o777), manifest["fresh"].Mode&0o7777)
			require.Equal(t, uint32(0o700), manifest["fresh/deep"].Mode&0o7777)
		}},
		{name: "rich metadata and empty directories", pending: func(d *core.Directory) *core.Directory {
			return inspector.WithMountedDirectory("/work", d).WithExec([]string{"python3", "-c", `
import os
os.mkdir('/work/rich')
os.mkdir('/work/rich/empty')
with open('/work/rich/data', 'w') as f: f.write('unselected rich data\n')
for p in ['/work', '/work/rich', '/work/rich/empty', '/work/rich/data']:
    os.chown(p, 123, 456)
    os.chmod(p, 0o2750 if os.path.isdir(p) else 0o4751)
    os.setxattr(p, 'user.oracle', b'preserve\x00metadata')
with open('/work/file.txt', 'w') as f: f.write('selected\n')
`}).Directory("/work")
		}, include: []string{"file.txt"}, checkInput: func(manifest map[string]workspaceCommitManifestEntry) {
			for _, path := range []string{"rich", "rich/empty", "rich/data"} {
				require.Contains(t, manifest, path)
				require.Equal(t, uint32(123), manifest[path].UID, path)
				require.Equal(t, uint32(456), manifest[path].GID, path)
				require.Equal(t, hex.EncodeToString([]byte("preserve\x00metadata")), manifest[path].Xattrs["user.oracle"], path)
			}
			require.Equal(t, uint32(0o2750), manifest["rich"].Mode&0o7777)
			require.Equal(t, uint32(0o4751), manifest["rich/data"].Mode&0o7777)
		}},
		{name: "incoming directory metadata and empty directories", incoming: func(d *core.Directory) *core.Directory {
			return inspector.WithMountedDirectory("/work", d).WithExec([]string{"python3", "-c", `
import os
os.makedirs('/work/made/empty')
os.mkdir('/work/nested/empty')
with open('/work/nested/a.txt', 'w') as f: f.write('nested incoming\n')
for p in ['/work/nested', '/work/made']:
    os.chown(p, 123, 456)
    os.chmod(p, 0o2770)
    os.setxattr(p, 'user.oracle', b'incoming')
`}).Directory("/work")
		}},
	} {
		// testctx subtests auto-parallelize. Inline cases keep fixture use and
		// client lifetime sequential, and make the last log name the failure.
		checkWorkspaceReconciliation(ctx, t, c, fixture, inspector, tc)
	}
	// Every case runs the identity-wrapped legacy merge, conflicts included.
	require.NoError(t, c.Close())
	requireScopedMergeBases(t, sink)
	// Every unwrapped case reconciles natively, directory metadata and empty
	// directories included; only changed Git controls, and the oracle's
	// commits (whose workspace side the wrapped Before obscures), take the
	// general merge.
	var natives int
	samples := slices.Collect(maps.Values(collectWorkspaceCommitTraceSamples(sink)))
	slices.SortFunc(samples, func(a, b workspaceCommitTraceSample) int { return cmp.Compare(a.start, b.start) })
	for _, s := range samples {
		if s.name != "git native workspace merge" {
			continue
		}
		t.Logf("native merge supported=%t fallback=%q", s.nativeSupported, s.fallbackReason)
		if s.nativeSupported {
			natives++
			continue
		}
		require.Contains(t, []string{"working-before-not-git-tree", "merge-controls-change"}, s.fallbackReason)
	}
	require.Positive(t, natives)
}

func (WorkspaceSuite) TestWorkspaceRemoteFirstNativeCommit(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture, inspector := workspaceReconciliationFixture(c)
	service, url := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("file.txt", "base\n").WithNewFile("pending.txt", "base\n"))
	const pushURL = "ssh://git@push.example.test/repo"
	base := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: service}).WithRemote("origin", url, core.GitRepositoryWithRemoteOpts{PushURL: pushURL}).Head().AsWorkspace()
	checkWorkspaceReconciliation(ctx, t, c, fixture, inspector, workspaceReconciliationCase{
		base: base, name: "remote first commit", include: []string{"file.txt"}, wantFile: "selected\n",
		pending: func(d *core.Directory) *core.Directory { return d.WithNewFile("file.txt", "selected\n") },
		checkResult: func(result *core.Workspace) {
			gotURL, err := result.Git().Head().AsRepository().URL(ctx)
			require.NoError(t, err)
			require.Equal(t, url, gotURL)
			routing, err := inspector.WithMountedDirectory("/committed", result.Git().Head().Tree(core.GitRefTreeOpts{DiscardGitDir: false})).WithWorkdir("/committed").WithExec([]string{"git", "remote", "get-url", "--push", "origin"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, pushURL, strings.TrimSpace(routing))
		},
	})
	require.NoError(t, c.Close())
	traces, _ := sink.capture()
	parents, names, native := map[string]string{}, map[string]string{}, map[string]bool{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					parents[id], names[id] = string(span.TraceId)+string(span.ParentSpanId), span.Name
					for _, attr := range span.Attributes {
						if span.Name == "git native commit transaction" && attr.Key == "dagger.git.native.supported" {
							native[id] = attr.Value.GetBoolValue()
						}
					}
				}
			}
		}
	}
	var promotions, writes, incremental int
	for id, name := range names {
		if names[parents[id]] == name {
			continue
		}
		if name == "git promote remote commit base" {
			promotions++
		}
		if name == "materialize incremental git checkout" {
			incremental++
		}
		if name != "git commit-tree" {
			continue
		}
		for parent := parents[id]; parent != ""; parent = parents[parent] {
			if native[parent] {
				writes++
				break
			}
		}
	}
	require.Equal(t, 1, promotions, "merge and commit reuse one exact-recipe owned promotion")
	require.Positive(t, writes, "remote first commit must really execute native commit-tree, not merely pass eligibility")
	require.Positive(t, incremental, "remote canonical parent must be reused")
}

// requireScopedMergeBases requires the general merges in sink's trace to have
// built their merge base scoped, without falling back to a full `git add -A`
// base.
func requireScopedMergeBases(t *testctx.T, sink *agentTraceSink) {
	t.Helper()
	traces, _ := sink.capture()
	scoped := map[string]string{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.Name != "scoped git merge base" || span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					scoped[id] = "no dagger.git.scoped_merge.supported"
					for _, attr := range span.Attributes {
						switch attr.Key {
						case "dagger.git.scoped_merge.supported":
							if attr.Value.GetBoolValue() {
								scoped[id] = ""
							}
						case "dagger.git.scoped_merge.fallback_reason":
							scoped[id] = attr.Value.GetStringValue()
						}
					}
				}
			}
		}
	}
	require.NotEmpty(t, scoped, "must exercise the general merge")
	for _, reason := range scoped {
		require.Empty(t, reason, "scoped merge base fell back")
	}
}

func (WorkspaceSuite) TestWorkspaceWithCommitNativeReconciliationTrace(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture, inspector := workspaceReconciliationFixture(c)
	// Unique content keeps the merge out of the cache that parallel tests share,
	// so this test always emits its own native merge spans.
	selected := "selected-" + cryptorand.Text() + "\n"
	checkWorkspaceReconciliation(ctx, t, c, fixture, inspector, workspaceReconciliationCase{
		name: "ordinary native trace", include: []string{"file.txt"}, wantFile: selected,
		pending: func(d *core.Directory) *core.Directory { return d.WithNewFile("file.txt", selected) },
	})
	require.NoError(t, c.Close()) // Drain telemetry, after all inline cases.
	traces, _ := sink.capture()
	parents, names, supported := map[string]string{}, map[string]string{}, map[string]bool{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					parents[id], names[id] = string(span.TraceId)+string(span.ParentSpanId), span.Name
					for _, attr := range span.Attributes {
						if span.Name == "git native workspace merge" && attr.Key == "dagger.git.native_merge.supported" {
							supported[id] = attr.Value.GetBoolValue()
						}
					}
				}
			}
		}
	}
	requireScopedMergeBases(t, sink)
	var nativeMerges, mergeTrees, legacyMerges int
	for id, name := range names {
		if supported[id] {
			nativeMerges++
		}
		if name == "Changeset.__mergeWithChangeset" {
			legacyMerges++
		}
		for parent := parents[id]; parent != ""; parent = parents[parent] {
			if !supported[parent] {
				continue
			}
			require.False(t, strings.HasPrefix(name, "git fetch") || strings.HasPrefix(name, "fetching "), "native reconciliation fetched objects: %s", name)
			require.NotEqual(t, "materialize local git checkout", name, "native reconciliation checked out the baseline")
			require.NotEqual(t, "git checkout", name)
			require.NotEqual(t, "Changeset.__mergeWithChangeset", name, "native reconciliation staged a legacy baseline")
			if name == "git merge-tree" {
				mergeTrees++
			}
			break
		}
	}
	require.Positive(t, nativeMerges, "ordinary same-base local workspace must use native reconciliation")
	require.Positive(t, mergeTrees, "must actually execute merge-tree, not only report eligibility")
	require.Positive(t, legacyMerges, "identity-wrapped oracle must exercise the legacy merger")
}

// Agent commits select pending paths with Changeset.filter, on workspaces
// checked out from a remote ref, with tool edits recorded as patches, and
// after pulls of other workers' commits. Report every native reconciliation
// decision for those shapes.
func (WorkspaceSuite) TestWorkspaceCommitterShapesNativeReconciliation(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture := core.NewQuery(c).Directory().
		WithNewFile("pending.txt", "base\n").
		WithNewFile("other/o.txt", "o\n")
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		fixture = fixture.WithNewFile("sdk/php/src/"+name+".php", "<?php\n// "+name+"\n")
	}
	service, url := gitService(ctx, t, c, fixture)
	base := snapshotWorkspace(ctx, t, c, core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: service}).Head().AsWorkspace())
	commit := func(name string, ws *core.Workspace, include ...string) *core.Workspace {
		t.Helper()
		id, err := ws.WithCommit(ws.Git().Uncommitted().Filter(core.ChangesetFilterOpts{Include: include}), name, workspaceCommitDate, core.WorkspaceWithCommitOpts{
			AuthorName: "Agent", AuthorEmail: "agent@example.com",
		}).ID(ctx)
		require.NoError(t, err, name)
		committed := core.Ref[*core.Workspace](core.NewQuery(c), id)
		_, err = committed.Git().Head().CommitSHA(ctx)
		require.NoError(t, err, name)
		return committed
	}
	const patch = `diff --git a/sdk/php/src/a.php b/sdk/php/src/a.php
--- a/sdk/php/src/a.php
+++ b/sdk/php/src/a.php
@@ -1,2 +1,2 @@
 <?php
-// a
+// a changed
diff --git a/sdk/php/src/new/x.php b/sdk/php/src/new/x.php
new file mode 100644
--- /dev/null
+++ b/sdk/php/src/new/x.php
@@ -0,0 +1 @@
+x
`
	patched := base.WithPatchFile(core.NewQuery(c).Directory().WithNewFile("edit.patch", patch).File("edit.patch")).
		WithNewFile("pending.txt", "keep pending\n")
	first := commit("patched edits, filtered", patched, "sdk/php/src/**")
	second := commit("tool edit, filtered", first.WithNewFile("sdk/php/src/b.php", "<?php\n// b changed\n"), "sdk/php/**")
	source := commit("source", base.WithNewFile("other/o.txt", "o changed\n"), "other/**")
	pulled, err := applyWorkspacePull(ctx, c, snapshotWorkspace(ctx, t, c, second), snapshotWorkspace(ctx, t, c, source), nil, 100)
	require.NoError(t, err)
	commit("after pull", pulled.WithNewFile("sdk/php/src/c.php", "<?php\n// c changed\n"), "sdk/php/src/c.php")
	commit("after pull, broad", pulled.WithNewFile("sdk/php/src/d.php", "<?php\n// d changed\n"), "sdk/**")
	require.NoError(t, c.Close())

	var merges, fallbacks int
	for _, s := range collectWorkspaceCommitTraceSamples(sink) {
		switch s.name {
		case "git native workspace merge":
			merges++
			if !s.nativeSupported {
				fallbacks++
			}
			t.Logf("native merge supported=%t fallback=%q", s.nativeSupported, s.fallbackReason)
		case "Changeset.__mergeWithChangeset":
			t.Logf("general merge span")
		}
	}
	require.Positive(t, merges)
	require.Zero(t, fallbacks, "committer-shaped reconciliations must stay native")
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
	base := snapshotWorkspace(ctx, t, c, core.NewQuery(c).CurrentWorkspace())
	baseSHA, err := base.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	commit := func(message string) *core.Workspace {
		t.Helper()
		state, err := commitWorkspace(ctx, c, base.WithNewFile("base.txt", message).
			WithNewFile("pending.txt", "keep pending"), message, []string{"base.txt"})
		require.NoError(t, err)
		require.Equal(t, []string{"pending.txt"}, state.Git.Uncommitted.AddedPaths)
		return core.Ref[*core.Workspace](core.NewQuery(c), state.ID)
	}
	next, side := commit("next"), commit("side")
	for _, tc := range []struct {
		name string
		head *core.GitRef
		opts core.GitRefLogOpts
		want []string
	}{
		{name: "ahead", head: next.Git().Head(), opts: core.GitRefLogOpts{Base: base.Git().Head(), Limit: 101}, want: []string{"next"}},
		{name: "behind", head: base.Git().Head(), opts: core.GitRefLogOpts{Base: next.Git().Head(), Limit: 101}},
		{name: "divergent", head: next.Git().Head(), opts: core.GitRefLogOpts{Base: side.Git().Head()}, want: []string{"next"}},
		{name: "reverse divergent", head: side.Git().Head(), opts: core.GitRefLogOpts{Base: next.Git().Head()}, want: []string{"side"}},
		{name: "path filter", head: next.Git().Head(), opts: core.GitRefLogOpts{Base: base.Git().Head(), Paths: []string{"base.txt"}}, want: []string{"next"}},
		{name: "pending is not history", head: next.Git().Head(), opts: core.GitRefLogOpts{Paths: []string{"pending.txt"}}},
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
	// Retained full checkouts share them copy-on-write (see
	// TestWorkspaceCommittedFullCheckoutCopyOnWrite).
	require.NotEmpty(t, discardedCheckouts, "must exercise real local tree checkouts")
	var checkoutGitCommands int
	for id, name := range names {
		for parent := parents[id]; parent != ""; parent = parents[parent] {
			if discardedCheckouts[parent] {
				require.False(t, strings.HasPrefix(name, "git fetch") || strings.HasPrefix(name, "fetching "), "local source tree fetched objects: %s", name)
				if strings.HasPrefix(name, "git checkout") {
					checkoutGitCommands++
				}
				break
			}
		}
	}
	// Without Git spans beneath the checkouts, the fetch check above is vacuous.
	require.NotZero(t, checkoutGitCommands, "must observe git commands beneath local tree checkouts")
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

// A committed head's repository snapshot already holds every object. Its
// retained full checkout must share them copy-on-write, not fetch the whole
// history into an empty snapshot, and still be a complete, ordinary checkout.
func (WorkspaceSuite) TestWorkspaceCommittedFullCheckoutCopyOnWrite(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to inspect checkout spans")
	}
	checkout, hostGit := workspaceExportCheckout(ctx, t)
	hostGit("tag", "v1")
	// Large history in several packs, with a worktree much smaller than it:
	// copying any inherited pack up into the checkout's layer shows in its size.
	big := make([]byte, 32<<20)
	_, err := rand.New(rand.NewSource(1)).Read(big)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "big.bin"), big, 0o644))
	hostGit("add", "big.bin")
	hostGit("commit", "-q", "-m", "big")
	hostGit("repack", "-d", "-q")
	hostGit("rm", "-q", "big.bin")
	hostGit("commit", "-q", "-m", "unbig")
	for i := range 5000 {
		require.NoError(t, os.MkdirAll(filepath.Join(checkout, "files", fmt.Sprint(i%100)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(checkout, "files", fmt.Sprint(i%100), fmt.Sprint(i)), []byte(fmt.Sprint(i)), 0o644))
	}
	hostGit("add", "files")
	hostGit("commit", "-q", "-m", "files")
	hostGit("repack", "-d", "-q")
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithWorkdir(checkout))...)
	base := snapshotWorkspace(ctx, t, c, core.NewQuery(c).CurrentWorkspace())
	initialSHA := hostGit("rev-parse", "v1")
	state, err := commitWorkspace(ctx, c, base.WithNewFile("base.txt", "next"), "next", []string{"base.txt"})
	require.NoError(t, err)
	next := core.Ref[*core.Workspace](core.NewQuery(c), state.ID)
	// The retained full checkout of the committed head (GitRef.__fullCheckout).
	// The Go SDK omits zero-valued depth, so use GraphQL here.
	refID, err := next.Git().Head().ID(ctx)
	require.NoError(t, err)
	var fullTree struct {
		Node struct {
			Tree struct{ ID core.ID }
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on GitRef { tree(depth: 0, discardGitDir: false) { id } } } }`,
		Variables: map[string]any{"id": refID},
	}, &dagger.Response{Data: &fullTree}))
	export := t.TempDir()
	_, err = core.Ref[*core.Directory](core.NewQuery(c), fullTree.Node.Tree.ID).Export(ctx, export)
	require.NoError(t, err)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = export
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return strings.TrimSpace(string(out))
	}
	git("fsck", "--strict", "--no-dangling")
	require.Equal(t, state.Git.Head.Commit, git("rev-parse", "HEAD"))
	require.Equal(t, "next\nfiles\nunbig\nbig\ninitial", git("log", "--format=%s"))
	require.Equal(t, initialSHA, git("rev-parse", "v1^{commit}"), "reachable tags are followed as by a fetch")
	require.Empty(t, git("status", "--porcelain"))
	require.Equal(t, "next", git("show", "HEAD:base.txt"))
	// Again from storage with a worktree to replace (the full checkout
	// itself), to a named ref with a different tree.
	var tagTree struct {
		Node struct {
			AsGit struct {
				Ref struct {
					Tree struct{ Entries []string }
				}
			}
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on Directory { asGit { ref(name: "v1") { tree(depth: 0, discardGitDir: false) { entries } } } } } }`,
		Variables: map[string]any{"id": fullTree.Node.Tree.ID},
	}, &dagger.Response{Data: &tagTree}))
	require.ElementsMatch(t, []string{".git/", "base.txt"}, tagTree.Node.AsGit.Ref.Tree.Entries)
	// A repository opened over the retained checkout has no checkout base,
	// as replayed recipes do: its HEAD's checkout starts from the checkout
	// itself rather than over again.
	var headTree struct {
		Node struct {
			AsGit struct {
				Head struct {
					Tree struct{ Entries []string }
				}
			}
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on Directory { asGit { head { tree(depth: 0, discardGitDir: false) { entries } } } } } }`,
		Variables: map[string]any{"id": fullTree.Node.Tree.ID},
	}, &dagger.Response{Data: &headTree}))
	require.ElementsMatch(t, []string{".git/", "base.txt", "files/"}, headTree.Node.AsGit.Head.Tree.Entries)
	require.NoError(t, c.Close()) // Drain telemetry before asserting absence.

	type spanInfo struct {
		name       string
		start, end uint64
	}
	traces, _ := sink.capture()
	parents, spans := map[string]string{}, map[string]spanInfo{}
	cow, paths := map[string]bool{}, map[string]string{}
	layerBytes, objectBytes := map[string]int64{}, map[string]int64{}
	var fallbacks []string
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano < span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					parents[id] = string(span.TraceId) + string(span.ParentSpanId)
					spans[id] = spanInfo{span.Name, span.StartTimeUnixNano, span.EndTimeUnixNano}
					if span.Name != "materialize copy-on-write git checkout" {
						continue
					}
					for _, attr := range span.Attributes {
						switch attr.Key {
						case "dagger.git.checkout.cow.supported":
							cow[id] = attr.Value.GetBoolValue()
						case "dagger.git.checkout.cow.path":
							paths[id] = attr.Value.GetStringValue()
						case "dagger.git.checkout.cow.skipped":
							for _, v := range attr.Value.GetArrayValue().GetValues() {
								fallbacks = append(fallbacks, v.GetStringValue())
							}
						case "dagger.git.checkout.cow.layer_bytes":
							layerBytes[id] = attr.Value.GetIntValue()
						case "dagger.git.checkout.cow.layer_object_bytes":
							objectBytes[id] = attr.Value.GetIntValue()
						}
					}
				}
			}
		}
	}
	var checkouts int
	byPath := map[string]int{}
	for id, ok := range cow {
		if !ok {
			continue
		}
		byPath[paths[id]]++
		root := spans[id]
		t.Logf("copy-on-write checkout (%s): %s, layer %d bytes (%d below .git/objects)", paths[id], time.Duration(root.end-root.start), layerBytes[id], objectBytes[id])
		var children []spanInfo
		for child, parent := range parents {
			if parent == id {
				children = append(children, spans[child])
			}
		}
		slices.SortFunc(children, func(a, b spanInfo) int { return int(int64(a.start) - int64(b.start)) })
		for _, child := range children {
			t.Logf("  +%s %s %s", time.Duration(child.start-root.start), time.Duration(child.end-child.start), child.name)
		}
		require.Contains(t, layerBytes, id, "overlay layer usage must be measured")
		require.Less(t, objectBytes[id], int64(1<<20), "inherited objects were copied into the checkout's layer")
		if paths[id] == "delta" {
			// One changed file: the index and metadata, not the worktree.
			require.Less(t, layerBytes[id], int64(4<<20), "a delta checkout's layer holds more than its delta")
		} else {
			// The worktree takes ~20MiB of 4KiB blocks; the history's pack 32MiB.
			require.Less(t, layerBytes[id], int64(30<<20), "the checkout's layer holds more than its worktree")
		}
	}
	t.Logf("copy-on-write checkouts by path: %v; skipped: %v", byPath, fallbacks)
	// The committed head moves its base's checkout by its delta, and so does
	// the repository opened over that checkout; the base (host storage, no
	// checkout base) and the tag checkout (not a descendant) start over.
	require.GreaterOrEqual(t, byPath["delta"], 2, "committed heads and retained checkouts must check out by delta")
	require.Positive(t, byPath["wipe"], "storage without a checkout base must check out copy-on-write")
	for id, span := range spans {
		name := span.name
		for parent := parents[id]; parent != ""; parent = parents[parent] {
			if !cow[parent] {
				continue
			}
			// Only the delta path fetches, and only the delta's objects.
			if paths[parent] != "delta" {
				require.False(t, strings.HasPrefix(name, "git fetch") || strings.HasPrefix(name, "fetching "), "copy-on-write checkout fetched objects: %s", name)
			}
			if strings.HasPrefix(name, "git checkout") {
				checkouts++
			}
			break
		}
	}
	// Without Git spans beneath the checkouts, the fetch check above is vacuous.
	require.Positive(t, checkouts, "must observe git commands beneath copy-on-write checkouts")
}

// Sessions recorded before checkout bases were retained replay repositories
// opened with GitRepository.withContents over a retained checkout, over its
// .git, or over a pull's scratch repository. Their full checkouts must start
// from those checkouts (a pull's from the receiver's, which its edits do not
// touch) by delta, and match full checkouts of the same commits.
func (WorkspaceSuite) TestWorkspaceRetainedCheckoutFullCheckouts(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture, inspector := gitIncrementalCheckoutFixture(c)
	repo := fixture.AsGit()
	discard := core.GitRefTreeOpts{DiscardGitDir: true}
	before := repo.Head().Tree(discard)
	head := repo.Head().WithCommit(before.WithNewFile("selected.txt", "committed\n").Changes(before), "receiver", workspaceCommitDate, "Oracle", "oracle@example.com")
	headTree := head.Tree(discard)
	source := head.WithCommit(headTree.WithNewFile("added/deep.txt", "added\n").WithoutFile("delete.txt").WithNewFile("selected.txt", "source\n").Changes(headTree), "source", workspaceCommitDate, "Oracle", "oracle@example.com")
	headID, err := head.ID(ctx)
	require.NoError(t, err)
	headSHA, err := head.CommitSHA(ctx)
	require.NoError(t, err)
	sourceSHA, err := source.CommitSHA(ctx)
	require.NoError(t, err)
	asWorkspace := core.GitRefAsWorkspaceOpts{Cwd: "/"}
	sourceID, err := source.AsWorkspace(asWorkspace).ID(ctx)
	require.NoError(t, err)
	// The receiver's edits ride along in the pull's directory.
	receiverID, err := head.AsWorkspace(asWorkspace).WithNewFile("pending.txt", "receiver edit\n").ID(ctx)
	require.NoError(t, err)
	fullCheckout := func(id core.ID) *core.Directory {
		var out struct {
			Node struct {
				FullCheckout struct{ ID core.ID } `json:"__fullCheckout"`
			}
		}
		require.NoError(t, c.Do(ctx, &dagger.Request{
			Query:     `query($id: ID!) { node(id: $id) { ... on GitRef { __fullCheckout { id } } } }`,
			Variables: map[string]any{"id": id},
		}, &dagger.Response{Data: &out}))
		return core.Ref[*core.Directory](core.NewQuery(c), out.Node.FullCheckout.ID)
	}
	full := fullCheckout(headID)
	pulled := core.Ref[*core.Directory](core.NewQuery(c), selectHidden(ctx, t, c, receiverID, "Workspace", "__pullDirectory", map[string]any{
		"source":         sourceID,
		"committerName":  "Committer",
		"committerEmail": "committer@example.com",
	}))
	// Git's view of a checkout: everything a fresh one determines.
	gitState := func(dir *core.Directory) string {
		out, err := inspector.WithMountedDirectory("/inspect", dir).WithWorkdir("/inspect").
			WithExec([]string{"sh", "-ec", `
git rev-parse HEAD
git symbolic-ref -q HEAD || echo detached
git for-each-ref --format='%(objectname) %(refname)'
git config --local --list
git status --porcelain --ignored
git ls-files --stage --debug | grep -v -e ctime -e mtime -e dev: -e uid:
git fsck --no-dangling
`}).Stdout(ctx)
		require.NoError(t, err)
		return out
	}
	for _, tc := range []struct {
		name     string
		contents *core.Directory
		sha      string
	}{
		{"checkout", full, headSHA},
		{"checkout .git", full.Directory(".git"), headSHA},
		{"pull", pulled, sourceSHA},
	} {
		ref := repo.WithContents(tc.contents).Ref(tc.sha)
		refID, err := ref.ID(ctx)
		require.NoError(t, err)
		got := fullCheckout(refID)
		// The oracle's metadata is rewritten so that it is not recognized
		// as a retained checkout: it takes the full checkout.
		oracleID, err := tc.contents.WithNewFile(".git/description", "oracle\n").AsGit().Ref(tc.sha).ID(ctx)
		if tc.name == "checkout .git" {
			oracleID, err = tc.contents.WithNewFile("description", "oracle\n").AsGit().Ref(tc.sha).ID(ctx)
		}
		require.NoError(t, err)
		want := fullCheckout(oracleID)
		require.Equal(t,
			workspaceCommitManifest(ctx, t, inspector, want.WithoutDirectory(".git")),
			workspaceCommitManifest(ctx, t, inspector, got.WithoutDirectory(".git")), tc.name)
		requireGitCheckoutTimes(ctx, t, inspector, got.WithoutDirectory(".git"))
		require.Equal(t, gitState(want), gitState(got), tc.name)
	}
	require.NoError(t, c.Close()) // Drain finished spans.

	traces, _ := sink.capture()
	details := map[string]int{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.Name != "materialize copy-on-write git checkout" || span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					var path, detail string
					var skipped []string
					for _, attr := range span.Attributes {
						switch attr.Key {
						case "dagger.git.checkout.cow.path":
							path = attr.Value.GetStringValue()
						case "dagger.git.checkout.cow.detail":
							detail = attr.Value.GetStringValue()
						case "dagger.git.checkout.cow.skipped":
							for _, v := range attr.Value.GetArrayValue().GetValues() {
								skipped = append(skipped, v.GetStringValue())
							}
						}
					}
					t.Logf("full checkout path=%s detail=%q skipped=%v", path, detail, skipped)
					details[path+":"+detail]++
				}
			}
		}
	}
	require.Positive(t, details["delta:base=checkout"], "a repository opened over a retained checkout")
	require.Positive(t, details["delta:base=checkout-git-dir"], "a repository opened over a retained checkout's .git")
	require.Positive(t, details["delta:base=pull-receiver"], "a repository opened over a pull's directory")
}

// TestWorkspaceScopedCommitPerformance is a deterministic, non-LLM latency
// harness. Run it alone with -v on a fresh engine for a cold capture followed by
// three sequential commits. Timings are observations, not performance gates.
// Fixture creation, connection and capture are reported separately; engine/CLI
// build time is outside this test. The fixture has no remote and uses packed
// objects, so fetch spans describe local copying rather than network traffic.
func (WorkspaceSuite) TestWorkspaceScopedCommitPerformance(ctx context.Context, t *testctx.T) {
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
	ws := snapshotWorkspace(ctx, t, c, core.NewQuery(c).CurrentWorkspace())
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
		selectedID, err := ws.Git().Uncommitted().Filter(core.ChangesetFilterOpts{Include: []string{"selected.txt"}}).ID(ctx)
		require.NoError(t, err)
		before := ws.Git().Head()
		beforeSHA, err := before.CommitSHA(ctx)
		require.NoError(t, err)
		preStatus := time.Since(started)

		started = time.Now()
		id, err := ws.WithCommit(core.Ref[*core.Changeset](core.NewQuery(c), selectedID), fmt.Sprintf("perf: edit %d", i), workspaceCommitDate,
			core.WorkspaceWithCommitOpts{AuthorName: "Performance Fixture", AuthorEmail: "performance@example.com"}).ID(ctx)
		require.NoError(t, err)
		commitTime := time.Since(started)
		next := core.Ref[*core.Workspace](core.NewQuery(c), id)

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
		ahead, err := head.Log(ctx, core.GitRefLogOpts{Base: before, Limit: 101})
		require.NoError(t, err)
		require.Len(t, ahead, 1)
		message, err := ahead[0].Message(ctx)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("perf: edit %d", i), strings.TrimSpace(message))
		parents, err := ahead[0].ParentSHAs(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{beforeSHA}, parents)
		behind, err := before.Log(ctx, core.GitRefLogOpts{Base: head, Limit: 101})
		require.NoError(t, err)
		require.Empty(t, behind)
		recent, err := head.Log(ctx, core.GitRefLogOpts{Limit: 2})
		require.NoError(t, err)
		require.Len(t, recent, 2)
		history := time.Since(started)

		started = time.Now()
		// Consume the committed tree as well as the overlaid workspace: an ID
		// alone could hide deferred filesystem materialization.
		tree := head.Tree(core.GitRefTreeOpts{DiscardGitDir: true})
		delta := tree.Changes(before.Tree(core.GitRefTreeOpts{DiscardGitDir: true}))
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

type workspaceCommitTraceSample struct {
	id, parent, name string
	start, end       uint64
	discard          bool
	nativeSupported  bool
	fallbackReason   string
	newObjects       int64
	newObjectBytes   int64
	scopedStagePaths int64
	incremental      bool
	changedPaths     int64
}

func collectWorkspaceCommitTraceSamples(sink *agentTraceSink) map[string]workspaceCommitTraceSample {
	byID := map[string]workspaceCommitTraceSample{}
	traces, _ := sink.capture()
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					s := workspaceCommitTraceSample{id: id, parent: string(span.TraceId) + string(span.ParentSpanId), name: span.Name, start: span.StartTimeUnixNano, end: span.EndTimeUnixNano}
					for _, attr := range span.Attributes {
						switch attr.Key {
						case "dagger.git.checkout.discard_git_dir":
							s.discard = attr.Value.GetBoolValue()
						case "dagger.git.native.supported", "dagger.git.native_merge.supported":
							s.nativeSupported = attr.Value.GetBoolValue()
						case "dagger.git.native.fallback_reason", "dagger.git.native_merge.fallback_reason":
							s.fallbackReason = attr.Value.GetStringValue()
						case "dagger.git.native.new_objects":
							s.newObjects = attr.Value.GetIntValue()
						case "dagger.git.native.new_object_bytes":
							s.newObjectBytes = attr.Value.GetIntValue()
						case "dagger.git.native_merge.scoped_stage_paths":
							s.scopedStagePaths = attr.Value.GetIntValue()
						case "dagger.git.checkout.incremental.supported":
							s.incremental = attr.Value.GetBoolValue()
						case "dagger.git.checkout.incremental.changed_paths":
							s.changedPaths = attr.Value.GetIntValue()
						}
					}
					byID[id] = s
				}
			}
		}
	}
	return byID
}

// Report completed, deduplicated engine spans separately from client wall time.
// Durations are inclusive and overlap: do not add these rows together. No byte
// traffic, filesystem-write or object-copy counts are inferred from spans.
func logWorkspaceCommitPerformanceTrace(t *testctx.T, sink *agentTraceSink, iterations int) {
	t.Helper()
	byID := collectWorkspaceCommitTraceSamples(sink)
	var ordered []workspaceCommitTraceSample
	for _, s := range byID {
		ordered = append(ordered, s)
	}
	slices.SortFunc(ordered, func(a, b workspaceCommitTraceSample) int {
		if a.start < b.start {
			return -1
		}
		if a.start > b.start {
			return 1
		}
		return strings.Compare(a.id, b.id)
	})
	var commits, merges, fetches, walks, gitCommits, nativeCommits, nativeMerges, scratchCommits, incrementalCheckouts int
	for _, s := range ordered {
		fetch := strings.HasPrefix(s.name, "git fetch") || strings.HasPrefix(s.name, "fetching ")
		if fetch {
			for parent, ok := byID[s.parent]; ok; parent, ok = byID[parent.parent] {
				require.NotEqual(t, "GitRef.log", parent.name, "history query fetched objects: %s", s.name)
				require.NotEqual(t, "Workspace.withCommit", parent.name, "commit fetched objects: %s", s.name)
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
		case "git commit-tree":
			// Reconciliation creates temporary branch objects too. Count only
			// the actual history transaction as a new workspace commit.
			for parent, ok := byID[s.parent]; ok; parent, ok = byID[parent.parent] {
				if parent.name == "git native commit transaction" {
					nativeCommits++
					break
				}
				if parent.name == "git native workspace merge" {
					scratchCommits++
					break
				}
			}
			interesting = true
		case "git native workspace merge":
			nativeMerges++
			t.Logf("PERF native_merge id=%x supported=%t fallback=%q scoped_stage_paths=%d", []byte(s.id)[16:], s.nativeSupported, s.fallbackReason, s.scopedStagePaths)
			require.True(t, s.nativeSupported, "ordinary workspace reconciliation fell back: %s", s.fallbackReason)
			require.EqualValues(t, 3, s.scopedStagePaths, "stage the two pending paths and one selected path, not the baseline")
			interesting = true
		case "git native commit transaction":
			t.Logf("PERF native id=%x supported=%t fallback=%q new_objects=%d new_object_file_bytes=%d", []byte(s.id)[16:], s.nativeSupported, s.fallbackReason, s.newObjects, s.newObjectBytes)
			require.True(t, s.nativeSupported, "ordinary captured-workspace commit fell back: %s", s.fallbackReason)
			interesting = true
		case "git read-tree", "git write-tree", "git merge-tree":
			interesting = true
		case "GitRef.log":
			walks++
			interesting = true
		case "materialize incremental git checkout":
			require.True(t, s.incremental, "ordinary commit source checkout must use its parent materialization")
			require.EqualValues(t, 1, s.changedPaths, "only the committed file needs a checkout update")
			incrementalCheckouts++
			t.Logf("PERF incremental_checkout id=%x changed_paths=%d", []byte(s.id)[16:], s.changedPaths)
			interesting = true
		case "materialize local git checkout":
			for parent, ok := byID[s.parent]; ok; parent, ok = byID[parent.parent] {
				require.NotEqual(t, "Workspace.withCommit", parent.name, "ordinary commit rematerialized a full source tree")
			}
			interesting = true
		case "GitRef.asWorkspace":
			interesting = true
		}
		if interesting {
			t.Logf("PERF span=%q id=%x duration=%s discard_git_dir=%t", s.name, []byte(s.id)[16:], time.Duration(s.end-s.start), s.discard)
		}
	}
	t.Logf("PERF spans workspace_commits=%d merges=%d fetches=%d history_calls=%d git_commits=%d native_commits=%d native_merges=%d scratch_commits=%d incremental_checkouts=%d (inclusive, deduplicated; counts are not bytes)", commits, merges, fetches, walks, gitCommits, nativeCommits, nativeMerges, scratchCommits, incrementalCheckouts)
	require.Equal(t, iterations, commits, "must observe actual commit calls, not an empty trace")
	require.Equal(t, iterations, nativeCommits, "captured-workspace commits must execute native transactions")
	require.Equal(t, iterations, nativeMerges, "captured-workspace commits must execute native reconciliation")
	require.GreaterOrEqual(t, incrementalCheckouts, iterations, "each committed tree must materialize incrementally")
	require.Zero(t, gitCommits, "must not fall back to retained-checkout commits")
	require.Zero(t, merges, "ordinary workspace commits must not reconstruct general merge repositories")
	require.GreaterOrEqual(t, walks, iterations*3)
}

func (WorkspaceSuite) TestWorkspaceWithCommitScopedHistory(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	daemon, url := gitService(ctx, t, c, core.NewQuery(c).Directory().
		WithNewFile("src/a.txt", "old-a").WithNewFile("src/b.txt", "old-b").
		WithNewFile("keep.txt", "untouched"))
	ws := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).
		Branch("main").AsWorkspace(core.GitRefAsWorkspaceOpts{Cwd: "src"}).
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
	frozen := core.Ref[*core.Workspace](core.NewQuery(c), first.ID)
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
	laterSHA, err := ws.WithCommit(ws.Git().Uncommitted().Filter(core.ChangesetFilterOpts{Include: []string{"src/a.txt"}}), "same message", "2026-09-05T12:00:01Z").Git().Head().CommitSHA(ctx)
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
	log, err := core.Ref[*core.Workspace](core.NewQuery(c), second.ID).Git().Head().Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 3)
	_, err = commitWorkspace(ctx, c, core.Ref[*core.Workspace](core.NewQuery(c), second.ID), "same message", nil)
	require.ErrorContains(t, err, "nothing to commit")
	// Input values are immutable, even after a path-scoped and a full commit.
	oldSHA, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, baseSHA, oldSHA)

	recipe, err := sink.captureLLMRecipe(ctx, t, c, core.NewQuery(c).LLM().WithWorkspace(core.Ref[*core.Workspace](core.NewQuery(c), second.ID)))
	require.NoError(t, err)
	id := new(call.ID)
	require.NoError(t, id.Decode(string(recipe)))
	dag, err := id.ToProto()
	require.NoError(t, err)
	for _, vertex := range dag.GetRecipe().CallsByDigest {
		require.NotContains(t, []string{"currentWorkspace", "checkpoint", "withCommit", "branch"}, vertex.Field)
	}
	restored := core.Ref[*core.LLM](core.NewQuery(c), recipe).Workspace()
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
	c, sink := connectWithTrace(ctx, t, engineconn.Config{Workdir: checkout})
	ws := core.NewQuery(c).CurrentWorkspace()
	_, err := ws.ID(ctx)
	require.NoError(t, err)
	// Commit authorship is sampled at commit time, even if the workspace
	// was loaded before the client changed its Git config.
	git("config", "user.name", "Later Author")
	git("config", "user.email", "later@example.com")
	headBefore, statusBefore := git("rev-parse", "HEAD"), git("status", "--porcelain")
	committed, err := commitWorkspace(ctx, c, ws, "engine commit", nil)
	require.NoError(t, err)
	recipe, err := sink.captureLLMRecipe(ctx, t, c, core.NewQuery(c).LLM().WithWorkspace(core.Ref[*core.Workspace](core.NewQuery(c), committed.ID)))
	require.NoError(t, err)
	requireWorkspaceRecipeUsesHostGit(t, recipe)
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
	ws := core.NewQuery(c).CurrentWorkspace().WithNewFile("base.txt", "changed")
	const message = "subject\n\nCommit body."

	t.Run("does not sign off by default", func(ctx context.Context, t *testctx.T) {
		got, err := ws.WithCommit(ws.Git().Uncommitted(), message, workspaceCommitDate).Git().Head().TargetCommit().Message(ctx)
		require.NoError(t, err)
		require.Equal(t, message, strings.TrimSpace(got))
	})

	for _, tc := range []struct {
		name        string
		opts        core.WorkspaceWithCommitOpts
		authorName  string
		authorEmail string
	}{
		{
			name:       "inherited identity",
			opts:       core.WorkspaceWithCommitOpts{Signoff: true},
			authorName: "Inherited Author", authorEmail: "inherited@example.com",
		},
		{
			name:       "explicit identity",
			opts:       core.WorkspaceWithCommitOpts{Signoff: true, AuthorName: "Explicit Author", AuthorEmail: "explicit@example.com"},
			authorName: "Explicit Author", authorEmail: "explicit@example.com",
		},
		{
			name:       "explicit name with inherited email",
			opts:       core.WorkspaceWithCommitOpts{Signoff: true, AuthorName: "Explicit Author"},
			authorName: "Explicit Author", authorEmail: "inherited@example.com",
		},
		{
			name:       "inherited name with explicit email",
			opts:       core.WorkspaceWithCommitOpts{Signoff: true, AuthorEmail: "explicit@example.com"},
			authorName: "Inherited Author", authorEmail: "explicit@example.com",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			id, err := ws.WithCommit(ws.Git().Uncommitted(), message, workspaceCommitDate, tc.opts).ID(ctx)
			require.NoError(t, err)
			meta := core.Ref[*core.Workspace](core.NewQuery(c), id).Git().Head().TargetCommit()
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
	daemon, url := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("base.txt", "base"))
	ws := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace()
	baseSHA, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)

	committed := ws.
		WithNewFile("feature.txt", "feature").
		WithNewFile("pending.txt", "pending").With(func(ws *core.Workspace) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted().Filter(core.ChangesetFilterOpts{Include: []string{"feature.txt"}}), "draft mesage", workspaceCommitDate)
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
	log, err := core.Ref[*core.Workspace](core.NewQuery(c), amended.ID).Git().Head().Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 2)
	feature, err := core.Ref[*core.Workspace](core.NewQuery(c), amended.ID).Git().Head().Tree().File("feature.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "feature", feature)

	// A hard reset discards everything since the commit, pending edits included.
	hard := committed.WithReset(baseSHA, core.WorkspaceWithResetOpts{Hard: true})
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
	// Abbreviations resolve against the same frozen repository, so they
	// cannot reach orphaned commits either.
	_, err = reset.WithReset(draftSHA[:12]).Git().Head().CommitSHA(ctx)
	require.ErrorContains(t, err, "in this workspace's repository")

	missing := strings.Repeat("ab", 20)
	_, err = committed.WithReset(missing).Git().Head().CommitSHA(ctx)
	require.ErrorContains(t, err, "is not in this workspace's repository")
}

func (WorkspaceSuite) TestWorkspaceWithResetRevisions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("base.txt", "base"))
	ws := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace()
	baseSHA, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	first := ws.WithNewFile("first.txt", "first").With(func(ws *core.Workspace) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "first", workspaceCommitDate)
	})
	firstSHA, err := first.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	second := first.WithNewFile("second.txt", "second").With(func(ws *core.Workspace) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "second", workspaceCommitDate)
	})
	secondSHA, err := second.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)

	// Targets resolve like GitRepository.ref against the frozen repository:
	// abbreviated hashes, ref names and revision suffixes on either.
	for _, tc := range []struct {
		target string
		want   string
	}{
		{secondSHA, secondSHA},
		{firstSHA[:7], firstSHA},
		{baseSHA[:12], baseSHA},
		{"HEAD", secondSHA},
		{"HEAD~", firstSHA},
		{"HEAD^", firstSHA},
		{"HEAD~2", baseSHA},
		{"HEAD^^", baseSHA},
		{"HEAD~1^1", baseSHA},
		{secondSHA[:7] + "~1", firstSHA},
		{firstSHA[:7] + "^", baseSHA},
	} {
		t.Run(tc.target, func(ctx context.Context, t *testctx.T) {
			reset := second.WithReset(tc.target)
			sha, err := reset.Git().Head().CommitSHA(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, sha)
			// A mixed reset keeps the reverted commits' files uncommitted.
			contents, err := reset.File("second.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "second", contents)
		})
	}

	hard := second.WithReset("HEAD~2", core.WorkspaceWithResetOpts{Hard: true})
	hardSHA, err := hard.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, baseSHA, hardSHA)
	_, err = hard.File("first.txt").Contents(ctx)
	require.Error(t, err)

	for _, tc := range []struct {
		target string
		err    string
	}{
		{"HEAD~3", "is a root commit"},
		{"HEAD^2", "cannot select parent 2"},
		{"HEAD^{tree}", "invalid revision"},
		{"HEAD@{1}", "invalid revision"},
		{"no-such-branch", "in this workspace's repository"},
	} {
		t.Run(tc.target, func(ctx context.Context, t *testctx.T) {
			_, err := second.WithReset(tc.target).Git().Head().CommitSHA(ctx)
			require.ErrorContains(t, err, tc.err)
		})
	}
}

func (WorkspaceSuite) TestWorkspaceWithResetPreservesTree(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, core.NewQuery(c).Directory().
		WithNewFile("keep.txt", "unchanged").
		WithNewFile("src/change.txt", "before").
		WithNewFile("removed.txt", "removed in draft"))
	base := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace()
	baseSHA, err := base.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	draft := base.WithNewFile("src/change.txt", "after").
		WithNewFile("added.txt", "added in draft").WithoutFile("removed.txt").
		With(func(ws *core.Workspace) *core.Workspace {
			return ws.WithCommit(ws.Git().Uncommitted(), "draft", workspaceCommitDate)
		}).Git().Head()

	for _, tc := range []struct {
		name string
		ws   *core.Workspace
	}{
		{"clean Git ref", draft.AsWorkspace()},
		{"directory with Git metadata", draft.AsWorkspace().Directory("/").WithDirectory(".git", draft.AsWorkspace().Git().Directory()).AsWorkspace()},
		{"pending overlay", draft.AsWorkspace().WithNewFile("pending.txt", "pending")},
		{"mounted directory", draft.AsWorkspace().WithMountedDirectory("/mounted", core.NewQuery(c).Directory().WithNewFile("data.txt", "read-only"))},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			// Use a nested cwd so preservation must address the boundary root.
			ws := tc.ws.WithWorkdir("src")
			id, err := ws.WithReset(baseSHA).ID(ctx)
			require.NoError(t, err)
			reset := core.Ref[*core.Workspace](core.NewQuery(c), id)
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
	daemon, url := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("old.txt", strings.Repeat("rename me\n", 20)))
	ws := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace().
		WithoutFile("old.txt").WithNewFile("new.txt", strings.Repeat("rename me\n", 20))
	// A filtered changeset, not Workspace, decides which rename sides to keep.
	addition, err := commitWorkspace(ctx, c, ws, "new side only", []string{"new.txt"})
	require.NoError(t, err)
	require.Equal(t, []string{"old.txt"}, addition.Git.Uncommitted.RemovedPaths)
	additionTree := core.Ref[*core.Workspace](core.NewQuery(c), addition.ID).Git().Head().Tree()
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
	_, err = commitWorkspace(ctx, c, core.NewQuery(c).Directory().WithNewFile("a", "no Git").AsWorkspace(), "no repo", nil)
	require.ErrorContains(t, err, "not in a git repository")
}

func (WorkspaceSuite) TestWorkspaceWithCommitFilteredDirectoryDeletion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("src/a.txt", "a").WithNewFile("src/b.txt", "b"))
	ws := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace().WithoutDirectory("src")
	partial, err := commitWorkspace(ctx, c, ws, "delete only a", []string{"src/a.txt"})
	require.NoError(t, err)
	committed := core.Ref[*core.Workspace](core.NewQuery(c), partial.ID)
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
	exists, err = core.Ref[*core.Workspace](core.NewQuery(c), rest.ID).Git().Head().Tree().Exists(ctx, "src/b.txt")
	require.NoError(t, err)
	require.False(t, exists)
}

// TestWorkspaceWithCommitKeepsBuildOutputsOutOfRecipe covers a commit scoped
// to one file of a workspace that also holds an uncommitted build output, e.g.
// a `go test -c` binary in tmp/ (trace dfa26d7aa772715725e427efc5b65cdb).
// Freezing the receiver used to re-record its whole pending overlay as one
// inline patch blob; the binary made one span's call 129 MB, past what any
// trace can hold. The overlay must stay by reference when freezing re-pins the
// base by SHA, for a remote branch or a local checkout whose remote selection
// it records, and the uncommitted file must survive.
func (WorkspaceSuite) TestWorkspaceWithCommitKeepsBuildOutputsOutOfRecipe(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name, path, build string
		local             bool
	}{
		{name: "binary", path: "tmp/app.test", build: "head -c 4194304 /dev/urandom > tmp/app.test"},
		{name: "oversized text", path: "tmp/build.log", build: "yes 'compiling a package' | head -c 17825792 > tmp/build.log"},
		{name: "binary in a local checkout", path: "tmp/app.test", build: "head -c 4194304 /dev/urandom > tmp/app.test", local: true},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c, sink := connectWithTrace(ctx, t)
			outputs := core.NewQuery(c).Container().From(alpineImage).WithWorkdir("/out").
				WithExec([]string{"sh", "-ec", "mkdir tmp; " + tc.build}).
				Directory("/out")
			size, err := outputs.File(tc.path).Size(ctx)
			require.NoError(t, err)
			var base *core.GitRef
			if tc.local {
				base = core.NewQuery(c).Container().From(alpineImage).
					WithExec([]string{"apk", "add", "git"}).With(gitUserConfig).
					WithWorkdir("/repo").WithExec([]string{"git", "init", "-b", "main"}).
					WithNewFile("notes.txt", "base\n").
					WithExec([]string{"git", "add", "."}).
					WithExec([]string{"git", "commit", "-m", "base"}).
					Directory(".").AsGit().Head()
			} else {
				daemon, url := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("notes.txt", "base\n"))
				base = core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Branch("main")
			}
			ws := base.AsWorkspace().
				WithNewFile("notes.txt", "committed\n").
				WithDirectory("/", outputs)

			got, err := commitWorkspace(ctx, c, ws, "notes only", []string{"notes.txt"})
			require.NoError(t, err)
			require.Equal(t, []string{"tmp/", tc.path}, got.Git.Uncommitted.AddedPaths)
			require.Empty(t, got.Git.Uncommitted.ModifiedPaths)

			committed := core.Ref[*core.Workspace](core.NewQuery(c), got.ID)
			if !tc.local {
				// The commit is detached: the remote branch the workspace was
				// built from still resolves through the remote, to its
				// original commit, not to the new one.
				baseSHA, err := base.CommitSHA(ctx)
				require.NoError(t, err)
				mainSHA, err := committed.Git().Head().AsRepository().Ref("main").CommitSHA(ctx)
				require.NoError(t, err)
				require.Equal(t, baseSHA, mainSHA, "committing must not advance the branch")
			}
			head := committed.Git().Head().Tree(core.GitRefTreeOpts{DiscardGitDir: true})
			notes, err := head.File("notes.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "committed\n", notes)
			exists, err := head.Exists(ctx, tc.path)
			require.NoError(t, err)
			require.False(t, exists, "the excluded build output must not be committed")
			kept, err := committed.File(tc.path).Size(ctx)
			require.NoError(t, err)
			require.Equal(t, size, kept, "the uncommitted build output must survive the commit")

			// The next commit freezes an overlay that holds only the output.
			rest, err := commitWorkspace(ctx, c, committed, "outputs", nil)
			require.NoError(t, err)
			require.Empty(t, rest.Git.Uncommitted.AddedPaths)
			restored := core.Ref[*core.Workspace](core.NewQuery(c), rest.ID)
			kept, err = restored.Git().Head().Tree().File(tc.path).Size(ctx)
			require.NoError(t, err)
			require.Equal(t, size, kept)

			// The recorded recipe, and every call the session published,
			// stays small: nothing inlined the output as a patch blob.
			recipe, err := sink.captureLLMRecipe(ctx, t, c, core.NewQuery(c).LLM().WithWorkspace(restored))
			require.NoError(t, err)
			require.Less(t, len(recipe), 1<<20, "the build output must not be inlined in the recipe")
			id := new(call.ID)
			require.NoError(t, id.Decode(string(recipe)))
			fields := map[string]bool{}
			collectIDFieldNames(id, fields)
			require.True(t, fields["__withCommitRepository"], "the recipe must hold the commits")
			require.True(t, fields["withExec"], "the overlay must reference the output's producer")
			require.False(t, fields["blob"], "the overlay must not be embedded as a patch blob")
			var large []string
			sink.read(func(db *dagui.DB) {
				for dgst, published := range db.Calls {
					if n := proto.Size(published); n > 1<<20 {
						large = append(large, fmt.Sprintf("%s %s (%d bytes)", dgst, published.Field, n))
					}
				}
			})
			require.Empty(t, large, "no published call may inline the build output")
		})
	}
}

func (WorkspaceSuite) TestWorkspaceWithCommitFileKindsAndMetadata(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, core.NewQuery(c).Directory().
		WithNewFile("delete/a", "a").WithNewFile("delete/b", "b").
		WithNewFile("binary", "before\x00binary"))
	ws := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace().
		WithoutDirectory("delete").
		WithNewFile("binary", "after\x00binary").
		WithNewFile("run", "#!/bin/sh\n", core.WorkspaceWithNewFileOpts{Permissions: 0o755}).
		WithDirectory("/", core.NewQuery(c).Directory().WithSymlink("binary", "link")).
		WithNewFile(":literal", "pathspecs are literal").
		WithConfigEnvironment("testing").
		WithMountedDirectory("/mounted", core.NewQuery(c).Directory().WithNewFile("readme", "read-only"))
	partial, err := commitWorkspace(ctx, c, ws, "literal path", []string{":literal"})
	require.NoError(t, err)
	committed, err := commitWorkspace(ctx, c, core.Ref[*core.Workspace](core.NewQuery(c), partial.ID), "file kinds", nil)
	require.NoError(t, err)
	require.Equal(t, "Dagger", committed.Git.Head.TargetCommit.AuthorName)
	require.Empty(t, committed.Git.Uncommitted.AddedPaths)
	require.Empty(t, committed.Git.Uncommitted.ModifiedPaths)
	require.Empty(t, committed.Git.Uncommitted.RemovedPaths)
	frozen := core.Ref[*core.Workspace](core.NewQuery(c), committed.ID)
	tree := frozen.Git().Head().Tree(core.GitRefTreeOpts{DiscardGitDir: true})
	out, err := core.NewQuery(c).Container().From(alpineImage).WithDirectory("/tree", tree).WithWorkdir("/tree").
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
	daemon, url := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("base.txt", "base"))
	directory := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).
		Branch("main").Tree().WithNewFile("base.txt", "changed")
	committed, err := commitWorkspace(ctx, c, directory.AsWorkspace(), "directory repo", nil)
	require.NoError(t, err)
	require.Empty(t, committed.Git.Uncommitted.ModifiedPaths)
	frozen := core.Ref[*core.Workspace](core.NewQuery(c), committed.ID)
	contents, err := frozen.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "changed", contents)
	log, err := frozen.Git().Head().Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 2)
}

func (WorkspaceSuite) TestWorkspaceWithCommitLoadsCommittedModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"modules/probe\"\n").
		WithNewFile("modules/probe/dagger.json", `{"name":"probe","engineVersion":"v1.0.0","sdk":"go"}`).
		WithNewFile("modules/probe/main.go", "package main\ntype Probe struct{}\n"))
	ws := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace().
		WithNewFile("modules/probe/main.go", `package main
type Probe struct{}
// +check
func (*Probe) Committed() error { return nil }
`)
	committed, err := commitWorkspace(ctx, c, ws, "add module", nil)
	require.NoError(t, err)
	checks, err := core.Ref[*core.Workspace](core.NewQuery(c), committed.ID).Artifacts().FilterTypes([]string{"Check"}).WithoutURI("**/stale").Items(ctx)
	require.NoError(t, err)
	require.Len(t, checks, 1)
	name, err := checks[0].URI(ctx)
	require.NoError(t, err)
	require.Equal(t, "dag://?check=probe/committed", name)
}

func (WorkspaceSuite) TestWorkspaceWithCommitIncomingChanges(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	before := core.NewQuery(c).Directory().WithNewFile("base.txt", "base\n").WithNewFile("keep.txt", "keep\n")
	daemon, url := gitService(ctx, t, c, before)
	base := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Head().AsWorkspace()
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
			got := core.Ref[*core.Workspace](core.NewQuery(c), id)
			for _, tree := range []*core.Directory{got.Directory("/"), got.Git().Head().Tree()} {
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
			recipe, err := sink.captureLLMRecipe(ctx, t, c, core.NewQuery(c).LLM().WithWorkspace(got))
			require.NoError(t, err)
			recipeID := new(call.ID)
			require.NoError(t, recipeID.Decode(string(recipe)))
			dag, err := recipeID.ToProto()
			require.NoError(t, err)
			for _, vertex := range dag.GetRecipe().CallsByDigest {
				require.NotContains(t, []string{"currentWorkspace", "checkpoint", "withCommit", "branch"}, vertex.Field)
			}
			restored := core.Ref[*core.LLM](core.NewQuery(c), recipe).Workspace()
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
	before := core.NewQuery(c).Directory().WithNewFile("base.txt", "base")
	daemon, url := gitService(ctx, t, c, before)
	ws := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Head().AsWorkspace()
	metadata := before.WithNewFile(".git/HEAD", "ref: refs/heads/injected\n").
		WithNewFile(".git/config", "[remote \"origin\"]\nurl = file:///injected\n")
	_, err := ws.WithCommit(metadata.Changes(before), "metadata only", workspaceCommitDate).ID(ctx)
	require.ErrorContains(t, err, "nothing to commit")
	id, err := ws.WithCommit(metadata.WithNewFile("base.txt", "changed").Changes(before), "mixed input", workspaceCommitDate).ID(ctx)
	require.NoError(t, err)
	got := core.Ref[*core.Workspace](core.NewQuery(c), id)
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
	for _, tree := range []*core.Directory{got.Directory("/"), got.Git().Head().Tree(core.GitRefTreeOpts{DiscardGitDir: true})} {
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
	before := core.NewQuery(c).Directory().WithNewFile("base.txt", "base\n")
	daemon, url := gitService(ctx, t, c, before)
	base := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Head().AsWorkspace()
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
		baseline := ours.Git().Head().Tree(core.GitRefTreeOpts{DiscardGitDir: true})
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
	daemon, url := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("a*.txt", "old").WithNewFile("abc.txt", "old").WithNewFile("dir[1]/file", "old"))
	ws := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: daemon}).Head().AsWorkspace().
		WithNewFile("a*.txt", "literal").WithNewFile("abc.txt", "unselected").WithNewFile("dir[1]/file", "selected directory")
	result := ws.WithCommit(ws.Git().Uncommitted().Filter(core.ChangesetFilterOpts{Include: []string{`a\*.txt`, `dir\[1]`}}), "literal paths", workspaceCommitDate)
	for file, want := range map[string]string{"a*.txt": "literal", "abc.txt": "old", "dir[1]/file": "selected directory"} {
		got, err := result.Git().Head().Tree().File(file).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	modified, err := result.Git().Uncommitted().ModifiedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"abc.txt"}, modified)
}
