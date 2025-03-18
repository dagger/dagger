package core

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	fs "github.com/tonistiigi/fsutil/copy"
)

// TODO: more tests:
// * no cache match when args of various types are different
// * services (they are stopped when the session is closed atm, right?)
// * idnetical module invocation (in terms of the gql query) but for different modules across sessions doesn't get de-duped

// TODO: add more tests for longer chains of cache hits that then diverge (i.e. constructor + some function cache hit, then diverge)
func (ModuleSuite) TestCrossSessionFunctionCaching(ctx context.Context, t *testctx.T) {
	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		callMod := func(c *dagger.Client) (string, error) {
			return goGitBase(t, c).
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

	import (
		"strconv"
		"time"
	)

	type Test struct {}

	func (*Test) Fn() string {
		return strconv.Itoa(int(time.Now().UnixNano()))
	}
	`,
				).
				WithEnvVariable("CACHEBUSTER", identity.NewID()).
				With(daggerCall("fn")).
				Stdout(ctx)
		}

		c1 := connect(ctx, t)
		out1, err := callMod(c1)
		require.NoError(t, err)

		c2 := connect(ctx, t)
		out2, err := callMod(c2)
		require.NoError(t, err)

		require.Equal(t, out1, out2)
	})

	t.Run("args", func(ctx context.Context, t *testctx.T) {
		callMod := func(c *dagger.Client, i *int, s *string) (string, error) {
			args := []string{"fn"}
			if i != nil {
				args = append(args, "--i", strconv.Itoa(*i))
			}
			if s != nil {
				args = append(args, "--s", *s)
			}
			return goGitBase(t, c).
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

	import (
		"strconv"
		"time"
	)

	type Test struct {}

	func (*Test) Fn(
		// +optional
		i int, 
		// +optional
		s string,
	) string {
		return strconv.Itoa(int(time.Now().UnixNano()))
	}
	`,
				).
				WithEnvVariable("CACHEBUSTER", identity.NewID()).
				With(daggerCall(args...)).
				Stdout(ctx)
		}

		for _, tc := range []struct {
			name           string
			i1             *int
			i2             *int
			s1             *string
			s2             *string
			expectCacheHit bool
		}{
			{
				name:           "unset",
				expectCacheHit: true,
			},
			{
				name:           "same",
				i1:             ptr(1),
				i2:             ptr(1),
				s1:             ptr("foo"),
				s2:             ptr("foo"),
				expectCacheHit: true,
			},
			{
				name:           "all different",
				i1:             ptr(1),
				i2:             ptr(2),
				s1:             ptr("foo"),
				s2:             ptr("bar"),
				expectCacheHit: false,
			},
			{
				name:           "some different",
				i1:             ptr(1),
				i2:             ptr(1),
				s1:             ptr("foo"),
				s2:             ptr("bar"),
				expectCacheHit: false,
			},
		} {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				out1, err := callMod(c1, tc.i1, tc.s1)
				require.NoError(t, err)

				c2 := connect(ctx, t)
				out2, err := callMod(c2, tc.i2, tc.s2)
				require.NoError(t, err)

				if tc.expectCacheHit {
					require.Equal(t, out1, out2)
				} else {
					require.NotEqual(t, out1, out2)
				}
			})
		}
	})

	t.Run("same schema but different implementations", func(ctx context.Context, t *testctx.T) {
		// right now calls are cached by module source digest via the `asModule` custom cache key plus
		// the fact that IDs include the module InstanceID, verify that behavior works as expected
		callMod := func(c *dagger.Client, t *testctx.T, x string) (string, error) {
			return goGitBase(t, c).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main
type Test struct {}

func (t *Test) Fn() string {
	return "`+x+`"
}
`).
				WithEnvVariable("CACHEBUSTER", identity.NewID()).
				With(daggerCall("fn")).
				Stdout(ctx)
		}

		c1 := connect(ctx, t)
		out1, err := callMod(c1, t, "1")
		require.NoError(t, err)
		require.Equal(t, "1", out1)

		c2 := connect(ctx, t)
		out2, err := callMod(c2, t, "2")
		require.NoError(t, err)
		require.Equal(t, "2", out2)
	})

	t.Run("same source different clients and first disconnects", func(ctx context.Context, t *testctx.T) {
		tmpdir1 := t.TempDir()

		depTmpdir1 := filepath.Join(tmpdir1, "dep")
		err := os.MkdirAll(depTmpdir1, 0755)
		require.NoError(t, err)

		initDepCmd := hostDaggerCommand(ctx, t, depTmpdir1, "init", "--source=.", "--name=dep", "--sdk=go")
		initDepOutput, err := initDepCmd.CombinedOutput()
		require.NoError(t, err, string(initDepOutput))
		err = os.WriteFile(filepath.Join(depTmpdir1, "main.go"), []byte(`package main
import (
	"strconv"
	"time"
)

type Dep struct {}

func (*Dep) Fn(rand string) string {
	return strconv.Itoa(int(time.Now().UnixNano()))
}
`), 0644)
		require.NoError(t, err)

		initCmd := hostDaggerCommand(ctx, t, tmpdir1, "init", "--source=.", "--name=test", "--sdk=go")
		initOutput, err := initCmd.CombinedOutput()
		require.NoError(t, err, string(initOutput))
		installCmd := hostDaggerCommand(ctx, t, tmpdir1, "install", depTmpdir1)
		installOutput, err := installCmd.CombinedOutput()
		require.NoError(t, err, string(installOutput))

		err = os.WriteFile(filepath.Join(tmpdir1, "main.go"), []byte(`package main
import (
	"context"
)

type Test struct {}

func (*Test) Fn(ctx context.Context, rand string) (string, error) {
	return dag.Dep().Fn(ctx, rand)
}
`), 0644)
		require.NoError(t, err)

		tmpdir2 := t.TempDir()
		err = fs.Copy(ctx, tmpdir1, "/", tmpdir2, "/")
		require.NoError(t, err)

		c1 := connect(ctx, t)
		mod1, err := c1.ModuleSource(tmpdir1).AsModule().Sync(ctx)
		require.NoError(t, err)
		modID1, err := mod1.ID(ctx)
		require.NoError(t, err)

		c2 := connect(ctx, t)
		mod2, err := c2.ModuleSource(tmpdir2).AsModule().Sync(ctx)
		require.NoError(t, err)
		modID2, err := mod2.ID(ctx)
		require.NoError(t, err)

		require.NotEqual(t, modID1, modID2)

		rand := identity.NewID()

		err = mod1.Serve(ctx)
		require.NoError(t, err)
		res1, err := testutil.QueryWithClient[struct {
			Test struct {
				Fn string
			}
		}](c1, t, `{test{fn(rand: "`+rand+`")}}`, nil)
		require.NoError(t, err)
		require.NotEmpty(t, res1.Test.Fn)

		err = mod2.Serve(ctx)
		require.NoError(t, err)

		res2A, err := testutil.QueryWithClient[struct {
			Test struct {
				Fn string
			}
		}](c2, t, `{test{fn(rand: "`+rand+`")}}`, nil)
		require.NoError(t, err)
		require.NotEmpty(t, res2A.Test.Fn)

		require.Equal(t, res1.Test.Fn, res2A.Test.Fn)

		require.NoError(t, c1.Close())
		err = os.RemoveAll(tmpdir1)
		require.NoError(t, err)

		rand = identity.NewID()
		res2B, err := testutil.QueryWithClient[struct {
			Test struct {
				Fn string
			}
		}](c2, t, `{test{fn(rand: "`+rand+`")}}`, nil)
		require.NoError(t, err)
		require.NotEmpty(t, res2B.Test.Fn)

		require.NotEqual(t, res1.Test.Fn, res2B.Test.Fn)
	})
}

func ptr[T any](v T) *T {
	return &v
}

func (ModuleSuite) TestCrossSessionServices(ctx context.Context, t *testctx.T) {
	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		callMod := func(c *dagger.Client, rand string) (string, error) {
			return goGitBase(t, c).
				WithWorkdir("/work/servicer").
				With(daggerExec("init", "--name=servicer", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

	import (
		"dagger/servicer/internal/dagger"
	)

	type Servicer struct {}

	func (*Servicer) EchoSvc() *dagger.Service {
		return dag.Container().
			From("alpine:3.20").
			WithExec([]string{"apk", "add", "socat"}).
			WithExposedPort(1234).
			// echo server, writes what it reads
			WithDefaultArgs([]string{"socat", "tcp-l:1234,fork", "exec:/bin/cat"}).
			AsService()
	}
	`,
				).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

	import (
		"context"
	)

	type Test struct {}

	func (*Test) Fn(ctx context.Context, rand string) (string, error) {
		return dag.Container().
			From("alpine:3.20").
			WithExec([]string{"apk", "add", "netcat-openbsd"}).
			WithServiceBinding("echoer", dag.Servicer().EchoSvc()).
			WithEnvVariable("CACHEBUSTER", rand).
			WithExec([]string{"sh", "-c", "echo -n $CACHEBUSTER | nc -N echoer 1234"}).
			Stdout(ctx)
	}
	`,
				).
				With(daggerExec("install", "/work/servicer")).
				With(daggerCall("fn", "--rand", rand)).
				Stdout(ctx)
		}

		c1 := connect(ctx, t)
		rand1 := identity.NewID()
		out1, err := callMod(c1, rand1)
		require.NoError(t, err)
		require.Equal(t, rand1, out1)

		c2 := connect(ctx, t)
		rand2 := identity.NewID()
		out2, err := callMod(c2, rand2)
		require.NoError(t, err)
		require.Equal(t, rand2, out2)

		require.NotEqual(t, out1, out2)

		require.NoError(t, c1.Close())
		// TODO: not sure if we can do much better than this...
		// time.Sleep(15 * time.Second)
		time.Sleep(5 * time.Second)

		c3 := connect(ctx, t)
		rand3 := identity.NewID()
		out3, err := callMod(c3, rand3)
		require.NoError(t, err)
		require.Equal(t, rand3, out3)

		require.NotEqual(t, out1, out3)
		require.NotEqual(t, out2, out3)
	})

	/* TODO:
	t.Run("nested", func(ctx context.Context, t *testctx.T) {
		callMod := func(c *dagger.Client, rand string) (string, error) {
			return goGitBase(t, c).
				WithWorkdir("/work/servicer").
				With(daggerExec("init", "--name=servicer", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

	import (
		"dagger/servicer/internal/dagger"
	)

	type Servicer struct {}

	func (*Servicer) EchoSvc() *dagger.Service {
		return dag.Container().
			From("alpine:3.20").
			WithExec([]string{"apk", "add", "socat"}).
			WithExposedPort(1234).
			// echo server, writes what it reads
			WithDefaultArgs([]string{"socat", "tcp-l:1234,fork", "exec:/bin/cat"}).
			AsService()
	}
	`,
				).
				WithWorkdir("/work/caller").
				With(daggerExec("init", "--name=caller", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

	import (
		"context"
		"strconv"
		"time"
	)

	type Caller struct {}

	func (*Caller) CallSvc(ctx context.Context) (string, error) {
		rand := strconv.Itoa(int(time.Now().UnixNano()))
		return dag.Container().
			From("alpine:3.20").
			WithExec([]string{"apk", "add", "netcat-openbsd"}).
			WithServiceBinding("echoer", dag.Servicer().EchoSvc()).
			WithEnvVariable("CACHEBUSTER", rand).
			WithExec([]string{"sh", "-c", "echo -n $CACHEBUSTER | nc -N echoer 1234"}).
			Stdout(ctx)
	}
	`,
				).
				With(daggerExec("install", "/work/servicer")).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

	import (
		"context"
	)

	type Test struct {}

	func (*Test) Fn(ctx context.Context, rand string) (string, error) {
		return dag.Caller().CallSvc(ctx, rand)
	}
	`,
				).
				With(daggerExec("install", "/work/caller")).
				With(daggerCall("fn", "--rand", rand)).
				Stdout(ctx)
		}

		c1 := connect(ctx, t)
		rand1 := identity.NewID()
		out1, err := callMod(c1, rand1)
		require.NoError(t, err)
		require.Equal(t, rand1, out1)

		c2 := connect(ctx, t)
		rand2 := identity.NewID()
		out2, err := callMod(c2, rand2)
		require.NoError(t, err)
		require.Equal(t, rand2, out2)

		require.NotEqual(t, out1, out2)

		require.NoError(t, c1.Close())
		// TODO: not sure if we can do much better than this...
		time.Sleep(15 * time.Second)

		c3 := connect(ctx, t)
		rand3 := identity.NewID()
		out3, err := callMod(c3, rand3)
		require.NoError(t, err)
		require.Equal(t, rand3, out3)

		require.NotEqual(t, out1, out3)
		require.NotEqual(t, out2, out3)
	})
	*/
}

func (SecretSuite) TestCrossSessionGitAuthLeak(ctx context.Context, t *testctx.T) {
	t.Run("core git", func(ctx context.Context, t *testctx.T) {
		authTokenTestCase := getVCSTestCase(t, "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")
		require.NotEmpty(t, authTokenTestCase.token)

		sshTestCase := getVCSTestCase(t, "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")
		require.True(t, sshTestCase.sshKey)

		runTest := func(ctx context.Context, t *testctx.T, testCase vcsTestCase, expectedErr string) {
			var err error

			// sanity test fail when no auth given
			c1 := connect(ctx, t)
			_, err = goGitBase(t, c1).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(daggerExec("core",
					"git",
					"--url", testCase.gitTestRepoRef,
					"head",
					"tree",
				)).
				Sync(ctx)
			requireErrOut(t, err, expectedErr)

			// pull the git repo with auth, get it into the cache
			c2 := connect(ctx, t)
			withRepo, withRepoCleanup := privateRepoSetup(c2, t, testCase)
			t.Cleanup(withRepoCleanup)
			_, err = goGitBase(t, c2).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(withRepo).
				With(daggerExec("core",
					"git",
					"--url", testCase.gitTestRepoRef,
					"head",
					"tree",
				)).
				Sync(ctx)
			require.NoError(t, err)

			// try again with no auth, should fail
			c3 := connect(ctx, t)
			_, err = goGitBase(t, c3).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(daggerExec("core",
					"git",
					"--url", testCase.gitTestRepoRef,
					"head",
					"tree",
				)).
				Sync(ctx)
			requireErrOut(t, err, expectedErr)

			// try again on same session but with auth, should succeed now
			withRepo, withRepoCleanup = privateRepoSetup(c3, t, testCase)
			t.Cleanup(withRepoCleanup)
			_, err = goGitBase(t, c3).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(withRepo).
				With(daggerExec("core",
					"git",
					"--url", testCase.gitTestRepoRef,
					"head",
					"tree",
				)).
				Sync(ctx)
			require.NoError(t, err)
		}

		t.Run("auth token", func(ctx context.Context, t *testctx.T) {
			runTest(ctx, t, authTokenTestCase, "Failed to retrieve credentials")
		})
		t.Run("ssh key", func(ctx context.Context, t *testctx.T) {
			runTest(ctx, t, sshTestCase, "SSH URLs are not supported without an SSH socket")
		})
	})

	t.Run("git module source", func(ctx context.Context, t *testctx.T) {
		authTokenTestCase := getVCSTestCase(t, "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")
		require.NotEmpty(t, authTokenTestCase.token)

		sshTestCase := getVCSTestCase(t, "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")
		require.True(t, sshTestCase.sshKey)

		runTest := func(ctx context.Context, t *testctx.T, testCase vcsTestCase, expectedErr string) {
			var err error

			// sanity test fail when no auth given
			c1 := connect(ctx, t)
			_, err = goGitBase(t, c1).
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(daggerExec("install", testGitModuleRef(testCase, "top-level"))).
				Sync(ctx)
			requireErrOut(t, err, expectedErr)

			// pull the git repo with auth, get it into the cache
			c2 := connect(ctx, t)
			withRepo, withRepoCleanup := privateRepoSetup(c2, t, testCase)
			t.Cleanup(withRepoCleanup)
			_, err = goGitBase(t, c2).
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(withRepo).
				With(daggerExec("install", testGitModuleRef(testCase, "top-level"))).
				Sync(ctx)
			require.NoError(t, err)

			// try again with no auth, should fail
			c3 := connect(ctx, t)
			_, err = goGitBase(t, c3).
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(daggerExec("install", testGitModuleRef(testCase, "top-level"))).
				Sync(ctx)
			requireErrOut(t, err, expectedErr)

			// try again on same session but with auth, should succeed now
			withRepo, withRepoCleanup = privateRepoSetup(c3, t, testCase)
			t.Cleanup(withRepoCleanup)
			_, err = goGitBase(t, c3).
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(withRepo).
				With(daggerExec("install", testGitModuleRef(testCase, "top-level"))).
				Sync(ctx)
			require.NoError(t, err)
		}

		t.Run("auth token", func(ctx context.Context, t *testctx.T) {
			runTest(ctx, t, authTokenTestCase, "Failed to retrieve credentials")
		})
		t.Run("ssh key", func(ctx context.Context, t *testctx.T) {
			runTest(ctx, t, sshTestCase, "SSH URLs are not supported without an SSH socket")
		})
	})
}

// TODO: more tests:
// * equivalent for sockets
// * use secret URIs
func (ModuleSuite) TestCrossSessionSecrets(ctx context.Context, t *testctx.T) {
	// verify that if a function call does SetSecret and is cached, the secret is
	// successfully transferred to clients even if they are in a different session
	t.Run("cached set-secret transfers", func(ctx context.Context, t *testctx.T) {
		callMod := func(c *dagger.Client) (string, error) {
			return goGitBase(t, c).
				With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"strconv"
	"time"

	"dagger/secreter/internal/dagger"
)

type Secreter struct {}

func (_ *Secreter) Make() *dagger.Secret {
	return dag.SetSecret("FOO", strconv.Itoa(int(time.Now().UnixNano())))
}
`,
				).
				WithEnvVariable("CACHEBUSTER", identity.NewID()).
				With(daggerCall("make", "plaintext")).
				Stdout(ctx)
		}

		c1 := connect(ctx, t)
		out1, err := callMod(c1)
		require.NoError(t, err)

		c2 := connect(ctx, t)
		out2, err := callMod(c2)
		require.NoError(t, err)

		require.Equal(t, out1, out2)

		// close original client, ensure secret is still available
		require.NoError(t, c1.Close())

		c3 := connect(ctx, t)
		out3, err := callMod(c3)
		require.NoError(t, err)

		require.Equal(t, out1, out3)
		require.Equal(t, out2, out3)
	})

	t.Run("different secrets with same name do not cache", func(ctx context.Context, t *testctx.T) {
		callMod := func(c *dagger.Client, val string) (string, error) {
			return goGitBase(t, c).
				WithWorkdir("/work/secreter").
				With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"dagger/secreter/internal/dagger"
)

type Secreter struct {}

func (*Secreter) GiveBack(s *dagger.Secret) *dagger.Secret {
	return s
}
`,
				).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=caller", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./secreter")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Caller struct {}

func (*Caller) Fn(ctx context.Context, val string) (string, error) {
	return dag.Secreter().GiveBack(dag.SetSecret("FOO", val)).Plaintext(ctx)
}
`,
				).
				WithEnvVariable("CACHEBUSTER", identity.NewID()).
				With(daggerCall("fn", "--val", val)).
				Stdout(ctx)
		}

		c1 := connect(ctx, t)
		randVal1 := identity.NewID()
		out1, err := callMod(c1, randVal1)
		require.NoError(t, err)
		require.Equal(t, randVal1, out1)

		c2 := connect(ctx, t)
		randVal2 := identity.NewID()
		out2, err := callMod(c2, randVal2)
		require.NoError(t, err)
		require.Equal(t, randVal2, out2)

		require.NotEqual(t, out1, out2)
	})
}
