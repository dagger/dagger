package core

import (
	"context"
	"strings"

	"github.com/containerd/platforms"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func (ContainerSuite) TestNestedDaggerCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("mounted read-only and appended to PATH", func(ctx context.Context, t *testctx.T) {
		out, err := c.Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", `command -v dagger; echo "$PATH"; touch /dev/.dagger/dagger 2>&1 || true`}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/dev/.dagger/dagger\n"+
			"/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/dev/.dagger\n"+
			"touch: /dev/.dagger/dagger: Read-only file system\n", out)
	})

	t.Run("connects to the engine", func(ctx context.Context, t *testctx.T) {
		out, err := c.Container().
			From(alpineImage).
			WithExec([]string{"dagger", "core", "version"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, strings.TrimSpace(out))
	})

	t.Run("container without a shell or libc", func(ctx context.Context, t *testctx.T) {
		out, err := c.Container().
			WithExec([]string{"dagger", "core", "version"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, strings.TrimSpace(out))
	})

	t.Run("emulated container", func(ctx context.Context, t *testctx.T) {
		native, err := c.DefaultPlatform(ctx)
		require.NoError(t, err)
		other := dagger.Platform("linux/arm64")
		if platforms.MustParse(string(native)).Architecture == "arm64" {
			other = "linux/amd64"
		}

		ctr := c.Container(dagger.ContainerOpts{Platform: other}).From(alpineImage)

		// dagger as the exec's own command
		out, err := ctr.WithExec([]string{"dagger", "core", "version"}).Stdout(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, strings.TrimSpace(out))

		// dagger started by an emulated shell
		out, err = ctr.WithExec([]string{"sh", "-c", "uname -m; dagger core version >/dev/null && echo ok"}).Stdout(ctx)
		require.NoError(t, err)
		uname := map[dagger.Platform]string{"linux/arm64": "aarch64", "linux/amd64": "x86_64"}[other]
		require.Equal(t, uname+"\nok\n", out)
	})

	t.Run("not mounted without nesting", func(ctx context.Context, t *testctx.T) {
		out, err := c.Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", `echo "$PATH"; test -e /dev/.dagger || echo absent`},
				dagger.ContainerWithExecOpts{DisableDaggerInDagger: true}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\nabsent\n", out)
	})
}
