package core

// These tests cover same-session secret and socket isolation for modules: a
// module running as a nested client must only resolve session resources it was
// explicitly passed, never ones it merely holds (or forged/guessed) the engine
// result ID of.
//
// Regression test for the report "a module can enumerate result IDs and read a
// caller secret it was never given": the module, the victim secret and the
// attack all run in one session, which is what distinguishes this from the
// already-blocked cross-session case.

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type SessionResourceIsolationSuite struct{}

func TestSessionResourceIsolation(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(SessionResourceIsolationSuite{})
}

// serveIsolationModule serves the session-secret-isolation module into the
// connected client's own session, so its functions run as a nested client in
// that session.
func serveIsolationModule(ctx context.Context, t *testctx.T, c *dagger.Client) {
	t.Helper()
	tmpdir := t.TempDir()
	copyTestdataFixture(ctx, t, tmpdir, "modules", "go", "session-secret-isolation")
	require.NoError(t, core.NewQuery(c).ModuleSource(tmpdir).AsModule().Serve(ctx))
}

func (SessionResourceIsolationSuite) TestModuleCannotStealSecretByID(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	serveIsolationModule(ctx, t, c)

	// The main client creates the victim secret in this session.
	victimID, err := core.NewQuery(c).SetSecret("victim", "SYNTHETIC-ISOLATION-2916").ID(ctx)
	require.NoError(t, err)

	// The attack: hand the module only the secret's ID and ask it to resolve
	// the plaintext. The module was never passed the secret, so it must fail.
	_, err = testutil.QueryWithClient[struct {
		Test struct{ StealFromID string }
	}](c, t, `{test{stealFromID(id:"`+string(victimID)+`")}}`, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "was not granted this resource")
}

func (SessionResourceIsolationSuite) TestModuleCannotStealSocketByID(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	serveIsolationModule(ctx, t, c)

	// A host socket handle bound by the main client. No listener is needed:
	// resolution is refused before any connection is attempted.
	socketID, err := core.NewQuery(c).Host().UnixSocket("/tmp/does-not-matter.sock").ID(ctx)
	require.NoError(t, err)

	_, err = testutil.QueryWithClient[struct {
		Test struct{ StealSocketFromID string }
	}](c, t, `{test{stealSocketFromID(id:"`+string(socketID)+`")}}`, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "was not granted this resource")
}

func (SessionResourceIsolationSuite) TestModuleResolvesSecretPassedAsArgument(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	serveIsolationModule(ctx, t, c)

	secretID, err := core.NewQuery(c).SetSecret("passed", "SYNTHETIC-PASSED-7413").ID(ctx)
	require.NoError(t, err)

	// Passed as a Secret argument, the secret is conveyed to the module, which
	// resolves it normally.
	res, err := testutil.QueryWithClient[struct {
		Test struct{ UseSecret string }
	}](c, t, `{test{useSecret(s:"`+string(secretID)+`")}}`, nil)
	require.NoError(t, err)
	require.Equal(t, "SYNTHETIC-PASSED-7413", res.Test.UseSecret)
}

func (SessionResourceIsolationSuite) TestModuleResolvesSecretNestedInContainerArgument(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	serveIsolationModule(ctx, t, c)

	secretID, err := core.NewQuery(c).SetSecret("nested", "SYNTHETIC-NESTED-5582").ID(ctx)
	require.NoError(t, err)

	// A secret carried inside a Container argument (as a secret env var) is
	// conveyed to the module transitively, so the module can run the container.
	ctrID, err := core.NewQuery(c).
		Container().
		From(alpineImage).
		WithSecretVariable("STOLEN", core.Ref[*core.Secret](core.NewQuery(c), secretID)).
		ID(ctx)
	require.NoError(t, err)

	res, err := testutil.QueryWithClient[struct {
		Test struct{ UseContainer string }
	}](c, t, `{test{useContainer(ctr:"`+string(ctrID)+`")}}`, nil)
	require.NoError(t, err)
	require.Equal(t, "resolved\n", res.Test.UseContainer)
}

func (SessionResourceIsolationSuite) TestCallerResolvesSecretReturnedByModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	serveIsolationModule(ctx, t, c)

	// A secret the module creates and returns is conveyed to the caller, which
	// may resolve it.
	res, err := testutil.QueryWithClient[struct {
		Test struct {
			MakeSecretReturn struct{ ID core.ID }
		}
	}](c, t, `{test{makeSecretReturn(plaintext:"SYNTHETIC-RETURNED-9021"){id}}}`, nil)
	require.NoError(t, err)

	plaintext, err := core.Ref[*core.Secret](core.NewQuery(c), res.Test.MakeSecretReturn.ID).Plaintext(ctx)
	require.NoError(t, err)
	require.Equal(t, "SYNTHETIC-RETURNED-9021", plaintext)
}
