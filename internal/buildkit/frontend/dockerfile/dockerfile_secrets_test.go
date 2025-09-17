package dockerfile

import (
	"strings"
	"testing"
	"time"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/dagger/dagger/buildkit/client"
	"github.com/dagger/dagger/buildkit/frontend/dockerui"
	"github.com/dagger/dagger/buildkit/session"
	"github.com/dagger/dagger/buildkit/session/secrets/secretsprovider"
	"github.com/dagger/dagger/buildkit/util/testutil/integration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

var secretsTests = integration.TestFuncs(
	testSecretFileParams,
	testSecretRequiredWithoutValue,
	testSecretAsEnviron,
	testSecretAsEnvironWithFileMount,
)

func init() {
	allTests = append(allTests, secretsTests...)
}

func testSecretFileParams(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN --mount=type=secret,required=false,mode=741,uid=100,gid=102,target=/mysecret [ "$(stat -c "%u %g %f" /mysecret)" = "100 102 81e1" ]
RUN [ ! -f /mysecret ] # check no stub left behind
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Session: []session.Attachable{secretsprovider.FromMap(map[string][]byte{
			"mysecret": []byte("pw"),
		})},
	}, nil)
	require.NoError(t, err)
}

func testSecretRequiredWithoutValue(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN --mount=type=secret,required,id=mysecret foo
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "secret mysecret: not found")
}

func testSecretAsEnviron(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
ENV SECRET_ENV=foo
RUN --mount=type=secret,id=mysecret,env=SECRET_ENV [ "$SECRET_ENV" == "pw" ] && [ ! -f /run/secrets/mysecret ] || false
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	done := make(chan struct{})
	status := make(chan *client.SolveStatus)
	hasStatus := false

	go func() {
		for st := range status {
			for _, v := range st.Vertexes {
				if strings.Contains(v.Name, "/run/secrets/mysecret") {
					hasStatus = true
					assert.Contains(t, v.Name, `[ "****" == "pw" ] && `)
				}
			}
		}
		close(done)
	}()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Session: []session.Attachable{secretsprovider.FromMap(map[string][]byte{
			"mysecret": []byte("pw"),
		})},
	}, status)
	require.NoError(t, err)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.Fail(t, "timed out waiting for status")
	}

	require.True(t, hasStatus)
}

func testSecretAsEnvironWithFileMount(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN --mount=type=secret,id=mysecret,target=/run/secrets/secret,env=SECRET_ENV [ "$SECRET_ENV" == "pw" ] && [ -f /run/secrets/secret ] || false
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Session: []session.Attachable{secretsprovider.FromMap(map[string][]byte{
			"mysecret": []byte("pw"),
		})},
	}, nil)
	require.NoError(t, err)
}
