package core

// TestServeModuleCacheNamespaces checks one invariant across the ways a module
// reaches another module of its own tree: the reached module, and the modules
// it depends on, keep the cache volumes of the same module loaded directly
// from the same path. Each case writes to a volume through the module it
// reaches, then loads that module directly and expects to find the write.
//
// See also module_serve_tree_test.go for the individual paths.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// serveNamespaceCallSource serves a module of the caller's tree and runs a
// query against it, so a case can reach any module through any chain.
const serveNamespaceCallSource = `package main

import (
	"context"
	"encoding/json"

	"github.com/Khan/genqlient/graphql"
)

type Caller struct{}

func (m *Caller) Call(ctx context.Context, address, query string) (string, error) {
	if err := dag.ServeModule(ctx, address); err != nil {
		return "", err
	}
	var data json.RawMessage
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{Query: query}, &graphql.Response{Data: &data})
	return string(data), err
}
`

// serveNamespaceLegacySource is a legacy module that appends a token to a
// cache volume and returns every token appended so far.
func serveNamespaceLegacySource(object, key string) string {
	return fmt.Sprintf(`type %s {
  pub cached(token: String!): String! {
    container
      .from(%q)
      .withMountedCache("/cache", cacheVolume(%q))
      .withExec(["sh", "-c", "echo " + token + " >> /cache/log; cat /cache/log"])
      .stdout
  }
}
`, object, alpineImage, key)
}

// serveNamespaceProcessScript is moduleCacheProcessScript with a nonce in the
// write, so the write of the direct load is not the cached write of the
// module reached.
func serveNamespaceProcessScript(key string) string {
	return strings.Replace(fmt.Sprintf(moduleCacheProcessScript, key), "echo $NAME >>", "echo $NAME-$(od -An -N8 -tx1 /dev/urandom | tr -d ' \\n') >>", 1)
}

func serveNamespaceLegacyManifest(name string, deps ...string) string {
	manifest := fmt.Sprintf("name = %q\nengineVersion = \"latest\"\nsource = \".\"\n\n[runtime]\nsource = \"dang\"\n", name)
	for _, dep := range deps {
		manifest += fmt.Sprintf("\n[[dependencies]]\nname = %q\nsource = \"../%s\"\n", dep, dep)
	}
	return manifest
}

func serveNamespaceObject(name string) string {
	return strings.ToUpper(name[:1]) + name[1:]
}

// gqlString quotes s as a GraphQL string literal, which JSON's quoting is.
func gqlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (ModuleLoadingSuite) TestServeModuleCacheNamespaces(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cli := daggerCliFile(t, c)
	key := "serve-namespace-" + rand.Text()

	type calleeKind string
	const (
		entrypointCallee calleeKind = "entrypoint module"
		legacyCallee     calleeKind = "legacy module"
	)
	type callerKind string
	const (
		gitCaller   callerKind = "git module"
		localCaller callerKind = "host module"
		dirCaller   callerKind = "served module"
		// hostDirCaller is a module a host module serves, whose origin is
		// the root of a local context.
		hostDirCaller callerKind = "module a host module serves"
		// wsClient is the client of a git workspace session itself, with no
		// module of its own.
		wsClient callerKind = "git workspace client"
	)
	type tc struct {
		caller callerKind
		callee calleeKind
		depth  int
		// checked is the module whose volume the case checks, reached is the
		// module the caller serves, and query is what the caller runs on it.
		checked, reached, query string
	}

	tree := core.NewQuery(c).Directory()
	for _, name := range []string{"caller", "mid", "mid2"} {
		tree = tree.
			WithNewFile("modules/"+name+"/dagger.json", strings.Replace(serveTreeCallerManifest, `"caller"`, gqlString(name), 1)).
			WithNewFile("modules/"+name+"/main.go", strings.ReplaceAll(serveNamespaceCallSource, "Caller", serveNamespaceObject(name)))
	}

	var cases []tc
	// A module loaded through a git workspace cannot serve its tree yet: it
	// has no tree whose identity its own covers when the workspace carries
	// an overlay.
	for _, caller := range []callerKind{gitCaller, localCaller, dirCaller, hostDirCaller, wsClient} {
		for _, callee := range []calleeKind{entrypointCallee, legacyCallee} {
			for _, depth := range []int{1, 2, 3} {
				if callee == entrypointCallee && (depth == 3 || depth == 2 && caller == wsClient) {
					// An entrypoint module's second level is a sibling that a
					// served sibling serves, which a module loaded through a
					// git workspace cannot do.
					continue
				}
				id := fmt.Sprintf("m%d", len(cases))
				k := tc{caller: caller, callee: callee, depth: depth, checked: id}
				switch callee {
				case entrypointCallee:
					tree = tree.
						WithNewFile("modules/"+id+"/dagger-module.toml", strings.Replace(sourcesModuleManifest, `name = "m"`, fmt.Sprintf("name = %q", id), 1)).
						WithNewFile("modules/"+id+"/entrypoint/main.dang", strings.ReplaceAll(fmt.Sprintf(moduleCacheEntrypoint, alpineImage, key), "Probe", serveNamespaceObject(id))).
						WithNewFile("modules/"+id+"/probe.sh", serveNamespaceProcessScript(key)).
						WithFile("modules/"+id+"/bin/dagger", cli)
					k.reached, k.query = id, fmt.Sprintf("{%s{process}}", id)
					if depth == 2 {
						// An entrypoint module declares no dependencies, so
						// its second level is a sibling a served sibling serves.
						k.reached, k.query = "mid2", fmt.Sprintf("{mid2{call(address: %s, query: %s)}}", gqlString("/modules/"+id), gqlString(k.query))
					}
				case legacyCallee:
					tree = tree.
						WithNewFile("modules/"+id+"/dagger-module.toml", serveNamespaceLegacyManifest(id)).
						WithNewFile("modules/"+id+"/main.dang", serveNamespaceLegacySource(serveNamespaceObject(id), key))
					k.reached = id
					// Each further level is a module that declares the
					// previous one as a dependency.
					for level := 2; level <= depth; level++ {
						app := fmt.Sprintf("app%c%s", 'a'+level, id[1:])
						tree = tree.
							WithNewFile("modules/"+app+"/dagger-module.toml", serveNamespaceLegacyManifest(app, k.reached)).
							WithNewFile("modules/"+app+"/main.dang", fmt.Sprintf("type %s {\n  pub cached(token: String!): String! {\n    %s.cached(token: token)\n  }\n}\n", serveNamespaceObject(app), k.reached))
						k.reached = app
					}
					k.query = fmt.Sprintf(`{%s{cached(token: "served")}}`, k.reached)
				}
				if caller == dirCaller || caller == hostDirCaller {
					k.query = fmt.Sprintf("{mid{call(address: %s, query: %s)}}", gqlString("/modules/"+k.reached), gqlString(k.query))
					k.reached = "mid"
				}
				cases = append(cases, k)
			}
		}
	}

	tree = tree.
		WithNewFile("dagger.toml", "\n").
		WithNewFile("modules/spelled/dagger-module.toml", serveNamespaceLegacyManifest("spelled")).
		WithNewFile("modules/spelled/main.dang", serveNamespaceLegacySource("Spelled", key)).
		WithNewFile("modules/other/dagger-module.toml", serveNamespaceLegacyManifest("other")).
		WithNewFile("modules/other/main.dang", serveNamespaceLegacySource("Other", key))
	wsRef := workspaceSelectionRemoteRef(ctx, t, c, tree)
	httpRepo := strings.TrimSuffix(wsRef, "@main")
	wsCtr := goGitBase(t, c)

	gitDaemon, repoURL := gitService(ctx, t, c, tree)
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)
	gitCtr := goGitBase(t, c).
		WithServiceBinding(gitHost, gitDaemon).
		WithNewFile("dagger.toml", "\n")
	localCtr := goGitBase(t, c).
		WithDirectory(".", tree).
		WithNewFile("dagger.toml", "[modules.caller]\nsource = \"modules/caller\"\n")

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s reaching a %s at depth %d", tc.caller, tc.callee, tc.depth), func(ctx context.Context, t *testctx.T) {
			ctr, callerRef, direct := gitCtr, repoURL+"#main:modules/caller", repoURL+"#main:modules/"+tc.checked
			switch tc.caller {
			case localCaller, hostDirCaller:
				ctr, callerRef, direct = localCtr, "caller", "./modules/"+tc.checked
			case wsClient:
				ctr, direct = wsCtr, httpRepo+"#main:modules/"+tc.checked
			}

			// The key makes the call new to this run: a host module's calls
			// are cached on its own files, not on the siblings it serves.
			query := tc.query + " # " + key
			var served string
			switch tc.caller {
			case wsClient:
				// Only a session's own client resolves serveModule in its git
				// workspace; a process it starts gets the workspace's tree.
				wc := connect(ctx, t, dagger.WithWorkspace(wsRef))
				require.NoError(t, core.NewQuery(wc).ServeModule(ctx, "/modules/"+tc.reached))
				res, err := testutil.QueryWithClient[map[string]any](wc, t, query, nil)
				require.NoError(t, err)
				served = fmt.Sprint(res)
			default:
				var err error
				served, err = ctr.
					With(daggerCallAt(callerRef, "call", "--address=/modules/"+tc.reached, "--query="+query)).
					Stdout(ctx)
				require.NoError(t, err)
			}

			switch tc.callee {
			case entrypointCallee:
				write := regexp.MustCompile(tc.checked + `-[0-9a-f]{16}`).FindString(served)
				require.NotEmpty(t, write, "the module reached wrote: %q", served)
				// The direct load either writes next to that write or, when
				// the two loads are the same module at the same commit,
				// replays the same process.
				out, err := ctr.With(daggerCallAt(direct, "process")).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, out, write, "the direct load finds the write of the module reached")
			case legacyCallee:
				out, err := ctr.With(daggerCallAt(direct, "cached", "--token=direct")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "served\ndirect\n", out)
			}
		})
	}

	t.Run("one repository spelled four ways, other modules and repositories apart", func(ctx context.Context, t *testctx.T) {
		host := strings.TrimSuffix(strings.TrimPrefix(httpRepo, "http://"), "/repo.git")
		var out string
		for i, repo := range []string{
			httpRepo,
			"http://anon@" + host + "/repo.git",
			"http://" + host + ":80/repo.git",
			"http://anon@" + host + ":80/repo.git",
		} {
			var err error
			out, err = wsCtr.With(daggerCallAt(repo+"#main:modules/spelled", "cached", fmt.Sprintf("--token=t%d", i))).Stdout(ctx)
			require.NoError(t, err, repo)
		}
		require.Equal(t, "t0\nt1\nt2\nt3\n", out)

		out, err := wsCtr.With(daggerCallAt(httpRepo+"#main:modules/other", "cached", "--token=other")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "other\n", out, "another module of the repository keeps its own volumes")

		out, err = gitCtr.With(daggerCallAt(repoURL+"#main:modules/other", "cached", "--token=elsewhere")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "elsewhere\n", out, "the same module path in another repository keeps its own volumes")
	})
}
