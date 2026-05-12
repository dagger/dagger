package core

// "Legacy default path" is a specific hidden config field that preserves the
// old behavior of +defaultPath specifically when it is used by the
// now-deprecated "toolchain modules" feature.
//
// These tests cover that behavior through both inputs that can enable it:
// explicit workspace config, via the hidden legacy-default-path field, and
// runtime compat projection from a legacy dagger.json toolchains config. In both
// cases, the toolchain module's +defaultPath inputs must resolve from the
// consuming workspace root instead of the toolchain module source root.

import (
	"context"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type WorkspaceLegacyDefaultPathSuite struct{}

func TestWorkspaceLegacyDefaultPath(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceLegacyDefaultPathSuite{})
}

func (WorkspaceLegacyDefaultPathSuite) TestToolchainDefaultPathResolvesFromWorkspace(ctx context.Context, t *testctx.T) {
	const (
		workspaceMarker = "from workspace root"
		toolMarker      = "from tool module source"
	)

	for _, tc := range []struct {
		name  string
		setup func(testing.TB, *dagger.Client) *dagger.Container
	}{
		{
			name: "native workspace config",
			setup: func(t testing.TB, c *dagger.Client) *dagger.Container {
				return legacyDefaultPathFixture(t, c, workspaceMarker, toolMarker).
					WithNewFile(".dagger/config.toml", `[modules.reader]
source = "../tool"
legacy-default-path = true
`)
			},
		},
		{
			name: "compat dagger.json",
			setup: func(t testing.TB, c *dagger.Client) *dagger.Container {
				return legacyDefaultPathFixture(t, c, workspaceMarker, toolMarker).
					WithNewFile("dagger.json", `{
  "name": "app",
  "toolchains": [
    {
      "name": "reader",
      "source": "tool"
    }
  ]
}`)
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := tc.setup(t, c)
			out, err := ctr.With(daggerCall("reader", "read")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, workspaceMarker, strings.TrimSpace(out))
			require.NotContains(t, out, toolMarker)
		})
	}
}

func legacyDefaultPathFixture(t testing.TB, c *dagger.Client, workspaceMarker, toolMarker string) *dagger.Container {
	t.Helper()

	return goGitBase(t, c).
		WithNewFile("/work/workspace-marker.txt", workspaceMarker).
		WithExec([]string{"mkdir", "-p", "/work/tool"}).
		WithWorkdir("/work/tool").
		With(daggerExec("module", "init", "--sdk=go", "--source=.", "reader", ".")).
		WithNewFile("/work/tool/workspace-marker.txt", toolMarker).
		WithNewFile("/work/tool/main.go", `package main

import (
	"context"

	"dagger/reader/internal/dagger"
)

type Reader struct {
	Source *dagger.Directory
}

func New(
	// +defaultPath="/"
	source *dagger.Directory,
) *Reader {
	return &Reader{Source: source}
}

func (m *Reader) Read(ctx context.Context) (string, error) {
	return m.Source.File("workspace-marker.txt").Contents(ctx)
}
`).
		WithWorkdir("/work")
}
