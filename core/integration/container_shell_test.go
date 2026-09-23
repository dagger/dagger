package core

import (
	"context"

	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ContainerSuite) TestShellMetadataIsLazy(ctx context.Context, t *testctx.T) {
	type command struct {
		Args                     []string
		Env                      []struct{ Name, Value string }
		Workdir                  *string
		PrivilegedNesting        bool
		InsecureRootCapabilities bool
	}
	res, err := testutil.Query[struct {
		Container struct {
			From struct {
				WithExec struct {
					WithShell struct {
						Interactive command
						Batch       command
						WithRun     struct{ ID string }
					}
				}
			}
		}
	}](t, `{
		container {
			from(address: "`+alpineImage+`") {
				withExec(args: ["false"]) {
					withShell(interactive: ["sh"], batch: ["sh", "-eu", "-c"], experimentalPrivilegedNesting: true, insecureRootCapabilities: true) {
						interactive: shell { args env { name value } workdir privilegedNesting insecureRootCapabilities }
						batch: shell(batch: true) { args privilegedNesting insecureRootCapabilities }
						withRun(command: "echo should-not-run") { id }
					}
				}
			}
		}
	}`, nil)
	require.NoError(t, err)
	configured := res.Container.From.WithExec.WithShell
	require.Equal(t, []string{"sh"}, configured.Interactive.Args)
	require.Empty(t, configured.Interactive.Env)
	require.Nil(t, configured.Interactive.Workdir)
	require.True(t, configured.Interactive.PrivilegedNesting)
	require.True(t, configured.Interactive.InsecureRootCapabilities)
	require.Equal(t, []string{"sh", "-eu", "-c"}, configured.Batch.Args)
	require.NotEmpty(t, configured.WithRun.ID)
}

func (ContainerSuite) TestWithRun(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Container struct {
			From struct {
				WithShell struct {
					Configured struct{ Stdout string }
					Override   struct{ Stdout string }
				}
			}
		}
	}](t, `{
		container {
			from(address: "`+alpineImage+`") {
				withShell(interactive: ["sh"], batch: ["sh", "-eu", "-c"], experimentalPrivilegedNesting: true) {
					configured: withRun(command: "test -n \"$DAGGER_SESSION_PORT\"; printf '%s' 'two words'") { stdout }
					override: withRun(command: "test -z \"${DAGGER_SESSION_PORT:-}\" && printf '%s' overridden", shell: ["sh", "-c"], experimentalPrivilegedNesting: false) { stdout }
				}
			}
		}
	}`, nil)
	require.NoError(t, err)
	require.Equal(t, "two words", res.Container.From.WithShell.Configured.Stdout)
	require.Equal(t, "overridden", res.Container.From.WithShell.Override.Stdout)
}

func (ContainerSuite) TestShellRejectsEmptyCommands(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct{ query, err string }{
		{`{ container { withShell(interactive: []) { id } } }`, "interactive shell arguments must not be empty"},
		{`{ container { withShell(interactive: ["sh"], batch: []) { id } } }`, "batch shell arguments must not be empty"},
		{`{ container { withRun(command: "echo hello", shell: []) { id } } }`, "shell arguments must not be empty"},
	} {
		t.Run(tc.err, func(ctx context.Context, t *testctx.T) {
			_, err := testutil.Query[any](t, tc.query, nil)
			require.ErrorContains(t, err, tc.err)
		})
	}
}
