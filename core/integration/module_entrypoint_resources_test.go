package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// resourcesProbeEntrypoint is a module entrypoint with a resources member,
// spliced in with %s. Its functions report what the engine loaded the way a
// generated SDK entrypoint reads the files it lists. Call returns the shared
// file and the call digest, which changes exactly when the call key changes.
const resourcesProbeEntrypoint = `type Entrypoint implements ModuleEntrypoint {
%s

  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Probe")
        .withConstructor(function("", typeDef.withObject("Probe")))
        .withFunction(function("ContextFiles", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("Shared", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("Call", typeDef.withKind(TypeDefKind.STRING_KIND))),
    ]
  }

  pub call(
    workspace: Workspace!,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
    callDigest: String,
  ): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else if (fnName == "ContextFiles") {
      (JSON.encode(currentModule.contextDirectory.glob("**").join("\n")) :: JSON!)
    } else if (fnName == "Shared") {
      (JSON.encode(shared) :: JSON!)
    } else if (fnName == "Call") {
      (JSON.encode(shared.trimSpace + "@" + (callDigest ?? "")) :: JSON!)
    } else {
      raise "unknown function: " + fnName
    }
  }

  let shared: String! {
    currentModule.contextDirectory.file(currentModule.sourceSubpath + "/../shared/value.txt").contents
  }
}
`

const sharedResourcesMember = `  pub resources: [String!]! { ["../shared"] }`

const resourcesModuleManifest = `name = "m"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`

func resourcesModuleFixture(c *dagger.Client, member string) *core.Directory {
	return core.NewQuery(c).Directory().
		WithNewFile("shared/value.txt", "from the shared sibling\n").
		WithNewFile("shared/testdata/big.txt", "big\n").
		WithNewFile("other/unlisted.txt", "unlisted\n").
		WithNewFile("m/dagger-module.toml", resourcesModuleManifest).
		WithNewFile("m/data/local.txt", "module data\n").
		WithNewFile("m/entrypoint/main.dang", fmt.Sprintf(resourcesProbeEntrypoint, member))
}

func moduleSourceContextFiles(ctx context.Context, ctr *core.Container, ref string) ([]string, error) {
	out, err := ctr.
		With(daggerExec("core", "module-source", "--ref-string", ref, "context-directory", "glob", "--pattern", "**")).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func moduleSourceDigest(ctx context.Context, t *testctx.T, ctr *core.Container, ref string) string {
	t.Helper()
	out, err := ctr.With(daggerExec("core", "module-source", "--ref-string", ref, "digest")).Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

func requireNoGitMetadata(t *testctx.T, files []string) {
	t.Helper()
	for _, file := range files {
		for _, part := range strings.Split(strings.TrimSuffix(file, "/"), "/") {
			require.NotEqual(t, ".git", part, "%s is repository metadata", file)
		}
	}
}

func (ModuleSuite) TestModuleEntrypointResources(ctx context.Context, t *testctx.T) {
	local := func(t *testctx.T, c *dagger.Client, member string) *core.Container {
		return goGitBase(t, c).WithDirectory(".", resourcesModuleFixture(c, member))
	}
	call := func(ctx context.Context, t *testctx.T, ctr *core.Container, ref, fn string) string {
		t.Helper()
		out, err := ctr.With(daggerCallAt(ref, fn)).Stdout(ctx)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}
	requireLoaded := func(t *testctx.T, files []string) {
		t.Helper()
		require.Contains(t, files, "m/dagger-module.toml")
		require.Contains(t, files, "m/entrypoint/main.dang")
		require.Contains(t, files, "shared/value.txt")
		require.NotContains(t, files, "other/unlisted.txt")
	}

	t.Run("local module loads the listed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, sharedResourcesMember)

		files, err := moduleSourceContextFiles(ctx, ctr, "./m")
		require.NoError(t, err)
		requireLoaded(t, files)
		requireLoaded(t, strings.Fields(call(ctx, t, ctr, "./m", "context-files")))
		require.Equal(t, "from the shared sibling", call(ctx, t, ctr, "./m", "shared"))
	})

	t.Run("workspace module loads the listed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, sharedResourcesMember).
			WithNewFile("dagger.toml", "[modules.m]\nsource = \"m\"\n")

		requireLoaded(t, strings.Fields(call(ctx, t, ctr, "m", "context-files")))
		require.Equal(t, "from the shared sibling", call(ctx, t, ctr, "m", "shared"))
	})

	t.Run("git module at a subpath loads the listed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		gitDaemon, repoURL := gitService(ctx, t, c, resourcesModuleFixture(c, sharedResourcesMember))
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)
		ctr := goGitBase(t, c).WithServiceBinding(gitHost, gitDaemon)

		files, err := moduleSourceContextFiles(ctx, ctr, repoURL+"#main:m")
		require.NoError(t, err)
		requireLoaded(t, files)
		requireNoGitMetadata(t, files)
		require.Equal(t, "from the shared sibling", call(ctx, t, ctr, repoURL+"#main:m", "shared"))
	})

	t.Run("exclusions apply in order", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, `  pub resources: [String!]! { ["../shared", "!../shared/testdata", "!data"] }`)

		files, err := moduleSourceContextFiles(ctx, ctr, "./m")
		require.NoError(t, err)
		require.Contains(t, files, "shared/value.txt")
		require.NotContains(t, files, "shared/testdata/big.txt")
		require.NotContains(t, files, "m/data/local.txt")
		require.Contains(t, files, "m/entrypoint/main.dang")
	})

	t.Run("an empty list loads the module directory only", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, `  pub resources: [String!]! { [] }`)

		files, err := moduleSourceContextFiles(ctx, ctr, "./m")
		require.NoError(t, err)
		require.Contains(t, files, "m/entrypoint/main.dang")
		for _, file := range files {
			require.True(t, strings.HasPrefix(file, "m/"), "%s is outside the module directory", file)
		}
	})

	t.Run("digest follows the listed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, sharedResourcesMember)
		original := moduleSourceDigest(ctx, t, ctr, "./m")

		ctr = ctr.WithNewFile("shared/value.txt", "changed\n")
		changed := moduleSourceDigest(ctx, t, ctr, "./m")
		require.NotEqual(t, original, changed)

		ctr = ctr.WithNewFile("other/unlisted.txt", "changed\n")
		require.Equal(t, changed, moduleSourceDigest(ctx, t, ctr, "./m"))
	})

	t.Run("digest follows the listed files at a git commit", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		repo := core.NewQuery(c).Container().From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithDirectory("/repo", resourcesModuleFixture(c, sharedResourcesMember)).
			WithWorkdir("/repo").
			WithExec([]string{"sh", "-c", `set -e
git init -q -b main
git config user.email dagger@example.com
git config user.name "Dagger Tests"
git add -A
git commit -qm original
git tag original
echo changed > shared/value.txt
git commit -qam "change a listed file"
git tag listed
echo changed > other/unlisted.txt
git commit -qam "change a file outside the context"
`}).
			Directory("/repo")
		gitDaemon, repoURL := gitService(ctx, t, c, repo)
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)
		ctr := goGitBase(t, c).WithServiceBinding(gitHost, gitDaemon)

		original := moduleSourceDigest(ctx, t, ctr, repoURL+"#original:m")
		listed := moduleSourceDigest(ctx, t, ctr, repoURL+"#listed:m")
		require.NotEqual(t, original, listed)
		require.Equal(t, listed, moduleSourceDigest(ctx, t, ctr, repoURL+"#main:m"))
	})

	// Each dagger call is a new client and session. The default cache policy
	// keeps the result across sessions while the call key holds, and a change
	// to a listed file changes the key.
	t.Run("call key follows the listed files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, sharedResourcesMember)

		first := call(ctx, t, ctr.WithEnvVariable("RUN", "1"), "./m", "call")
		require.True(t, strings.HasPrefix(first, "from the shared sibling@xxh3:"), first)
		require.Equal(t, first, call(ctx, t, ctr.WithEnvVariable("RUN", "2"), "./m", "call"))

		ctr = ctr.WithNewFile("shared/value.txt", "changed\n")
		changed := call(ctx, t, ctr.WithEnvVariable("RUN", "3"), "./m", "call")
		require.True(t, strings.HasPrefix(changed, "changed@xxh3:"), changed)
		require.NotEqual(t, strings.TrimPrefix(first, "from the shared sibling"), strings.TrimPrefix(changed, "changed"))

		ctr = ctr.WithNewFile("other/unlisted.txt", "changed\n")
		require.Equal(t, changed, call(ctx, t, ctr.WithEnvVariable("RUN", "4"), "./m", "call"))
	})

	t.Run("refusals", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []struct {
			name   string
			member string
			top    string
			err    string
		}{
			{
				name:   "pattern escaping the context root",
				member: `  pub resources: [String!]! { ["../../outside"] }`,
				err:    `include/exclude path "../outside" escapes context`,
			},
			{
				name:   "value field",
				member: `  pub resources: [String!]! = ["../shared"]`,
				err:    "resources must be a function",
			},
			{
				name:   "raised error",
				member: `  pub resources: [String!]! { raise "no resources today" }`,
				err:    "no resources today",
			},
			{
				name:   "member reads a file",
				member: `  pub resources: [String!]! { [container.from("` + alpineImage + `").file("/etc/os-release").contents] }`,
				err:    "entrypoint resources must not call the Dagger API",
			},
			{
				name:   "member reads the current module",
				member: `  pub resources: [String!]! { currentModule.source.entries }`,
				err:    "entrypoint resources must not call the Dagger API",
			},
			{
				name:   "member reads a module source",
				member: `  pub resources: [String!]! { [moduleSource("./other").digest] }`,
				err:    "entrypoint resources must not call the Dagger API",
			},
			{
				name:   "top-level read",
				member: sharedResourcesMember,
				top:    `let release = container.from("` + alpineImage + `").file("/etc/os-release").contents` + "\n",
				err:    "entrypoint resources must not call the Dagger API",
			},
			{
				name:   "exclusion of the entrypoint",
				member: `  pub resources: [String!]! { ["../shared", "!entrypoint"] }`,
				err:    `exclude the entrypoint source "./entrypoint"`,
			},
		} {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				ctr := local(t, c, tc.member)
				if tc.top != "" {
					ctr = ctr.WithNewFile("m/entrypoint/main.dang", tc.top+fmt.Sprintf(resourcesProbeEntrypoint, tc.member))
				}
				_, err := moduleSourceContextFiles(ctx, ctr, "./m")
				requireErrOut(t, err, tc.err)
			})
		}

		t.Run("no .git above the module", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := core.NewQuery(c).Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				WithDirectory(".", resourcesModuleFixture(c, sharedResourcesMember))

			_, err := moduleSourceContextFiles(ctx, ctr, "./m")
			requireErrOut(t, err, "no .git directory was found above the module")
		})

		// A shared entrypoint serves many modules, so it is the wrong place
		// for one module's file list.
		t.Run("entrypoint named by module reference", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			gitDaemon, repoURL := gitService(ctx, t, c, core.NewQuery(c).Directory().
				WithNewFile("entry/main.dang", fmt.Sprintf(resourcesProbeEntrypoint, sharedResourcesMember)))
			gitHost, err := gitDaemon.Hostname(ctx)
			require.NoError(t, err)
			ctr := local(t, c, "").
				WithServiceBinding(gitHost, gitDaemon).
				WithNewFile("m/dagger-module.toml", `name = "m"

[entrypoint]
kind = "dang"
source = "`+repoURL+`#main:entry"
`)

			files, err := moduleSourceContextFiles(ctx, ctr, "./m")
			require.NoError(t, err)
			require.NotContains(t, files, "shared/value.txt")

			_, err = ctr.With(daggerCallAt("./m", "shared")).Stdout(ctx)
			requireErrOut(t, err, "resources needs a local entrypoint")
		})
	})

	t.Run("repository metadata stays out", func(ctx context.Context, t *testctx.T) {
		t.Run("nested module with a broad pattern", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := local(t, c, `  pub resources: [String!]! { ["../**"] }`).
				WithExec([]string{"git", "init", "-q", "shared/vendor"})

			files, err := moduleSourceContextFiles(ctx, ctr, "./m")
			require.NoError(t, err)
			require.Contains(t, files, "other/unlisted.txt")
			require.Contains(t, files, "shared/vendor/")
			requireNoGitMetadata(t, files)
		})

		t.Run("worktree whose .git is a file", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := local(t, c, `  pub resources: [String!]! { ["../**"] }`).
				WithExec([]string{"sh", "-c", "mv .git /repo.git && echo 'gitdir: /repo.git' > .git"})

			files, err := moduleSourceContextFiles(ctx, ctr, "./m")
			require.NoError(t, err)
			require.Contains(t, files, "other/unlisted.txt")
			requireNoGitMetadata(t, files)
		})

		t.Run("git module with a broad pattern", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			gitDaemon, repoURL := gitService(ctx, t, c, resourcesModuleFixture(c, `  pub resources: [String!]! { ["../**"] }`))
			gitHost, err := gitDaemon.Hostname(ctx)
			require.NoError(t, err)
			ctr := goGitBase(t, c).WithServiceBinding(gitHost, gitDaemon)

			files, err := moduleSourceContextFiles(ctx, ctr, repoURL+"#main:m")
			require.NoError(t, err)
			require.Contains(t, files, "other/unlisted.txt")
			requireNoGitMetadata(t, files)
		})

		t.Run("root module", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			root := func(member string) *core.Container {
				return goGitBase(t, c).
					WithNewFile("dagger-module.toml", resourcesModuleManifest).
					WithNewFile("other/unlisted.txt", "unlisted\n").
					WithNewFile("entrypoint/main.dang", fmt.Sprintf(resourcesProbeEntrypoint, member))
			}

			files, err := moduleSourceContextFiles(ctx, root(`  pub resources: [String!]! { ["!other"] }`), ".")
			require.NoError(t, err)
			require.Contains(t, files, "entrypoint/main.dang")
			require.NotContains(t, files, "other/unlisted.txt")
			requireNoGitMetadata(t, files)

			withoutMember, err := moduleSourceContextFiles(ctx, root(""), ".")
			require.NoError(t, err)
			emptyList, err := moduleSourceContextFiles(ctx, root(`  pub resources: [String!]! { [] }`), ".")
			require.NoError(t, err)
			require.ElementsMatch(t, withoutMember, emptyList, "an empty list leaves the context as the module loads without the member")
		})
	})
}

// A module serves its declared clients after the engine has looked up the
// calling function, so the target's listed files must reach the caller's call
// key through the target's digest.
func (ModuleSuite) TestModuleEntrypointResourcesDeclaredClient(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := goGitBase(t, c).
		WithDirectory(".", resourcesModuleFixture(c, sharedResourcesMember)).
		WithNewFile("dagger.toml", `[modules.caller]
source = ".dagger/modules/caller"

[modules.go]
source = "go"

[sdks.go]
module = "go"

[sdks.go.scopes.".dagger/modules/caller"]
is-module = true
clients = ["./m"]
`).
		WithNewFile(".dagger/modules/caller/dagger.json", `{"name":"caller","engineVersion":"latest","sdk":{"source":"go"},"source":"."}`).
		WithNewFile(".dagger/modules/caller/main.go", `package main

import (
	"context"
	"strconv"
	"time"
)

type Caller struct{}

func (m *Caller) Message(ctx context.Context) (string, error) {
	if err := dag.ServeModule(ctx, "/m"); err != nil {
		return "", err
	}
	var message string
	q := dag.QueryBuilder().Select("m").Select("shared").Bind(&message)
	if err := q.Execute(ctx); err != nil {
		return "", err
	}
	return message + " at " + strconv.FormatInt(time.Now().UnixNano(), 10), nil
}
`)
	call := func(ctr *core.Container, run string) string {
		out, err := ctr.
			WithEnvVariable("RESOURCES_RUN", run).
			With(daggerCallAt("caller", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	first := call(ctr, "1")
	require.Contains(t, first, "from the shared sibling\n at ")
	require.Equal(t, first, call(ctr, "2"), "an unchanged target keeps the caller's result")

	ctr = ctr.WithNewFile("shared/value.txt", "changed\n")
	changed := call(ctr, "3")
	require.Contains(t, changed, "changed\n at ")
	require.Equal(t, changed, call(ctr, "4"))
}

// entrypointResourcesEvaluatedLine is the log line the engine writes when it
// evaluates a declared resources member (core/schema/modulesource.go).
const entrypointResourcesEvaluatedLine = "entrypoint resources evaluated"

// A module whose entrypoint declares no resources member loads exactly as it
// does without the feature, and a declared member is evaluated once per
// entrypoint program, not once per client or per edit of other module files.
func (ModuleDefinitionSuite) TestEntrypointResources(ctx context.Context, t *testctx.T) {
	outer := connect(ctx, t)
	e := startDefinitionEngine(ctx, t, outer, "resources")

	writeTree := func(t *testctx.T, entrypointSource string) string {
		t.Helper()
		dir := t.TempDir()
		for path, content := range map[string]string{
			".git/HEAD":              "ref: refs/heads/main\n",
			"shared/value.txt":       "from the shared sibling\n",
			"other/unlisted.txt":     "unlisted\n",
			"m/dagger-module.toml":   resourcesModuleManifest,
			"m/data/local.txt":       "module data\n",
			"m/entrypoint/main.dang": entrypointSource,
		} {
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644))
		}
		return dir
	}
	type loadedSource struct {
		ModuleSource struct {
			RebasedIncludePaths []string `json:"_rebasedIncludePaths"`
			ContextDirectory    struct{ Digest string }
		}
		Host struct {
			Directory struct{ Digest string }
		}
	}
	// load reads the module source on a fresh client, beside the context that
	// the engine loads for a module without the member.
	load := func(t *testctx.T, dir string) loadedSource {
		t.Helper()
		client := e.connect(ctx, t, dir)
		defer func() { require.NoError(t, client.Close()) }()
		var data loadedSource
		require.NoError(t, client.Do(ctx, &dagger.Request{Query: `{
  moduleSource(refString: "./m") { _rebasedIncludePaths contextDirectory { digest } }
  host { directory(path: ".", include: ["m/dagger-module.toml", "m"], gitignore: true) { digest } }
}`}, &dagger.Response{Data: &data}))
		return data
	}
	asModule := func(t *testctx.T, dir string) error {
		t.Helper()
		client := e.connect(ctx, t, dir)
		defer func() { require.NoError(t, client.Close()) }()
		return client.Do(ctx, &dagger.Request{Query: `{moduleSource(refString: "./m") { asModule { name } }}`}, &dagger.Response{})
	}

	// The cases run in order on one engine, so the log count covers them all.
	for _, tc := range []struct {
		name   string
		source string
		broken bool
	}{
		{name: "no mention", source: fmt.Sprintf(resourcesProbeEntrypoint, "")},
		{
			name:   "comment and string mentions",
			source: "# resources are listed elsewhere\n" + fmt.Sprintf(resourcesProbeEntrypoint, `  let label: String! { "resources" }`),
		},
		{
			name: "private member and another type",
			source: fmt.Sprintf(resourcesProbeEntrypoint, `  let resources: [String!]! { ["../shared"] }`) +
				"type Other {\n  pub resources: [String!]! { [\"../shared\"] }\n}\n",
		},
		{
			name:   "mention beside a syntax error",
			source: "# resources\n" + fmt.Sprintf(resourcesProbeEntrypoint, `  let broken: String! { "unterminated }`),
			broken: true,
		},
	} {
		dir := writeTree(t, tc.source)
		loaded := load(t, dir)
		require.Empty(t, loaded.ModuleSource.RebasedIncludePaths, tc.name)
		require.Equal(t, loaded.Host.Directory.Digest, loaded.ModuleSource.ContextDirectory.Digest, "%s: the context is the manifest and the module directory", tc.name)
		if tc.broken {
			err := asModule(t, dir)
			require.Error(t, err, "%s: the syntax error surfaces when the module loads its types", tc.name)
			require.NotContains(t, err.Error(), "resources", tc.name)
		}
	}
	require.Zero(t, e.logCount(ctx, t, outer, entrypointResourcesEvaluatedLine), "no entrypoint was evaluated at load")

	evaluations := func() int { return e.logCount(ctx, t, outer, entrypointResourcesEvaluatedLine) }
	dir := writeTree(t, fmt.Sprintf(resourcesProbeEntrypoint, sharedResourcesMember))

	first := load(t, dir)
	require.Equal(t, []string{"shared"}, first.ModuleSource.RebasedIncludePaths)
	require.NotEqual(t, first.Host.Directory.Digest, first.ModuleSource.ContextDirectory.Digest)
	require.Equal(t, 1, evaluations(), "the first client evaluated the member")

	load(t, dir)
	require.Equal(t, 1, evaluations(), "a second client reused the result")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "m/data/local.txt"), []byte("edited\n"), 0o644))
	edited := load(t, dir)
	require.NotEqual(t, first.ModuleSource.ContextDirectory.Digest, edited.ModuleSource.ContextDirectory.Digest)
	require.Equal(t, 1, evaluations(), "an edit outside the entrypoint reused the result")

	source := fmt.Sprintf(resourcesProbeEntrypoint, sharedResourcesMember) + "# edited\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "m/entrypoint/main.dang"), []byte(source), 0o644))
	load(t, dir)
	require.Equal(t, 2, evaluations(), "an edit of the entrypoint evaluated it again")
}
