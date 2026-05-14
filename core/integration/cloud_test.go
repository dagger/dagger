package core

// These tests cover `dag.Cloud().TraceURL`. They verify the trace URL returned
// to direct API callers and to module code, including the error when no Dagger
// Cloud organization is configured.
//
// See also:
// - telemetry_test.go: local telemetry collection and export behavior.

import (
	"context"
	"testing"

	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type CloudSuite struct{}

func TestCloud(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CloudSuite{})
}

func (CloudSuite) TestTraceURL(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// depends on where the test runs - in an already nested test, we're *not* logged in
	org, _ := auth.CurrentOrgName()

	url, err := c.Cloud().TraceURL(ctx)
	if org == "" {
		requireErrOut(t, err, "no cloud organization configured")
	} else {
		require.NoError(t, err)
		require.Contains(t, url, "https://dagger.cloud/")
		require.Contains(t, url, org)
	}
}

func (CloudSuite) TestTraceURLNested(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// depends on where the test runs - in an already nested test, we're *not* logged in
	org, _ := auth.CurrentOrgName()

	src := `package main

import (
	"context"
)

type Test struct {}

func (m *Test) TraceURL(ctx context.Context) (string, error) {
	return dag.Cloud().TraceURL(ctx)
}
`
	modGen := modInit(t, c, "go", src)
	out, err := modGen.With(daggerCall("trace-url")).Stdout(ctx)
	if org == "" {
		requireErrOut(t, err, "no cloud organization configured")
	} else {
		require.NoError(t, err)
		require.Contains(t, out, "https://dagger.cloud/")
		require.Contains(t, out, org)
	}
}
