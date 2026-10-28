package core

import (
	"context"
	"crypto/rand"
	"strings"
	"time"

	sdkcore "dagger.io/dagger/core"
	"github.com/containerd/platforms"
	"github.com/creack/pty"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ContainerSuite) TestNestedDaggerCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("mounted read-only and appended to PATH", func(ctx context.Context, t *testctx.T) {
		out, err := sdkcore.NewQuery(c).Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", `command -v dagger; echo "$PATH"; touch /dev/.dagger/dagger 2>&1 || true`}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/dev/.dagger/dagger\n"+
			"/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/dev/.dagger\n"+
			"touch: /dev/.dagger/dagger: Read-only file system\n", out)
	})

	t.Run("connects to the engine", func(ctx context.Context, t *testctx.T) {
		out, err := sdkcore.NewQuery(c).Container().
			From(alpineImage).
			WithExec([]string{"dagger", "core", "version"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, strings.TrimSpace(out))
	})

	t.Run("uses the container's session at once", func(ctx context.Context, t *testctx.T) {
		// The session helper starts alongside the command, so a command whose
		// first call reads the container's files or env through the session
		// waits for the helper in the engine.
		ctr := sdkcore.NewQuery(c).Container().
			From(alpineImage).
			WithNewFile("/data/hello.txt", "hello from the container").
			WithEnvVariable("NESTED_SECRET", "from the env").
			WithEnvVariable("ID", rand.Text())
		out, err := ctr.WithExec([]string{"dagger", "-c", "host | file /data/hello.txt | contents"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from the container", strings.TrimSpace(out))
		out, err = ctr.WithExec([]string{"dagger", "-c", "secret env://NESTED_SECRET | plaintext"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "from the env", strings.TrimSpace(out))
	})

	t.Run("container without a shell or libc", func(ctx context.Context, t *testctx.T) {
		out, err := sdkcore.NewQuery(c).Container().
			WithExec([]string{"dagger", "core", "version"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, strings.TrimSpace(out))
	})

	t.Run("emulated container", func(ctx context.Context, t *testctx.T) {
		native, err := sdkcore.NewQuery(c).DefaultPlatform(ctx)
		require.NoError(t, err)
		other := sdkcore.Platform("linux/arm64")
		if platforms.MustParse(string(native)).Architecture == "arm64" {
			other = "linux/amd64"
		}

		ctr := sdkcore.NewQuery(c).Container(sdkcore.ContainerOpts{Platform: other}).From(alpineImage)

		// dagger as the exec's own command
		out, err := ctr.WithExec([]string{"dagger", "core", "version"}).Stdout(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, strings.TrimSpace(out))

		// dagger started by an emulated shell
		out, err = ctr.WithExec([]string{"sh", "-c", "uname -m; dagger core version >/dev/null && echo ok"}).Stdout(ctx)
		require.NoError(t, err)
		uname := map[sdkcore.Platform]string{"linux/arm64": "aarch64", "linux/amd64": "x86_64"}[other]
		require.Equal(t, uname+"\nok\n", out)
	})

	t.Run("new session", func(ctx context.Context, t *testctx.T) {
		// The CLI connects through DAGGER_ENGINE as the main client of its own
		// session.
		out, err := sdkcore.NewQuery(c).Container().
			From(alpineImage).
			WithNewFile("/clients.graphql", `{ engine { clients } }`).
			WithEnvVariable("ID", rand.Text()).
			WithExec([]string{"sh", "-ec", nestedMainClientCheck + `
				command -v dagger
				echo "$PATH"
				isMain "$ID" && echo ok
			`}, sdkcore.ContainerWithExecOpts{DaggerInDaggerNewSession: true}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/dev/.dagger/dagger\n"+
			"/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/dev/.dagger\n"+
			"ok\n", out)
	})

	t.Run("service terminal", func(ctx context.Context, t *testctx.T) {
		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()
		require.NoError(t, pty.Setsize(tty, &pty.Winsize{Rows: 35, Cols: 160}))

		cmd := hostDaggerCommandRaw(ctx, t, t.TempDir(), "-c",
			`container | from `+alpineImage+` | with-new-file /probe.sh --contents='printf "lookup="; command -v dagger || echo absent; echo probe-done' | as-service --args=sleep,60 | terminal --cmd=sh,/probe.sh`)
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty
		require.NoError(t, cmd.Start())

		out, err := console.ExpectString("probe-done\r\n")
		require.NoError(t, err)
		require.Contains(t, out, "lookup=/dev/.dagger/dagger\r\n")

		go console.ExpectEOF()
		require.NoError(t, cmd.Wait())
	})

	t.Run("not mounted without nesting", func(ctx context.Context, t *testctx.T) {
		out, err := sdkcore.NewQuery(c).Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", `echo "$PATH"; test -e /dev/.dagger || echo absent`},
				sdkcore.ContainerWithExecOpts{DisableDaggerInDagger: true}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\nabsent\n", out)
	})
}

func (ContainerSuite) TestNestedDaggerCLIServesChild(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const queryScript = `wget -q -O- \
	--header "Authorization: Basic $(printf '%s:' "$DAGGER_SESSION_TOKEN" | base64)" \
	--header 'Content-Type: application/json' \
	--post-data '{"query":"{ version }"}' \
	"http://127.0.0.1:$DAGGER_SESSION_PORT/query"
`
	// `dagger session` serves until its stdin closes, so the FIFO's writer
	// keeps it up until the child is done.
	const sessionScript = `set -e
mkfifo /tmp/stdin
sleep 300 > /tmp/stdin &
stdin=$!
dagger session < /tmp/stdin > /tmp/params &
session=$!
until [ -s /tmp/params ]; do
	kill -0 "$session"
	sleep 0.1
done
export DAGGER_SESSION_PORT=$(sed -n 's/.*"port":\([0-9]*\).*/\1/p' /tmp/params)
export DAGGER_SESSION_TOKEN=$(sed -n 's/.*"session_token":"\([^"]*\)".*/\1/p' /tmp/params)
rc=0
"$@" || rc=$?
kill "$stdin"
wait "$session"
exit "$rc"
`
	ctr := sdkcore.NewQuery(c).Container().
		From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithNewFile("/query.sh", queryScript).
		WithNewFile("/session.sh", sessionScript).
		WithNewFile("/version.graphql", "{ version }").
		WithNewFile("/host.graphql", `{ host { file(path: "/version.graphql") { contents } } }`).
		WithEnvVariable("ID", rand.Text())

	for _, nesting := range []struct {
		name string
		opts sdkcore.ContainerWithExecOpts
	}{
		{"nested session", sdkcore.ContainerWithExecOpts{}},
		{"new session", sdkcore.ContainerWithExecOpts{DaggerInDaggerNewSession: true}},
	} {
		t.Run(nesting.name, func(ctx context.Context, t *testctx.T) {
			for _, tc := range []struct {
				name string
				args []string
				want string
			}{
				{"dagger run", []string{"dagger", "run", "sh", "/query.sh"}, `{"data":{"version":`},
				{"dagger session", []string{"sh", "/session.sh", "sh", "/query.sh"}, `{"data":{"version":`},
				{
					"dagger run, CLI child",
					[]string{"dagger", "run", "dagger", "query", "--doc", "/version.graphql"},
					`"version":`,
				},
				{
					"dagger session, CLI child",
					[]string{"sh", "/session.sh", "dagger", "query", "--doc", "/version.graphql"},
					`"version":`,
				},
				{
					"with-session, CLI child",
					[]string{"dagger", "api", "with-session", "dagger", "query", "--doc", "/version.graphql"},
					`"version":`,
				},
				{
					// The child reaches the host through its parent.
					"with-session, CLI child reads the host",
					[]string{"dagger", "api", "with-session", "dagger", "query", "--doc", "/host.graphql"},
					"{ version }",
				},
				{
					// A child's shutdown ends only the child: the next child
					// and the parent's own session still work.
					"with-session, CLI children in turn",
					[]string{"dagger", "api", "with-session", "sh", "-ec", `
dagger query --doc /version.graphql
dagger query --doc /version.graphql
sh /query.sh
`},
					`{"data":{"version":`,
				},
				{
					"with-session, CLI grandchild",
					[]string{"dagger", "api", "with-session", "dagger", "api", "with-session", "dagger", "query", "--doc", "/host.graphql"},
					"{ version }",
				},
			} {
				t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
					out, err := ctr.WithExec(tc.args, nesting.opts).Stdout(ctx)
					require.NoError(t, err)
					require.Contains(t, out, tc.want)
				})
			}
		})
	}
}
