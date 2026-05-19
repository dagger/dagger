package core

// These tests cover what happens while loaded module functions execute. They
// verify returned values, nested secret use, services started by module code,
// nil values, cache-control APIs, and float values.
//
// See also:
// - module_loading_test.go: choosing which module source to load.
// - module_path_inputs_test.go: file, directory, and Git path arguments.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func (ModuleSuite) TestSecretNested(ctx context.Context, t *testctx.T) {
	t.Run("pass secrets between modules", func(ctx context.Context, t *testctx.T) {
		// check that we can pass valid secret objects between functions in
		// different modules

		c := connect(ctx, t)
		ctr := moduleFixture(t, c, "go/runtime-secret-pass")

		t.Run("can pass secrets", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{tryArg}`)).Stdout(ctx)
			require.NoError(t, err)
		})

		t.Run("can return secrets", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{tryReturn}`)).Stdout(ctx)
			require.NoError(t, err)
		})
	})

	t.Run("dockerfiles in modules", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := moduleFixture(t, c, "go/runtime-dockerfile-secret")

		_, err := ctr.
			With(daggerCall("ctr", "--src", "./input", "stdout")).
			Sync(ctx)
		require.NoError(t, err)

		_, err = ctr.
			With(daggerCall("evaluated", "--src", "./input")).
			Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("pass embedded secrets between modules", func(ctx context.Context, t *testctx.T) {
		// check that we can pass valid secret objects between functions in
		// different modules when the secrets are embedded in containers rather than
		// passed directly

		t.Run("embedded in returns", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := moduleFixture(t, c, "go/runtime-secret-embedded-return")

			encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
			require.NoError(t, err)
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
			require.NoError(t, err)
			require.Equal(t, "shhh\n", string(decoded))

			censoredOut, err := ctr.With(daggerCall("get-censored")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "***\n", censoredOut)
		})

		t.Run("embedded in args", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := moduleFixture(t, c, "go/runtime-secret-embedded-arg")

			encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
			require.NoError(t, err)
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
			require.NoError(t, err)
			require.Equal(t, "shhh\n", string(decoded))

			censoredOut, err := ctr.With(daggerCall("get-censored")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "***\n", censoredOut)
		})

		t.Run("embedded through struct field", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := moduleFixture(t, c, "go/runtime-secret-embedded-field")

			out, err := ctr.With(daggerCall("test")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "HELLO FROM FOO", out)
		})

		t.Run("embedded through private struct field", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := moduleFixture(t, c, "go/runtime-secret-embedded-private-field")

			out, err := ctr.With(daggerCall("test")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "HELLO FROM FOO", out)
		})

		t.Run("double nested and called repeatedly", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := moduleFixture(t, c, "go/runtime-secret-double-nested")

			_, err := ctr.With(daggerCall("issue")).Sync(ctx)
			require.NoError(t, err)
		})

		t.Run("cached", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := moduleFixture(t, c, "go/runtime-secret-cached")

			out, err := ctr.With(daggerQuery("{foo,bar}")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo": "FOO\nHELLO FROM MOUNT", "bar": "BAR\nHELLO FROM MOUNT"}`, out)
		})
	})

	t.Run("parent fields", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := moduleFixture(t, c, "go/runtime-parent-field")

		encodedOut, err := ctr.With(daggerCall("fn-a", "fn-b")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omg\n", string(decoded))
	})

	t.Run("private parent fields", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := moduleFixture(t, c, "go/runtime-parent-field-private")

		encodedOut, err := ctr.With(daggerCall("fn-a", "fn-b")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omg\n", string(decoded))
	})

	t.Run("parent field set in constructor", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := moduleFixture(t, c, "go/runtime-parent-field-constructor")

		encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omfg\n", string(decoded))
	})

	t.Run("duplicate secret names", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))
		ctr := moduleFixture(t, c, "go/runtime-secret-duplicate-names")

		_, err := ctr.With(daggerQuery(`{attempt}`)).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})

	t.Run("secret by id leak", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))
		ctr := moduleFixture(t, c, "go/runtime-secret-id-leak")

		_, err := ctr.With(daggerQuery(`{attempt(uniq: %q)}`, identity.NewID())).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})

	t.Run("secrets cache normally", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := moduleFixture(t, c, "go/runtime-secret-cache-normal")

		t.Run("internal secrets cache", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{attemptInternal}`)).Stdout(ctx)
			require.NoError(t, err)
		})

		t.Run("external secrets cache", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{attemptExternal}`)).Stdout(ctx)
			require.NoError(t, err)
		})
	})

	t.Run("optional secret field on module object", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := moduleFixture(t, c, "python/runtime-optional-secret-field").
			WithEnvVariable("TOP_SECRET", "omg").
			With(daggerCall("getobj", "--top-secret", "env://TOP_SECRET", "get-secret")).
			Stdout(ctx)

		require.NoError(t, err)
		decodeOut, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
		require.NoError(t, err)
		require.Equal(t, "omg", string(decodeOut))
	})
}

func (ModuleSuite) TestStartServices(ctx context.Context, t *testctx.T) {
	// regression test for https://github.com/dagger/dagger/pull/6914
	t.Run("use service in multiple functions", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := moduleFixture(t, c, "go/runtime-service-reuse").
			With(daggerCall("fn-a", "fn-b")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hey there", strings.TrimSpace(out))
	})

	t.Run("service in multiple containers", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := moduleFixture(t, c, "go/runtime-service-multiple-containers").
			With(daggerCall("fn", "stdout")).
			Sync(ctx)
		require.NoError(t, err)
	})
}

func (ModuleSuite) TestReturnNilField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := moduleFixture(t, c, "go/runtime-return-nil-field").
		With(daggerCall("hello")).
		Sync(ctx)
	require.NoError(t, err)
}

func (ModuleSuite) TestGetEmptyField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("without constructor", func(ctx context.Context, t *testctx.T) {
		out, err := moduleFixture(t, c, "go/runtime-empty-field").
			With(daggerQuery("{a,b}")).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"a": "", "b": 0}`, out)
	})

	t.Run("with constructor", func(ctx context.Context, t *testctx.T) {
		out, err := moduleFixture(t, c, "go/runtime-empty-field-constructor").
			With(daggerQuery("{a,b}")).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"a": "", "b": 0}`, out)
	})
}

func (ModuleSuite) TestFloat(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk     string
		fixture string
	}

	testCases := []testCase{
		{
			sdk:     "go",
			fixture: "go/runtime-float",
		},
		{
			sdk:     "typescript",
			fixture: "typescript/runtime-float",
		},
		{
			sdk:     "python",
			fixture: "python/runtime-float",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := moduleFixture(t, c, tc.fixture)

			t.Run("float64", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test", "--n=3.14")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `3.14`, out)
			})

			t.Run("float32", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-float-32", "--n=1.73424")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `1.73424`, out)
			})

			t.Run("call dep with float64 to float32 conversion", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("dep", "--n=232.3454")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `232.3454`, out)
			})
		})
	}
}

func (ModuleSuite) TestReturnNil(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/runtime-return-nil")

	out, err := modGen.With(daggerQuery(`{nothing{entries}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"nothing":null}`, out)

	out, err = modGen.With(daggerQuery(`{listWithNothing{entries}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"listWithNothing":[null]}`, out)

	out, err = modGen.With(daggerQuery(`{objsWithNothing{dirs{entries}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"objsWithNothing":[null,{"dirs":[null]}]}`, out)
}

func (ModuleSuite) TestFunctionCacheControl(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		sdk     string
		fixture string
	}{
		{
			// TODO: add test that function doc strings still get parsed correctly, don't include //+ etc.
			sdk:     "go",
			fixture: "go/runtime-cache-control",
		},
		{
			sdk:     "python",
			fixture: "python/runtime-cache-control",
		},
		{
			sdk:     "typescript",
			fixture: "typescript/runtime-cache-control",
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			t.Run("always cache", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := moduleFixture(t, c1, tc.fixture)

				out1, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-always-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := moduleFixture(t, c2, tc.fixture)

				out2, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-always-cache")).Stdout(ctx)
				require.NoError(t, err)

				require.Equal(t, out1, out2, "outputs should be equal since the result is always cached")
			})

			t.Run("cache per session", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := moduleFixture(t, c1, tc.fixture)

				out1a, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				out1b, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, out1a, out1b, "outputs should be equal since they are from the same session")
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := moduleFixture(t, c2, tc.fixture)

				out2a, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				out2b, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, out2a, out2b, "outputs should be equal since they are from the same session")

				require.NotEqual(t, out1a, out2a, "outputs should not be equal since they are from different sessions")
			})

			t.Run("never cache", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := moduleFixture(t, c1, tc.fixture)

				out1a, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				out1b, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out1a, out1b, "outputs should not be equal since they are never cached")
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := moduleFixture(t, c2, tc.fixture)

				out2a, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				out2b, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out2a, out2b, "outputs should not be equal since they are never cached")

				require.NotEqual(t, out1a, out2a, "outputs should not be equal since they are never cached")
			})

			t.Run("cache ttl", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := moduleFixture(t, c1, tc.fixture)

				out1, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := moduleFixture(t, c2, tc.fixture)

				out2, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c2.Close())

				require.Equal(t, out1, out2, "outputs should be equal since the cache ttl has not expired")
				time.Sleep(41 * time.Second)

				c3 := connect(ctx, t)
				modGen3 := moduleFixture(t, c3, tc.fixture)

				out3, err := modGen3.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out1, out3, "outputs should not be equal since the cache ttl has expired")
			})
		})
	}

	t.Run("setSecret invalidates cache", func(ctx context.Context, t *testctx.T) {
		c1 := connect(ctx, t)
		modGen1 := moduleFixture(t, c1, "go/runtime-cache-set-secret")

		out1a, err := modGen1.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		out1b, err := modGen1.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out1a, out1b)
		require.NoError(t, c1.Close())

		c2 := connect(ctx, t)
		modGen2 := moduleFixture(t, c2, "go/runtime-cache-set-secret")

		out2a, err := modGen2.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		out2b, err := modGen2.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out2a, out2b)

		require.NotEqual(t, out1a, out2a)
	})

	t.Run("dependency contextual arg", func(ctx context.Context, t *testctx.T) {
		getModGen := func(c *dagger.Client) *dagger.Container {
			return moduleFixture(t, c, "go/runtime-contextual-arg-dep")
		}

		t.Run("dir", func(ctx context.Context, t *testctx.T) {
			c1 := connect(ctx, t)
			out1, err := getModGen(c1).
				With(daggerCall("call-dep", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.NoError(t, c1.Close())

			c2 := connect(ctx, t)
			out2, err := getModGen(c2).
				With(daggerCall("call-dep", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)

			require.Equal(t, out1, out2)
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			c1 := connect(ctx, t)
			out1, err := getModGen(c1).
				With(daggerCall("call-dep-file", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.NoError(t, c1.Close())

			c2 := connect(ctx, t)
			out2, err := getModGen(c2).
				With(daggerCall("call-dep-file", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)

			require.Equal(t, out1, out2)
		})
	})

	t.Run("git contextual arg", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		copyTestdataFixture(ctx, t, modDir, "modules", "go", "runtime-contextual-git-arg")

		gitCmd := exec.Command("git", "init")
		gitCmd.Dir = modDir
		gitOutput, err := gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		gitCmd = exec.Command("git", "config", "user.email", "dagger@example.com")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		gitCmd = exec.Command("git", "config", "user.name", "Dagger Tests")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		gitCmd = exec.Command("git", "add", ".")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		gitCmd = exec.Command("git", "commit", "-m", "make HEAD exist")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		require.NoError(t, os.MkdirAll(filepath.Join(modDir, "crap"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(modDir, "config"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(modDir, "config", "config.local.js"), []byte("1"), 0o644))

		callCmd := hostDaggerCommand(ctx, t, modDir, "call", "fn")
		callOutput, err := callCmd.CombinedOutput()
		require.NoError(t, err, string(callOutput))

		require.NoError(t, os.WriteFile(filepath.Join(modDir, "config", "config.local.js"), []byte("2"), 0o644))

		callCmd = hostDaggerCommand(ctx, t, modDir, "call", "fn")
		callOutput, err = callCmd.CombinedOutput()
		require.NoError(t, err, string(callOutput))
	})
}
