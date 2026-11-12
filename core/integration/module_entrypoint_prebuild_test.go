package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// prebuildEntrypoint is a module entrypoint whose prebuild member returns the
// file its calls read. The file holds the time, in seconds, at which its exec
// ran. The nonce, spliced in with %q, keeps the exec from being cached by an
// earlier run of the test.
const prebuildEntrypoint = `type Entrypoint implements ModuleEntrypoint {
  pub sources(workspace: Workspace!): Directory! {
    directory.withDirectory(workspace.cwd, workspace.directory(".", gitignore: true))
  }

  let built: File! {
    container
      .from(%q)
      .withEnvVariable("NONCE", %q)
      .withExec(["sh", "-c", "date +%%s > /built"])
      .file("/built")
  }

  pub prebuild: File! {
    built
  }

  pub types: [TypeDef!]! {
    [
      typeDef
        .withObject("M")
        .withConstructor(function("", typeDef.withObject("M")))
        .withFunction(function("Built", typeDef.withKind(TypeDefKind.STRING_KIND))),
    ]
  }

  pub call(receiverType: String!, receiverValue: JSON, fnName: String!, fnArgs: JSON!): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else {
      (JSON.encode(built.contents.trimSpace) :: JSON!)
    }
  }
}
`

// A module's _prebuild field returns the file its entrypoint's prebuild member
// declares, as the module's calls build it: selecting the field builds the
// file, and a call made later reads that file rather than building another,
// which would hold a later time.
func (ModuleSuite) TestModuleEntrypointPrebuildField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := goGitBase(t, c).
		WithNewFile("m/dagger-module.toml", sourcesModuleManifest).
		WithNewFile("m/entrypoint/main.dang", fmt.Sprintf(prebuildEntrypoint, alpineImage, rand.Text()))

	out, err := ctr.With(daggerQuery(`{moduleSource(refString: "m") {asModule {_prebuild {contents}}}}`)).Stdout(ctx)
	require.NoError(t, err)
	var res struct {
		ModuleSource struct {
			AsModule struct {
				Prebuild struct {
					Contents string
				} `json:"_prebuild"`
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &res), out)
	prebuilt := strings.TrimSpace(res.ModuleSource.AsModule.Prebuild.Contents)
	require.NotEmpty(t, prebuilt, out)

	called, err := ctr.
		WithExec([]string{"sleep", "2"}).
		With(daggerCallAt("m", "built")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, prebuilt, strings.TrimSpace(called), "the call read the file _prebuild built")
}

// platformPrebuildEntrypoint is a module entrypoint whose prebuild member
// builds for a platform it is given, or the engine's: its file names the
// platform it was built for.
var platformPrebuildEntrypoint = strings.ReplaceAll(`type Entrypoint implements ModuleEntrypoint {
  pub sources(workspace: Workspace!): Directory! {
    directory.withDirectory(workspace.cwd, workspace.directory(".", gitignore: true))
  }

  pub prebuild(platform: Platform): File! {
    directory.withNewFile("platform", BACKTICK${platform ?? defaultPlatform}BACKTICK).file("platform")
  }

  pub types: [TypeDef!]! {
    [
      typeDef
        .withObject("M")
        .withConstructor(function("", typeDef.withObject("M"))),
    ]
  }

  pub call(receiverType: String!, receiverValue: JSON, fnName: String!, fnArgs: JSON!): JSON! {
    ("{}" :: JSON!)
  }
}
`, "BACKTICK", "`")

// A module's _prebuild field with a platform returns the file its
// entrypoint's prebuild member builds for that platform, as an engine of
// that platform's calls build it, and without one, the engine's own.
func (ModuleSuite) TestModuleEntrypointPrebuildForPlatform(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := goGitBase(t, c).
		WithNewFile("m/dagger-module.toml", sourcesModuleManifest).
		WithNewFile("m/entrypoint/main.dang", platformPrebuildEntrypoint)

	out, err := ctr.With(daggerQuery(`{defaultPlatform moduleSource(refString: "m") {asModule {arm: _prebuild(platform: "linux/arm64") {contents} own: _prebuild {contents}}}}`)).Stdout(ctx)
	require.NoError(t, err)
	var res struct {
		DefaultPlatform string
		ModuleSource    struct {
			AsModule struct {
				Arm struct{ Contents string }
				Own struct{ Contents string }
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &res), out)
	require.Equal(t, "linux/arm64", res.ModuleSource.AsModule.Arm.Contents)
	require.Equal(t, res.DefaultPlatform, res.ModuleSource.AsModule.Own.Contents)
}

// A prebuild member that takes no platform builds for the engine's platform
// only: asked for another, _prebuild fails rather than return the engine's.
func (ModuleSuite) TestModuleEntrypointPrebuildWithoutPlatform(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := goGitBase(t, c).
		WithNewFile("m/dagger-module.toml", sourcesModuleManifest).
		WithNewFile("m/entrypoint/main.dang", fmt.Sprintf(prebuildEntrypoint, alpineImage, rand.Text()))

	_, err := ctr.With(daggerQuery(`{moduleSource(refString: "m") {asModule {_prebuild(platform: "linux/arm64") {contents}}}}`)).Sync(ctx)
	requireErrOut(t, err, "takes no platform")
}

// runtimeImageEntrypoint is a module entrypoint whose runtimeImage member
// is the image its calls run in, for a platform it is given, or the
// engine's. Its call reads the image's Alpine release.
const runtimeImageEntrypoint = `type Entrypoint implements ModuleEntrypoint {
  pub sources(workspace: Workspace!): Directory! {
    directory.withDirectory(workspace.cwd, workspace.directory(".", gitignore: true))
  }

  pub runtimeImage(platform: Platform): Container! {
    container(platform: platform ?? defaultPlatform).from(%q)
  }

  pub types: [TypeDef!]! {
    [
      typeDef
        .withObject("M")
        .withConstructor(function("", typeDef.withObject("M")))
        .withFunction(function("Release", typeDef.withKind(TypeDefKind.STRING_KIND))),
    ]
  }

  pub call(receiverType: String!, receiverValue: JSON, fnName: String!, fnArgs: JSON!): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else {
      (JSON.encode(runtimeImage(null).withExec(["cat", "/etc/alpine-release"]).stdout.trimSpace) :: JSON!)
    }
  }
}
`

// A module's _runtimeImage field returns the image its entrypoint's
// runtimeImage member declares, the one its calls run in: for a platform it
// is given, as an engine of that platform's calls run in it, and without
// one, the engine's own.
func (ModuleSuite) TestModuleEntrypointRuntimeImageField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := goGitBase(t, c).
		WithNewFile("m/dagger-module.toml", sourcesModuleManifest).
		WithNewFile("m/entrypoint/main.dang", fmt.Sprintf(runtimeImageEntrypoint, alpineImage))

	out, err := ctr.With(daggerQuery(`{defaultPlatform moduleSource(refString: "m") {asModule {
		arm: _runtimeImage(platform: "linux/arm64") {platform}
		own: _runtimeImage {platform file(path: "/etc/alpine-release") {contents}}
	}}}`)).Stdout(ctx)
	require.NoError(t, err)
	var res struct {
		DefaultPlatform string
		ModuleSource    struct {
			AsModule struct {
				Arm struct{ Platform string }
				Own struct {
					Platform string
					File     struct{ Contents string }
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &res), out)
	require.Equal(t, "linux/arm64", res.ModuleSource.AsModule.Arm.Platform)
	require.Equal(t, res.DefaultPlatform, res.ModuleSource.AsModule.Own.Platform)

	called, err := ctr.With(daggerCallAt("m", "release")).Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(called))
	require.Equal(t, strings.TrimSpace(res.ModuleSource.AsModule.Own.File.Contents), strings.TrimSpace(called), "the call ran in the image _runtimeImage returned")
}

// A module whose entrypoint declares no runtimeImage member has no
// _runtimeImage.
func (ModuleSuite) TestModuleEntrypointWithoutRuntimeImage(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := goGitBase(t, c).
		WithNewFile("m/dagger-module.toml", sourcesModuleManifest).
		WithNewFile("m/entrypoint/main.dang", fmt.Sprintf(prebuildEntrypoint, alpineImage, rand.Text()))

	out, err := ctr.With(daggerQuery(`{moduleSource(refString: "m") {asModule {_runtimeImage {platform}}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"moduleSource": {"asModule": {"_runtimeImage": null}}}`, out)
}
