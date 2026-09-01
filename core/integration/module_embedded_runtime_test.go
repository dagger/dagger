package core

// These tests cover embedded runtimes: modules whose dagger-module.toml
// `[runtime] source` is the internal embed:<filename> form. The engine reads
// the named Dang file from the module's own source root (next to
// dagger-module.toml) and evaluates it in-process with the native Dang
// interpreter — no separate runtime module is resolved or loaded.

import (
	"context"
	"errors"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type EmbeddedRuntimeSuite struct{}

func TestEmbeddedRuntime(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(EmbeddedRuntimeSuite{})
}

// The happy path: the runtime container is built by the committed
// runtime.dang, functions are discovered through it and calls execute in it.
// The result also proves modSource.sdk.debug resolves from inside the
// embedded runtime's evaluation, and that the engine never passes
// introspectionJson (the runtime forwards both via env vars).
func (EmbeddedRuntimeSuite) TestCallViaEmbeddedRuntime(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceFixture(t, c, "embedded-runtime")

	out, err := ctr.With(daggerFunctions()).CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "hello-world")

	out, err = ctr.With(daggerCall("hello-world")).CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "Hello from embedded runtime (debug=false, introspection=absent)")
}

// A missing runtime file is a missing-generated-files situation, pointing at
// `dagger generate` — never a fallback to builtin or external SDK loading.
func (EmbeddedRuntimeSuite) TestMissingRuntimeFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := workspaceFixture(t, c, "embedded-runtime").
		WithoutFile("runtime.dang").
		With(daggerCall("hello-world")).
		Sync(ctx)

	requireErrOut(t, err, `embedded runtime file "runtime.dang" not found`)
	requireErrOut(t, err, "run `dagger generate`")
}

// The moduleRuntime signature is a locked cross-repo contract: a runtime
// file that deviates from it is rejected up front, with the expected
// signature spelled out.
func (EmbeddedRuntimeSuite) TestInvalidRuntimeSignature(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := workspaceFixture(t, c, "embedded-runtime").
		With(fileContents("runtime.dang", `type EmbeddedRuntime {
  pub moduleRuntime(modSource: ModuleSource!): Container! {
    container.from("alpine")
  }
}
`)).
		With(daggerCall("hello-world")).
		Sync(ctx)

	requireErrOut(t, err, "must declare an introspectionJson argument")
	requireErrOut(t, err, "expected moduleRuntime(modSource: ModuleSource!, introspectionJson: File): Container!")
}

// Malformed embed refs fail hard on validation; they are never reinterpreted
// as builtin names, git refs or local paths.
func (EmbeddedRuntimeSuite) TestInvalidEmbedRef(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name          string
		source        string
		expectedError string
	}{
		{
			name:          "path traversal",
			source:        "embed:../evil.dang",
			expectedError: "must be a bare file name",
		},
		{
			name:          "wrong extension",
			source:        "embed:runtime.txt",
			expectedError: "must end in .dang",
		},
		{
			name:          "missing filename",
			source:        "embed:",
			expectedError: "missing filename",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			_, err := workspaceFixture(t, c, "embedded-runtime").
				With(fileContents("dagger-module.toml", `name = "test"
engineVersion = "latest"
source = "src"

[runtime]
  source = "`+tc.source+`"
`)).
				With(daggerCall("hello-world")).
				Sync(ctx)

			requireErrOut(t, err, tc.expectedError)
			// the failed embed ref must not fall through to the SDK dispatch's
			// unknown-ref help text
			var execErr *dagger.ExecError
			if errors.As(err, &execErr) {
				require.NotContains(t, execErr.Stdout+execErr.Stderr, "The available SDKs are")
			}
		})
	}
}
