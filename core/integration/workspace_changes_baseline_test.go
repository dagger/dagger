package core

import (
	"context"
	"strings"

	"dagger.io/dagger"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceChangesAgainstExistingBaseline covers comparing a workspace
// against a baseline it already shares content with. Generation is the case
// that matters: an SDK rewrites a module's whole context, so the changeset it
// produces spans files that already sit on the workspace unchanged. Only the
// files whose content actually differs may come back from Workspace.changes —
// otherwise every regenerate re-reports the entire generated context,
// including engine-owned files the SDK never touched.
//
// The fixture stands in for codegen without needing an SDK: the changeset's
// after side is the before side with fresh timestamps, which is what a
// rewritten-in-place generated file looks like.
func (WorkspaceSuite) TestWorkspaceChangesAgainstExistingBaseline(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceFixture(t, c, "changes-baseline").
		WithNewFile("generated.txt", "generated")

	t.Run("rewriting existing content adds nothing", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("reporter", "rewritten-baseline")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"added.txt"}, strings.Fields(out))
	})

	t.Run("editing existing content reports a modification", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("reporter", "modified-baseline")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"generated.txt"}, strings.Fields(out))
	})
}

// The idiom the SDKs generate through — moduleSource.generate(ws).changes(ws)
// — measured against a workspace that already holds the module's generated
// output, which is every regenerate after the first. What comes back has to be
// the generation, and nothing else: an SDK is contractually forbidden from
// writing the engine's own dagger-module.toml, so a changeset that carries it
// makes a conforming SDK violate that contract on every run.
//
// Two failure shapes share this cause and both are pinned by comparing against
// the changeset's own paths: the whole generated context coming back (go, php),
// and just the files that live on the workspace base without ever entering an
// overlay leaking in (python).
func (WorkspaceSuite) TestModuleRegenerateOverExistingOutput(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceFixture(t, c, "changes-baseline").
		WithNewFile("mod/dagger-module.toml", "name = \"foo\"\nengineVersion = \"v1.0.0\"\n\n[runtime]\n  source = \"go\"\n").
		WithNewFile("mod/main.go", "package main\n\ntype Foo struct{}\n\nfunc (f *Foo) Hello() string { return \"hi\" }\n").
		WithExec([]string{"dagger", "call", "reporter", "apply", "-y", "--progress=plain"},
			dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true, UseEntrypoint: true}).
		WithoutFile("mod/internal/dagger/dagger.gen.go")

	reported, err := ctr.With(daggerReportCall("reporter", "reported")).Stdout(ctx)
	require.NoError(t, err)
	roundTrip, err := ctr.With(daggerReportCall("reporter", "round-trip")).Stdout(ctx)
	require.NoError(t, err)

	require.Equal(t, []string{"mod/internal/dagger/dagger.gen.go"}, strings.Fields(reported))

	// The directory entries are a separate, older defect and not this test's
	// subject: sparseHostBase only asks the host for the touched paths
	// themselves, so when a touched path is an addition the host does not have
	// yet, the directories leading to it are missing from the baseline and read
	// as added even though the host has them. Pinned exactly so that fixing it
	// fails here and this tightens to equality with reported.
	require.Equal(t, []string{
		"mod/",
		"mod/internal/",
		"mod/internal/dagger/",
		"mod/internal/dagger/dagger.gen.go",
	}, strings.Fields(roundTrip))

	for _, p := range strings.Fields(roundTrip) {
		require.False(t, strings.HasSuffix(p, "dagger-module.toml"),
			"an SDK must never be told it wrote the engine's module config")
		require.True(t, strings.HasSuffix(p, "/") || p == "mod/internal/dagger/dagger.gen.go",
			"no file outside the generation may be reported, got %q", p)
	}
}
