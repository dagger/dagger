package schema

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestCapturedGitRemoteNames(t *testing.T) {
	for _, name := range []string{"team/trunk", "team.fork", "-fork", "origin", "", "bad..name", "bad name", "bad\nname"} {
		t.Run(name, func(t *testing.T) {
			remotes, err := json.Marshal([]core.GitRemote{{Name: name, URL: "https://example.test/repo"}})
			require.NoError(t, err)
			repo, err := (&gitSchema{}).withRemoteSelection(t.Context(), &core.GitRepository{}, gitRemoteSelectionArgs{Remotes: string(remotes), UpstreamRemote: name})
			if name == "team/trunk" || name == "team.fork" || name == "-fork" || name == "origin" {
				require.NoError(t, err)
				require.Equal(t, name, repo.Remotes[0].Name)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestGitRefAndCommitPreserveRemoteIdentity(t *testing.T) {
	for _, kind := range []string{"ref", "commit"} {
		for _, input := range []string{"selection", "capability", "routing"} {
			t.Run(kind+"/"+input, func(t *testing.T) {
				ctx, srv, cache, _ := resolverOutputFixture(t)
				srv.InstallObject(dagql.NewClass[*core.GitRef](srv))
				srv.InstallObject(dagql.NewClass[*core.GitCommit](srv))
				srv.InstallObject(dagql.NewClass[*core.Secret](srv))
				s := &gitSchema{}
				dagql.Fields[*core.GitRepository]{
					dagql.NodeFunc("__resolvedRef", s.resolvedRef).View(AllVersion),
					dagql.NodeFunc("commit", s.commit).View(AllVersion),
				}.Install(srv)
				dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
				dir.SetPath("/")
				dir.SetSnapshot(nil)
				storage := resolverAttach(t, ctx, srv, cache, "storage", dir)
				url, err := gitutil.ParseURL("https://unreachable.invalid/repository.git")
				require.NoError(t, err)
				sha := strings.Repeat("a", 40)
				var previousDigests []string
				count := 4
				if input == "routing" {
					count = 2
				}
				for i := range count {
					backend := &core.LocalGitRepository{Directory: storage}
					repo := &core.GitRepository{
						Backend: backend,
						URL:     dagql.NonNull(dagql.String(url.Remote())),
						Remotes: []core.GitRemote{{Name: "fork", URL: url.Remote()}, {Name: "trunk", URL: "https://unreachable.invalid/trunk.git"}},
					}
					if input == "routing" {
						repo.Remotes = []core.GitRemote{{Name: "origin", URL: url.Remote(), Implicit: i == 1}}
					} else if input == "selection" {
						// An absent hint and a captured detached HEAD are distinct too:
						// only the former may discover tracking configuration from storage.
						if i > 0 {
							upstream := []string{"", "fork", "trunk"}[i-1]
							repo.UpstreamRemote = &upstream
						}
					} else if i > 0 {
						// Identical storage can be supplied without a source capability,
						// or derived from differently authenticated remote repositories.
						name := "source-" + strconv.Itoa(i)
						token := resolverAttach(t, ctx, srv, cache, "token-"+name, &core.Secret{Handle: dagql.SessionResourceHandle(name)})
						backend.Upstream = resolverAttach(t, ctx, srv, cache, name, &core.GitRepository{
							Backend: &core.RemoteGitRepository{URL: url, AuthToken: token},
							URL:     repo.URL,
						})
					}
					parent := resolverAttach(t, ctx, srv, cache, "repository-"+strconv.Itoa(i), repo)
					var resultDigest string
					var retained dagql.ObjectResult[*core.GitRepository]
					if kind == "ref" {
						var ref dagql.ObjectResult[*core.GitRef]
						require.NoError(t, srv.Select(ctx, parent, &ref, dagql.Selector{Field: "__resolvedRef", Args: []dagql.NamedInput{
							{Name: "name", Value: dagql.String("refs/heads/main")}, {Name: "commit", Value: dagql.String(sha)},
						}}))
						retained = ref.Self().Repo
						digest, err := ref.ContentPreferredDigest(ctx)
						require.NoError(t, err)
						resultDigest = digest.String()
					} else {
						var commit dagql.ObjectResult[*core.GitCommit]
						require.NoError(t, srv.Select(ctx, parent, &commit, dagql.Selector{Field: "commit", Args: []dagql.NamedInput{
							{Name: "id", Value: dagql.String(sha)},
						}}))
						retained = commit.Self().Repo
						digest, err := commit.ContentPreferredDigest(ctx)
						require.NoError(t, err)
						resultDigest = digest.String()
					}
					require.Equal(t, repo.UpstreamRemote, retained.Self().UpstreamRemote)
					require.Same(t, backend.Upstream.Self(), core.GitUpstream(retained).Self())
					require.NotContains(t, previousDigests, resultDigest, "different remote selection or authority must not share cached refs or commits")
					previousDigests = append(previousDigests, resultDigest)
				}
			})
		}
	}
}
