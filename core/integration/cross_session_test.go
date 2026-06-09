package core

// These tests cover values reused after one Dagger session ends and another
// begins. They verify module function caching, services, context directories,
// Git auth, sockets, secrets, LLM calls, interfaces, and Dockerfile/Git socket
// use across session boundaries.
//
// See also:
// - cache_test.go: core cache volume and cache-key behavior.
// - engine_persistence_test.go: engine state across restarts.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/identity"
	fscopy "github.com/dagger/dagger/internal/fsutil/copy"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TODO: add more tests for longer chains of cache hits that then diverge (i.e. constructor + some function cache hit, then diverge)
func (ModuleSuite) TestCrossSessionFunctionCaching(ctx context.Context, t *testctx.T) {
	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		callMod := func(c *dagger.Client) (string, error) {
			return moduleFixture(t, c, "go/cross-session-function-basic").
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
		callMod := func(c *dagger.Client, parentArg *string, i *int, s *string) (string, error) {
			args := []string{"fn"}
			if parentArg != nil {
				args = append([]string{"--parentArg", *parentArg}, args...)
			}
			if i != nil {
				args = append(args, "--i", strconv.Itoa(*i))
			}
			if s != nil {
				args = append(args, "--s", *s)
			}
			return moduleFixture(t, c, "go/cross-session-function-args").
				WithEnvVariable("CACHEBUSTER", identity.NewID()).
				With(daggerCall(args...)).
				Stdout(ctx)
		}

		for _, tc := range []struct {
			name           string
			parentArg1     *string
			parentArg2     *string
			i1             *int
			i2             *int
			s1             *string
			s2             *string
			expectCacheHit bool
		}{
			// NOTE: be careful to not make the same function call in different test cases,
			// it can result in timing of cache hits that interferes with the isolated test
			{
				name:           "unset",
				expectCacheHit: true,
			},
			{
				name:           "unset but diff parent",
				parentArg1:     ptr("p2"),
				parentArg2:     ptr("p3"),
				expectCacheHit: false,
			},
			{
				name:           "same",
				i1:             ptr(1),
				i2:             ptr(1),
				s1:             ptr("1"),
				s2:             ptr("1"),
				expectCacheHit: true,
			},
			{
				name:           "same but diff parent",
				parentArg1:     ptr("p4"),
				i1:             ptr(2),
				i2:             ptr(2),
				s1:             ptr("2"),
				s2:             ptr("2"),
				expectCacheHit: false,
			},
			{
				name:           "all different",
				i1:             ptr(3),
				i2:             ptr(4),
				s1:             ptr("3"),
				s2:             ptr("4"),
				expectCacheHit: false,
			},
			{
				name:           "some different",
				i1:             ptr(5),
				i2:             ptr(5),
				s1:             ptr("5"),
				s2:             ptr("6"),
				expectCacheHit: false,
			},
		} {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				out1, err := callMod(c1, tc.parentArg1, tc.i1, tc.s1)
				require.NoError(t, err)

				c2 := connect(ctx, t)
				out2, err := callMod(c2, tc.parentArg2, tc.i2, tc.s2)
				require.NoError(t, err)

				if tc.expectCacheHit {
					require.Equal(t, out1, out2)
				} else {
					require.NotEqual(t, out1, out2)
				}
			})
		}
	})

	t.Run("module object field id rebinds on function-cache hit", func(ctx context.Context, t *testctx.T) {
		t.Skip("caller-specific ID rebinding is intentionally removed for now")
	})

	t.Run("same schema but different implementations", func(ctx context.Context, t *testctx.T) {
		// right now calls are cached by module source digest via the `asModule` custom cache key plus
		// the fact that IDs include the module ResultID, verify that behavior works as expected
		callMod := func(c *dagger.Client, fixture string) (string, error) {
			return moduleFixture(t, c, fixture).
				WithEnvVariable("CACHEBUSTER", identity.NewID()).
				With(daggerCall("fn")).
				Stdout(ctx)
		}

		c1 := connect(ctx, t)
		out1, err := callMod(c1, "go/cross-session-impl-one")
		require.NoError(t, err)
		require.Equal(t, "1", out1)

		c2 := connect(ctx, t)
		out2, err := callMod(c2, "go/cross-session-impl-two")
		require.NoError(t, err)
		require.Equal(t, "2", out2)
	})

	t.Run("same source different clients and first disconnects", func(ctx context.Context, t *testctx.T) {
		tmpdir1 := t.TempDir()
		copyTestdataFixture(ctx, t, tmpdir1, "modules", "go", "cross-session-same-source")

		tmpdir2 := t.TempDir()
		err := fscopy.Copy(ctx, tmpdir1, "/", tmpdir2, "/")
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
			return moduleFixture(t, c, "go/cross-session-services").
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

// This covers the behavior previously checked through the private
// _contextDirectory field. A Directory argument with +defaultPath="/" must
// resolve from the module source context even after the client that first
// loaded the module has closed, so the second client cannot depend on the first
// client's in-memory module/context lookup state.
func (ModuleSuite) TestCrossSessionContextDirectoryDefaultPath(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "cross-session-context-directory-default")

	callEntries := func(c *dagger.Client, cacheBust string) []string {
		mod, err := c.ModuleSource(modDir).AsModule().Sync(ctx)
		require.NoError(t, err)
		require.NoError(t, mod.Serve(ctx))

		res, err := testutil.QueryWithClient[struct {
			Test struct {
				Entries []string
			}
		}](c, t, `query Entries($cacheBust: String!) { test { entries(cacheBust: $cacheBust) } }`, &testutil.QueryOptions{
			Operation: "Entries",
			Variables: map[string]any{
				"cacheBust": cacheBust,
			},
		})
		require.NoError(t, err)
		return res.Test.Entries
	}

	c1 := connect(ctx, t)
	entries1 := callEntries(c1, identity.NewID())
	require.Contains(t, entries1, "dagger.json")
	require.Contains(t, entries1, "foo.txt")
	require.NoError(t, c1.Close())

	c2 := connect(ctx, t)
	entries2 := callEntries(c2, identity.NewID())
	require.Contains(t, entries2, "dagger.json")
	require.Contains(t, entries2, "foo.txt")
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
				With(daggerExecRaw("core",
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
				With(daggerExecRaw("core",
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
				With(daggerExecRaw("core",
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
				With(daggerExecRaw("core",
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
			fixture := "go/private-git-dep-http"
			if testCase.sshKey {
				fixture = "go/private-git-dep-ssh"
			}

			// sanity test fail when no auth given
			c1 := connect(ctx, t)
			_, err = moduleFixture(t, c1, fixture).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(daggerCallAt(".", "fn")).
				Sync(ctx)
			requireErrOut(t, err, expectedErr)

			// pull the git repo with auth, get it into the cache
			c2 := connect(ctx, t)
			withRepo, withRepoCleanup := privateRepoSetup(c2, t, testCase)
			t.Cleanup(withRepoCleanup)
			_, err = moduleFixture(t, c2, fixture).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(withRepo).
				With(daggerCallAt(".", "fn")).
				Sync(ctx)
			require.NoError(t, err)

			// try again with no auth, should fail
			c3 := connect(ctx, t)
			_, err = moduleFixture(t, c3, fixture).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(daggerCallAt(".", "fn")).
				Sync(ctx)
			requireErrOut(t, err, expectedErr)

			// try again on same session but with auth, should succeed now
			withRepo, withRepoCleanup = privateRepoSetup(c3, t, testCase)
			t.Cleanup(withRepoCleanup)
			_, err = moduleFixture(t, c3, fixture).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				With(withRepo).
				With(daggerCallAt(".", "fn")).
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
	copyTestdataFixture(ctx, t, modTmpdir, "modules", "go", "cross-session-socket")

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
	// verify that setSecret-producing calls do not transfer cached secrets across
	// sessions; each session should rerun and produce a fresh secret value
	t.Run("cached set-secret transfers", func(ctx context.Context, t *testctx.T) {
		callMod := func(c *dagger.Client) (string, error) {
			return moduleFixture(t, c, "go/cross-session-secret-set").
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

		require.NotEqual(t, out1, out2)

		// close original client and ensure a later session still reruns rather than
		// reusing either earlier secret value
		require.NoError(t, c1.Close())

		c3 := connect(ctx, t)
		out3, err := callMod(c3)
		require.NoError(t, err)

		require.NotEqual(t, out1, out3)
		require.NotEqual(t, out2, out3)
	})

	t.Run("different secrets with same name do not cache", func(ctx context.Context, t *testctx.T) {
		callMod := func(c *dagger.Client, val string) (string, error) {
			return moduleFixture(t, c, "go/cross-session-secret-same-name").
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

	t.Run("secret uris", func(ctx context.Context, t *testctx.T) {
		tmpdir := t.TempDir()
		copyTestdataFixture(ctx, t, tmpdir, "modules", "go", "cross-session-secret-uri-container")

		c1 := connect(ctx, t)
		err := c1.ModuleSource(tmpdir).AsModule().Serve(ctx)
		require.NoError(t, err)

		secretID1, err := c1.Secret("cmd://echo -n foo").ID(ctx)
		require.NoError(t, err)

		res1, err := testutil.QueryWithClient[struct {
			Test struct {
				Fn struct {
					ID dagger.ID
				}
			}
		}](c1, t, `{test{fn(secret:"`+string(secretID1)+`"){id}}}`, nil)
		require.NoError(t, err)
		ctrID1 := res1.Test.Fn.ID
		require.NotEmpty(t, ctrID1)
		ctr1, err := dagger.Load[*dagger.Container](ctx, c1, ctrID1)
		require.NoError(t, err)
		_, err = ctr1.
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
					ID dagger.ID
				}
			}
		}](c2, t, `{test{fn(secret:"`+string(secretID2)+`"){id}}}`, nil)
		require.NoError(t, err)
		ctrID2 := res2.Test.Fn.ID
		require.NotEmpty(t, ctrID2)

		require.NoError(t, c1.Close())

		ctr2, err := dagger.Load[*dagger.Container](ctx, c2, ctrID2)
		require.NoError(t, err)
		_, err = ctr2.
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithExec([]string{"true"}).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("secret uri plaintext", func(ctx context.Context, t *testctx.T) {
		callMod := func(c *dagger.Client, val string) (string, error) {
			return moduleFixture(t, c, "go/cross-session-secret-plaintext").
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
			return moduleFixture(t, c, "go/cross-session-secret-contenthash").
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
		With(daggerExecRaw("core", "llm", "model")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "claude-3-5-sonnet-latest", out)

	c2 := connect(ctx, t)
	out, err = goGitBase(t, c2).
		WithEnvVariable("OPENAI_MODEL", "gpt-4.1").
		With(daggerExecRaw("core", "llm", "model")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "gpt-4.1", out)
}

func (ModuleSuite) TestCrossSessionContextualDirWithPrivate(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "cross-session-contextual-private")

	err := os.MkdirAll(filepath.Join(modDir, "crap"), 0755)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "crap", "foo.txt"), []byte(identity.NewID()), 0644))

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
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "cross-session-contextual-change")

	err := os.MkdirAll(filepath.Join(modDir, "crap"), 0755)
	require.NoError(t, err)
	rand1 := identity.NewID()
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "crap", "foo.txt"), []byte(rand1), 0644))

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
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "cross-session-contextual-cache-hit")

	err := os.MkdirAll(filepath.Join(modDir, "crap"), 0755)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "crap", "foo.txt"), []byte(identity.NewID()), 0644))

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

func (ModuleSuite) TestCrossSessionInlineDependencyContextualDirChange(ctx context.Context, t *testctx.T) {
	tmpdir := t.TempDir()
	depDir := filepath.Join(tmpdir, "dep")
	modDir := filepath.Join(tmpdir, "mod")
	dataDir := filepath.Join(tmpdir, "data")
	require.NoError(t, os.MkdirAll(depDir, 0755))
	require.NoError(t, os.MkdirAll(modDir, 0755))
	require.NoError(t, os.MkdirAll(dataDir, 0755))

	gitInitCmd := exec.Command("git", "init")
	gitInitCmd.Dir = tmpdir
	gitInitOutput, err := gitInitCmd.CombinedOutput()
	require.NoError(t, err, string(gitInitOutput))

	initDepCmd := hostDaggerCommand(ctx, t, tmpdir, "init", "--source=dep", "--name=dep", "--sdk=go", "dep")
	initDepOutput, err := initDepCmd.CombinedOutput()
	require.NoError(t, err, string(initDepOutput))

	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "value.txt"), []byte("one"), 0644))

	err = os.WriteFile(filepath.Join(depDir, "main.go"), []byte(`package main
import (
	"context"

	"dagger/dep/internal/dagger"
)

type Dep struct {
	Dir *dagger.Directory
}

func New(
	// +defaultPath="/data"
	dir *dagger.Directory,
) *Dep {
	return &Dep{Dir: dir}
}

func (d *Dep) Read(ctx context.Context) (string, error) {
	return d.Dir.File("value.txt").Contents(ctx)
}
`), 0644)
	require.NoError(t, err)

	initModCmd := hostDaggerCommand(ctx, t, tmpdir, "init", "--source=mod", "--name=test", "--sdk=go", "mod")
	initModOutput, err := initModCmd.CombinedOutput()
	require.NoError(t, err, string(initModOutput))

	installDepCmd := hostDaggerCommand(ctx, t, tmpdir, "install", "-m=mod", "dep")
	installDepOutput, err := installDepCmd.CombinedOutput()
	require.NoError(t, err, string(installDepOutput))

	err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main
import "context"

type Test struct {}

func (*Test) Read(ctx context.Context) (string, error) {
	return dag.Dep().Read(ctx)
}
`), 0644)
	require.NoError(t, err)

	read := func(c *dagger.Client) string {
		mod, err := c.ModuleSource(modDir).AsModule().Sync(ctx)
		require.NoError(t, err)
		require.NoError(t, mod.Serve(ctx))

		res, err := testutil.QueryWithClient[struct {
			Test struct {
				Read string
			}
		}](c, t, `{test{read}}`, nil)
		require.NoError(t, err)
		return res.Test.Read
	}

	c1 := connect(ctx, t)
	require.Equal(t, "one", read(c1))
	require.NoError(t, c1.Close())

	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "value.txt"), []byte("two"), 0644))

	c2 := connect(ctx, t)
	require.Equal(t, "two", read(c2))
}

func (SecretSuite) TestCrossSessionSecretURICaching(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()
	err := fscopy.Copy(ctx, testDataPath(t, "modules", "go", "secret-uri-caching"), "/", modDir, "/")
	require.NoError(t, err)

	t.Run("default plaintext based cache key", func(ctx context.Context, t *testctx.T) {
		c1 := connect(ctx, t, dagger.WithEnvironmentVariable("FOO", "1"))
		c2 := connect(ctx, t, dagger.WithEnvironmentVariable("FOO", "2"))

		err := c1.ModuleSource(modDir).AsModule().Serve(ctx)
		require.NoError(t, err)

		err = c2.ModuleSource(modDir).AsModule().Serve(ctx)
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

		err := c1.ModuleSource(modDir).AsModule().Serve(ctx)
		require.NoError(t, err)

		err = c2.ModuleSource(modDir).AsModule().Serve(ctx)
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
				WithMountedDirectory("/src", c1.Host().Directory(modDir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", "1").
				With(daggerCallAt(".", "fn-2", "--secret", "env://FOO", "stdout")).
				Stdout(ctx)
			require.NoError(t, err)
			outDecoded, err := base64.StdEncoding.DecodeString(out)
			require.NoError(t, err, out)
			require.Equal(t, "1", string(outDecoded))
		}
		{
			out, err := goGitBase(t, c2).
				WithMountedDirectory("/src", c2.Host().Directory(modDir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", "2").
				With(daggerCallAt(".", "fn-2", "--secret", "env://FOO", "stdout")).
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
					WithMountedDirectory("/src", c1.Host().Directory(modDir)).
					WithWorkdir("/src").
					WithEnvVariable("FOO", plaintext).
					With(daggerCallAt(".", "fn-2", "--secret", "env://FOO?cacheKey="+cacheKey, "stdout")).
					Stdout(ctx)
				require.NoError(t, err)
				outDecoded, err := base64.StdEncoding.DecodeString(out)
				require.NoError(t, err, out)
				require.Equal(t, plaintext, string(outDecoded))
			}
			{
				out, err := goGitBase(t, c2).
					WithMountedDirectory("/src", c2.Host().Directory(modDir)).
					WithWorkdir("/src").
					WithEnvVariable("FOO", identity.NewID()).
					With(daggerCallAt(".", "fn-2", "--secret", "env://FOO?cacheKey="+cacheKey, "stdout")).
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
					WithMountedDirectory("/src", c1.Host().Directory(modDir)).
					WithWorkdir("/src").
					WithNewFile("/foo.txt", plaintext).
					With(daggerCallAt(".", "fn-2", "--secret", "file:///foo.txt?cacheKey="+cacheKey, "stdout")).
					Stdout(ctx)
				require.NoError(t, err, out)
				outDecoded, err := base64.StdEncoding.DecodeString(out)
				require.NoError(t, err, out)
				require.Equal(t, plaintext, string(outDecoded))
			}
			{
				out, err := goGitBase(t, c2).
					WithMountedDirectory("/src", c2.Host().Directory(modDir)).
					WithWorkdir("/src").
					WithNewFile("/bar.txt", identity.NewID()).
					With(daggerCallAt(".", "fn-2", "--secret", "file:///bar.txt?cacheKey="+cacheKey, "stdout")).
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
					WithMountedDirectory("/src", c1.Host().Directory(modDir)).
					WithWorkdir("/src").
					With(daggerCallAt(".", "fn-2", "--secret", "cmd://"+secretCommand+"?cacheKey="+cacheKey, "stdout")).
					Stdout(ctx)
				require.NoError(t, err, out)
				outDecoded, err := base64.StdEncoding.DecodeString(out)
				require.NoError(t, err, out)
				require.Equal(t, plaintext, string(outDecoded))
			}
			{
				secretCommand := "echo -n " + identity.NewID()
				out, err := goGitBase(t, c2).
					WithMountedDirectory("/src", c2.Host().Directory(modDir)).
					WithWorkdir("/src").
					With(daggerCallAt(".", "fn-2", "--secret", "cmd://"+secretCommand+"?cacheKey="+cacheKey, "stdout")).
					Stdout(ctx)
				require.NoError(t, err, out)
				outDecoded, err := base64.StdEncoding.DecodeString(out)
				require.NoError(t, err, out)
				require.Equal(t, plaintext, string(outDecoded))
			}
		})
	})

	t.Run("dagger call custom cache key, different keys", func(ctx context.Context, t *testctx.T) {
		c1 := connect(ctx, t)
		c2 := connect(ctx, t)

		{
			out, err := goGitBase(t, c1).
				WithMountedDirectory("/src", c1.Host().Directory(modDir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", "1").
				With(daggerCallAt(".", "fn-2", "--secret", "env://FOO?cacheKey="+identity.NewID(), "stdout")).
				Stdout(ctx)
			require.NoError(t, err, out)
			outDecoded, err := base64.StdEncoding.DecodeString(out)
			require.NoError(t, err, out)
			require.Equal(t, "1", string(outDecoded))
		}
		{
			out, err := goGitBase(t, c2).
				WithMountedDirectory("/src", c2.Host().Directory(modDir)).
				WithWorkdir("/src").
				WithEnvVariable("FOO", "2").
				With(daggerCallAt(".", "fn-2", "--secret", "env://FOO?cacheKey="+identity.NewID(), "stdout")).
				Stdout(ctx)
			require.NoError(t, err, out)
			outDecoded, err := base64.StdEncoding.DecodeString(out)
			require.NoError(t, err, out)
			require.Equal(t, "2", string(outDecoded))
		}
	})
}

func (ModuleSuite) TestCrossSessionDedupeOfNestedExec(ctx context.Context, t *testctx.T) {
	t.Skip("disabled until Theseus lands")
}

func (ModuleSuite) TestPrivateGitRepoArgCaching(ctx context.Context, t *testctx.T) {
	// Call a function with a directory arg sourced from a private git repo that uses
	// an auth token. Do this from two different clients with different tokens for the
	// same repo. Ensure that even though the git repo is cached between the two, we
	// don't hit errors about missing auth tokens.

	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "cross-session-private-git-arg")

	tc := getVCSTestCase(t, "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")

	gitConfigDir1 := t.TempDir()
	gitConfigFile1 := filepath.Join(gitConfigDir1, "config")
	err := os.WriteFile(
		gitConfigFile1,
		[]byte(makeGitCredentials("https://"+tc.expectedHost, "git", decodedGitToken(tc.encodedToken))),
		0644,
	)
	require.NoError(t, err)
	c1 := connect(ctx, t, dagger.WithEnvironmentVariable("GIT_CONFIG_GLOBAL", gitConfigFile1))

	err = c1.ModuleSource(modDir).AsModule().Serve(ctx)
	require.NoError(t, err)

	gitRepoID1, err := c1.Address(tc.gitTestRepoRef).Directory().ID(ctx)
	require.NoError(t, err)

	rand1 := rand.Text()
	res1, err := testutil.QueryWithClient[struct {
		Test struct {
			Fn []string
		}
	}](c1, t, `{test{fn(dir: "`+string(gitRepoID1)+`", rand: "`+rand1+`")}}`, nil)
	require.NoError(t, err)

	gitConfigDir2 := t.TempDir()
	gitConfigFile2 := filepath.Join(gitConfigDir2, "config")
	err = os.WriteFile(
		gitConfigFile2,
		[]byte(makeGitCredentials("https://"+tc.expectedHost, "git", decodedGitToken(tc.encodedToken2))),
		0644,
	)
	require.NoError(t, err)
	c2 := connect(ctx, t, dagger.WithEnvironmentVariable("GIT_CONFIG_GLOBAL", gitConfigFile2))

	err = c2.ModuleSource(modDir).AsModule().Serve(ctx)
	require.NoError(t, err)

	gitRepoID2, err := c2.Address(tc.gitTestRepoRef).Directory().ID(ctx)
	require.NoError(t, err)

	rand2 := rand.Text()
	res2, err := testutil.QueryWithClient[struct {
		Test struct {
			Fn []string
		}
	}](c2, t, `{test{fn(dir: "`+string(gitRepoID2)+`", rand: "`+rand2+`")}}`, nil)
	require.NoError(t, err)

	require.Equal(t, res1.Test.Fn, res2.Test.Fn)
}

func (InterfaceSuite) TestCrossSessionInterfaceCaching(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "cross-session-interface")

	callCmd1 := hostDaggerCommandRaw(ctx, t, modDir, "call", "-m", ".", "drive-rolls-royce")
	callOutput1, err := callCmd1.CombinedOutput()
	require.NoError(t, err, string(callOutput1))

	callCmd2 := hostDaggerCommandRaw(ctx, t, modDir, "call", "-m", ".", "drive-rolls-royce")
	callOutput2, err := callCmd2.CombinedOutput()
	require.NoError(t, err, string(callOutput2))
}

func (DirectorySuite) TestContentHashedDirectoryFile(ctx context.Context, t *testctx.T) {
	// create two dirs with identical contents, but at different subpaths

	rando := rand.Text()

	rootA := t.TempDir()
	fileA := filepath.Join(rootA, "subdirA", rando)
	require.NoError(t, os.MkdirAll(filepath.Dir(fileA), 0755))
	require.NoError(t, os.WriteFile(fileA, []byte(rando), 0644))

	rootB := t.TempDir()
	fileB := filepath.Join(rootB, "subdirB", rando)
	require.NoError(t, os.MkdirAll(filepath.Dir(fileB), 0755))
	require.NoError(t, os.WriteFile(fileB, []byte(rando), 0644))

	c1 := connect(ctx, t)
	c2 := connect(ctx, t)

	// populate engine with cache entry from dir A
	_, err := c1.Host().Directory(rootA).Directory("subdirA").Entries(ctx)
	require.NoError(t, err)

	// Try to load the subdir from B and read the file, it should succeed.
	// The error case the engine needs to avoid is:
	// 1. cache hit between rootB/subdirB + rootA/subdirA, using rootA/subdirA because it came first
	// 2. try to read "subdirB/rando" from rootA, which doesn't exist
	contents, err := c2.Host().Directory(rootB).Directory("subdirB").File(rando).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, rando, contents)
}

func (DockerfileSuite) TestCrossSessionDockerbuildSockets(ctx context.Context, t *testctx.T) {
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
				t.Logf("copy: %s", err)
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
	copyTestdataFixture(ctx, t, modTmpdir, "modules", "go", "cross-session-dockerbuild-socket")

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

func (ModuleSuite) TestCrossSessionGitSockets(ctx context.Context, t *testctx.T) {
	tc := getVCSTestCase(t, "git@bitbucket.org:dagger-modules/private-modules-test.git")
	url := tc.gitTestRepoRef
	ref := tc.gitTestRepoCommit

	agentSockPath1, cleanup1 := setupPrivateRepoSSHAgent(t)
	c1 := connect(ctx, t, dagger.WithEnvironmentVariable("SSH_AUTH_SOCK", agentSockPath1))
	ref1ID, err := c1.Git(url).Commit(ref).ID(ctx)
	require.NoError(t, err)
	var id1 call.ID
	err = id1.Decode(string(ref1ID))
	require.NoError(t, err)

	agentSockPath2, _ := setupPrivateRepoSSHAgent(t)
	c2 := connect(ctx, t, dagger.WithEnvironmentVariable("SSH_AUTH_SOCK", agentSockPath2))
	ref2ID, err := c2.Git(url).Commit(ref).ID(ctx)
	require.NoError(t, err)
	var id2 call.ID
	err = id2.Decode(string(ref2ID))
	require.NoError(t, err)

	cleanup1()
	require.NoError(t, c1.Close())

	gitRef, err := dagger.Load[*dagger.GitRef](ctx, c2, ref2ID)
	require.NoError(t, err)
	_, err = gitRef.Tree().Sync(ctx)
	require.NoError(t, err)
}
