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
	"github.com/moby/buildkit/frontend/subrequests/outline"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/moby/buildkit/util/testutil/workers"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

var outlineTests = integration.TestFuncs(
	testOutlineArgs,
	testOutlineSecrets,
	testOutlineDescribeDefinition,
)

func testOutlineArgs(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureFrontendOutline)
	f := getFrontend(t, sb)
	if _, ok := f.(*clientFrontend); !ok {
		t.Skip("only test with client frontend")
	}

	dockerfile := []byte(`ARG inherited=box
ARG inherited2=box2
ARG unused=abc${inherited2}
# sfx is a suffix
ARG sfx="usy${inherited}"

FROM b${sfx} AS first
# this is not assigned to anything
ARG FOO=123
# BAR is a number
ARG BAR=456
RUN true

FROM alpine${unused} AS second
ARG BAZ
RUN true

FROM scratch AS third
ARG ABC=a

# target defines build target
FROM third AS target
COPY --from=first /etc/passwd /

FROM second
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
				"requestid":     "frontend.outline",
				"build-arg:BAR": "678",
				"target":        "target",
			},
			Frontend: "dockerfile.v0",
		})
		require.NoError(t, err)

		outline, err := unmarshalOutline(res)
		require.NoError(t, err)

		require.Equal(t, "target", outline.Name)
		require.Equal(t, "defines build target", outline.Description)

		require.Equal(t, 1, len(outline.Sources))
		require.Equal(t, dockerfile, outline.Sources[0])

		require.Equal(t, 5, len(outline.Args))

		arg := outline.Args[0]
		require.Equal(t, "inherited", arg.Name)
		require.Equal(t, "box", arg.Value)
		require.Equal(t, "", arg.Description)
		require.Equal(t, int32(0), arg.Location.SourceIndex)
		require.Equal(t, int32(1), arg.Location.Ranges[0].Start.Line)

		arg = outline.Args[1]
		require.Equal(t, "sfx", arg.Name)
		require.Equal(t, "usybox", arg.Value)
		require.Equal(t, "is a suffix", arg.Description)
		require.Equal(t, int32(5), arg.Location.Ranges[0].Start.Line)

		arg = outline.Args[2]
		require.Equal(t, "FOO", arg.Name)
		require.Equal(t, "123", arg.Value)
		require.Equal(t, "", arg.Description)
		require.Equal(t, int32(9), arg.Location.Ranges[0].Start.Line)

		arg = outline.Args[3]
		require.Equal(t, "BAR", arg.Name)
		require.Equal(t, "678", arg.Value)
		require.Equal(t, "is a number", arg.Description)

		arg = outline.Args[4]
		require.Equal(t, "ABC", arg.Name)
		require.Equal(t, "a", arg.Value)

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

func testOutlineSecrets(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureFrontendOutline)
	f := getFrontend(t, sb)
	if _, ok := f.(*clientFrontend); !ok {
		t.Skip("only test with client frontend")
	}

	dockerfile := []byte(`
FROM busybox AS first
RUN --mount=type=secret,target=/etc/passwd,required=true --mount=type=ssh true

FROM alpine AS second
RUN --mount=type=secret,id=unused --mount=type=ssh,id=ssh2 true

FROM scratch AS third
ARG BAR
RUN --mount=type=secret,id=second${BAR} true

FROM third AS target
COPY --from=first /foo /
RUN --mount=type=ssh,id=ssh3,required true

FROM second
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
				"requestid":     "frontend.outline",
				"build-arg:BAR": "678",
				"target":        "target",
			},
			Frontend: "dockerfile.v0",
		})
		require.NoError(t, err)

		outline, err := unmarshalOutline(res)
		require.NoError(t, err)

		require.Equal(t, 1, len(outline.Sources))
		require.Equal(t, dockerfile, outline.Sources[0])

		require.Equal(t, 2, len(outline.Secrets))

		secret := outline.Secrets[0]
		require.Equal(t, "passwd", secret.Name)
		require.Equal(t, true, secret.Required)
		require.Equal(t, int32(0), secret.Location.SourceIndex)
		require.Equal(t, int32(3), secret.Location.Ranges[0].Start.Line)

		secret = outline.Secrets[1]
		require.Equal(t, "second678", secret.Name)
		require.Equal(t, false, secret.Required)
		require.Equal(t, int32(0), secret.Location.SourceIndex)
		require.Equal(t, int32(10), secret.Location.Ranges[0].Start.Line)

		require.Equal(t, 2, len(outline.SSH))

		ssh := outline.SSH[0]
		require.Equal(t, "default", ssh.Name)
		require.Equal(t, false, ssh.Required)
		require.Equal(t, int32(0), ssh.Location.SourceIndex)
		require.Equal(t, int32(3), ssh.Location.Ranges[0].Start.Line)

		ssh = outline.SSH[1]
		require.Equal(t, "ssh3", ssh.Name)
		require.Equal(t, true, ssh.Required)
		require.Equal(t, int32(0), ssh.Location.SourceIndex)
		require.Equal(t, int32(14), ssh.Location.Ranges[0].Start.Line)

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

func testOutlineDescribeDefinition(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureFrontendOutline)
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

		hasOutline := false

		for _, req := range reqs {
			if req.Name != "frontend.outline" {
				continue
			}
			hasOutline = true
			require.Equal(t, subrequests.RequestType("rpc"), req.Type)
			require.NotEqual(t, req.Version, "")
		}
		require.True(t, hasOutline)

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

func unmarshalOutline(res *gateway.Result) (*outline.Outline, error) {
	dt, ok := res.Metadata["result.json"]
	if !ok {
		return nil, errors.Errorf("missing frontend.outline")
	}
	var o outline.Outline
	if err := json.Unmarshal(dt, &o); err != nil {
		return nil, err
	}
	return &o, nil
}
