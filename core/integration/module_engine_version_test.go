package core

// Workspace alignment: mostly aligned; coverage targets post-workspace module-owned engine-version behavior, though setup still relies on historical module helpers.
// Scope: Module-observed schema version and module config mutations that read, preserve, or update `engineVersion`.
// Intent: Keep module-owned engine-version semantics separate from engine handshake tests, legacy compatibility tests, and the remaining module runtime umbrella coverage.

import (
	"context"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func (ModuleSuite) TestModuleSchemaVersion(ctx context.Context, t *testctx.T) {
	t.Run("standalone", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)

		require.NotEmpty(t, gjson.Get(out, "__schemaVersion").String())
	})

	t.Run("standalone explicit", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v2.0.0").
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"__schemaVersion":"v2.0.0"}`, out)
	})

	t.Run("standalone explicit dev", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v2.0.0-dev-123").
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"__schemaVersion":"v2.0.0"}`, out)
	})

	t.Run("module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=foo", "--sdk=go", "--source=.")).
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.11.0"}`).
			WithNewFile("main.go", `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Foo struct {}

func (m *Foo) GetVersion(ctx context.Context) (string, error) {
	return schemaVersion(ctx)
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}
`,
			)
		out, err := work.
			With(daggerQuery("{getVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"getVersion": "v0.11.0"}`, out)

		out, err = work.
			With(daggerCall("get-version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "v0.11.0")
	})

	t.Run("module deps", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
			WithNewFile("dagger.json", `{"name": "dep", "sdk": "go", "source": ".", "engineVersion": "v0.11.0"}`).
			WithNewFile("main.go", `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Dep struct {}

func (m *Dep) GetVersion(ctx context.Context) (string, error) {
	return schemaVersion(ctx)
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}
`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=foo", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./dep")).
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.10.0", "dependencies": [{"name": "dep", "source": "dep"}]}`).
			WithNewFile("main.go", `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Foo struct {}

func (m *Foo) GetVersion(ctx context.Context) (string, error) {
	myVersion, err := schemaVersion(ctx)
	if err != nil {
		return "", err
	}
	depVersion, err := dag.Dep().GetVersion(ctx)
	if err != nil {
		return "", err
	}
	return myVersion + " " + depVersion, nil
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}
`,
			)

		out, err := work.
			With(daggerQuery("{getVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"getVersion": "v0.10.0 v0.11.0"}`, out)

		out, err = work.
			With(daggerCall("get-version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "v0.10.0 v0.11.0")
	})
}

func (ModuleSuite) TestModuleDevelopVersion(ctx context.Context, t *testctx.T) {
	moduleSrc := `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Foo struct {}

func (m *Foo) GetVersion(ctx context.Context) (string, error) {
	return schemaVersion(ctx)
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}`

	t.Run("from low", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("from high", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v100.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		_, err := work.
			File("dagger.json").
			Contents(ctx)

		// sadly, just no way to handle this :(
		// in the future, the format of dagger.json might change dramatically,
		// and so there's no real way to know from the older version how to
		// convert it back down
		require.Error(t, err)
		requireErrOut(t, err, `module requires dagger v100.0.0, but you have`)
	})

	t.Run("from missing", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("to specified", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v0.0.0"}`)

		work = work.With(daggerExec("develop", "--compat=v0.9.9"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "v0.9.9", gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("skipped", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v0.9.9"}`)

		work = work.With(daggerExec("develop", "--compat"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "v0.9.9", gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("in install", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("install", "github.com/shykes/hello"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("in uninstall", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "dependencies": [{ "name": "hello", "source": "github.com/shykes/hello", "pin": "2d789671a44c4d559be506a9bc4b71b0ba6e23c9" }], "source": ".", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("uninstall", "hello"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("in update", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("update"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})
}
