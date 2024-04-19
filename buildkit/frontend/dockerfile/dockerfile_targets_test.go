package dockerfile

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerui"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/moby/buildkit/util/testutil/workers"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

var targetsTests = integration.TestFuncs(
	testTargetsList,
	testTargetsDescribeDefinition,
)

func testTargetsList(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureFrontendTargets)
	f := getFrontend(t, sb)
	if _, ok := f.(*clientFrontend); !ok {
		t.Skip("only test with client frontend")
	}

	dockerfile := []byte(`
# build defines stage for compiling the binary
FROM alpine AS build
RUN true

FROM busybox as second
RUN false

FROM alpine
RUN false

# binary returns the compiled binary
FROM second AS binary
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir, err := os.MkdirTemp("", "buildkit")
	require.NoError(t, err)
	defer os.RemoveAll(destDir)

	called := false
	frontend := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		res, err := c.Solve(ctx, gateway.SolveRequest{
			FrontendOpt: map[string]string{
				"frontend.caps": "moby.buildkit.frontend.subrequests",
				"requestid":     "frontend.targets",
			},
			Frontend: "dockerfile.v0",
		})
		require.NoError(t, err)

		list, err := unmarshalTargets(res)
		require.NoError(t, err)

		require.Equal(t, 1, len(list.Sources))
		require.Equal(t, dockerfile, list.Sources[0])

		require.Equal(t, 4, len(list.Targets))

		target := list.Targets[0]
		require.Equal(t, "build", target.Name)
		require.Equal(t, "alpine", target.Base)
		require.Equal(t, "defines stage for compiling the binary", target.Description)
		require.Equal(t, false, target.Default)
		require.Equal(t, int32(0), target.Location.SourceIndex)
		require.Equal(t, int32(3), target.Location.Ranges[0].Start.Line)

		target = list.Targets[1]
		require.Equal(t, "second", target.Name)
		require.Equal(t, "", target.Description)
		require.Equal(t, "busybox", target.Base)
		require.Equal(t, false, target.Default)
		require.Equal(t, int32(0), target.Location.SourceIndex)
		require.Equal(t, int32(6), target.Location.Ranges[0].Start.Line)

		target = list.Targets[2]
		require.Equal(t, "", target.Name)
		require.Equal(t, "", target.Description)
		require.Equal(t, "alpine", target.Base)
		require.Equal(t, false, target.Default)
		require.Equal(t, int32(0), target.Location.SourceIndex)
		require.Equal(t, int32(9), target.Location.Ranges[0].Start.Line)

		target = list.Targets[3]
		require.Equal(t, "binary", target.Name)
		require.Equal(t, "returns the compiled binary", target.Description)
		require.Equal(t, true, target.Default)
		require.Equal(t, int32(0), target.Location.SourceIndex)
		require.Equal(t, int32(13), target.Location.Ranges[0].Start.Line)

		called = true
		return nil, nil
	}

	_, err = c.Build(sb.Context(), client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
		},
	}, "", frontend, nil)
	require.NoError(t, err)

	require.True(t, called)
}

func testTargetsDescribeDefinition(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureFrontendTargets)
	f := getFrontend(t, sb)
	if _, ok := f.(*clientFrontend); !ok {
		t.Skip("only test with client frontend")
	}

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dockerfile := []byte(`
FROM scratch
COPY Dockerfile Dockerfile
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	called := false

	frontend := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		reqs, err := subrequests.Describe(ctx, c)
		require.NoError(t, err)

		require.True(t, len(reqs) > 0)

		hasTargets := false

		for _, req := range reqs {
			if req.Name != "frontend.targets" {
				continue
			}
			hasTargets = true
			require.Equal(t, subrequests.RequestType("rpc"), req.Type)
			require.NotEqual(t, req.Version, "")
		}
		require.True(t, hasTargets)

		called = true
		return nil, nil
	}

	_, err = c.Build(sb.Context(), client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
		},
	}, "", frontend, nil)
	require.NoError(t, err)

	require.True(t, called)
}

func unmarshalTargets(res *gateway.Result) (*targets.List, error) {
	dt, ok := res.Metadata["result.json"]
	if !ok {
		return nil, errors.Errorf("missing frontend.outline")
	}
	var l targets.List
	if err := json.Unmarshal(dt, &l); err != nil {
		return nil, err
	}
	return &l, nil
}
