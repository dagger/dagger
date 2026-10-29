package core

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
)

// dangTomlModule is a Git workspace holding the Dang SDK module tiny at tiny/,
// declaring engineVersion in its dagger.json, with main.dang holding source,
// plus extra files under the module directory, such as a dang.toml. A path
// starting with / is at the workspace root instead.
func dangTomlModule(t *testctx.T, c *dagger.Client, engineVersion, source string, files map[string]string) *core.Container {
	t.Helper()
	ctr := goGitBase(t, c).
		WithNewFile("tiny/dagger.json", `{"name":"tiny","engineVersion":"`+engineVersion+`","sdk":{"source":"dang"}}`).
		WithNewFile("tiny/main.dang", source)
	for path, contents := range files {
		if rootPath, ok := strings.CutPrefix(path, "/"); ok {
			ctr = ctr.WithNewFile(rootPath, contents)
			continue
		}
		ctr = ctr.WithNewFile("tiny/"+path, contents)
	}
	return ctr
}

// dangSelection is a Dang module that selects fields of an object with open
// and close, `.{ }` in Dang 1.0 and `.{{ }}` from Dang 2.0 on.
func dangSelection(open, close string) string {
	return `type Tiny {
  pub size: Int! {
    let info = Dagger.directory.withNewFile("hello.txt", "some text").file("hello.txt").` + open + `contents, size` + close + `
    info.size
  }
}
`
}

// A Dang SDK module's dang.toml wins over the engineVersion of its dagger.json:
// `[imports.Dagger] dagger` is its engine version. Without one, the
// engineVersion stays. The nearest dang.toml from the source directory up to
// the module directory counts, never one above it.
func (DangSuite) TestDangTomlEngineVersion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := dangSelection("{{", "}}")

	for _, tc := range []struct {
		name     string
		manifest string
		files    map[string]string
		version  string
	}{
		{
			name:     "no dang.toml",
			manifest: "v0.21.9",
			version:  "v0.21.9",
		},
		{
			name:     "dang.toml without a Dagger import",
			manifest: "v0.21.9",
			files:    map[string]string{"dang.toml": "dang = \"2.1\"\n"},
			version:  "v0.21.9",
		},
		{
			name:     "dagger = true follows engineVersion",
			manifest: "v0.21.9",
			files:    map[string]string{"dang.toml": "[imports.Dagger]\ndagger = true\n"},
			version:  "v0.21.9",
		},
		{
			name:     "declared",
			manifest: "v0.21.9",
			files:    map[string]string{"dang.toml": "[imports.Dagger]\ndagger = \"v1.0.0-beta.20\"\n"},
			version:  "v1.0.0-beta.20",
		},
		{
			name:     "declared older than engineVersion",
			manifest: "v1.0.0-beta.20",
			files:    map[string]string{"dang.toml": "dang = \"2.1\"\n\n[imports.Dagger]\ndagger = \"0.21.9\"\n"},
			version:  "v0.21.9",
		},
		{
			name:     "a dang.toml above the module directory does not count",
			manifest: "v0.21.9",
			files:    map[string]string{"/dang.toml": "[imports.Dagger]\ndagger = \"v1.0.0-beta.20\"\n"},
			version:  "v0.21.9",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := dangTomlModule(t, c, tc.manifest, source, tc.files).
				With(daggerExec("core", "module-source", "--ref-string", "tiny", "engine-version")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.version, strings.TrimSpace(out))
		})
	}

	t.Run("module directory above a source directory", func(ctx context.Context, t *testctx.T) {
		out, err := goGitBase(t, c).
			WithNewFile("tiny/dagger.json", `{"name":"tiny","engineVersion":"v0.21.9","sdk":{"source":"dang"},"source":"src"}`).
			WithNewFile("tiny/dang.toml", "[imports.Dagger]\ndagger = \"v1.0.0-beta.20\"\n").
			WithNewFile("tiny/src/main.dang", source).
			With(daggerExec("core", "module-source", "--ref-string", "tiny", "engine-version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "v1.0.0-beta.20", strings.TrimSpace(out))
	})

	t.Run("directory source ignores a dang.toml above the module", func(ctx context.Context, t *testctx.T) {
		version, err := core.NewQuery(c).Directory().
			WithNewFile("dang.toml", "[imports.Dagger]\ndagger = \"v1.0.0-beta.20\"\n").
			WithNewFile("tiny/dagger.json", `{"name":"tiny","engineVersion":"v0.21.9","sdk":{"source":"dang"}}`).
			WithNewFile("tiny/main.dang", source).
			AsModuleSource(core.DirectoryAsModuleSourceOpts{SourceRootPath: "tiny"}).
			EngineVersion(ctx)
		require.NoError(t, err)
		require.Equal(t, "v0.21.9", version)
	})

	// The engine version a dang.toml declares wins on load, but a config
	// write keeps the manifest's own, and the caching default that follows
	// from it.
	t.Run("config write keeps engineVersion", func(ctx context.Context, t *testctx.T) {
		for _, declared := range []string{"v1.0.0-beta.20", "v0.19.0"} {
			contents, err := core.NewQuery(c).Directory().
				WithNewFile("dagger.json", `{"name":"tiny","engineVersion":"v0.21.9","sdk":{"source":"dang"}}`).
				WithNewFile("dang.toml", "dang = \"2.1\"\n\n[imports.Dagger]\ndagger = \""+declared+"\"\n").
				WithNewFile("main.dang", source).
				AsModuleSource().
				WithName("renamed").
				GeneratedContextChangeset().
				Layer().
				File("dagger.json").
				Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, contents, `"engineVersion": "v0.21.9"`)
			require.NotContains(t, contents, declared)
			require.NotContains(t, contents, "disableDefaultFunctionCaching")
		}
	})

	t.Run("too new", func(ctx context.Context, t *testctx.T) {
		_, err := dangTomlModule(t, c, "v0.21.9", source, map[string]string{
			"dang.toml": "[imports.Dagger]\ndagger = \"v999.0.0\"\n",
		}).
			With(daggerCallAt("tiny", "size")).
			Sync(ctx)
		requireErrOut(t, err, "module requires dagger v999.0.0, but you have")
	})

	t.Run("mismatch warns", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(&logs))
		modDir := t.TempDir()
		for name, contents := range map[string]string{
			"dagger.json": `{"name":"tiny","engineVersion":"v0.21.9","sdk":{"source":"dang"}}`,
			"dang.toml":   "[imports.Dagger]\ndagger = \"v1.0.0-beta.20\"\n",
			// Unique, so the module source is loaded, not cached.
			"main.dang": source + "# " + rand.Text() + "\n",
		} {
			require.NoError(t, os.WriteFile(filepath.Join(modDir, name), []byte(contents), 0o644))
		}
		version, err := core.NewQuery(c).ModuleSource(modDir).EngineVersion(ctx)
		require.NoError(t, err)
		require.Equal(t, "v1.0.0-beta.20", version)
		require.NoError(t, c.Close()) // close + flush logs
		require.Contains(t, logs.String(), `WARNING: module "tiny": dang.toml declares dagger v1.0.0-beta.20, but dagger.json declares engineVersion v0.21.9; using v1.0.0-beta.20 from dang.toml`)
	})
}

// The Dagger version a dang.toml declares selects the module's naming rules,
// like an engineVersion: a module whose dagger.json predates the naming cut
// (engine.IdentifierCasingVersion) gets the new rules from a dang.toml that
// declares the cut.
func (DangSuite) TestDangTomlNamingCut(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := `type Tiny {
  pub client: HttpClient! { HttpClient }
}

type HttpClient {
  pub ping: String! { "pong" }
}
`
	const workspace = "[modules.tiny]\nsource = \"tiny\"\n"
	for _, tc := range []struct {
		name     string
		files    map[string]string
		typeName string
	}{
		{name: "engineVersion before the cut", files: map[string]string{"/dagger.toml": workspace}, typeName: "TinyHttpClient"},
		{
			name: "dang.toml declares the cut",
			files: map[string]string{
				"/dagger.toml": workspace,
				"dang.toml":    "[imports.Dagger]\ndagger = \"" + engine.IdentifierCasingVersion + "\"\n",
			},
			typeName: "TinyHTTPClient",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := dangTomlModule(t, c, "v1.0.0-beta.20", source, tc.files).
				With(daggerQuery(`{tiny{client{__typename ping}}}`)).
				Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"tiny":{"client":{"__typename":"`+tc.typeName+`","ping":"pong"}}}`, out)
		})
	}
}

// A Dang SDK module is parsed as the Dang version its dang.toml declares.
// Without one, a module whose engineVersion predates Dang 2
// (engine.MinimumDangV2ModuleVersion) is Dang 1.0, where `.{ }` is selection,
// and a later one is the current Dang, where it is `.{{ }}`. The engine
// version a dang.toml declares picks that default too.
func (DangSuite) TestDangTomlDangVersion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	legacy := dangSelection("{", "}")
	current := dangSelection("{{", "}}")

	for _, tc := range []struct {
		name     string
		manifest string
		source   string
		files    map[string]string
	}{
		{name: "old engineVersion is Dang 1.0", manifest: "v0.20.6", source: legacy},
		{name: "new engineVersion is current Dang", manifest: "v0.21.9", source: current},
		{
			name:     "old engineVersion, dang.toml without keys",
			manifest: "v0.20.6",
			source:   legacy,
			files:    map[string]string{"dang.toml": ""},
		},
		{
			name:     "old engineVersion declares Dang 2.1",
			manifest: "v0.20.6",
			source:   current,
			files:    map[string]string{"dang.toml": "dang = \"2.1\"\n"},
		},
		{
			name:     "new engineVersion declares Dang 1.0",
			manifest: "v0.21.9",
			source:   legacy,
			files:    map[string]string{"dang.toml": "dang = \"1.0\"\n"},
		},
		{
			name:     "old engineVersion, dang.toml declares a new engine",
			manifest: "v0.20.6",
			source:   current,
			files:    map[string]string{"dang.toml": "[imports.Dagger]\ndagger = \"v0.21.9\"\n"},
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := dangTomlModule(t, c, tc.manifest, tc.source, tc.files).
				With(daggerCallAt("tiny", "size")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "9", strings.TrimSpace(out))
		})
	}

	for _, tc := range []struct {
		name     string
		manifest string
		source   string
		files    map[string]string
	}{
		{name: "old engineVersion rejects Dang 2 selection", manifest: "v0.20.6", source: current},
		{name: "new engineVersion rejects Dang 1.0 selection", manifest: "v0.21.9", source: legacy},
		{
			name:     "declared Dang 2.1 rejects Dang 1.0 selection",
			manifest: "v0.20.6",
			source:   legacy,
			files:    map[string]string{"dang.toml": "dang = \"2.1\"\n"},
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			_, err := dangTomlModule(t, c, tc.manifest, tc.source, tc.files).
				With(daggerCallAt("tiny", "size")).
				Sync(ctx)
			require.Error(t, err)
		})
	}

	t.Run("too new", func(ctx context.Context, t *testctx.T) {
		_, err := dangTomlModule(t, c, "v0.21.9", current, map[string]string{"dang.toml": "dang = \"99.0\"\n"}).
			With(daggerCallAt("tiny", "size")).
			Sync(ctx)
		requireErrOut(t, err, "module requires Dang 99.0, but this Dagger engine supports Dang")
		requireErrOut(t, err, "upgrade Dagger")
	})

	// Dang reads a dang.toml above the module directory, which the engine
	// does not: the module's own versions decide all the same.
	t.Run("directory source ignores a dang version above the module", func(ctx context.Context, t *testctx.T) {
		_, err := core.NewQuery(c).Directory().
			WithNewFile("dang.toml", "dang = \"1.0\"\n").
			WithNewFile("tiny/dagger.json", `{"name":"tiny","engineVersion":"v0.21.9","sdk":{"source":"dang"}}`).
			WithNewFile("tiny/main.dang", current).
			AsModuleSource(core.DirectoryAsModuleSourceOpts{SourceRootPath: "tiny"}).
			AsModule().
			Sync(ctx)
		require.NoError(t, err)
	})

	// Fields without a type annotation are typed by inferring their values,
	// which the SDK of the Dang v1 line did when loading a module's types.
	t.Run("Dang 1.0 fields typed by their values", func(ctx context.Context, t *testctx.T) {
		out, err := dangTomlModule(t, c, "v0.20.6", `type Tiny {
  pub tags = ["a", "b"]
  pub greeting = "hello, " + "world"
}
`, nil).
			With(daggerCallAt("tiny", "greeting")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello, world", strings.TrimSpace(out))
	})

	// Names later versions took: `rescue` was an ordinary identifier before
	// Dang 2.1 reserved it, and a module's own `loop` shadows the builtin.
	t.Run("Dang 1.0 names later versions took", func(ctx context.Context, t *testctx.T) {
		out, err := dangTomlModule(t, c, "v0.20.6", `let loop = 2

type Tiny {
  pub rescue: Int! = 1
  pub total(rescue: Int! = 3): Int! { rescue + loop }
}
`, nil).
			With(daggerCallAt("tiny", "total")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "5", strings.TrimSpace(out))
	})
}
