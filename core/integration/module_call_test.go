package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/testctx"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"dagger.io/dagger"
)

type CallSuite struct{}

func TestCall(t *testing.T) {
	testctx.Run(testCtx, t, CallSuite{}, Middleware()...)
}

func (CallSuite) TestHelp(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := modInit(t, c, "go", `package main

import (
	"dagger/test/internal/dagger"
)

func New(source *dagger.Directory) *Test {
    return &Test{
        Source: source,
    }
}

type Test struct {
    Source *dagger.Directory
}

func (m *Test) Container() *dagger.Container {
    return dag.
        Container().
        From("`+alpineImage+`").
        WithDirectory("/src", m.Source)
}
`,
	)

	t.Run("no required arg validation", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("container", "--help")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "USAGE")
		require.Contains(t, out, "dagger call container <function>")
	})

	t.Run("globally parsed", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("container", "--help", "directory")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "USAGE")
		require.Contains(t, out, "dagger call container directory [arguments] <function>")
	})
}

func (CallSuite) TestArgTypes(ctx context.Context, t *testctx.T) {
	t.Run("service args", func(ctx context.Context, t *testctx.T) {
		t.Run("used as service binding", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("main.go", fmt.Sprintf(`package main
import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, svc *dagger.Service) (string, error) {
	return dag.Container().From("%s").WithExec([]string{"apk", "add", "curl"}).
		WithServiceBinding("daserver", svc).
		WithExec([]string{"curl", "http://daserver:8000"}).
		Stdout(ctx)
}
`, alpineImage),
				)

			logGen(ctx, t, modGen.Directory("."))

			httpServer, _ := httpService(ctx, t, c, "im up")
			endpoint, err := httpServer.Endpoint(ctx)
			require.NoError(t, err)

			out, err := modGen.
				WithServiceBinding("testserver", httpServer).
				With(daggerCall("fn", "--svc", "tcp://"+endpoint)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "im up", out)
		})

		t.Run("used directly", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("main.go", `package main
import (
	"context"
	"fmt"
	"strings"
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, svc *dagger.Service) (string, error) {
	ports, err := svc.Ports(ctx)
	if err != nil {
		return "", err
	}
	var out []string
	out = append(out, fmt.Sprintf("%d exposed ports:", len(ports)))
	for _, port := range ports {
		number, err := port.Port(ctx)
		if err != nil {
			return "", err
		}
		out = append(out, fmt.Sprintf("- TCP/%d", number))
	}
	return strings.Join(out, "\n"), nil
}
`,
				)

			logGen(ctx, t, modGen.Directory("."))

			httpServer, _ := httpService(ctx, t, c, "im up")
			endpoint, err := httpServer.Endpoint(ctx)
			require.NoError(t, err)

			out, err := modGen.
				WithServiceBinding("testserver", httpServer).
				With(daggerCall("fn", "--svc", "tcp://"+endpoint)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "1 exposed ports:\n- TCP/8000", out)
		})
	})

	t.Run("list args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
			WithNewFile("foo.txt", "bar").
			WithNewFile("main.go", `package main
import (
	"context"
	"strings"
	"dagger/minimal/internal/dagger"
)

type Minimal struct {}

func (m *Minimal) Hello(msgs []string) string {
	return strings.Join(msgs, "+")
}

func (m *Minimal) Reads(ctx context.Context, files []dagger.File) (string, error) {
	var contents []string
	for _, f := range files {
		content, err := f.Contents(ctx)
		if err != nil {
			return "", err
		}
		contents = append(contents, content)
	}
	return strings.Join(contents, "+"), nil
}
`,
			)

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.With(daggerCall("hello", "--msgs", "yo", "--msgs", "my", "--msgs", "friend")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo+my+friend", out)

		out, err = modGen.With(daggerCall("reads", "--files=foo.txt", "--files=foo.txt")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar+bar", out)
	})

	t.Run("directory arg inputs", func(ctx context.Context, t *testctx.T) {
		t.Run("local dir", func(ctx context.Context, t *testctx.T) {
			t.Run("abs path", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
					WithNewFile("/dir/subdir/foo.txt", "foo").
					WithNewFile("/dir/subdir/bar.txt", "bar").
					WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(dir *dagger.Directory) *dagger.Directory {
	return dir
}
	`,
					)

				out, err := modGen.With(daggerCall("fn", "--dir", "/dir/subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\nfoo.txt\n", out)

				out, err = modGen.With(daggerCall("fn", "--dir", "file:///dir/subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\nfoo.txt\n", out)
			})

			t.Run("expand home directory", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
					WithNewFile("/root/foo.txt", "foo").
					WithNewFile("/root/subdir/bar.txt", "bar").
					WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)
type Test struct {}

func (m *Test) Fn(dir *dagger.Directory) *dagger.Directory {
	return dir
}
`,
					)

				out, err := modGen.With(daggerCall("fn", "--dir", "~", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "foo.txt\nsubdir\n", out)

				out, err = modGen.With(daggerCall("fn", "--dir", "~/subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\n", out)
			})

			t.Run("rel path", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := goGitBase(t, c).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/dir").
					With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
					WithNewFile("/work/otherdir/foo.txt", "foo").
					WithNewFile("/work/otherdir/bar.txt", "bar").
					WithNewFile("/work/dir/subdir/blah.txt", "blah").
					WithNewFile("main.go", `package main
import (
	"dagger/test/internal/dagger"
)
type Test struct {}

func (m *Test) Fn(dir *dagger.Directory) *dagger.Directory {
	return dir
}
	`,
					)

				out, err := modGen.With(daggerCall("fn", "--dir", "../otherdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\nfoo.txt\n", out)

				out, err = modGen.With(daggerCall("fn", "--dir", "file://../otherdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\nfoo.txt\n", out)

				out, err = modGen.With(daggerCall("fn", "--dir", "subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "blah.txt\n", out)

				out, err = modGen.With(daggerCall("fn", "--dir", "file://subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "blah.txt\n", out)
			})
		})

		t.Run("git dir", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("main.go", `package main
import (
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(
	dir *dagger.Directory,
	subpath string, // +optional
) *dagger.Directory {
	if subpath == "" {
		subpath = "."
	}
	return dir.Directory(subpath)
}
	`,
				)

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
				tc := tc
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
					out, err := modGen.With(daggerCall(args...)).Stdout(ctx)
					require.NoError(t, err)

					require.Contains(t, out, "v0.9.1.md")
					require.NotContains(t, out, "v0.9.2.md")
				})
			}
		})
	})

	t.Run("file arg inputs", func(ctx context.Context, t *testctx.T) {
		t.Run("abs path", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/dir/subdir/foo.txt", "foo").
				WithNewFile("/root/foo.txt", "foo").
				WithNewFile("main.go", `package main
import (
	"dagger/test/internal/dagger"
)
type Test struct {}

func (m *Test) Fn(file *dagger.File) *dagger.File {
	return file
}
`,
				)

			out, err := modGen.With(daggerCall("fn", "--file", "/dir/subdir/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)

			out, err = modGen.With(daggerCall("fn", "--file", "file:///dir/subdir/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)
		})

		t.Run("expand home directory", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/root/foo.txt", "foo").
				WithNewFile("main.go", `package main
import (
	"dagger/test/internal/dagger"
)
type Test struct {}

func (m *Test) Fn(file *dagger.File) *dagger.File {
	return file
}
`,
				)
			out, err := modGen.With(daggerCall("fn", "--file", "~/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)
		})

		t.Run("rel path", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dir").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/otherdir/foo.txt", "foo").
				WithNewFile("/work/dir/subdir/blah.txt", "blah").
				WithNewFile("main.go", `package main
import (
	"dagger/test/internal/dagger"
)
type Test struct {}

func (m *Test) Fn(file *dagger.File) *dagger.File {
	return file
}
	`,
				)

			out, err := modGen.With(daggerCall("fn", "--file", "../otherdir/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)

			out, err = modGen.With(daggerCall("fn", "--file", "file://../otherdir/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)

			out, err = modGen.With(daggerCall("fn", "--file", "subdir/blah.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "blah", out)

			out, err = modGen.With(daggerCall("fn", "--file", "file://subdir/blah.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "blah", out)
		})
	})

	t.Run("secret args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Insecure(ctx context.Context, token *dagger.Secret) (string, error) {
	return token.Plaintext(ctx)
}
`,
			).
			WithEnvVariable("TOPSECRET", "shhh").
			WithNewFile("/mysupersecret", "file shhh").
			WithNewFile("/root/homesupersecret", "file shhh")

		t.Run("explicit env", func(ctx context.Context, t *testctx.T) {
			t.Run("happy", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("insecure", "--token", "env:TOPSECRET")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "shhh", out)
			})
			t.Run("sad", func(ctx context.Context, t *testctx.T) {
				_, err := modGen.With(daggerCall("insecure", "--token", "env:NOWHERETOBEFOUND")).Stdout(ctx)
				requireErrOut(t, err, `secret env var not found: "NOW..."`)
			})
		})

		t.Run("implicit env", func(ctx context.Context, t *testctx.T) {
			t.Run("happy", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("insecure", "--token", "TOPSECRET")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "shhh", out)
			})
			t.Run("sad", func(ctx context.Context, t *testctx.T) {
				_, err := modGen.With(daggerCall("insecure", "--token", "NOWHERETOBEFOUND")).Stdout(ctx)
				requireErrOut(t, err, `secret env var not found: "NOW..."`)
			})
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			t.Run("happy", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("insecure", "--token", "file:/mysupersecret")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "file shhh", out)

				out, err = modGen.With(daggerCall("insecure", "--token", "file:~/homesupersecret")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "file shhh", out)
			})
			t.Run("sad", func(ctx context.Context, t *testctx.T) {
				_, err := modGen.With(daggerCall("insecure", "--token", "file:/nowheretobefound")).Stdout(ctx)
				requireErrOut(t, err, `failed to read secret file "/nowheretobefound": open /nowheretobefound: no such file or directory`)
			})
		})

		t.Run("cmd", func(ctx context.Context, t *testctx.T) {
			t.Run("happy", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("insecure", "--token", "cmd:echo -n cmd shhh")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "cmd shhh", out)
			})
			t.Run("sad", func(ctx context.Context, t *testctx.T) {
				_, err := modGen.With(daggerCall("insecure", "--token", "cmd:exit 1")).Stdout(ctx)
				requireErrOut(t, err, `failed to run secret command "exit 1": exit status 1`)
			})
		})

		t.Run("invalid source", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCall("insecure", "--token", "wtf:HUH")).Stdout(ctx)
			requireErrOut(t, err, `unsupported secret arg source: "wtf"`)
		})
	})

	t.Run("cache volume args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		volName := identity.NewID()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Cacher(ctx context.Context, cache *dagger.CacheVolume, val string) (string, error) {
	return dag.Container().
		From("`+alpineImage+`").
		WithMountedCache("/cache", cache).
		WithExec([]string{"sh", "-c", "echo $0 >> /cache/vals", val}).
		WithExec([]string{"cat", "/cache/vals"}).
		Stdout(ctx)
}
`,
			)

		out, err := modGen.With(daggerCall("cacher", "--cache", volName, "--val", "foo")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\n", out)
		out, err = modGen.With(daggerCall("cacher", "--cache", volName, "--val", "bar")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\nbar\n", out)
	})

	t.Run("platform args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) FromPlatform(
    // +default="linux/arm64"
    platform dagger.Platform,
) string {
	return string(platform)
}

func (m *Test) ToPlatform(platform string) dagger.Platform {
	return dagger.Platform(platform)
}
`,
			)

		t.Run("default input value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-platform")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "linux/arm64", out)
		})

		t.Run("valid input", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-platform", "--platform", "linux/amd64")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "linux/amd64", out)
		})

		t.Run("value from host", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-platform", "--platform", "current")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, platforms.DefaultString(), out)
		})

		t.Run("invalid input", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCall("from-platform", "--platform", "invalid")).Stdout(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")
		})

		t.Run("valid output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("to-platform", "--platform", "linux/amd64")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "linux/amd64", out)
		})

		t.Run("invalid output", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCall("to-platform", "--platform", "invalid")).Stdout(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")
		})
	})

	t.Run("platform list args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) FromPlatforms(
    // +default=["linux/arm64", "linux/amd64"]
    platforms []dagger.Platform,
) []string {
    r := make([]string, 0, len(platforms))
    for _, p := range platforms {
        r = append(r, string(p))
    }
	return r
}

func (m *Test) ToPlatforms(platforms []string) []dagger.Platform {
    r := make([]dagger.Platform, 0, len(platforms))
    for _, p := range platforms {
        r = append(r, dagger.Platform(p))
    }
	return r
}
`,
			)

		t.Run("default input value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-platforms", "--json")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `["linux/arm64", "linux/amd64"]`, out)
		})

		t.Run("valid input", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-platforms", "--platforms", "linux/amd64,linux/arm64", "--json")).Stdout(ctx)
			require.NoError(t, err)
			// different order from default on purpose
			require.JSONEq(t, `["linux/amd64", "linux/arm64"]`, out)
		})

		t.Run("value from host", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-platforms", "--platforms", "linux/amd64,current", "--json")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, fmt.Sprintf(`["linux/amd64", "%s"]`, platforms.DefaultString()), out)
		})

		t.Run("invalid input", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCall("from-platforms", "--platforms", "invalid")).Stdout(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")
		})

		t.Run("valid output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("to-platforms", "--platforms", "linux/amd64,linux/arm64", "--json")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `["linux/amd64", "linux/arm64"]`, out)
		})

		t.Run("invalid output", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCall("to-platforms", "--platforms", "invalid")).Stdout(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")
		})
	})

	t.Run("enum args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

type Test struct {}

func (m *Test) FromProto(
	// +default="UDP"
	proto dagger.NetworkProtocol,
) string {
	return string(proto)
}

func (m *Test) ToProto(proto string) dagger.NetworkProtocol {
	return dagger.NetworkProtocol(proto)
}
`,
			)

		t.Run("valid input", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-proto", "--proto", "TCP")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "TCP", out)
		})

		t.Run("invalid input", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCall("from-proto", "--proto", "INVALID")).Stdout(ctx)
			requireErrOut(t, err, "value should be one of")
			requireErrOut(t, err, "TCP")
			requireErrOut(t, err, "UDP")
		})

		t.Run("default input value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-proto")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "UDP", out)
		})

		t.Run("valid output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("to-proto", "--proto", "TCP")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "TCP", out)
		})

		t.Run("invalid output", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCall("to-proto", "--proto", "INVALID")).Stdout(ctx)
			requireErrOut(t, err, "invalid enum value")
		})

		t.Run("choices in help", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-proto", "--help")).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "TCP")
			require.Contains(t, out, "UDP")
		})
	})

	t.Run("custom enum args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

type Status string

const (
	Active Status = "ACTIVE"
	Inactive Status = "INACTIVE"
)

type Test struct {}

func (m *Test) FromStatus(
	// +default="INACTIVE"
	status Status,
) string {
	return string(status)
}

func (m *Test) ToStatus(status string) Status {
	return Status(status)
}
`,
			)

		t.Run("valid input", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-status", "--status", "ACTIVE")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "ACTIVE", out)
		})

		t.Run("invalid input", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCall("from-status", "--status", "INVALID")).Stdout(ctx)
			requireErrOut(t, err, "value should be one of")
			requireErrOut(t, err, "ACTIVE")
			requireErrOut(t, err, "INACTIVE")
		})

		t.Run("default input value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-status")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "INACTIVE", out)
		})

		t.Run("valid output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("to-status", "--status", "ACTIVE")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "ACTIVE", out)
		})

		t.Run("invalid output", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCall("to-status", "--status", "INVALID")).Stdout(ctx)
			requireErrOut(t, err, "invalid enum value")
		})

		t.Run("choices in help", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("from-status", "--help")).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "ACTIVE")
			require.Contains(t, out, "INACTIVE")
		})
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("module args", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
			defer cleanup()

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(mountedSocket).
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("foo.txt", "foo").
				WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) ModSrc(ctx context.Context, modSrc *dagger.ModuleSource) *dagger.ModuleSource {
	return modSrc
}

func (m *Test) Mod(ctx context.Context, module *dagger.Module) *dagger.Module {
	return module
}
`,
				)

			out, err := modGen.With(daggerCall("mod-src", "--mod-src", ".", "directory", "--path", ".", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, ".gitattributes\n.gitignore\nLICENSE\ndagger.gen.go\ndagger.json\nfoo.txt\ngo.mod\ngo.sum\ninternal\nmain.go\n", out)

			out, err = modGen.With(daggerCall("mod", "--module", ".", "source", "directory", "--path", ".", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, ".gitattributes\n.gitignore\nLICENSE\ndagger.gen.go\ndagger.json\nfoo.txt\ngo.mod\ngo.sum\ninternal\nmain.go\n", out)

			out, err = modGen.With(daggerCall("mod-src", "--mod-src", testGitModuleRef(tc, "top-level"), "as-string")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, testGitModuleRef(tc, "top-level"), out)

			out, err = modGen.With(daggerCall("mod", "--module", testGitModuleRef(tc, "top-level"), "source", "as-string")).Stdout(ctx)
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
		err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main

import (
	"context"
	"fmt"

	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, sock *dagger.Socket) error {
	out, err := dag.Container().From("`+alpineImage+`").
		WithExec([]string{"apk", "add", "netcat-openbsd"}).
		WithUnixSocket("/var/run/host.sock", sock).
		WithExec([]string{"nc", "-w", "5", "-U", "/var/run/host.sock"}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	if out != "yoyoyo" {
		return fmt.Errorf("unexpected output: %s", out)
	}
	return nil
}
`), 0o644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()

		_, err = hostDaggerExec(ctx, t, modDir, "call", "fn", "--sock", sockPath)
		require.NoError(t, err)
	})

	t.Run("passed to another module", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		depModDir := filepath.Join(modDir, "dep")
		require.NoError(t, os.MkdirAll(depModDir, 0o755))

		err := os.WriteFile(filepath.Join(depModDir, "main.go"), []byte(`package main

import (
	"context"
	"fmt"

	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (m *Dep) Fn(ctx context.Context, sock *dagger.Socket) error {
	out, err := dag.Container().From("`+alpineImage+`").
		WithExec([]string{"apk", "add", "netcat-openbsd"}).
		WithUnixSocket("/var/run/host.sock", sock).
		WithExec([]string{"nc", "-w", "5", "-U", "/var/run/host.sock"}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	if out != "yoyoyo" {
		return fmt.Errorf("unexpected output: %s", out)
	}
	return nil
}
`), 0o644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, depModDir, "init", "--source=.", "--name=dep", "--sdk=go")
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, sock *dagger.Socket) error {
	return dag.Dep().Fn(ctx, sock)
}
`), 0o644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "install", depModDir)
		require.NoError(t, err)

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()

		_, err = hostDaggerExec(ctx, t, modDir, "call", "fn", "--sock", sockPath)
		require.NoError(t, err)
	})

	t.Run("passed embedded in arg", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		depModDir := filepath.Join(modDir, "dep")
		require.NoError(t, os.MkdirAll(depModDir, 0o755))

		err := os.WriteFile(filepath.Join(depModDir, "main.go"), []byte(`package main

import (
	"context"
	"fmt"

	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (m *Dep) Fn(ctx context.Context, ctr *dagger.Container) error {
	out, err := ctr.Stdout(ctx)
	if err != nil {
		return err
	}
	if out != "yoyoyo" {
		return fmt.Errorf("unexpected output: %s", out)
	}
	return nil
}
`), 0o644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, depModDir, "init", "--source=.", "--name=dep", "--sdk=go")
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, sock *dagger.Socket) error {
	ctr := dag.Container().From("`+alpineImage+`").
		WithExec([]string{"apk", "add", "netcat-openbsd"}).
		WithUnixSocket("/var/run/host.sock", sock).
		WithExec([]string{"nc", "-w", "5", "-U", "/var/run/host.sock"})
	err := dag.Dep().Fn(ctx, ctr)
	return err
}
`), 0o644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "install", depModDir)
		require.NoError(t, err)

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()

		_, err = hostDaggerExec(ctx, t, modDir, "call", "fn", "--sock", sockPath)
		require.NoError(t, err)
	})

	t.Run("passed back and forth", func(ctx context.Context, t *testctx.T) {
		// Pass a container and a socket, have the caller attach the socket to the container, return that
		// and then have the caller use it. This is mainly meant to exercise the code-paths involving
		// client resources that a given client already knows about being handled when returned back to them
		// via a call return value.

		modDir := t.TempDir()
		depModDir := filepath.Join(modDir, "dep")
		require.NoError(t, os.MkdirAll(depModDir, 0o755))

		err := os.WriteFile(filepath.Join(depModDir, "main.go"), []byte(`package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (m *Dep) Fn(ctr *dagger.Container, sock *dagger.Socket) *dagger.Container {
	return ctr.WithUnixSocket("/var/run/host.sock", sock)
}
`), 0o644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, depModDir, "init", "--source=.", "--name=dep", "--sdk=go")
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main

import (
	"context"
	"fmt"

	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, sock *dagger.Socket) error {
	ctr := dag.Container().From("`+alpineImage+`").
		WithExec([]string{"apk", "add", "netcat-openbsd"})
	out, err := dag.Dep().Fn(ctr, sock).
		WithExec([]string{"nc", "-w", "5", "-U", "/var/run/host.sock"}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	if out != "yoyoyo" {
		return fmt.Errorf("unexpected output: %s", out)
	}
	return nil
}
`), 0o644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "install", depModDir)
		require.NoError(t, err)

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()

		_, err = hostDaggerExec(ctx, t, modDir, "call", "fn", "--sock", sockPath)
		require.NoError(t, err)
	})

	t.Run("nested exec", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()

		_, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"

	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, sock *dagger.Socket) error {
	out, err := dag.Container().From("`+alpineImage+`").
		WithExec([]string{"apk", "add", "netcat-openbsd"}).
		WithUnixSocket("/var/run/host.sock", sock).
		WithExec([]string{"nc", "-w", "5", "-U", "/var/run/host.sock"}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	if out != "yoyoyo" {
		return fmt.Errorf("unexpected output: %s", out)
	}
	return nil
}
`,
			).WithUnixSocket("/nested.sock", c.Host().UnixSocket(sockPath)).
			With(daggerCall("fn", "--sock", "/nested.sock")).
			Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("no implicit host access", func(ctx context.Context, t *testctx.T) {
		// verify that a sneaky module can't use raw gql queries to access host sockets that they weren't passed

		runContainerQuery := `query Run($sockID: SocketID!) {
	container {
		from(address: "` + alpineImage + `") {
			withExec(args: ["apk", "add", "netcat-openbsd"]) {
				withUnixSocket(path: "/var/run/host.sock", source: $sockID) {
					withExec(args: ["stat", "/var/run/host.sock"]) {
						stdout
					}
				}
			}
		}
	}
}
`

		modDir := t.TempDir()
		err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/Khan/genqlient/graphql"
)

type Test struct{}

type SocketIDResponse struct {
	Host struct{ 
		UnixSocket struct{ 
			Id string 
		} 
	} 
}

func (m *Test) Fn(ctx context.Context, sockPath string, runContainerQuery string) error {
	sockResp := &SocketIDResponse{}
	resp := &graphql.Response{Data: sockResp}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{host{unixSocket(path:\""+sockPath+"\"){id}}}",
	}, resp)
	if err != nil {
		return fmt.Errorf("get socket id req: %w", err)
	}

	sockID := sockResp.Host.UnixSocket.Id
	if sockID == "" {
		return fmt.Errorf("unexpected response: %+v", resp)
	}

	resp = &graphql.Response{}
	err = dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: runContainerQuery,
		Variables: map[string]interface{}{"sockID": sockID},
	}, resp)
	if err == nil {
		return fmt.Errorf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("socket %s not found", sockPath)) {
		return fmt.Errorf("unexpected error: %w", err)
	}
	return nil
}
`), 0o644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		sockPath, cleanup := getHostSocket(t)
		defer cleanup()

		_, err = hostDaggerExec(ctx, t, modDir, "call", "fn", "--sockPath", sockPath, "--runContainerQuery", runContainerQuery)
		require.NoError(t, err)
	})
}

func (CallSuite) TestReturnTypes(ctx context.Context, t *testctx.T) {
	t.Run("return list objects", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
			WithNewFile("main.go", `package main
type Minimal struct {}

type Foo struct {
	Bar int `+"`"+`json:"bar"`+"`"+`
}

func (m *Minimal) Fn() []*Foo {
	var foos []*Foo
	for i := 0; i < 3; i++ {
		foos = append(foos, &Foo{Bar: i})
	}
	return foos
}
`,
			)

		logGen(ctx, t, modGen.Directory("."))
		expected := "0\n1\n2\n"
		expectedJSON := `[{"bar": 0}, {"bar": 1}, {"bar": 2}]`

		t.Run("default", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("fn")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, expectedJSON, gjson.Get(out, "#.{bar}").Raw)
		})

		t.Run("print", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("fn", "bar")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, expected, out)
		})

		t.Run("output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.
				With(daggerCall("fn", "bar", "-o", "./outfile")).
				File("./outfile").
				Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, expected, out)
		})

		t.Run("json", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.
				With(daggerCall("fn", "bar", "--json")).
				Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, expectedJSON, out)
		})
	})

	t.Run("return container", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", fmt.Sprintf(`package main

import (
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Ctr() *dagger.Container {
    return dag.Container().
        From("%[1]s").
        WithDefaultArgs([]string{"echo", "hello"}).
        WithExec([]string{})
}

func (m *Test) Fail() *dagger.Container {
    return dag.Container().
        From("%[1]s").
        WithExec([]string{"sh", "-c", "echo goodbye; exit 127"})
}
`, alpineImage),
			)

		logGen(ctx, t, modGen.Directory("."))

		t.Run("default", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ctr")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t,
				`["echo", "hello"]`,
				gjson.Get(out, "[@this].#(_type==Container).defaultArgs").Raw,
			)
		})

		t.Run("exec", func(ctx context.Context, t *testctx.T) {
			// Container doesn't show output but executes withExecs
			out, err := modGen.With(daggerCall("fail")).Stdout(ctx)
			requireErrOut(t, err, "goodbye")
			require.NotContains(t, out, "goodbye")
		})

		t.Run("output", func(ctx context.Context, t *testctx.T) {
			modGen, err := modGen.With(daggerCall("ctr", "-o", "./container.tar")).Sync(ctx)
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

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

func New() *Test {
	return &Test{
		Dir: dag.Directory().WithNewFile("foo.txt", "foo").WithNewFile("bar.txt", "bar"),
	}
}

type Test struct {
	Dir *dagger.Directory
}
`,
			)

		logGen(ctx, t, modGen.Directory("."))

		t.Run("default", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("dir")).Stdout(ctx)
			require.NoError(t, err)
			actual := gjson.Get(out, "[@this].#(_type==Directory).entries").Array()
			require.Len(t, actual, 2)
			require.Equal(t, "bar.txt", actual[0].String())
			require.Equal(t, "foo.txt", actual[1].String())
		})

		t.Run("output", func(ctx context.Context, t *testctx.T) {
			modGen, err := modGen.With(daggerCall("dir", "-o", "./outdir")).Sync(ctx)
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

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

func New() *Test {
	return &Test{
		File: dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt"),
	}
}

type Test struct {
	File *dagger.File
}
`,
			)

		logGen(ctx, t, modGen.Directory("."))

		t.Run("default", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("file")).Stdout(ctx)
			require.NoError(t, err)
			actual := gjson.Get(out, "[@this].#(_type==File).name").String()
			require.Equal(t, "foo.txt", actual)
		})

		t.Run("output", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.
				With(daggerCall("file", "-o", "./outfile")).
				File("./outfile").
				Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)
		})
	})

	t.Run("return secret", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := modInit(t, c, "go", `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (*Test) Secret() *dagger.Secret {
    return dag.SetSecret("foo", "bar")
}

func (m *Test) Secrets() []*dagger.Secret {
    return []*dagger.Secret{
        m.Secret(),
    }
}
`,
		)

		t.Run("single", func(context.Context, *testctx.T) {
			out, err := modGen.
				With(daggerCall("secret")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "foo")
			require.NotContains(t, out, "bar")
		})

		t.Run("multiple", func(context.Context, *testctx.T) {
			out, err := modGen.
				With(daggerCall("secrets")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "foo")
			require.NotContains(t, out, "bar")
		})
	})

	t.Run("sync", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", fmt.Sprintf(`package main

import (
	"dagger/test/internal/dagger"
)

func New() *Test {
	return &Test{Ctr: dag.Container().From("%s").WithExec([]string{"echo", "hello", "world"})}
}

type Test struct {
	Ctr *dagger.Container
}
`, alpineImage),
			)

		// adding sync disables the default behavior of **not** printing the ID
		// just verify it works without error for now
		_, err := modGen.With(daggerCall("ctr", "sync")).Stdout(ctx)
		require.NoError(t, err)
	})
}

func (CallSuite) TestCoreChaining(ctx context.Context, t *testctx.T) {
	t.Run("container", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", fmt.Sprintf(`package main

import (
	"dagger/test/internal/dagger"
)

func New() *Test {
	return &Test{Ctr: dag.Container().From("%s")}
}

type Test struct {
	Ctr *dagger.Container
}
`, alpineImage),
			)

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ctr", "file", "--path=/etc/alpine-release", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, distconsts.AlpineVersion, strings.TrimSpace(out))
		})

		t.Run("export", func(ctx context.Context, t *testctx.T) {
			modGen, err := modGen.With(daggerCall("ctr", "export", "--path=./container.tar.gz")).Sync(ctx)
			require.NoError(t, err)
			size, err := modGen.File("./container.tar.gz").Size(ctx)
			require.NoError(t, err)
			require.Greater(t, size, 0)
		})
	})

	t.Run("directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

func New() *Test {
	return &Test{
		Dir: dag.Directory().WithNewFile("foo.txt", "foo").WithNewFile("bar.txt", "bar"),
	}
}

type Test struct {
	Dir *dagger.Directory
}
`,
			)

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("dir", "file", "--path=foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)
		})

		t.Run("export", func(ctx context.Context, t *testctx.T) {
			modGen, err := modGen.With(daggerCall("dir", "export", "--path=./outdir")).Sync(ctx)
			require.NoError(t, err)
			ents, err := modGen.Directory("./outdir").Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"bar.txt", "foo.txt"}, ents)
		})
	})

	t.Run("return file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

func New() *Test {
	return &Test{
		File: dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt"),
	}
}

type Test struct {
	File *dagger.File
}
`,
			)

		t.Run("size", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("file", "size")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "3", out)
		})

		t.Run("export", func(ctx context.Context, t *testctx.T) {
			modGen, err := modGen.With(daggerCall("file", "export", "--path=./outfile")).Sync(ctx)
			require.NoError(t, err)
			contents, err := modGen.File("./outfile").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", contents)
		})
	})
}

func (CallSuite) TestReturnObject(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := modInit(t, c, "go", `package main

import (
	"dagger/test/internal/dagger"
)

func New() *Test {
    return &Test{
        BaseImage: "`+alpineImage+`",
    }
}

type Test struct {
    BaseImage string
}

func (t *Test) Foo() *Foo {
    return &Foo{Ctr: dag.Container().From(t.BaseImage)}
}

func (t *Test) Files() []*dagger.File {
    return []*dagger.File{
        dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt"),
        dag.Directory().WithNewFile("bar.txt", "bar").File("bar.txt"),
    }
}

func (*Test) Deploy() string {
    return "here be dragons!"
}

type Foo struct {
    Ctr *dagger.Container
}
`,
	)

	t.Run("main object", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall()).Stdout(ctx)
		require.NoError(t, err)
		// Deploy function should not be included
		require.JSONEq(t, fmt.Sprintf(`{"_type": "Test", "baseImage": "%s"}`, alpineImage), out)
	})

	t.Run("no scalars", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("foo")).Stdout(ctx)
		require.NoError(t, err)
		// At minimum should print the type of the object
		require.JSONEq(t, `{"_type": "TestFoo"}`, out)
	})

	t.Run("list of objects", func(ctx context.Context, t *testctx.T) {
		expected := []string{"foo.txt", "bar.txt"}
		out, err := modGen.With(daggerCall("files")).Stdout(ctx)
		require.NoError(t, err)
		actual := gjson.Get(out, "@this").Array()
		require.Len(t, actual, len(expected))
		for i, res := range actual {
			require.Equal(t, "File", res.Get("_type").String())
			require.Equal(t, expected[i], res.Get("name").String())
		}
	})
}

func (CallSuite) TestSaveOutput(ctx context.Context, t *testctx.T) {
	// NB: Normal usage is tested in TestModuleDaggerCallReturnTypes.

	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct {
}

func (t *Test) Hello() string {
    return "hello"
}

func (t *Test) File() *dagger.File {
    return dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt")
}
`,
		)

	logGen(ctx, t, modGen.Directory("."))

	t.Run("truncate file", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			WithNewFile("foo.txt", "foobar").
			With(daggerCall("hello", "-o", "foo.txt")).
			File("foo.txt").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello", out)
	})

	t.Run("not a file", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerCall("hello", "-o", ".")).Sync(ctx)
		requireErrOut(t, err, "is a directory")
	})

	t.Run("allow dir for file", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerCall("file", "-o", ".")).
			File("foo.txt").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo", out)
	})

	t.Run("create parent dirs", func(ctx context.Context, t *testctx.T) {
		ctr, err := modGen.With(daggerCall("hello", "-o", "foo/bar.txt")).Sync(ctx)
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
			With(daggerCall("hello", "-o", "/tmp/foo/bar.txt")).
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

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/mod-a").
			With(daggerExec("init", "--source=.", "--name=mod-a", "--sdk=go")).
			WithNewFile("/work/mod-a/main.go", `package main

			import "context"

			type ModA struct {}

			func (m *ModA) Fn(ctx context.Context) string {
				return "hi from mod-a"
			}
			`,
			).
			WithWorkdir("/work/mod-b").
			With(daggerExec("init", "--source=.", "--name=mod-b", "--sdk=go")).
			WithNewFile("/work/mod-b/main.go", `package main

			import "context"

			type ModB struct {}

			func (m *ModB) Fn(ctx context.Context) string {
				return "hi from mod-b"
			}
			`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.")).
			With(daggerExec("install", "--name", "foo", "./mod-a")).
			With(daggerExec("install", "--name", "bar", "./mod-b"))

		out, err := ctr.With(daggerCallAt("foo", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from mod-a", strings.TrimSpace(out))

		out, err = ctr.With(daggerCallAt("bar", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from mod-b", strings.TrimSpace(out))
	})

	t.Run("local with absolute paths", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/mod-a").
			With(daggerExec("init", "--source=.", "--name=mod-a", "--sdk=go")).
			WithNewFile("/work/mod-a/main.go", `package main

			import "context"

			type ModA struct {}

			func (m *ModA) Fn(ctx context.Context) string {
				return "hi from mod-a"
			}
			`,
			).
			WithWorkdir("/work/mod-b").
			With(daggerExec("init", "--source=.", "--name=mod-b", "--sdk=go")).
			WithNewFile("/work/mod-b/main.go", `package main

			import "context"

			type ModB struct {}

			func (m *ModB) Fn(ctx context.Context) string {
				return "hi from mod-b"
			}
			`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.")).
			With(daggerExec("install", "--name", "foo", "/work/mod-a")).
			With(daggerExec("install", "--name", "bar", "/work/mod-b"))

		// Check dagger.json for absolute paths
		jsonContent, err := ctr.File("/work/dagger.json").Contents(ctx)
		require.NoError(t, err)

		var config map[string]interface{}
		err = json.Unmarshal([]byte(jsonContent), &config)
		require.NoError(t, err)

		dependencies, ok := config["dependencies"].([]interface{})
		require.True(t, ok, "dependencies should be an array")

		for _, dep := range dependencies {
			depMap, ok := dep.(map[string]interface{})
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
			WithWorkdir("/outside/mod-a").
			With(daggerExec("init", "--source=.", "--name=mod-a", "--sdk=go")).
			WithNewFile("/outside/mod-a/main.go", `package main

			import "context"

			type ModA struct {}

			func (m *ModA) Fn(ctx context.Context) string {
				return "hi from mod-a"
			}
			`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.")).
			With(daggerExec("install", "--name", "foo", "/outside/mod-a"))

		// call main module at /work path
		_, err := ctr.With(daggerCallAt("foo", "fn")).Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, `local module dep source path "../outside/mod-a" escapes context "/work"`)
	})

	t.Run("local ref with @", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/").
			With(daggerExec("init", "--source=test@test", "--name=mod-a", "--sdk=go", "test@test")).
			WithNewFile("/work/test@test/main.go", `package main

			import "context"

			type ModA struct {}

			func (m *ModA) Fn(ctx context.Context) string {
				return "hi from mod-a"
			}
			`,
			).
			With(daggerExec("init", "--source=.")).
			With(daggerExec("install", "--name", "foo", "/work/test@test"))

		// call main module at /work path
		out, err := ctr.With(daggerCallAt("foo", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from mod-a", strings.TrimSpace(out))
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("git", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
			defer cleanup()

			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(mountedSocket).
				With(daggerExec("init", "--source=.")).
				With(daggerExec("install", "--name", "foo", testGitModuleRef(tc, ""))).
				With(daggerExec("install", "--name", "bar", testGitModuleRef(tc, "subdir/dep2")))

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
			mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
			defer cleanup()

			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				With(mountedSocket).
				With(daggerCallAt(testGitModuleRef(tc, "top-level"), "fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi from top level hi from dep hi from dep2", strings.TrimSpace(out))
		})

		t.Run("typescript", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
			defer cleanup()

			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				With(mountedSocket).
				With(daggerCallAt(testGitModuleRef(tc, "ts"), "container-echo", "--string-arg", "yoyo", "stdout")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yoyo", strings.TrimSpace(out))
		})

		t.Run("python", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
			defer cleanup()

			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				With(mountedSocket).
				With(daggerCallAt(testGitModuleRef(tc, "py"), "container-echo", "--string-arg", "yoyo", "stdout")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yoyo", strings.TrimSpace(out))
		})
	})
}

func (CallSuite) TestFindup(ctx context.Context, t *testctx.T) {
	prep := func(t *testctx.T) (*dagger.Client, *safeBuffer, *dagger.Container) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(&logs))

		mod := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=foo", "--sdk=go"))
		return c, &logs, mod
	}

	t.Run("workdir subdir", func(ctx context.Context, t *testctx.T) {
		_, _, mod := prep(t)
		out, err := mod.
			WithWorkdir("/work/some/subdir").
			With(daggerCall("container-echo", "--string-arg", "yo", "stdout")).
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
		require.Contains(t, logs.String(), "failed to lstat bad/subdir")
	})
}

func (CallSuite) TestUnsupportedFunctions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := modInit(t, c, "go", `package main

type Test struct {}

// Sanity check
func (m *Test) Echo(msg string) string {
    return msg
}

// Skips adding the function
func (m *Test) FnA(msg string, matrix [][]string) string {
    return msg
}

// Skips adding the optional flag
func (m *Test) FnB(
    msg string,
    // +optional
    matrix [][]string,
) *Chain {
    return new(Chain)
}

type Chain struct {}

// Repeat message back
func (m *Chain) Echo(msg string) string {
    return msg
}
`,
	)

	t.Run("functions list", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("--help")).Stdout(ctx)
		require.NoError(t, err)

		require.Contains(t, out, "echo")
		require.Contains(t, out, "Sanity check")

		require.NotContains(t, out, "fn-a")
		require.NotContains(t, out, "Skips adding the function")

		require.Contains(t, out, "fn-b")
		require.Contains(t, out, "Skips adding the optional flag")
	})

	t.Run("arguments list", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("fn-b", "--help")).Stdout(ctx)
		require.NoError(t, err)

		require.Contains(t, out, "--msg")
		require.NotContains(t, out, "--matrix")
	})

	t.Run("in chain", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("fn-b", "--msg", "", "echo", "--msg", "hello")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello")
	})

	t.Run("no sub-command", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerCall("fn-a")).Sync(ctx)
		requireErrOut(t, err, `unknown command "fn-a"`)
	})

	t.Run("no flag", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerCall("fn-b", "--msg", "hello", "--matrix", "")).Sync(ctx)
		requireErrOut(t, err, `unknown flag: --matrix`)
	})
}

func (CallSuite) TestInvalidEnum(ctx context.Context, t *testctx.T) {
	t.Run("duplicated enum value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := modInit(t, c, "go", `package main

type Status string

const (
	Active Status = "ACTIVE"
	Inactive Status = "INACTIVE"
	Duplicated Status = "ACTIVE"
)

type Test struct{}

func (m *Test) FromStatus(status Status) string {
	return string(status)
}

func (m *Test) ToStatus(status string) Status {
	return Status(status)
}
	`)

		_, err := modGen.With(daggerCall("--help")).Stdout(ctx)
		requireErrOut(t, err, `enum value "ACTIVE" is already defined`)
	})

	t.Run("invalid value", func(ctx context.Context, t *testctx.T) {
		type testCase struct {
			enumValue string
		}

		// Test few invalid values
		for _, tc := range []testCase{
			{
				enumValue: "1ACTIVE",
			},
			{
				enumValue: "#ACTIVE",
			},
			{
				enumValue: " ACTIVE",
			},
			{
				enumValue: "ACTI#E",
			},
			{
				enumValue: "ACTIVE ",
			},
			{
				enumValue: "foo bar",
			},
		} {
			tc := tc

			t.Run(tc.enumValue, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := modInit(t, c, "go", fmt.Sprintf(`package main

type Status string

const (
	Value Status = "%s"
)

type Test struct{}

func (m *Test) FromStatus(status Status) string {
	return string(status)
}
`, tc.enumValue))

				_, err := modGen.With(daggerCall("--help")).Stdout(ctx)
				requireErrOut(t, err, fmt.Sprintf("enum value %q is not valid", tc.enumValue))
			})
		}
	})

	t.Run("empty value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := modInit(t, c, "go", `package main

type Status string

const (
	Value Status = ""
)

type Test struct{}

func (m *Test) FromStatus(status Status) string {
	return string(status)
}
`)

		_, err := modGen.With(daggerCall("--help")).Stdout(ctx)
		requireErrOut(t, err, "enum value must not be empty")
	})
}

func (CallSuite) TestEnumList(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}
	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import (
	"fmt"
	"strings"
)

type Language string

const (
	Go Language = "GO"
	Python Language = "PYTHON"
	TypeScript Language = "TYPESCRIPT"
	PHP Language = "PHP"
	Elixir Language = "ELIXIR"
)

type Test struct{}

func (m *Test) Faves(
    // +default=["GO", "PYTHON"]
    langs []Language,
) string {
	return strings.Trim(fmt.Sprint(langs), "[]")
}

func (m *Test) Official() []Language {
	return []Language{Go, Python, TypeScript}
}
`,
		},
		{
			sdk: "python",
			source: `from typing import Final

import dagger
from dagger import dag


@dagger.enum_type
class Language(dagger.Enum):
    GO = "GO" 
    PYTHON = "PYTHON"
    TYPESCRIPT = "TYPESCRIPT"
    PHP = "PHP"
    ELIXIR = "ELIXIR"


FAVES: Final = [Language.GO, Language.PYTHON]


@dagger.object_type
class Test:
    @dagger.function
    def faves(self, langs: list[Language] = FAVES) -> str:
        return " ".join(langs)

    @dagger.function
    def official(self) -> list[Language]:
        return [Language.GO, Language.PYTHON, Language.TYPESCRIPT]
`,
		},
		{
			sdk: "typescript",
			source: `import { dag, enumType, func, object } from "@dagger.io/dagger"

@enumType()
export class Language {
  static readonly Go: string = "GO"
  static readonly Python: string = "PYTHON"
  static readonly TypeScript: string = "TYPESCRIPT"
  static readonly PHP: string = "PHP"
  static readonly Elixir: string = "ELIXIR"
}

@object()
export class Test {
  @func()
  faves(langs: Language[] = ["GO", "PYTHON"]): string {
    return langs.join(" ")
  }

  @func()
  official(): Language[] {
    return [Language.Go, Language.Python, Language.TypeScript]
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := modInit(t, c, tc.sdk, tc.source)

			t.Run("default input", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("faves")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "GO PYTHON", out)
			})

			t.Run("happy input", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("faves", "--langs", "TYPESCRIPT,PHP")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "TYPESCRIPT PHP", out)
			})

			t.Run("sad input", func(ctx context.Context, t *testctx.T) {
				_, err := modGen.With(daggerCall("faves", "--langs", "GO,FOO,BAR")).Sync(ctx)
				requireErrOut(t, err, "invalid argument")
				requireErrOut(t, err, "should be one of GO,PYTHON,TYPESCRIPT,PHP,ELIXIR")
			})

			t.Run("output", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("official")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "GO\nPYTHON\nTYPESCRIPT\n", out)
			})
		})
	}
}

func (CallSuite) TestExit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	_, err := modInit(t, c, "go", `package main

import "os"

type Test struct {}

func (m *Test) Quit() {
	os.Exit(6)
}
`,
	).
		With(daggerCall("quit")).
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
				"with-exec", "--args", "ls,wat",
				"stdout",
			)).
			Sync(ctx)

		requireErrOut(t, err, "ls: wat: No such file or directory")
	})

	t.Run("plain", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := daggerCliBase(t, c).
			With(daggerExec(
				"core", "--progress", "plain",
				"container",
				"from", "--address", alpineImage,
				"with-exec", "--args", "ls,wat",
				"stdout",
			)).
			Sync(ctx)

		requireErrOut(t, err, "ls: wat: No such file or directory")
	})
}
