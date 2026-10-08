package core

import (
	"context"
	"strings"
	"time"

	sdkcore "dagger.io/dagger/core"
	"github.com/containerd/platforms"
	"github.com/creack/pty"
	"github.com/dagger/dagger/internal/buildkit/identity"
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
			WithEnvVariable("ID", identity.NewID()).
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
