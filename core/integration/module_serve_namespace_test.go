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
	"strings"

	"dagger.io/dagger/core"

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
	for _, caller := range []callerKind{gitCaller, localCaller, dirCaller} {
		for _, callee := range []calleeKind{entrypointCallee, legacyCallee} {
			for _, depth := range []int{1, 2} {
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
					k.reached, k.query = id, fmt.Sprintf(`{%s{cached(token: "served")}}`, id)
					if depth == 2 {
						app := "app" + id[1:]
						tree = tree.
							WithNewFile("modules/"+app+"/dagger-module.toml", serveNamespaceLegacyManifest(app, id)).
							WithNewFile("modules/"+app+"/main.dang", fmt.Sprintf("type %s {\n  pub cached(token: String!): String! {\n    %s.cached(token: token)\n  }\n}\n", serveNamespaceObject(app), id))
						k.reached, k.query = app, fmt.Sprintf(`{%s{cached(token: "served")}}`, app)
					}
				}
				if caller == dirCaller {
					k.query = fmt.Sprintf("{mid{call(address: %s, query: %s)}}", gqlString("/modules/"+k.reached), gqlString(k.query))
					k.reached = "mid"
				}
				cases = append(cases, k)
			}
		}
	}

	gitDaemon, repoURL := gitService(ctx, t, c, tree)
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)
	gitCtr := goGitBase(t, c).
		WithServiceBinding(gitHost, gitDaemon).
		WithNewFile("dagger.toml", "\n")
	localCtr := goGitBase(t, c).
		WithNewFile("dagger.toml", "[modules.caller]\nsource = \"modules/caller\"\n").
		WithDirectory(".", tree)

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s reaching a %s at depth %d", tc.caller, tc.callee, tc.depth), func(ctx context.Context, t *testctx.T) {
			ctr, callerRef, direct := gitCtr, repoURL+"#main:modules/caller", repoURL+"#main:modules/"+tc.checked
			if tc.caller == localCaller {
				ctr, callerRef, direct = localCtr, "caller", "./modules/"+tc.checked
			}

			_, err := ctr.
				// The key makes the call new to this run: a host module's
				// calls are cached on its own files, not on the siblings it
				// serves.
				With(daggerCallAt(callerRef, "call", "--address=/modules/"+tc.reached, "--query="+tc.query+" # "+key)).
				Stdout(ctx)
			require.NoError(t, err)

			switch tc.callee {
			case entrypointCallee:
				out, err := ctr.With(daggerCallAt(direct, "process")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, 2, strings.Count(out, tc.checked+"-"), "the direct load finds the write of the module reached: %q", out)
			case legacyCallee:
				out, err := ctr.With(daggerCallAt(direct, "cached", "--token=direct")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "served\ndirect\n", out)
			}
		})
	}
}
