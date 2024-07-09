package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/testctx"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"dagger.io/dagger"
)

func (ModuleSuite) TestDaggerCallHelp(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := modInit(t, c, "go", `package main

func New(source *Directory) *Test {
    return &Test{
        Source: source,
    }
}

type Test struct {
    Source *Directory
}

func (m *Test) Container() *Container {
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

func (ModuleSuite) TestDaggerCallArgTypes(ctx context.Context, t *testctx.T) {
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
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, svc *Service) (string, error) {
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
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, svc *Service) (string, error) {
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
)

type Minimal struct {}

func (m *Minimal) Hello(msgs []string) string {
	return strings.Join(msgs, "+")
}

func (m *Minimal) Reads(ctx context.Context, files []File) (string, error) {
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
type Test struct {}

func (m *Test) Fn(dir *Directory) *Directory {
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
type Test struct {}

func (m *Test) Fn(dir *Directory) *Directory {
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
type Test struct {}

func (m *Test) Fn(dir *Directory) *Directory {
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
type Test struct {}

func (m *Test) Fn(
	dir *Directory,
	subpath string, // +optional
) *Directory {
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
type Test struct {}

func (m *Test) Fn(file *File) *File {
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
type Test struct {}

func (m *Test) Fn(file *File) *File {
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
type Test struct {}

func (m *Test) Fn(file *File) *File {
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

import "context"

type Test struct {}

func (m *Test) Insecure(ctx context.Context, token *Secret) (string, error) {
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
				require.ErrorContains(t, err, `secret env var not found: "NOW..."`)
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
				require.ErrorContains(t, err, `secret env var not found: "NOW..."`)
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
				require.ErrorContains(t, err, `failed to read secret file "/nowheretobefound": open /nowheretobefound: no such file or directory`)
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
				require.ErrorContains(t, err, `failed to run secret command "exit 1": exit status 1`)
			})
		})

		t.Run("invalid source", func(ctx context.Context, t *testctx.T) {
			_, err := modGen.With(daggerCall("insecure", "--token", "wtf:HUH")).Stdout(ctx)
			require.ErrorContains(t, err, `unsupported secret arg source: "wtf"`)
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
)

type Test struct {}

func (m *Test) Cacher(ctx context.Context, cache *CacheVolume, val string) (string, error) {
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

type Test struct {}

func (m *Test) FromPlatform(platform Platform) string {
	return string(platform)
}

func (m *Test) ToPlatform(platform string) Platform {
	return Platform(platform)
}
`,
			)

		out, err := modGen.With(daggerCall("from-platform", "--platform", "linux/amd64")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "linux/amd64", out)
		out, err = modGen.With(daggerCall("from-platform", "--platform", "current")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, platforms.DefaultString(), out)
		_, err = modGen.With(daggerCall("from-platform", "--platform", "invalid")).Stdout(ctx)
		require.ErrorContains(t, err, "unknown operating system or architecture")

		out, err = modGen.With(daggerCall("to-platform", "--platform", "linux/amd64")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "linux/amd64", out)
		_, err = modGen.With(daggerCall("to-platform", "--platform", "invalid")).Stdout(ctx)
		require.ErrorContains(t, err, "unknown operating system or architecture")
	})

	t.Run("enum args", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

type Test struct {}

func (m *Test) FromProto(proto NetworkProtocol) string {
	return string(proto)
}

func (m *Test) ToProto(proto string) NetworkProtocol {
	return NetworkProtocol(proto)
}
`,
			)

		out, err := modGen.With(daggerCall("from-proto", "--proto", "TCP")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "TCP", out)
		_, err = modGen.With(daggerCall("from-proto", "--proto", "INVALID")).Stdout(ctx)
		require.ErrorContains(t, err, "value should be one of")
		require.ErrorContains(t, err, "TCP")
		require.ErrorContains(t, err, "UDP")

		out, err = modGen.With(daggerCall("to-proto", "--proto", "TCP")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "TCP", out)
		_, err = modGen.With(daggerCall("to-proto", "--proto", "INVALID")).Stdout(ctx)
		require.ErrorContains(t, err, "invalid enum value")

		out, err = modGen.With(daggerCall("from-proto", "--help")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "TCP")
		require.Contains(t, out, "UDP")
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

func (m *Test) FromStatus(status Status) string {
	return string(status)
}

func (m *Test) ToStatus(status string) Status {
	return Status(status)
}
`,
			)

		out, err := modGen.With(daggerCall("from-status", "--status", "ACTIVE")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ACTIVE", out)
		_, err = modGen.With(daggerCall("from-status", "--status", "INVALID")).Stdout(ctx)
		require.ErrorContains(t, err, "value should be one of")
		require.ErrorContains(t, err, "ACTIVE")
		require.ErrorContains(t, err, "INACTIVE")

		out, err = modGen.With(daggerCall("to-status", "--status", "ACTIVE")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ACTIVE", out)
		_, err = modGen.With(daggerCall("to-status", "--status", "INVALID")).Stdout(ctx)
		require.ErrorContains(t, err, "invalid enum value")

		out, err = modGen.With(daggerCall("from-status", "--help")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "ACTIVE")
		require.Contains(t, out, "INACTIVE")
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("module args", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("foo.txt", "foo").
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (m *Test) ModSrc(ctx context.Context, modSrc *ModuleSource) *ModuleSource {
	return modSrc
}

func (m *Test) Mod(ctx context.Context, module *Module) *Module {
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

func (ModuleSuite) TestDaggerCallReturnTypes(ctx context.Context, t *testctx.T) {
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

func New() *Test {
	return &Test{Ctr: dag.Container().From("%s").WithExec([]string{"echo", "hello", "world"})}
}

type Test struct {
	Ctr *Container
}
`, alpineImage),
			)

		logGen(ctx, t, modGen.Directory("."))

		t.Run("default", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ctr")).Stdout(ctx)
			require.NoError(t, err)
			actual := gjson.Get(out, "[@this].#(_type==Container).stdout").String()
			require.Equal(t, "hello world\n", actual)
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

func New() *Test {
	return &Test{
		Dir: dag.Directory().WithNewFile("foo.txt", "foo").WithNewFile("bar.txt", "bar"),
	}
}

type Test struct {
	Dir *Directory
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

func New() *Test {
	return &Test{
		File: dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt"),
	}
}

type Test struct {
	File *File
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

	t.Run("sync", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", fmt.Sprintf(`package main

func New() *Test {
	return &Test{Ctr: dag.Container().From("%s").WithExec([]string{"echo", "hello", "world"})}
}

type Test struct {
	Ctr *Container
}
`, alpineImage),
			)

		// adding sync disables the default behavior of **not** printing the ID
		// just verify it works without error for now
		_, err := modGen.With(daggerCall("ctr", "sync")).Stdout(ctx)
		require.NoError(t, err)
	})
}

func (ModuleSuite) TestDaggerCallCoreChaining(ctx context.Context, t *testctx.T) {
	t.Run("container", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", fmt.Sprintf(`package main

func New() *Test {
	return &Test{Ctr: dag.Container().From("%s")}
}

type Test struct {
	Ctr *Container
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

func New() *Test {
	return &Test{
		Dir: dag.Directory().WithNewFile("foo.txt", "foo").WithNewFile("bar.txt", "bar"),
	}
}

type Test struct {
	Dir *Directory
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

func New() *Test {
	return &Test{
		File: dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt"),
	}
}

type Test struct {
	File *File
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

func (ModuleSuite) TestDaggerCallReturnObject(ctx context.Context, t *testctx.T) {
	// NB: Container, Directory and File are tested in TestDaggerCallReturnTypes.

	c := connect(ctx, t)

	modGen := modInit(t, c, "go", `package main

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

func (t *Test) Files() []*File {
    return []*File{
        dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt"),
        dag.Directory().WithNewFile("bar.txt", "bar").File("bar.txt"),
    }
}

type Foo struct {
    Ctr *Container
}
`,
	)

	t.Run("main object", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall()).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Test", gjson.Get(out, "_type").String())
		require.Equal(t, alpineImage, gjson.Get(out, "baseImage").String())
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

func (ModuleSuite) TestDaggerCallSaveOutput(ctx context.Context, t *testctx.T) {
	// NB: Normal usage is tested in TestModuleDaggerCallReturnTypes.

	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main

type Test struct {
}

func (t *Test) Hello() string {
    return "hello"
}

func (t *Test) File() *File {
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
		require.ErrorContains(t, err, "is a directory")
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

func (ModuleSuite) TestCallByName(ctx context.Context, t *testctx.T) {
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
			With(daggerExec("init")).
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
			With(daggerExec("init")).
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
			With(daggerExec("init")).
			With(daggerExec("install", "--name", "foo", "/outside/mod-a"))

		// call main module at /work path
		_, err := ctr.With(daggerCallAt("foo", "fn")).Stdout(ctx)
		require.Error(t, err)
		require.ErrorContains(t, err, `local module dep source path "../outside/mod-a" escapes context "/work"`)
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("git", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init")).
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

func (ModuleSuite) TestCallGitMod(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("go", func(ctx context.Context, t *testctx.T) {
			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				With(daggerCallAt(testGitModuleRef(tc, "top-level"), "fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi from top level hi from dep hi from dep2", strings.TrimSpace(out))
		})

		t.Run("typescript", func(ctx context.Context, t *testctx.T) {
			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				With(daggerCallAt(testGitModuleRef(tc, "ts"), "container-echo", "--string-arg", "yoyo", "stdout")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yoyo", strings.TrimSpace(out))
		})

		t.Run("python", func(ctx context.Context, t *testctx.T) {
			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				With(daggerCallAt(testGitModuleRef(tc, "py"), "container-echo", "--string-arg", "yoyo", "stdout")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yoyo", strings.TrimSpace(out))
		})
	})
}

func (ModuleSuite) TestCallFindup(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=foo", "--sdk=go")).
		WithWorkdir("/work/some/subdir").
		With(daggerCall("container-echo", "--string-arg", "yo", "stdout")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "yo", strings.TrimSpace(out))
}

func (ModuleSuite) TestCallUnsupportedFunctions(ctx context.Context, t *testctx.T) {
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
		require.ErrorContains(t, err, `unknown command "fn-a"`)
	})

	t.Run("no flag", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerCall("fn-b", "--msg", "hello", "--matrix", "")).Sync(ctx)
		require.ErrorContains(t, err, `unknown flag: --matrix`)
	})
}

func (ModuleSuite) TestCallInvalidEnum(ctx context.Context, t *testctx.T) {
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
		require.ErrorContains(t, err, `enum value "ACTIVE" is already defined`)
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
				require.ErrorContains(t, err, fmt.Sprintf("enum value %q is not valid", tc.enumValue))
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
		require.ErrorContains(t, err, "enum value must not be empty")
	})
}
