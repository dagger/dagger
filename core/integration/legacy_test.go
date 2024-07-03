package core

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"github.com/dagger/dagger/testctx"
)

// LegacySuite contains tests for module versioning compatibility
type LegacySuite struct{}

func TestLegacy(t *testing.T) {
	testctx.Run(testCtx, t, LegacySuite{}, Middleware()...)
}

func (LegacySuite) TestLegacyExportAbsolutePath(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#7500
	//
	// Ensure that the old schemas return bools instead of absolute paths. This
	// change *likely* doesn't affect any prod code, but its probably still
	// worth making "absolute"ly sure (haha).

	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=bare", "--sdk=go")).
		WithNewFile("dagger.json", dagger.ContainerWithNewFileOpts{
			Contents: `{"name": "bare", "sdk": "go", "source": ".", "engineVersion": "v0.11.9"}`,
		}).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "context"

type Bare struct {}

func (m *Bare) TestContainer(ctx context.Context) (bool, error) {
	return dag.Container().WithNewFile("./path").Export(ctx, "./path")
}

func (m *Bare) TestDirectory(ctx context.Context) (bool, error) {
	return dag.Container().WithNewFile("./path").Directory("").Export(ctx, "./path")
}

func (m *Bare) TestFile(ctx context.Context) (bool, error) {
	return dag.Container().WithNewFile("./path").File("./path").Export(ctx, "./path")
}
`,
		})

	out, err := modGen.
		With(daggerQuery(`{bare{testContainer, testDirectory, testFile}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"bare": {"testContainer": true, "testDirectory": true, "testFile": true}}`, out)
}
