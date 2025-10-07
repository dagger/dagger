package core

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"dagger.io/dagger"
	fscopy "github.com/dagger/dagger/engine/filesync/copy"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

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
		callMod := func(c *dagger.Client, b bool, i *int, s *string) (string, error) {
			args := []string{"fn"}
			if b {
				args = append([]string{"--b"}, args...)
			}
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

	type Test struct {
		ArbitraryBool bool
	}

	func New(
		// +optional
		b bool,
	) Test {
		return Test{ArbitraryBool: b}
	}

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
			b1             bool
			b2             bool
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
				name:           "unset but diff parent",
				b2:             true,
				expectCacheHit: false,
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
				name:           "same but diff parent",
				b1:             true,
				i1:             ptr(1),
				i2:             ptr(1),
				s1:             ptr("foo"),
				s2:             ptr("foo"),
				expectCacheHit: false,
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
				out1, err := callMod(c1, tc.b1, tc.i1, tc.s1)
				require.NoError(t, err)

				c2 := connect(ctx, t)
				out2, err := callMod(c2, tc.b2, tc.i2, tc.s2)
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
		// the fact that IDs include the module ResultID, verify that behavior works as expected
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
		err = fscopy.Copy(ctx, tmpdir1, "/", tmpdir2, "/")
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

		c3 := connect(ctx, t)
		rand3 := identity.NewID()
		out3, err := callMod(c3, rand3)
		require.NoError(t, err)
		require.Equal(t, rand3, out3)

		require.NotEqual(t, out1, out3)
		require.NotEqual(t, out2, out3)
	})
}

func (SecretSuite) TestCrossSessionGitAuthLeak(ctx context.Context, t *testctx.T) {
	t.Run("core git", func(ctx context.Context, t *testctx.T) {
		authTokenTestCase := getVCSTestCase(t, "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")
		require.NotEmpty(t, authTokenTestCase.encodedToken)

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
			runTest(ctx, t, authTokenTestCase, "Authentication failed")
		})
		t.Run("ssh key", func(ctx context.Context, t *testctx.T) {
			runTest(ctx, t, sshTestCase, "SSH URLs are not supported without an SSH socket")
		})
	})

	t.Run("git module source", func(ctx context.Context, t *testctx.T) {
		authTokenTestCase := getVCSTestCase(t, "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")
		require.NotEmpty(t, authTokenTestCase.encodedToken)

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
			runTest(ctx, t, authTokenTestCase, "Authentication failed")
		})
		t.Run("ssh key", func(ctx context.Context, t *testctx.T) {
			runTest(ctx, t, sshTestCase, "SSH URLs are not supported without an SSH socket")
		})
	})
}

func (ModuleSuite) TestCrossSessionSockets(ctx context.Context, t *testctx.T) {
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "test.sock")

	l, err := net.Listen("unix", sock)
	require.NoError(t, err)

	defer l.Close()

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Logf("accept: %s", err)
					panic(err)
				}
				return
			}

			n, err := io.Copy(c, c)
			if err != nil {
				t.Logf("hello: %s", err)
				panic(err)
			}

			t.Logf("copied %d bytes", n)

			err = c.Close()
			if err != nil {
				t.Logf("close: %s", err)
				panic(err)
			}
		}
	}()

	modTmpdir := filepath.Join(tmp, "mod")
	err = os.MkdirAll(modTmpdir, 0755)
	require.NoError(t, err)

	initModCmd := hostDaggerCommand(ctx, t, modTmpdir, "init", "--source=.", "--name=test", "--sdk=go")
	initModOutput, err := initModCmd.CombinedOutput()
	require.NoError(t, err, string(initModOutput))

	err = os.WriteFile(filepath.Join(modTmpdir, "main.go"), []byte(`package main
import (
	"context"
	"strconv"
	"time"

	"dagger/test/internal/dagger"
)

type Test struct {}

func (*Test) Fn(ctx context.Context, sock *dagger.Socket, msg string) (string, error) {
		return dag.Container().
			From("alpine:3.20").
			WithExec([]string{"apk", "add", "netcat-openbsd"}).
			WithEnvVariable("BUSTA", strconv.Itoa(int(time.Now().UnixNano()))).
			WithUnixSocket("/foo.sock", sock).
			WithExec([]string{"sh", "-c", "echo -n "+msg+" | nc -N -U /foo.sock"}).
			Stdout(ctx)
}
`), 0644)
	require.NoError(t, err)

	c1 := connect(ctx, t)
	err = c1.ModuleSource(modTmpdir).AsModule().Serve(ctx)
	require.NoError(t, err)
	sockID1, err := c1.Host().UnixSocket(sock).ID(ctx)
	require.NoError(t, err)
	res1, err := testutil.QueryWithClient[struct {
		Test struct {
			Fn string
		}
	}](c1, t, `{test{fn(sock: "`+string(sockID1)+`", msg: "blah")}}`, nil)
	require.NoError(t, err)
	require.Equal(t, "blah", res1.Test.Fn)

	c2 := connect(ctx, t)
	err = c2.ModuleSource(modTmpdir).AsModule().Serve(ctx)
	require.NoError(t, err)
	sockID2, err := c2.Host().UnixSocket(sock).ID(ctx)
	require.NoError(t, err)
	res2, err := testutil.QueryWithClient[struct {
		Test struct {
			Fn string
		}
	}](c2, t, `{test{fn(sock: "`+string(sockID2)+`", msg: "blah")}}`, nil)
	require.NoError(t, err)
	require.Equal(t, "blah", res2.Test.Fn)

	require.NoError(t, c1.Close())

	res2b, err := testutil.QueryWithClient[struct {
		Test struct {
			Fn string
		}
	}](c2, t, `{test{fn(sock: "`+string(sockID2)+`", msg: "omg")}}`, nil)
	require.NoError(t, err)
	require.Equal(t, "omg", res2b.Test.Fn)
}

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

func (*Caller) Fn(ctx context.Context) (string, error) {
	return dag.Secreter().GiveBack(dag.SetSecret("FOO", "`+val+`")).Plaintext(ctx)
}
`,
				).
				With(daggerCall("fn")).
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

	t.Run("secret uris", func(ctx context.Context, t *testctx.T) {
		tmpdir := t.TempDir()
		initCmd := hostDaggerCommand(ctx, t, tmpdir, "init", "--source=.", "--name=test", "--sdk=go")
		initOutput, err := initCmd.CombinedOutput()
		require.NoError(t, err, string(initOutput))
		err = os.WriteFile(filepath.Join(tmpdir, "main.go"), []byte(`package main
import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {}

func (*Test) Fn(ctx context.Context, secret *dagger.Secret) (*dagger.Container, error) {
	return dag.Container().From("alpine:3.20").
		WithSecretVariable("TOPSECRET", secret).
		Sync(ctx)
}
`), 0644)
		require.NoError(t, err)

		c1 := connect(ctx, t)
		err = c1.ModuleSource(tmpdir).AsModule().Serve(ctx)
		require.NoError(t, err)

		secretID1, err := c1.Secret("cmd://echo -n foo").ID(ctx)
		require.NoError(t, err)

		res1, err := testutil.QueryWithClient[struct {
			Test struct {
				Fn struct {
					ID dagger.ContainerID
				}
			}
		}](c1, t, `{test{fn(secret:"`+string(secretID1)+`"){id}}}`, nil)
		require.NoError(t, err)
		ctrID1 := res1.Test.Fn.ID
		require.NotEmpty(t, ctrID1)
		_, err = c1.LoadContainerFromID(ctrID1).
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithExec([]string{"true"}).
			Stdout(ctx)
		require.NoError(t, err)

		c2 := connect(ctx, t)
		err = c2.ModuleSource(tmpdir).AsModule().Serve(ctx)
		require.NoError(t, err)

		secretID2, err := c2.Secret("cmd://echo -n foo").ID(ctx)
		require.NoError(t, err)

		res2, err := testutil.QueryWithClient[struct {
			Test struct {
				Fn struct {
					ID dagger.ContainerID
				}
			}
		}](c2, t, `{test{fn(secret:"`+string(secretID2)+`"){id}}}`, nil)
		require.NoError(t, err)
		ctrID2 := res2.Test.Fn.ID
		require.NotEmpty(t, ctrID2)

		require.NoError(t, c1.Close())

		_, err = c2.LoadContainerFromID(ctrID2).
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithExec([]string{"true"}).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("secret uri plaintext", func(ctx context.Context, t *testctx.T) {
		callMod := func(c *dagger.Client, val string) (string, error) {
			return goGitBase(t, c).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"context"
	"fmt"

	"dagger/secreter/internal/dagger"
)

type Secreter struct {}

func (*Secreter) CheckPlaintext(ctx context.Context, s *dagger.Secret, expected string) error {
	plaintext, err := s.Plaintext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get plaintext: %w", err)
	}
	if plaintext != expected {
		return fmt.Errorf("expected %q, got %q", expected, plaintext)
	}
	return nil
}
`,
				).
				WithEnvVariable("DASECRET", val).
				With(daggerCall(
					"check-plaintext",
					"--s", "env://DASECRET",
					"--expected", val,
				)).
				Stdout(ctx)
		}

		c1 := connect(ctx, t)
		randVal1 := identity.NewID()
		_, err := callMod(c1, randVal1)
		require.NoError(t, err)

		c2 := connect(ctx, t)
		randVal2 := identity.NewID()
		_, err = callMod(c2, randVal2)
		require.NoError(t, err)
	})

	t.Run("contenthash cache hit with secrets", func(ctx context.Context, t *testctx.T) {
		authTokenTestCase := getVCSTestCase(t, "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")
		require.NotEmpty(t, authTokenTestCase.encodedToken)

		callMod := func(c *dagger.Client, cacheBust string) (string, error) {
			return goGitBase(t, c).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"dagger/secreter/internal/dagger"
)

type Secreter struct {}

func (*Secreter) Fn(cacheBust string, tokenPlaintext string) *dagger.Container {
	authSecret := dag.SetSecret("GIT_AUTH", tokenPlaintext)
	gitRepo := dag.Git("https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private", dagger.GitOpts{HTTPAuthToken: authSecret}).
		Branch("main").
		Tree()

	return dag.Container().From("alpine:3.20").
		WithEnvVariable("CACHEBUST", cacheBust).
		WithMountedDirectory("/src", gitRepo).
		WithExec([]string{"true"})
}
`,
				).
				With(daggerCall(
					"fn",
					"--cache-bust", cacheBust,
					"--token-plaintext", authTokenTestCase.token(),
					"stdout",
				)).
				Stdout(ctx)
		}

		c1 := connect(ctx, t)
		randVal1 := identity.NewID()
		_, err := callMod(c1, randVal1)
		require.NoError(t, err)

		c2 := connect(ctx, t)
		randVal2 := identity.NewID()
		_, err = callMod(c2, randVal2)
		require.NoError(t, err)
	})
}

func (LLMSuite) TestCrossSessionLLM(ctx context.Context, t *testctx.T) {
	// verify that llm settings read from clients don't cache across sessions
	c1 := connect(ctx, t)
	out, err := goGitBase(t, c1).
		WithEnvVariable("ANTHROPIC_MODEL", "claude-3-5-sonnet-latest").
		With(daggerExec("core", "llm", "model")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "claude-3-5-sonnet-latest", out)

	c2 := connect(ctx, t)
	out, err = goGitBase(t, c2).
		WithEnvVariable("OPENAI_MODEL", "gpt-4.1").
		With(daggerExec("core", "llm", "model")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "gpt-4.1", out)
}

func (ModuleSuite) TestCrossSessionContextualDirWithPrivate(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()

	initCmd := hostDaggerCommand(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
	initOutput, err := initCmd.CombinedOutput()
	require.NoError(t, err, string(initOutput))

	require.NoError(t, os.MkdirAll(filepath.Join(modDir, "crap"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "crap", "foo.txt"), []byte(identity.NewID()), 0644))

	err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main
import (
	"context"
	"dagger/test/internal/dagger"
)
type Test struct {
	Obj *Obj
}
func New(
	// +defaultPath="/crap"
	dir *dagger.Directory,
) *Test {
	return &Test{Obj: &Obj{Dir: dir}}
}
func (*Test) Nop(ctx context.Context) (string, error) {
	return "nop", nil
}
func (*Test) Nop2(ctx context.Context) (string, error) {
	return "nop2", nil
}
type Obj struct {
	// +private
	Dir *dagger.Directory
}
func (o *Obj) Ents(ctx context.Context) ([]string, error) {
	return o.Dir.Entries(ctx)
}
`), 0644)
	require.NoError(t, err)

	c1 := connect(ctx, t)
	mod1, err := c1.ModuleSource(modDir).AsModule().Sync(ctx)
	require.NoError(t, err)
	err = mod1.Serve(ctx)
	require.NoError(t, err)

	c2 := connect(ctx, t)
	mod2, err := c2.ModuleSource(modDir).AsModule().Sync(ctx)
	require.NoError(t, err)
	err = mod2.Serve(ctx)
	require.NoError(t, err)

	res1, err := testutil.QueryWithClient[struct {
		Test struct {
			Nop string
		}
	}](c1, t, `{test{nop}}`, nil)
	require.NoError(t, err)
	require.Equal(t, "nop", res1.Test.Nop)

	res2, err := testutil.QueryWithClient[struct {
		Test struct {
			Nop2 string
		}
	}](c2, t, `{test{nop2}}`, nil)
	require.NoError(t, err)
	require.Equal(t, "nop2", res2.Test.Nop2)

	require.NoError(t, c1.Close())
	time.Sleep(1 * time.Second)

	res3, err := testutil.QueryWithClient[struct {
		Test struct {
			Obj struct {
				Ents []string
			}
		}
	}](c2, t, `{test{obj{ents}}}`, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res3.Test.Obj.Ents)
}

func (ModuleSuite) TestCrossSessionContextualDirChange(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()

	initCmd := hostDaggerCommand(ctx, t, modDir, "init", "--source=src", "--name=test", "--sdk=go")
	initOutput, err := initCmd.CombinedOutput()
	require.NoError(t, err, string(initOutput))

	require.NoError(t, os.MkdirAll(filepath.Join(modDir, "crap"), 0755))
	rand1 := identity.NewID()
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "crap", "foo.txt"), []byte(rand1), 0644))

	err = os.WriteFile(filepath.Join(modDir, "src", "main.go"), []byte(`package main
import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {
	Obj *Obj
}

func New(
	// +defaultPath="/crap"
	dir *dagger.Directory,
) *Test {
	return &Test{Obj: &Obj{Dir: dir}}
}

type Obj struct {
	Dir *dagger.Directory
}

func (o *Obj) Foo(ctx context.Context) (string, error) {
	return o.Dir.File("foo.txt").Contents(ctx)
}
`), 0644)
	require.NoError(t, err)

	c1 := connect(ctx, t)
	mod1, err := c1.ModuleSource(modDir).AsModule().Sync(ctx)
	require.NoError(t, err)
	err = mod1.Serve(ctx)
	require.NoError(t, err)

	res1, err := testutil.QueryWithClient[struct {
		Test struct {
			Obj struct {
				Foo string
			}
		}
	}](c1, t, `{test{obj{foo}}}`, nil)
	require.NoError(t, err)
	require.Equal(t, rand1, res1.Test.Obj.Foo)

	rand2 := identity.NewID()
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "crap", "foo.txt"), []byte(rand2), 0644))

	c2 := connect(ctx, t)
	mod2, err := c2.ModuleSource(modDir).AsModule().Sync(ctx)
	require.NoError(t, err)
	err = mod2.Serve(ctx)
	require.NoError(t, err)

	res2, err := testutil.QueryWithClient[struct {
		Test struct {
			Obj struct {
				Foo string
			}
		}
	}](c2, t, `{test{obj{foo}}}`, nil)
	require.NoError(t, err)
	require.Equal(t, rand2, res2.Test.Obj.Foo)
}

func (ModuleSuite) TestCrossSessionContextualDirCacheHit(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()

	initCmd := hostDaggerCommand(ctx, t, modDir, "init", "--source=src", "--name=test", "--sdk=go")
	initOutput, err := initCmd.CombinedOutput()
	require.NoError(t, err, string(initOutput))

	require.NoError(t, os.MkdirAll(filepath.Join(modDir, "crap"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "crap", "foo.txt"), []byte(identity.NewID()), 0644))

	err = os.WriteFile(filepath.Join(modDir, "src", "main.go"), []byte(`package main
import (
	"strconv"
	"time"

	"dagger/test/internal/dagger"
)

type Test struct {}

func (*Test) Rand(
	// +defaultPath="/crap"
	dir *dagger.Directory,
) string {
	return strconv.Itoa(int(time.Now().UnixNano()))
}
`), 0644)
	require.NoError(t, err)

	c1 := connect(ctx, t)
	mod1, err := c1.ModuleSource(modDir).AsModule().Sync(ctx)
	require.NoError(t, err)
	err = mod1.Serve(ctx)
	require.NoError(t, err)

	res1, err := testutil.QueryWithClient[struct {
		Test struct {
			Rand string
		}
	}](c1, t, `{test{rand}}`, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res1.Test.Rand)

	c2 := connect(ctx, t)
	mod2, err := c2.ModuleSource(modDir).AsModule().Sync(ctx)
	require.NoError(t, err)
	err = mod2.Serve(ctx)
	require.NoError(t, err)

	res2, err := testutil.QueryWithClient[struct {
		Test struct {
			Rand string
		}
	}](c2, t, `{test{rand}}`, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res2.Test.Rand)

	require.Equal(t, res1.Test.Rand, res2.Test.Rand)

	require.NoError(t, os.WriteFile(filepath.Join(modDir, "crap", "foo.txt"), []byte(identity.NewID()), 0644))

	c3 := connect(ctx, t)
	mod3, err := c3.ModuleSource(modDir).AsModule().Sync(ctx)
	require.NoError(t, err)
	err = mod3.Serve(ctx)
	require.NoError(t, err)

	res3, err := testutil.QueryWithClient[struct {
		Test struct {
			Rand string
		}
	}](c3, t, `{test{rand}}`, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res3.Test.Rand)

	require.NotEqual(t, res1.Test.Rand, res3.Test.Rand)
}

func (SecretSuite) TestCrossSessionSecretURICaching(ctx context.Context, t *testctx.T) {
	tmpdir := t.TempDir()
	initCmd := hostDaggerCommand(ctx, t, tmpdir, "init", "--source=.", "--name=test", "--sdk=go")
	initOutput, err := initCmd.CombinedOutput()
	require.NoError(t, err, string(initOutput))
	err = os.WriteFile(filepath.Join(tmpdir, "main.go"), []byte(`package main
import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {}

func (*Test) Fn(ctx context.Context, secret *dagger.Secret) (string, error) {
	return secret.Plaintext(ctx)
}

func (*Test) Fn2(ctx context.Context, secret *dagger.Secret) *dagger.Container {
	return dag.Container().From("alpine:3.20").
		WithSecretVariable("TOPSECRET", secret).
		WithExec([]string{"sh", "-c", "echo -n $(echo -n $TOPSECRET | base64)"})
}
`), 0644)
	require.NoError(t, err)

	t.Run("default plaintext based cache key", func(ctx context.Context, t *testctx.T) {
		c1 := connect(ctx, t, dagger.WithEnvironmentVariable("FOO", "1"))
		c2 := connect(ctx, t, dagger.WithEnvironmentVariable("FOO", "2"))

		err := c1.ModuleSource(tmpdir).AsModule().Serve(ctx)
		require.NoError(t, err)

		err = c2.ModuleSource(tmpdir).AsModule().Serve(ctx)
		require.NoError(t, err)

		s1 := c1.Secret("env://FOO")
		s1id, err := s1.ID(ctx)
		require.NoError(t, err)
		{
			res, err := testutil.QueryWithClient[struct {
				Test struct {
					Fn string
				}
			}](c1, t, `{test{fn(secret: "`+string(s1id)+`")}}`, nil)
			require.NoError(t, err)
			require.Equal(t, "1", res.Test.Fn)
		}
		{
			res, err := testutil.QueryWithClient[struct {
				Test struct {
					Fn2 struct {
						Stdout string
					}
				}
			}](c1, t, `{test{fn2(secret: "`+string(s1id)+`"){stdout}}}`, nil)
			require.NoError(t, err)
			out1, err := base64.StdEncoding.DecodeString(res.Test.Fn2.Stdout)
			require.NoError(t, err)
			require.Equal(t, "1", string(out1))
		}

		s2 := c2.Secret("env://FOO")
		s2id, err := s2.ID(ctx)
		require.NoError(t, err)
		{
			res, err := testutil.QueryWithClient[struct {
				Test struct {
					Fn string
				}
			}](c2, t, `{test{fn(secret: "`+string(s2id)+`")}}`, nil)
			require.NoError(t, err)
			require.Equal(t, "2", res.Test.Fn)
		}
		{
			res, err := testutil.QueryWithClient[struct {
				Test struct {
					Fn2 struct {
						Stdout string
					}
				}
			}](c2, t, `{test{fn2(secret: "`+string(s2id)+`"){stdout}}}`, nil)
			require.NoError(t, err)
			out2, err := base64.StdEncoding.DecodeString(res.Test.Fn2.Stdout)
			require.NoError(t, err)
			require.Equal(t, "2", string(out2))
		}
	})

	t.Run("custom cache key", func(ctx context.Context, t *testctx.T) {
		c1 := connect(ctx, t, dagger.WithEnvironmentVariable("FOO", "1"))
		c2 := connect(ctx, t, dagger.WithEnvironmentVariable("FOO", "2"))

		err := c1.ModuleSource(tmpdir).AsModule().Serve(ctx)
		require.NoError(t, err)

		err = c2.ModuleSource(tmpdir).AsModule().Serve(ctx)
		require.NoError(t, err)

		cacheKey := identity.NewID()

		s1 := c1.Secret("env://FOO", dagger.SecretOpts{CacheKey: cacheKey})
		s1id, err := s1.ID(ctx)
		require.NoError(t, err)
		{
			res, err := testutil.QueryWithClient[struct {
				Test struct {
					Fn string
				}
			}](c1, t, `{test{fn(secret: "`+string(s1id)+`")}}`, nil)
			require.NoError(t, err)
			require.Equal(t, "1", res.Test.Fn)
		}
		{
			res, err := testutil.QueryWithClient[struct {
				Test struct {
					Fn2 struct {
						Stdout string
					}
				}
			}](c1, t, `{test{fn2(secret: "`+string(s1id)+`"){stdout}}}`, nil)
			require.NoError(t, err)
			out1, err := base64.StdEncoding.DecodeString(res.Test.Fn2.Stdout)
			require.NoError(t, err)
			require.Equal(t, "1", string(out1))
		}

		s2 := c2.Secret("env://FOO", dagger.SecretOpts{CacheKey: cacheKey})
		s2id, err := s2.ID(ctx)
		require.NoError(t, err)
		{
			res, err := testutil.QueryWithClient[struct {
				Test struct {
					Fn string
				}
			}](c2, t, `{test{fn(secret: "`+string(s2id)+`")}}`, nil)
			require.NoError(t, err)
			require.Equal(t, "1", res.Test.Fn)
		}
		{
			res, err := testutil.QueryWithClient[struct {
				Test struct {
					Fn2 struct {
						Stdout string
					}
				}
			}](c2, t, `{test{fn2(secret: "`+string(s2id)+`"){stdout}}}`, nil)
			require.NoError(t, err)
			out2, err := base64.StdEncoding.DecodeString(res.Test.Fn2.Stdout)
			require.NoError(t, err)
			require.Equal(t, "1", string(out2))
		}
	})

	t.Run("dagger call default cache key", func(ctx context.Context, t *testctx.T) {
		c1 := connect(ctx, t)
		c2 := connect(ctx, t)

		{
			out, err := goGitBase(t, c1).
				WithMountedDirectory("/src", c1.Host().Directory(tmpdir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", "1").
				With(daggerCall("fn-2", "--secret", "env://FOO", "stdout")).
				Stdout(ctx)
			require.NoError(t, err)
			outDecoded, err := base64.StdEncoding.DecodeString(out)
			require.NoError(t, err, out)
			require.Equal(t, "1", string(outDecoded))
		}
		{
			out, err := goGitBase(t, c2).
				WithMountedDirectory("/src", c2.Host().Directory(tmpdir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", "2").
				With(daggerCall("fn-2", "--secret", "env://FOO", "stdout")).
				Stdout(ctx)
			require.NoError(t, err, out)
			outDecoded, err := base64.StdEncoding.DecodeString(out)
			require.NoError(t, err, out)
			require.Equal(t, "2", string(outDecoded))
		}
	})

	t.Run("dagger shell default cache key", func(ctx context.Context, t *testctx.T) {
		c1 := connect(ctx, t)
		c2 := connect(ctx, t)

		{
			out, err := goGitBase(t, c1).
				WithMountedDirectory("/src", c1.Host().Directory(tmpdir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", "1").
				With(daggerExec("-s", "-c", "fn-2 env://FOO | stdout")).
				Stdout(ctx)
			require.NoError(t, err, out)
			outDecoded, err := base64.StdEncoding.DecodeString(out)
			require.NoError(t, err, out)
			require.Equal(t, "1", string(outDecoded))
		}
		{
			out, err := goGitBase(t, c2).
				WithMountedDirectory("/src", c2.Host().Directory(tmpdir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", "2").
				With(daggerExec("-s", "-c", "fn-2 env://FOO | stdout")).
				Stdout(ctx)
			require.NoError(t, err, out)
			outDecoded, err := base64.StdEncoding.DecodeString(out)
			require.NoError(t, err, out)
			require.Equal(t, "2", string(outDecoded))
		}
	})

	t.Run("dagger call custom cache key", func(ctx context.Context, t *testctx.T) {
		t.Run("env", func(ctx context.Context, t *testctx.T) {
			c1 := connect(ctx, t)
			c2 := connect(ctx, t)

			cacheKey := identity.NewID()
			plaintext := identity.NewID()
			{
				out, err := goGitBase(t, c1).
					WithMountedDirectory("/src", c1.Host().Directory(tmpdir)).
					WithWorkdir("/src").
					WithEnvVariable("FOO", plaintext).
					With(daggerCall("fn-2", "--secret", "env://FOO?cacheKey="+cacheKey, "stdout")).
					Stdout(ctx)
				require.NoError(t, err)
				outDecoded, err := base64.StdEncoding.DecodeString(out)
				require.NoError(t, err, out)
				require.Equal(t, plaintext, string(outDecoded))
			}
			{
				out, err := goGitBase(t, c2).
					WithMountedDirectory("/src", c2.Host().Directory(tmpdir)).
					WithWorkdir("/src").
					WithEnvVariable("FOO", identity.NewID()).
					With(daggerCall("fn-2", "--secret", "env://FOO?cacheKey="+cacheKey, "stdout")).
					Stdout(ctx)
				require.NoError(t, err, out)
				outDecoded, err := base64.StdEncoding.DecodeString(out)
				require.NoError(t, err, out)
				require.Equal(t, plaintext, string(outDecoded))
			}
		})

		// run some more to ensure that other providers parse correctly

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			c1 := connect(ctx, t)
			c2 := connect(ctx, t)

			cacheKey := identity.NewID()
			plaintext := identity.NewID()
			{
				out, err := goGitBase(t, c1).
					WithMountedDirectory("/src", c1.Host().Directory(tmpdir)).
					WithWorkdir("/src").
					WithNewFile("/foo.txt", plaintext).
					With(daggerCall("fn-2", "--secret", "file:///foo.txt?cacheKey="+cacheKey, "stdout")).
					Stdout(ctx)
				require.NoError(t, err, out)
				outDecoded, err := base64.StdEncoding.DecodeString(out)
				require.NoError(t, err, out)
				require.Equal(t, plaintext, string(outDecoded))
			}
			{
				out, err := goGitBase(t, c2).
					WithMountedDirectory("/src", c2.Host().Directory(tmpdir)).
					WithWorkdir("/src").
					WithNewFile("/bar.txt", identity.NewID()).
					With(daggerCall("fn-2", "--secret", "file:///bar.txt?cacheKey="+cacheKey, "stdout")).
					Stdout(ctx)
				require.NoError(t, err, out)
				outDecoded, err := base64.StdEncoding.DecodeString(out)
				require.NoError(t, err, out)
				require.Equal(t, plaintext, string(outDecoded))
			}
		})

		t.Run("cmd", func(ctx context.Context, t *testctx.T) {
			c1 := connect(ctx, t)
			c2 := connect(ctx, t)

			cacheKey := identity.NewID()
			plaintext := identity.NewID()
			{
				secretCommand := "echo -n " + plaintext
				out, err := goGitBase(t, c1).
					WithMountedDirectory("/src", c1.Host().Directory(tmpdir)).
					WithWorkdir("/src").
					With(daggerCall("fn-2", "--secret", "cmd://"+secretCommand+"?cacheKey="+cacheKey, "stdout")).
					Stdout(ctx)
				require.NoError(t, err, out)
				outDecoded, err := base64.StdEncoding.DecodeString(out)
				require.NoError(t, err, out)
				require.Equal(t, plaintext, string(outDecoded))
			}
			{
				secretCommand := "echo -n " + identity.NewID()
				out, err := goGitBase(t, c2).
					WithMountedDirectory("/src", c2.Host().Directory(tmpdir)).
					WithWorkdir("/src").
					With(daggerCall("fn-2", "--secret", "cmd://"+secretCommand+"?cacheKey="+cacheKey, "stdout")).
					Stdout(ctx)
				require.NoError(t, err, out)
				outDecoded, err := base64.StdEncoding.DecodeString(out)
				require.NoError(t, err, out)
				require.Equal(t, plaintext, string(outDecoded))
			}
		})
	})

	t.Run("dagger shell custom cache key", func(ctx context.Context, t *testctx.T) {
		c1 := connect(ctx, t)
		c2 := connect(ctx, t)

		cacheKey := identity.NewID()
		plaintext := identity.NewID()
		{
			out, err := goGitBase(t, c1).
				WithMountedDirectory("/src", c1.Host().Directory(tmpdir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", plaintext).
				With(daggerExec("-s", "-c", "fn-2 $(secret env://FOO --cache-key "+cacheKey+") | stdout")).
				Stdout(ctx)
			require.NoError(t, err, out)
			outDecoded, err := base64.StdEncoding.DecodeString(out)
			require.NoError(t, err, out)
			require.Equal(t, plaintext, string(outDecoded))
		}
		{
			out, err := goGitBase(t, c2).
				WithMountedDirectory("/src", c2.Host().Directory(tmpdir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", identity.NewID()).
				With(daggerExec("-s", "-c", "fn-2 $(secret env://FOO --cache-key "+cacheKey+") | stdout")).
				Stdout(ctx)
			require.NoError(t, err, out)
			outDecoded, err := base64.StdEncoding.DecodeString(out)
			require.NoError(t, err, out)
			require.Equal(t, plaintext, string(outDecoded))
		}
	})

	t.Run("dagger call custom cache key, different keys", func(ctx context.Context, t *testctx.T) {
		c1 := connect(ctx, t)
		c2 := connect(ctx, t)

		{
			out, err := goGitBase(t, c1).
				WithMountedDirectory("/src", c1.Host().Directory(tmpdir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", "1").
				With(daggerCall("fn-2", "--secret", "env://FOO?cacheKey="+identity.NewID(), "stdout")).
				Stdout(ctx)
			require.NoError(t, err, out)
			outDecoded, err := base64.StdEncoding.DecodeString(out)
			require.NoError(t, err, out)
			require.Equal(t, "1", string(outDecoded))
		}
		{
			out, err := goGitBase(t, c2).
				WithMountedDirectory("/src", c2.Host().Directory(tmpdir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", "2").
				With(daggerCall("fn-2", "--secret", "env://FOO?cacheKey="+identity.NewID(), "stdout")).
				Stdout(ctx)
			require.NoError(t, err, out)
			outDecoded, err := base64.StdEncoding.DecodeString(out)
			require.NoError(t, err, out)
			require.Equal(t, "2", string(outDecoded))
		}
	})
}

func (ModuleSuite) TestCrossSessionDedupeOfNestedExec(ctx context.Context, t *testctx.T) {
	callMod := func(c *dagger.Client) error {
		_, err := goGitBase(t, c).
			WithWorkdir("/work").
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

type Test struct {}

func (Test) Fn(ctx context.Context) error {
	ctr, err := dag.Container().
		From("alpine:3.20").
		WithExec([]string{"sh", "-c", "echo "+strconv.Itoa(int(time.Now().UnixNano()))+"> /foo.txt"}).
		Sync(ctx)
	if err != nil {
		return err
	}

	fmt.Println("sleeping", time.Now().UnixNano())
	time.Sleep(20 * time.Second)
	fmt.Println("awoken", time.Now().UnixNano())

	ctr, err = ctr.WithExec([]string{"true"}).Sync(ctx)
	return err
}
	`,
			).
			With(daggerCall("fn")).
			Sync(ctx)
		return err
	}

	c1 := connect(ctx, t)
	c2 := connect(ctx, t)

	var eg errgroup.Group

	eg.Go(func() error {
		time.Sleep(10 * time.Second)
		t.Log("closing c1")
		c1.Close()
		t.Log("closed c1")
		return nil
	})

	eg.Go(func() error {
		callMod(c1)
		t.Log("c1 call complete")
		return nil
	})

	eg.Go(func() error {
		time.Sleep(5 * time.Second)
		return callMod(c2)
	})

	err := eg.Wait()
	require.NoError(t, err)
}
