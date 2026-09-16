package schema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestGitResolvedFrames(t *testing.T) {
	sha := strings.Repeat("a", 40)
	override := strings.Repeat("b", 40)
	for _, test := range []struct {
		name, field, arg, wantField, wantName string
		fixed, detached                       bool
	}{
		{name: "head", field: "head", wantField: "__resolvedRef", wantName: "refs/heads/main"},
		{name: "detached", field: "head", wantField: "__resolvedRef", detached: true},
		{name: "branch", field: "branch", arg: "main", wantField: "__resolvedRef", wantName: "refs/heads/main"},
		{name: "tag", field: "tag", arg: "v1.0.0", wantField: "__resolvedRef", wantName: "refs/tags/v1.0.0"},
		{name: "ref", field: "ref", arg: "main", wantField: "__resolvedRef", wantName: "refs/heads/main"},
		{name: "legacy", field: "commit", arg: "main", wantField: "__resolvedRef", wantName: "refs/heads/main"},
		{name: "latest", field: "latest", wantField: "__resolvedRef", wantName: "refs/tags/v1.0.0"},
		{name: "override", field: "ref", arg: "main", wantField: "__resolvedRef", wantName: "refs/heads/main", fixed: true},
		{name: "fixed", field: "ref", arg: sha, wantField: "ref", wantName: sha},
		{name: "internal", field: "__resolvedRef", arg: "saved-fetch-name", wantField: "__resolvedRef", wantName: "saved-fetch-name", fixed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, srv, cache, server := resolverOutputFixture(t)
			srv.View = "v0.18.0"
			srv.InstallObject(dagql.NewClass[*core.GitRef](srv))
			s := &gitSchema{}
			dagql.Fields[*core.GitRepository]{
				dagql.NodeFunc("__resolvedRef", s.resolvedRef).View(AllVersion).IsPersistable(),
				dagql.NodeFunc("head", s.head).View(AllVersion), dagql.NodeFunc("ref", s.ref).View(AllVersion), dagql.NodeFunc("branch", s.branch).View(AllVersion), dagql.NodeFunc("tag", s.tag).View(AllVersion), dagql.NodeFunc("commit", s.commitRef).View(AllVersion), dagql.NodeFunc("latest", s.latest).View(AllVersion),
			}.Install(srv)
			dagql.Fields[*core.GitRef]{dagql.NodeFunc("tree", s.tree).View(AllVersion).IsPersistable()}.Install(srv)
			dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
			dir.SetPath("/")
			dir.SetSnapshot(nil)
			source := resolverAttach(t, ctx, srv, cache, "source", dir)
			remote := &gitutil.Remote{Refs: []*gitutil.Ref{{Name: "HEAD", SHA: sha}, {Name: "refs/heads/main", SHA: sha}, {Name: "refs/tags/v1.0.0", SHA: sha}}, Symrefs: map[string]string{"HEAD": "refs/heads/main"}}
			if test.detached {
				remote.Symrefs = nil
			}
			if test.name == "internal" {
				remote = nil
			} // An internal selection must not try to open the source.
			parent := resolverAttach(t, ctx, srv, cache, "repository", &core.GitRepository{Backend: &core.LocalGitRepository{Directory: source}, Remote: remote})
			selector := dagql.Selector{Field: test.field, View: srv.View}
			if test.arg != "" {
				name := "name"
				if test.field == "commit" {
					name = "id"
				}
				selector.Args = append(selector.Args, dagql.NamedInput{Name: name, Value: dagql.String(test.arg)})
			}
			if test.fixed {
				selector.Args = append(selector.Args, dagql.NamedInput{Name: "commit", Value: dagql.String(override)})
			}
			var result dagql.ObjectResult[*core.GitRef]
			rowsBefore := cache.Size()
			require.NoError(t, srv.Select(ctx, parent, &result, selector))
			t.Logf("Git %s selection: retained rows before=%d after=%d delta=%d", test.name, rowsBefore, cache.Size(), cache.Size()-rowsBefore)
			wantSHA := sha
			if test.fixed {
				wantSHA = override
			}
			require.Equal(t, wantSHA, result.Self().Ref.SHA)
			require.Equal(t, test.wantName, result.Self().Ref.Name)
			record, err := cache.CapturePersistedRecord(ctx, result)
			require.NoError(t, err)
			require.Equal(t, test.wantField, record.Call.Field)
			require.NotNil(t, record.Call.Receiver)
			require.Empty(t, record.Call.ImplicitInputs)
			if test.wantField == "__resolvedRef" {
				args := map[string]string{}
				for _, arg := range record.Call.Args {
					args[arg.Name] = arg.Value.StringValue
				}
				require.Equal(t, map[string]string{"name": test.wantName, "commit": wantSHA}, args)
			}
			var output dagql.ObjectResult[*core.Directory]
			rowsBefore = cache.Size()
			require.NoError(t, srv.Select(ctx, result, &output, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(true)}, {Name: "depth", Value: dagql.Int(3)}, {Name: "includeTags", Value: dagql.Boolean(true)}}}))
			t.Logf("Git %s tree: retained row delta=%d", test.name, cache.Size()-rowsBefore)
			lazy := output.Self().Lazy.(*core.DirectoryGitTreeLazy)
			require.False(t, lazy.IsEvaluated())
			require.Equal(t, wantSHA, lazy.Ref.Self().Ref.SHA)
			require.True(t, lazy.DiscardGitDir)
			require.True(t, lazy.IncludeTags)
			require.Equal(t, 3, lazy.Depth)
			tree, err := cache.CapturePersistedRecord(ctx, output)
			require.NoError(t, err)
			var payload struct {
				Form, LazyKind string
				LazyJSON       json.RawMessage
			}
			require.NoError(t, json.Unmarshal(tree.Envelope.ObjectJSON, &payload))
			require.Equal(t, "lazy", payload.Form)
			var inputs struct{ RefResultID uint64 }
			require.NoError(t, json.Unmarshal(payload.LazyJSON, &inputs))
			require.Equal(t, record.ResultID, inputs.RefResultID)
			require.Empty(t, server.manager.outputs, "tree handle construction must not check out files")
		})
	}
}

func TestGitFixedCommitAndLockFrames(t *testing.T) {
	for _, mode := range []string{"commit", "ref", "locked branch"} {
		t.Run(mode, func(t *testing.T) {
			ctx, srv, cache, server := resolverOutputFixture(t)
			srv.View = workspaceLockingVersion
			srv.InstallObject(dagql.NewClass[*core.GitRef](srv))
			srv.InstallObject(dagql.NewClass[*core.GitCommit](srv))
			s := &gitSchema{}
			dagql.Fields[*core.GitRepository]{dagql.NodeFunc("__resolvedRef", s.resolvedRef).View(AllVersion).IsPersistable(), dagql.NodeFunc("branch", s.branch).View(AllVersion), dagql.NodeFunc("ref", s.ref).View(AllVersion), dagql.NodeFunc("commit", s.commit).View(AllVersion)}.Install(srv)
			u, err := gitutil.ParseURL("https://unreachable.invalid/repository.git")
			require.NoError(t, err)
			repo := &core.GitRepository{Backend: &core.RemoteGitRepository{URL: u}, URL: dagql.NonNull(dagql.String(u.Remote()))}
			parent := resolverAttach(t, ctx, srv, cache, "repository", repo)
			sha := strings.Repeat("c", 40)
			var result dagql.AnyResult
			if mode == "commit" {
				var commit dagql.ObjectResult[*core.GitCommit]
				require.NoError(t, srv.Select(ctx, parent, &commit, dagql.Selector{Field: "commit", View: srv.View, Args: []dagql.NamedInput{{Name: "id", Value: dagql.String(sha)}}}))
				require.Equal(t, sha, commit.Self().Ref.SHA)
				require.Empty(t, commit.Self().FetchRef.Name)
				result = commit
			} else {
				selector := dagql.Selector{Field: "ref", View: srv.View, Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(sha)}}}
				if mode == "locked branch" {
					lock := workspace.NewLock()
					inputs, err := gitLockInputs(repo, "refs/heads/main")
					require.NoError(t, err)
					require.NoError(t, lock.SetLookup(workspace.CoreLockNamespace, workspace.LockOperationGitSHA, inputs, sha))
					ctx = withWorkspaceLookupLockOverride(ctx, lock)
					selector.Field = "branch"
					selector.Args[0].Value = dagql.String("main")
				}
				var ref dagql.ObjectResult[*core.GitRef]
				require.NoError(t, srv.Select(ctx, parent, &ref, selector))
				require.Equal(t, sha, ref.Self().Ref.SHA)
				result = ref
			}
			record, err := cache.CapturePersistedRecord(ctx, result)
			require.NoError(t, err)
			wantField := mode
			if mode == "locked branch" {
				wantField = "__resolvedRef"
			}
			require.Equal(t, wantField, record.Call.Field)
			require.Nil(t, repo.Remote, "fixed and locked paths must not resolve the remote")
			require.Empty(t, server.manager.outputs)
			parentID, err := cache.PersistedResultID(parent)
			require.NoError(t, err)
			require.Equal(t, parentID, record.Call.Receiver.ResultID)
			require.Empty(t, record.Call.ImplicitInputs)
		})
	}
}
