package core

// These tests cover `dagger call` and related CLI paths for invoking module
// functions. They verify help text, argument parsing, return-value rendering,
// and command UX.
//
// See also:
// - module_introspection_cli_test.go: inspecting callable functions.
// - module_error_test.go: errors returned by module calls.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

type CallSuite struct{}

func TestCall(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CallSuite{})
}

func (CallSuite) TestHelp(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/call-help")

	t.Run("no required arg validation", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(".", "container", "--help")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "USAGE")
		require.Contains(t, out, "dagger call container <function>")
	})

	t.Run("globally parsed", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(".", "container", "--help", "directory")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "USAGE")
		require.Contains(t, out, "dagger call container directory [arguments] <function>")
	})

	t.Run("flag conflict", func(ctx context.Context, t *testctx.T) {
		// The function's --mod argument shadows the parent's persistent
		// --mod/-m flag. Cobra/pflag allows this — the child's local flag
		// takes precedence. Verify the command works (shows help) rather
		// than erroring.
		out, err := modGen.With(daggerCallAt(".", "conflict", "--help")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "--mod")
	})
}

func (CallSuite) TestArgTypes(ctx context.Context, t *testctx.T) {
	t.Run("service args", func(ctx context.Context, t *testctx.T) {
		t.Run("used as service binding", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := moduleFixture(t, c, "go/call-service-binding")

			logGen(ctx, t, modGen.Directory("."))

			httpServer, _ := httpService(ctx, t, c, "im up")
			endpoint, err := httpServer.Endpoint(ctx)
			require.NoError(t, err)

			out, err := modGen.
				WithServiceBinding("testserver", httpServer).
				With(daggerCallAt(".", "fn", "--svc", "tcp://"+endpoint)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "im up", out)
		})

		t.Run("used directly", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := moduleFixture(t, c, "go/call-service-direct")

			logGen(ctx, t, modGen.Directory("."))

			httpServer, _ := httpService(ctx, t, c, "im up")
			endpoint, err := httpServer.Endpoint(ctx)
			require.NoError(t, err)

			out, err := modGen.
				WithServiceBinding("testserver", httpServer).
				With(daggerCallAt(".", "fn", "--svc", "tcp://"+endpoint)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "1 exposed ports:\n- TCP/80", out)
		})
	})

	t.Run("list args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-list-args")

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.With(daggerCallAt(".", "hello", "--msgs", "yo", "--msgs", "my", "--msgs", "friend")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo+my+friend", out)

		out, err = modGen.With(daggerCallAt(".", "reads", "--files=foo.txt", "--files=foo.txt")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar+bar", out)
	})

	t.Run("directory arg inputs", func(ctx context.Context, t *testctx.T) {
		t.Run("local dir", func(ctx context.Context, t *testctx.T) {
			t.Run("abs path", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := moduleFixture(t, c, "go/call-directory-arg").
					WithNewFile("/dir/subdir/foo.txt", "foo").
					WithNewFile("/dir/subdir/bar.txt", "bar")

				out, err := modGen.With(daggerCallAt(".", "fn", "--dir", "/dir/subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\nfoo.txt\n", out)

				out, err = modGen.With(daggerCallAt(".", "fn", "--dir", "file:///dir/subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\nfoo.txt\n", out)
			})

			t.Run("expand home directory", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := moduleFixture(t, c, "go/call-directory-arg").
					WithNewFile("/root/foo.txt", "foo").
					WithNewFile("/root/subdir/bar.txt", "bar")

				out, err := modGen.With(daggerCallAt(".", "fn", "--dir", "~", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "foo.txt\nsubdir/\n", out)

				out, err = modGen.With(daggerCallAt(".", "fn", "--dir", "~/subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\n", out)
			})

			t.Run("rel path", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := goGitBase(t, c).
					With(withModuleFixture(t, c, "/work/dir", "go/call-directory-arg")).
					WithWorkdir("/work/dir").
					WithNewFile("/work/otherdir/foo.txt", "foo").
					WithNewFile("/work/otherdir/bar.txt", "bar").
					WithNewFile("/work/dir/subdir/blah.txt", "blah")

				out, err := modGen.With(daggerCallAt(".", "fn", "--dir", "../otherdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\nfoo.txt\n", out)

				out, err = modGen.With(daggerCallAt(".", "fn", "--dir", "file://../otherdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\nfoo.txt\n", out)

				out, err = modGen.With(daggerCallAt(".", "fn", "--dir", "subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "blah.txt\n", out)

				out, err = modGen.With(daggerCallAt(".", "fn", "--dir", "file://subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "blah.txt\n", out)
			})
		})

		t.Run("git dir", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := moduleFixture(t, c, "go/call-git-dir")

			for _, tc := range []struct {
				baseURL string
				subpath string
			}{
				{
					baseURL: "https://github.com/dagger/dagger",
				},
				{
					baseURL: "https://github.com/dagger/dagger",
					subpath: ".changes",
				},
				{
					baseURL: "https://github.com/dagger/dagger.git",
				},
				{
					baseURL: "https://github.com/dagger/dagger.git",
					subpath: ".changes",
				},
			} {
				t.Run(fmt.Sprintf("%s:%s", tc.baseURL, tc.subpath), func(ctx context.Context, t *testctx.T) {
					url := tc.baseURL + "#v0.9.1"
					if tc.subpath != "" {
						url += ":" + tc.subpath
					}

					args := []string{"fn", "--dir", url}
					if tc.subpath == "" {
						args = append(args, "--subpath", ".changes")
					}
					args = append(args, "entries")
					out, err := modGen.With(daggerCallAt(".", args...)).Stdout(ctx)
					require.NoError(t, err)

					require.Contains(t, out, "v0.9.1.md")
					require.NotContains(t, out, "v0.9.2.md")
				})
			}
		})
	})

	t.Run("git arg inputs", func(ctx context.Context, t *testctx.T) {
		t.Run("local dir", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				With(withModuleFixture(t, c, "/work", "go/call-git-arg")).
				WithWorkdir("/work").
				WithExec([]string{"git", "add", "."}).
				WithExec([]string{"git", "commit", "-m", "initial commit"}).
				WithExec([]string{"git", "branch", "ye-olde"}).
				WithExec([]string{"git", "add", "."}).
				WithExec([]string{"git", "commit", "-m", "my content"})

			mainSha, err := modGen.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
			require.NoError(t, err)
			require.Regexp(t, `^[a-f0-9]{40}$`, strings.TrimSpace(mainSha))
			yeOldeSha, err := modGen.WithExec([]string{"git", "rev-parse", "ye-olde"}).Stdout(ctx)
			require.NoError(t, err)
			require.Regexp(t, `^[a-f0-9]{40}$`, strings.TrimSpace(yeOldeSha))

			out, err := modGen.With(daggerCallAt(".", "fn-repo", "--repo", ".git", "file", "--path=.git/HEAD", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "ref: refs/heads/master", strings.TrimSpace(out))

			out, err = modGen.With(daggerCallAt(".", "fn-ref", "--ref", ".git", "file", "--path=.git/HEAD", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "ref: refs/heads/master", strings.TrimSpace(out))
			out, err = modGen.With(daggerCallAt(".", "fn-ref", "--ref", ".git#ye-olde", "file", "--path=.git/HEAD", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "ref: refs/heads/ye-olde", strings.TrimSpace(out))
		})

		t.Run("remote git", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				With(withModuleFixture(t, c, "/work", "go/call-git-arg")).
				WithWorkdir("/work")

			remote := "https://github.com/dagger/dagger.git"
			out, err := modGen.With(daggerCallAt(".", "fn-repo", "--repo", remote, "file", "--path=.git/HEAD", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "ref: refs/heads/main", strings.TrimSpace(out))

			out, err = modGen.With(daggerCallAt(".", "fn-ref", "--ref", remote, "file", "--path=.git/HEAD", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "ref: refs/heads/main", strings.TrimSpace(out))
			out, err = modGen.With(daggerCallAt(".", "fn-ref", "--ref", remote+"#v0.16.2", "file", "--path=.git/HEAD", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "b3e6f765a547b22edc61a24336177348b9f00d94", strings.TrimSpace(out))
		})
	})

	t.Run("file arg inputs", func(ctx context.Context, t *testctx.T) {
		t.Run("abs path", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := moduleFixture(t, c, "go/call-file-arg").
				WithNewFile("/dir/subdir/foo.txt", "foo").
				WithNewFile("/root/foo.txt", "foo")

			out, err := modGen.With(daggerCallAt(".", "fn", "--file", "/dir/subdir/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)

			out, err = modGen.With(daggerCallAt(".", "fn", "--file", "file:///dir/subdir/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)
		})

		t.Run("expand home directory", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := moduleFixture(t, c, "go/call-file-arg").
				WithNewFile("/root/foo.txt", "foo")
			out, err := modGen.With(daggerCallAt(".", "fn", "--file", "~/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)
		})

		t.Run("rel path", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				With(withModuleFixture(t, c, "/work/dir", "go/call-file-arg")).
				WithWorkdir("/work/dir").
				WithNewFile("/work/otherdir/foo.txt", "foo").
				WithNewFile("/work/dir/subdir/blah.txt", "blah")

			out, err := modGen.With(daggerCallAt(".", "fn", "--file", "../otherdir/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)

			out, err = modGen.With(daggerCallAt(".", "fn", "--file", "file://../otherdir/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)

			out, err = modGen.With(daggerCallAt(".", "fn", "--file", "subdir/blah.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "blah", out)

			out, err = modGen.With(daggerCallAt(".", "fn", "--file", "file://subdir/blah.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "blah", out)
		})
	})

	t.Run("secret args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-secret-arg").
			WithEnvVariable("TOPSECRET", "shhh").
			WithNewFile("/mysupersecret", "file shhh").
			WithNewFile("/root/homesupersecret", "file shhh")

		t.Run("env", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "insecure", "--token", "env://TOPSECRET")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "shhh", out)
		})

		t.Run("env (legacy explicit)", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "insecure", "--token", "env:TOPSECRET")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "shhh", out)
		})

		t.Run("env (legacy implicit)", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "insecure", "--token", "TOPSECRET")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "shhh", out)
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "insecure", "--token", "file:///mysupersecret")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "file shhh", out)

			out, err = modGen.With(daggerCallAt(".", "insecure", "--token", "file://~/homesupersecret")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "file shhh", out)
		})

		t.Run("file (legacy)", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "insecure", "--token", "file:/mysupersecret")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "file shhh", out)

			out, err = modGen.With(daggerCallAt(".", "insecure", "--token", "file:~/homesupersecret")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "file shhh", out)
		})

		t.Run("cmd", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "insecure", "--token", "cmd://echo -n cmd shhh")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "cmd shhh", out)
		})

		t.Run("cmd (legacy)", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "insecure", "--token", "cmd:echo -n cmd shhh")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "cmd shhh", out)
		})
	})

	t.Run("cache volume args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		volName := identity.NewID()

		modGen := moduleFixture(t, c, "go/call-cache-volume")

		out, err := modGen.With(daggerCallAt(".", "cacher", "--cache", volName, "--val", "foo")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\n", out)
		out, err = modGen.With(daggerCallAt(".", "cacher", "--cache", volName, "--val", "bar")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\nbar\n", out)
	})

	t.Run("platform args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-platform")

		t.Run("default input value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-platform")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "linux/arm64", out)
		})

		t.Run("valid input", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-platform", "--platform", "linux/amd64")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "linux/amd64", out)
		})

		t.Run("value from host", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-platform", "--platform", "current")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, platforms.DefaultString(), out)
		})

		t.Run("invalid input", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCallAt(".", "from-platform", "--platform", "invalid")).Stdout(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")
		})

		t.Run("valid output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "to-platform", "--platform", "linux/amd64")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "linux/amd64", out)
		})

		t.Run("invalid output", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCallAt(".", "to-platform", "--platform", "invalid")).Stdout(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")
		})
	})

	t.Run("platform list args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-platform-list")

		t.Run("default input value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-platforms", "--json")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `["linux/arm64", "linux/amd64"]`, out)
		})

		t.Run("valid input", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-platforms", "--platforms", "linux/amd64,linux/arm64", "--json")).Stdout(ctx)
			require.NoError(t, err)
			// different order from default on purpose
			require.JSONEq(t, `["linux/amd64", "linux/arm64"]`, out)
		})

		t.Run("value from host", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-platforms", "--platforms", "linux/amd64,current", "--json")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, fmt.Sprintf(`["linux/amd64", "%s"]`, platforms.DefaultString()), out)
		})

		t.Run("invalid input", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCallAt(".", "from-platforms", "--platforms", "invalid")).Stdout(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")
		})

		t.Run("valid output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "to-platforms", "--platforms", "linux/amd64,linux/arm64", "--json")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `["linux/amd64", "linux/arm64"]`, out)
		})

		t.Run("invalid output", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCallAt(".", "to-platforms", "--platforms", "invalid")).Stdout(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")
		})
	})

	t.Run("build args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("Dockerfile", `
FROM scratch

ARG OS=linux
ARG ARCH=amd64

ENV PLATFORM=${OS}/${ARCH}
`,
			)

		t.Run("invalid value", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerExec(
				"core", "host",
				"directory", "--path", ".",
				"docker-build", "--build-args", "darwin",
				"env-variable", "--name", "PLATFORM",
			)).Sync(ctx)
			requireErrOut(t, err, "must be formatted as name=value")
		})

		t.Run("missing name", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerExec(
				"core", "host",
				"directory", "--path", ".",
				"docker-build", "--build-args", "=darwin",
				"env-variable", "--name", "PLATFORM",
			)).Sync(ctx)
			requireErrOut(t, err, "cannot have an empty name")
		})

		t.Run("single value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerExec(
				"core", "host",
				"directory", "--path", ".",
				"docker-build", "--build-args", "OS=darwin",
				"env-variable", "--name", "PLATFORM",
			)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "darwin/amd64", out)
		})

		t.Run("multiple values", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerExec(
				"core", "host",
				"directory", "--path", ".",
				"docker-build", "--build-args", "OS=darwin,ARCH=arm64",
				"env-variable", "--name", "PLATFORM",
			)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "darwin/arm64", out)
		})
	})

	t.Run("enum args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-enum")

		t.Run("valid input", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-proto", "--proto", "TCP")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "TCP", out)
		})

		t.Run("invalid input", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCallAt(".", "from-proto", "--proto", "INVALID")).Stdout(ctx)
			requireErrOut(t, err, "value should be one of")
			requireErrOut(t, err, "TCP")
			requireErrOut(t, err, "UDP")
		})

		t.Run("default input value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-proto")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "UDP", out)
		})

		t.Run("valid output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "to-proto", "--proto", "TCP")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "TCP", out)
		})

		t.Run("invalid output", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCallAt(".", "to-proto", "--proto", "INVALID")).Stdout(ctx)
			requireErrOut(t, err, "invalid enum")
		})

		t.Run("choices in help", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-proto", "--help")).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "TCP")
			require.Contains(t, out, "UDP")
		})
	})

	t.Run("custom enum args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-custom-enum")

		t.Run("valid input", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-status", "--status", "ACTIVE")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "ACTIVE", out)
		})

		t.Run("invalid input", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCallAt(".", "from-status", "--status", "INVALID")).Stdout(ctx)
			requireErrOut(t, err, "value should be one of")
			requireErrOut(t, err, "ACTIVE")
			requireErrOut(t, err, "INACTIVE")
		})

		t.Run("default input value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-status")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "INACTIVE", out)
		})

		t.Run("valid output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "to-status", "--status", "ACTIVE")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "ACTIVE", out)
		})

		t.Run("invalid output", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCallAt(".", "to-status", "--status", "INVALID")).Stdout(ctx)
			requireErrOut(t, err, "invalid enum")
		})

		t.Run("choices in help", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "from-status", "--help")).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "ACTIVE")
			require.Contains(t, out, "INACTIVE")
		})
	})

	t.Run("module args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		modGen := goGitBase(t, c).
			With(withModuleFixture(t, c, "/work", "go/call-module-args")).
			WithWorkdir("/work")

		out, err := modGen.With(daggerCallAt(".", "mod-src", "--mod-src", ".", "context-directory", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, strings.Join([]string{
			".git/",
			".gitattributes",
			".gitignore",
			"dagger.json",
			"foo.txt",
			"go.mod",
			"go.sum",
			"internal/",
			"main.go",
		}, "\n"), strings.TrimSpace(out))

		out, err = modGen.With(daggerCallAt(".", "mod", "--module", ".", "source", "context-directory", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, strings.Join([]string{
			".git/",
			".gitattributes",
			".gitignore",
			"dagger.json",
			"foo.txt",
			"go.mod",
			"go.sum",
			"internal/",
			"main.go",
		}, "\n"), strings.TrimSpace(out))
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("module args", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			modGen := goGitBase(t, c).
				With(privateSetup).
				With(withModuleFixture(t, c, "/work", "go/call-module-args")).
				WithWorkdir("/work")

			out, err := modGen.With(daggerCallAt(".", "mod-src", "--mod-src", testGitModuleRef(tc, "top-level"), "as-string")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, testGitModuleRef(tc, "top-level"), out)

			out, err = modGen.With(daggerCallAt(".", "mod", "--module", testGitModuleRef(tc, "top-level"), "source", "as-string")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, testGitModuleRef(tc, "top-level"), out)
		})
	})
}

func (CallSuite) TestSocketArg(ctx context.Context, t *testctx.T) {
	getHostSocket := func(t *testctx.T) (string, func()) {
		sockDir := t.TempDir()
		sockPath := filepath.Join(sockDir, "host.sock")
		sock, err := net.Listen("unix", sockPath)
		require.NoError(t, err)
		t.Cleanup(func() {
			sock.Close()
		})

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				conn, err := sock.Accept()
				if err != nil {
					if !errors.Is(err, net.ErrClosed) {
						t.Logf("failed to accept connection: %v", err)
					}
					return
				}

				_, err = conn.Write([]byte("yoyoyo"))
				if err != nil {
					conn.Close()
					t.Logf("failed to write to connection: %v", err)
					return
				}
				conn.Close()
			}
		}()

		return sockPath, func() {
			sock.Close()
			wg.Wait()
		}
	}

	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		copyTestdataFixture(ctx, t, modDir, "modules", "go/call-socket-basic")

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()

		_, err := hostDaggerExec(ctx, t, modDir, "call", "-m", ".", "fn", "--sock", sockPath)
		require.NoError(t, err)
	})

	t.Run("passed to another module", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		copyTestdataFixture(ctx, t, modDir, "modules", "go/call-socket-dep-sock")

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()

		_, err := hostDaggerExec(ctx, t, modDir, "call", "-m", ".", "fn", "--sock", sockPath)
		require.NoError(t, err)
	})

	t.Run("passed embedded in arg", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		copyTestdataFixture(ctx, t, modDir, "modules", "go/call-socket-dep-container")

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()

		_, err := hostDaggerExec(ctx, t, modDir, "call", "-m", ".", "fn", "--sock", sockPath)
		require.NoError(t, err)
	})

	t.Run("passed back and forth", func(ctx context.Context, t *testctx.T) {
		// Pass a container and a socket, have the caller attach the socket to the container, return that
		// and then have the caller use it. This is mainly meant to exercise the code-paths involving
		// client resources that a given client already knows about being handled when returned back to them
		// via a call return value.
		modDir := t.TempDir()
		copyTestdataFixture(ctx, t, modDir, "modules", "go/call-socket-back-and-forth")

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()

		_, err := hostDaggerExec(ctx, t, modDir, "call", "-m", ".", "fn", "--sock", sockPath)
		require.NoError(t, err)
	})

	t.Run("nested exec", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()

		_, err := moduleFixture(t, c, "go/call-socket-nested").
			WithUnixSocket("/nested.sock", c.Host().UnixSocket(sockPath)).
			With(daggerCallAt(".", "fn", "--sock", "/nested.sock")).
			Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("no implicit host access", func(ctx context.Context, t *testctx.T) {
		// verify that a socket ID from one session cannot be reused in another
		// session to actually communicate with the host socket
		idModDir := t.TempDir()
		copyTestdataFixture(ctx, t, idModDir, "modules", "go/call-socket-id")

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()
		sockID, err := hostDaggerOutput(ctx, t, idModDir, "call", "-m", ".", "fn", "--sockPath", sockPath)
		require.NoError(t, err)

		modDir := t.TempDir()
		copyTestdataFixture(ctx, t, modDir, "modules", "go/call-socket-no-access")

		_, err = hostDaggerExec(ctx, t, modDir, "call", "-m", ".", "fn", "--sockID", strings.TrimSpace(string(sockID)))
		require.NoError(t, err)
	})
}

func (CallSuite) TestReturnTypes(ctx context.Context, t *testctx.T) {
	t.Run("return list objects", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-return-list-objects")

		logGen(ctx, t, modGen.Directory("."))
		expected := "0\n1\n2\n"
		expectedJSON := `[{"bar": 0}, {"bar": 1}, {"bar": 2}]`

		t.Run("default", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Regexp(t, strings.Repeat(`- MinimalFoo@xxh3:[a-f0-9]{16}\n`, 3), out)
		})

		t.Run("print", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "fn", "bar")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, expected, out)
		})

		t.Run("output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.
				With(daggerCallAt(".", "fn", "bar", "-o", "./outfile")).
				File("./outfile").
				Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, expected, out)
		})

		t.Run("json", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.
				With(daggerCallAt(".", "fn", "bar", "--json")).
				Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, expectedJSON, out)
		})
	})

	t.Run("return container", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-return-container")

		logGen(ctx, t, modGen.Directory("."))

		t.Run("default", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "ctr")).Stdout(ctx)
			require.NoError(t, err)
			require.Regexp(t, `Container@xxh3:[a-f0-9]{16}`, out)
		})

		t.Run("exec", func(ctx context.Context, t *testctx.T) {
			// Container doesn't show output but executes withExecs
			out, err := modGen.With(daggerCallAt(".", "fail")).Stdout(ctx)
			requireErrOut(t, err, "goodbye")
			require.NotContains(t, out, "goodbye")
		})

		t.Run("output", func(ctx context.Context, t *testctx.T) {
			modGen, err := modGen.With(daggerCallAt(".", "ctr", "-o", "./container.tar")).Sync(ctx)
			require.NoError(t, err)
			size, err := modGen.File("./container.tar").Size(ctx)
			require.NoError(t, err)
			require.Greater(t, size, 0)
			_, err = modGen.WithExec([]string{"tar", "tf", "./container.tar", "oci-layout"}).Sync(ctx)
			require.NoError(t, err)
		})
	})

	t.Run("return directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-return-directory")

		logGen(ctx, t, modGen.Directory("."))

		t.Run("default", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "dir")).Stdout(ctx)
			require.NoError(t, err)
			require.Regexp(t, `Directory@xxh3:[a-f0-9]{16}`, out)
		})

		t.Run("output", func(ctx context.Context, t *testctx.T) {
			modGen, err := modGen.With(daggerCallAt(".", "dir", "-o", "./outdir")).Sync(ctx)
			require.NoError(t, err)

			entries, err := modGen.Directory("./outdir").Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, "bar.txt\nfoo.txt", strings.Join(entries, "\n"))

			foo, err := modGen.Directory("./outdir").File("foo.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", foo)

			bar, err := modGen.Directory("./outdir").File("bar.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "bar", bar)
		})
	})

	t.Run("return file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-return-file")

		logGen(ctx, t, modGen.Directory("."))

		t.Run("default", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "file")).Stdout(ctx)
			require.NoError(t, err)
			require.Regexp(t, `File@xxh3:[a-f0-9]{16}`, out)
		})

		t.Run("output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.
				With(daggerCallAt(".", "file", "-o", "./outfile")).
				File("./outfile").
				Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)
		})
	})

	t.Run("return secret", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-return-secret")

		t.Run("single", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.
				With(daggerCallAt(".", "secret")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Regexp(t, `Secret@xxh3:[a-f0-9]{16}`, out)
		})

		t.Run("multiple", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.
				With(daggerCallAt(".", "secrets")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Regexp(t, strings.Repeat(`- Secret@xxh3:[a-f0-9]{16}\n`, 2), out)
		})
	})

	t.Run("sync", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-sync")

		// adding sync disables the default behavior of **not** printing the ID
		// just verify it works without error for now
		_, err := modGen.With(daggerCallAt(".", "ctr", "sync")).Stdout(ctx)
		require.NoError(t, err)
	})
}

func (CallSuite) TestCoreChaining(ctx context.Context, t *testctx.T) {
	t.Run("container", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-core-container")

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "ctr", "file", "--path=/etc/alpine-release", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, distconsts.AlpineVersion, strings.TrimSpace(out))
		})

		t.Run("export", func(ctx context.Context, t *testctx.T) {
			modGen, err := modGen.With(daggerCallAt(".", "ctr", "export", "--path=./container.tar.gz")).Sync(ctx)
			require.NoError(t, err)
			size, err := modGen.File("./container.tar.gz").Size(ctx)
			require.NoError(t, err)
			require.Greater(t, size, 0)
		})
	})

	t.Run("directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-return-directory")

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "dir", "file", "--path=foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)
		})

		t.Run("export", func(ctx context.Context, t *testctx.T) {
			modGen, err := modGen.With(daggerCallAt(".", "dir", "export", "--path=./outdir")).Sync(ctx)
			require.NoError(t, err)
			ents, err := modGen.Directory("./outdir").Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"bar.txt", "foo.txt"}, ents)
		})
	})

	t.Run("return file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-return-file")

		t.Run("size", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCallAt(".", "file", "size")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "3", out)
		})

		t.Run("export", func(ctx context.Context, t *testctx.T) {
			modGen, err := modGen.With(daggerCallAt(".", "file", "export", "--path=./outfile")).Sync(ctx)
			require.NoError(t, err)
			contents, err := modGen.File("./outfile").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", contents)
		})
	})
}

func (CallSuite) TestReturnObject(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/call-return-object")

	t.Run("main object", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall()).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Query\n", out)
	})

	t.Run("no scalars", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(".", "foo")).Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, `TestFoo@xxh3:[a-f0-9]{16}`, out)
	})

	t.Run("list of objects", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(".", "files")).Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, strings.Repeat(`- File@xxh3:[a-f0-9]{16}\n`, 2), out)
	})
}

func (CallSuite) TestSaveOutput(ctx context.Context, t *testctx.T) {
	// NB: Normal usage is tested in TestModuleDaggerCallReturnTypes.

	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/call-save-output")

	logGen(ctx, t, modGen.Directory("."))

	t.Run("truncate file", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			WithNewFile("foo.txt", "foobar").
			With(daggerCallAt(".", "hello", "-o", "foo.txt")).
			File("foo.txt").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello", out)
	})

	t.Run("not a file", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerCallAt(".", "hello", "-o", ".")).Sync(ctx)
		requireErrOut(t, err, "is a directory")
	})

	t.Run("allow dir for file", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerCallAt(".", "file", "-o", ".")).
			File("foo.txt").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo", out)
	})

	t.Run("create parent dirs", func(ctx context.Context, t *testctx.T) {
		ctr, err := modGen.With(daggerCallAt(".", "hello", "-o", "foo/bar.txt")).Sync(ctx)
		require.NoError(t, err)

		t.Run("print success", func(ctx context.Context, t *testctx.T) {
			// should print success to stderr so it doesn't interfere with piping output
			out, err := ctr.Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, out, `Saved output to "/work/foo/bar.txt"`)
		})

		t.Run("check directory permissions", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.WithExec([]string{"stat", "-c", "%a", "foo"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "755\n", out)
		})

		t.Run("check file permissions", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.WithExec([]string{"stat", "-c", "%a", "foo/bar.txt"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "644\n", out)
		})
	})

	t.Run("check umask", func(ctx context.Context, t *testctx.T) {
		ctr, err := modGen.
			WithNewFile(
				"/entrypoint.sh",
				`#!/bin/sh
umask 027
exec "$@"
`,
				dagger.ContainerWithNewFileOpts{Permissions: 0o750},
			).
			WithEntrypoint([]string{"/entrypoint.sh"}).
			With(daggerCallAt(".", "hello", "-o", "/tmp/foo/bar.txt")).
			Sync(ctx)
		require.NoError(t, err)

		t.Run("directory", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.WithExec([]string{"stat", "-c", "%a", "/tmp/foo"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "750\n", out)
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.WithExec([]string{"stat", "-c", "%a", "/tmp/foo/bar.txt"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "640\n", out)
		})
	})
}

func (CallSuite) TestByName(ctx context.Context, t *testctx.T) {
	t.Run("local", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := moduleFixture(t, c, "go/call-by-name-local")

		out, err := ctr.With(daggerCallAt("foo", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from mod-a", strings.TrimSpace(out))

		out, err = ctr.With(daggerCallAt("bar", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from mod-b", strings.TrimSpace(out))
	})

	t.Run("local with absolute paths", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := moduleFixture(t, c, "go/call-by-name-absolute")

		// Check dagger.json for absolute paths
		jsonContent, err := ctr.File("/work/dagger.json").Contents(ctx)
		require.NoError(t, err)

		var config map[string]any
		err = json.Unmarshal([]byte(jsonContent), &config)
		require.NoError(t, err)

		dependencies, ok := config["dependencies"].([]any)
		require.True(t, ok, "dependencies should be an array")

		for _, dep := range dependencies {
			depMap, ok := dep.(map[string]any)
			require.True(t, ok, "each dependency should be a map")

			source, ok := depMap["source"].(string)
			require.True(t, ok, "source should be a string")

			require.False(t, filepath.IsAbs(source), "dependency source should not be an absolute path")
		}

		// call main module at /work path
		out, err := ctr.With(daggerCallAt("foo", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from mod-a", strings.TrimSpace(out))

		out, err = ctr.With(daggerCallAt("bar", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from mod-b", strings.TrimSpace(out))

		// call submodules module with absolute path
		out, err = ctr.With(daggerCallAt("/work/mod-a", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from mod-a", strings.TrimSpace(out))

		out, err = ctr.With(daggerCallAt("/work/mod-b", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from mod-b", strings.TrimSpace(out))
	})

	t.Run("local with absolute paths linking to modules outside of root", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(withModuleFixture(t, c, "/work", "go/call-by-name-outside-root")).
			With(withModuleFixture(t, c, "/outside/mod-a", "go/call-by-name-mod-a")).
			WithWorkdir("/work")

		// call main module at /work path
		_, err := ctr.With(daggerCallAt("foo", "fn")).Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, `local module dependency context directory "/outside/mod-a" is not in parent context directory "/work"`)
	})

	t.Run("local ref with @", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := moduleFixture(t, c, "go/call-by-name-at")

		// call main module at /work path
		out, err := ctr.With(daggerCallAt("foo", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from mod-a", strings.TrimSpace(out))
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("git", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			ctr := goGitBase(t, c).
				With(privateSetup).
				With(withModuleFixture(t, c, "/work", "go/call-by-name-git")).
				WithWorkdir("/work").
				With(configFile(".", &modules.ModuleConfig{
					Name:   "test",
					SDK:    &modules.SDK{Source: "go"},
					Source: ".",
					Dependencies: []*modules.ModuleConfigDependency{
						{Name: "foo", Source: testGitModuleRef(tc, "")},
						{Name: "bar", Source: testGitModuleRef(tc, "subdir/dep2")},
					},
				}))

			out, err := ctr.With(daggerCallAt("foo", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi from root hi from dep hi from dep2", strings.TrimSpace(out))

			out, err = ctr.With(daggerCallAt("bar", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi from dep2", strings.TrimSpace(out))
		})
	})
}

func (CallSuite) TestGitMod(ctx context.Context, t *testctx.T) {
	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("go", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			out, err := goGitBase(t, c).
				With(privateSetup).
				With(daggerCallAt(testGitModuleRef(tc, "top-level"), "fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi from top level hi from dep hi from dep2", strings.TrimSpace(out))
		})

		t.Run("go dep", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			out, err := goGitBase(t, c).
				With(privateSetup).
				With(withModuleFixture(t, c, "/work", "go/call-gitmod-go-dep")).
				WithWorkdir("/work").
				With(configFile(".", &modules.ModuleConfig{
					Name:   "foo",
					SDK:    &modules.SDK{Source: "go"},
					Source: ".",
					Dependencies: []*modules.ModuleConfigDependency{{
						Name:   "top-level",
						Source: testGitModuleRef(tc, "top-level"),
					}},
				})).
				With(daggerCallAt(".", "fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi from top level hi from dep hi from dep2", strings.TrimSpace(out))
		})

		t.Run("typescript", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			out, err := goGitBase(t, c).
				With(privateSetup).
				With(daggerCallAt(testGitModuleRef(tc, "ts"), "container-echo", "--string-arg", "yoyo", "stdout")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yoyo", strings.TrimSpace(out))
		})

		t.Run("typescript dep", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			out, err := goGitBase(t, c).
				With(privateSetup).
				With(withModuleFixture(t, c, "/work", "go/call-gitmod-ts-dep")).
				WithWorkdir("/work").
				With(configFile(".", &modules.ModuleConfig{
					Name:   "foo",
					SDK:    &modules.SDK{Source: "go"},
					Source: ".",
					Dependencies: []*modules.ModuleConfigDependency{{
						Name:   "test",
						Source: testGitModuleRef(tc, "ts"),
					}},
				})).
				With(daggerCallAt(".", "fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yoyoyo", strings.TrimSpace(out))
		})

		t.Run("python", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			out, err := goGitBase(t, c).
				With(privateSetup).
				With(daggerCallAt(testGitModuleRef(tc, "py"), "container-echo", "--string-arg", "yoyo", "stdout")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yoyo", strings.TrimSpace(out))
		})

		t.Run("python dep", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			out, err := goGitBase(t, c).
				With(privateSetup).
				With(withModuleFixture(t, c, "/work", "go/call-gitmod-py-dep")).
				WithWorkdir("/work").
				With(configFile(".", &modules.ModuleConfig{
					Name:   "foo",
					SDK:    &modules.SDK{Source: "go"},
					Source: ".",
					Dependencies: []*modules.ModuleConfigDependency{{
						Name:   "test",
						Source: testGitModuleRef(tc, "py"),
					}},
				})).
				With(daggerCallAt(".", "fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yoyoyo", strings.TrimSpace(out))
		})
	})
}

func (CallSuite) TestFindup(ctx context.Context, t *testctx.T) {
	prep := func(t *testctx.T) (*dagger.Client, *safeBuffer, *dagger.Container) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(&logs))

		mod := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(withModuleFixture(t, c, "/work", "go/call-findup")).
			WithWorkdir("/work")
		return c, &logs, mod
	}

	t.Run("workdir subdir", func(ctx context.Context, t *testctx.T) {
		_, _, mod := prep(t)
		out, err := mod.
			WithWorkdir("/work/some/subdir").
			With(daggerCallAt(".", "container-echo", "--string-arg", "yo", "stdout")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo", strings.TrimSpace(out))
	})

	t.Run("explicit subdir", func(ctx context.Context, t *testctx.T) {
		c, _, mod := prep(t)
		out, err := mod.
			WithDirectory("/work/some/subdir", c.Directory()).
			With(daggerCallAt("some/subdir", "container-echo", "--string-arg", "yo", "stdout")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo", strings.TrimSpace(out))
	})

	t.Run("non-existent subdir", func(ctx context.Context, t *testctx.T) {
		c, logs, mod := prep(t)
		_, err := mod.
			With(daggerCallAt("bad/subdir", "container-echo", "--string-arg", "yo", "stdout")).
			Stdout(ctx)
		require.Error(t, err)
		require.NoError(t, c.Close())
		require.Contains(t, logs.String(), `"bad/subdir" does not exist`)
	})
}

func (CallSuite) TestUnsupportedFunctions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/call-unsupported-functions")

	t.Run("functions list", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(".", "--help")).Stdout(ctx)
		require.NoError(t, err)

		require.Contains(t, out, "echo")
		require.Contains(t, out, "Sanity check")

		require.NotContains(t, out, "fn-a")
		require.NotContains(t, out, "Skips adding the function")

		require.Contains(t, out, "fn-b")
		require.Contains(t, out, "Skips adding the optional flag")
	})

	t.Run("arguments list", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(".", "fn-b", "--help")).Stdout(ctx)
		require.NoError(t, err)

		require.Contains(t, out, "--msg")
		require.NotContains(t, out, "--matrix")
	})

	t.Run("in chain", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(".", "fn-b", "--msg", "", "echo", "--msg", "hello")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello")
	})

	t.Run("no sub-command", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerCallAt(".", "fn-a")).Sync(ctx)
		requireErrOut(t, err, `unknown command "fn-a"`)
	})

	t.Run("no flag", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerCallAt(".", "fn-b", "--msg", "hello", "--matrix", "")).Sync(ctx)
		requireErrOut(t, err, `unknown flag: --matrix`)
	})
}

func (CallSuite) TestWeirdEnum(ctx context.Context, t *testctx.T) {
	t.Run("duplicated enum value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-weird-enum-duplicate")

		_, err := modGen.With(daggerCallAt(".", "--help")).Stdout(ctx)
		requireErrOut(t, err, `enum "ACTIVE" is already defined with value "ACTIVE"`)
	})

	t.Run("weird value", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []struct {
			enumValue string
			fixture   string
		}{
			{enumValue: "1ACTIVE", fixture: "go/call-weird-enum-value-1"},
			{enumValue: "#ACTIVE", fixture: "go/call-weird-enum-value-2"},
			{enumValue: " ACTIVE", fixture: "go/call-weird-enum-value-3"},
			{enumValue: "ACTI#E", fixture: "go/call-weird-enum-value-4"},
			{enumValue: "ACTIVE ", fixture: "go/call-weird-enum-value-5"},
			{enumValue: "foo bar", fixture: "go/call-weird-enum-value-6"},
		} {
			t.Run(tc.enumValue, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				modGen := moduleFixture(t, c, tc.fixture)

				_, err := modGen.With(daggerCallAt(".", "--help")).Stdout(ctx)
				require.NoError(t, err)
			})
		}
	})

	t.Run("empty value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "go/call-weird-enum-empty")

		_, err := modGen.With(daggerCallAt(".", "--help")).Stdout(ctx)
		require.NoError(t, err)

		out, err := modGen.With(daggerCallAt(".", "from-status", "--status=value")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})
}

func (CallSuite) TestEnumList(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		sdk     string
		fixture string
	}{
		{sdk: "go", fixture: "go/call-enum-list"},
		{sdk: "python", fixture: "python/call-enum-list"},
		{sdk: "typescript", fixture: "typescript/call-enum-list"},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := moduleFixture(t, c, tc.fixture)

			t.Run("default input", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCallAt(".", "faves")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "GO PYTHON", out)
			})

			t.Run("happy input", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCallAt(".", "faves", "--langs", "TYPESCRIPT,PHP")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "TYPESCRIPT PHP", out)
			})

			t.Run("sad input", func(ctx context.Context, t *testctx.T) {
				_, err := modGen.With(daggerCallAt(".", "faves", "--langs", "GO,FOO,BAR")).Sync(ctx)
				requireErrOut(t, err, "invalid argument")
				requireErrOut(t, err, "should be one of ELIXIR,GO,PHP,PYTHON,TYPESCRIPT")
			})

			t.Run("output", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCallAt(".", "official")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "GO\nPYTHON\nTYPESCRIPT\n", out)
			})
		})
	}
}

func (CallSuite) TestExit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	_, err := moduleFixture(t, c, "go/call-exit").
		With(daggerCallAt(".", "quit")).
		Sync(ctx)

	var exErr *dagger.ExecError
	require.ErrorAs(t, err, &exErr)
	require.Equal(t, 6, exErr.ExitCode)
}

func (CallSuite) TestCore(ctx context.Context, t *testctx.T) {
	t.Run("call container", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			With(daggerExec(
				"core", "container",
				"from", "--address", alpineImage,
				"file", "--path", "/etc/os-release",
				"contents",
			)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Alpine Linux")
	})
}

func (CallSuite) TestExecStderr(ctx context.Context, t *testctx.T) {
	t.Run("no TUI", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := daggerCliBase(t, c).
			With(daggerExec(
				"core", "--silent",
				"container",
				"from", "--address", alpineImage,
				"with-exec", "--args", "ls,wat-call1",
				"stdout",
			)).
			Sync(ctx)

		requireErrOut(t, err, "ls: wat-call1: No such file or directory")
	})

	t.Run("plain", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := daggerCliBase(t, c).
			With(daggerExec(
				"core", "--progress", "plain",
				"container",
				"from", "--address", alpineImage,
				"with-exec", "--args", "ls,wat-call2",
				"stdout",
			)).
			Sync(ctx)

		requireErrOut(t, err, "ls: wat-call2: No such file or directory")
	})
}

func (CallSuite) TestErrNoModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerCall()).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "Query\n", out)
}
