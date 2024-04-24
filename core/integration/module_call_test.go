package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func TestModuleDaggerCallArgTypes(t *testing.T) {
	t.Parallel()

	t.Run("service args", func(t *testing.T) {
		t.Parallel()

		t.Run("used as service binding", func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main
import (
	"context"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, svc *Service) (string, error) {
	return dag.Container().From("alpine:3.18").WithExec([]string{"apk", "add", "curl"}).
		WithServiceBinding("daserver", svc).
		WithExec([]string{"curl", "http://daserver:8000"}).
		Stdout(ctx)
}
`,
				})

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

		t.Run("used directly", func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main
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
				})

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

	t.Run("list args", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
			WithNewFile("foo.txt", dagger.ContainerWithNewFileOpts{
				Contents: "bar",
			}).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main
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
			})

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.With(daggerCall("hello", "--msgs", "yo", "--msgs", "my", "--msgs", "friend")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo+my+friend", out)

		out, err = modGen.With(daggerCall("reads", "--files=foo.txt", "--files=foo.txt")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar+bar", out)
	})

	t.Run("directory arg inputs", func(t *testing.T) {
		t.Parallel()

		t.Run("local dir", func(t *testing.T) {
			t.Parallel()

			t.Run("abs path", func(t *testing.T) {
				c, ctx := connect(t)

				modGen := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
					WithNewFile("/dir/subdir/foo.txt", dagger.ContainerWithNewFileOpts{
						Contents: "foo",
					}).
					WithNewFile("/dir/subdir/bar.txt", dagger.ContainerWithNewFileOpts{
						Contents: "bar",
					}).
					WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
						Contents: `package main
type Test struct {}

func (m *Test) Fn(dir *Directory) *Directory {
	return dir
}
	`,
					})

				out, err := modGen.With(daggerCall("fn", "--dir", "/dir/subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\nfoo.txt\n", out)

				out, err = modGen.With(daggerCall("fn", "--dir", "file:///dir/subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\nfoo.txt\n", out)
			})

			t.Run("rel path", func(t *testing.T) {
				t.Parallel()

				c, ctx := connect(t)

				modGen := goGitBase(t, c).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/dir").
					With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
					WithNewFile("/work/otherdir/foo.txt", dagger.ContainerWithNewFileOpts{
						Contents: "foo",
					}).
					WithNewFile("/work/otherdir/bar.txt", dagger.ContainerWithNewFileOpts{
						Contents: "bar",
					}).
					WithNewFile("/work/dir/subdir/blah.txt", dagger.ContainerWithNewFileOpts{
						Contents: "blah",
					}).
					WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
						Contents: `package main
type Test struct {}

func (m *Test) Fn(dir *Directory) *Directory {
	return dir
}
	`,
					})

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

		t.Run("git dir", func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main
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
				})

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
				t.Run(fmt.Sprintf("%s:%s", tc.baseURL, tc.subpath), func(t *testing.T) {
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

	t.Run("file arg inputs", func(t *testing.T) {
		t.Parallel()

		t.Run("abs path", func(t *testing.T) {
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/dir/subdir/foo.txt", dagger.ContainerWithNewFileOpts{
					Contents: "foo",
				}).
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main
type Test struct {}

func (m *Test) Fn(file *File) *File {
	return file
}
	`,
				})

			out, err := modGen.With(daggerCall("fn", "--file", "/dir/subdir/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)

			out, err = modGen.With(daggerCall("fn", "--file", "file:///dir/subdir/foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)
		})

		t.Run("rel path", func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dir").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/otherdir/foo.txt", dagger.ContainerWithNewFileOpts{
					Contents: "foo",
				}).
				WithNewFile("/work/dir/subdir/blah.txt", dagger.ContainerWithNewFileOpts{
					Contents: "blah",
				}).
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main
type Test struct {}

func (m *Test) Fn(file *File) *File {
	return file
}
	`,
				})

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

	t.Run("secret args", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import "context"

type Test struct {}

func (m *Test) Insecure(ctx context.Context, token *Secret) (string, error) {
	return token.Plaintext(ctx)
}
`,
			}).
			WithEnvVariable("TOPSECRET", "shhh").
			WithNewFile("/mysupersecret", dagger.ContainerWithNewFileOpts{Contents: "file shhh"})

		t.Run("explicit env", func(t *testing.T) {
			t.Run("happy", func(t *testing.T) {
				out, err := modGen.With(daggerCall("insecure", "--token", "env:TOPSECRET")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "shhh", out)
			})
			t.Run("sad", func(t *testing.T) {
				_, err := modGen.With(daggerCall("insecure", "--token", "env:NOWHERETOBEFOUND")).Stdout(ctx)
				require.ErrorContains(t, err, `secret env var not found: "NOW..."`)
			})
		})

		t.Run("implicit env", func(t *testing.T) {
			t.Parallel()
			t.Run("happy", func(t *testing.T) {
				out, err := modGen.With(daggerCall("insecure", "--token", "TOPSECRET")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "shhh", out)
			})
			t.Run("sad", func(t *testing.T) {
				_, err := modGen.With(daggerCall("insecure", "--token", "NOWHERETOBEFOUND")).Stdout(ctx)
				require.ErrorContains(t, err, `secret env var not found: "NOW..."`)
			})
		})

		t.Run("file", func(t *testing.T) {
			t.Run("happy", func(t *testing.T) {
				out, err := modGen.With(daggerCall("insecure", "--token", "file:/mysupersecret")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "file shhh", out)
			})
			t.Run("sad", func(t *testing.T) {
				_, err := modGen.With(daggerCall("insecure", "--token", "file:/nowheretobefound")).Stdout(ctx)
				require.ErrorContains(t, err, `failed to read secret file "/nowheretobefound": open /nowheretobefound: no such file or directory`)
			})
		})

		t.Run("cmd", func(t *testing.T) {
			t.Run("happy", func(t *testing.T) {
				out, err := modGen.With(daggerCall("insecure", "--token", "cmd:echo -n cmd shhh")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "cmd shhh", out)
			})
			t.Run("sad", func(t *testing.T) {
				_, err := modGen.With(daggerCall("insecure", "--token", "cmd:exit 1")).Stdout(ctx)
				require.ErrorContains(t, err, `failed to run secret command "exit 1": exit status 1`)
			})
		})

		t.Run("invalid source", func(t *testing.T) {
			_, err := modGen.With(daggerCall("insecure", "--token", "wtf:HUH")).Stdout(ctx)
			require.ErrorContains(t, err, `unsupported secret arg source: "wtf"`)
		})
	})

	t.Run("cache volume args", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		volName := identity.NewID()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import (
	"context"
)

type Test struct {}

func (m *Test) Cacher(ctx context.Context, cache *CacheVolume, val string) (string, error) {
	return dag.Container().
		From("` + alpineImage + `").
		WithMountedCache("/cache", cache).
		WithExec([]string{"sh", "-c", "echo $0 >> /cache/vals", val}).
		WithExec([]string{"cat", "/cache/vals"}).
		Stdout(ctx)
}
`,
			})

		out, err := modGen.With(daggerCall("cacher", "--cache", volName, "--val", "foo")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\n", out)
		out, err = modGen.With(daggerCall("cacher", "--cache", volName, "--val", "bar")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\nbar\n", out)
	})

	t.Run("platform args", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

type Test struct {}

func (m *Test) FromPlatform(platform Platform) string {
	return string(platform)
}

func (m *Test) ToPlatform(platform string) Platform {
	return Platform(platform)
}
`,
			})

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
		_, err = modGen.With(daggerCall("from-platform", "--platform", "invalid")).Stdout(ctx)
		require.ErrorContains(t, err, "unknown operating system or architecture")
	})

	t.Run("enum args", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

type Test struct {}

func (m *Test) FromProto(proto NetworkProtocol) string {
	return string(proto)
}

func (m *Test) ToProto(proto string) NetworkProtocol {
	return NetworkProtocol(proto)
}
`,
			})

		out, err := modGen.With(daggerCall("from-proto", "--proto", "TCP")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "TCP", out)
		_, err = modGen.With(daggerCall("from-proto", "--proto", "INVALID")).Stdout(ctx)
		require.ErrorContains(t, err, "invalid enum value")

		out, err = modGen.With(daggerCall("to-proto", "--proto", "TCP")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "TCP", out)
		_, err = modGen.With(daggerCall("to-proto", "--proto", "INVALID")).Stdout(ctx)
		require.ErrorContains(t, err, "invalid enum value")
	})

	t.Run("module args", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("foo.txt", dagger.ContainerWithNewFileOpts{
				Contents: "foo",
			}).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

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
			})

		out, err := modGen.With(daggerCall("mod-src", "--mod-src", ".", "directory", "--path", ".", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, ".gitattributes\n.gitignore\nLICENSE\ndagger.gen.go\ndagger.json\nfoo.txt\ngo.mod\ngo.sum\ninternal\nmain.go\n", out)

		out, err = modGen.With(daggerCall("mod", "--module", ".", "source", "directory", "--path", ".", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, ".gitattributes\n.gitignore\nLICENSE\ndagger.gen.go\ndagger.json\nfoo.txt\ngo.mod\ngo.sum\ninternal\nmain.go\n", out)

		out, err = modGen.With(daggerCall("mod-src", "--mod-src", testGitModuleRef("top-level"), "as-string")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, testGitModuleRef("top-level"), out)

		out, err = modGen.With(daggerCall("mod", "--module", testGitModuleRef("top-level"), "source", "as-string")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, testGitModuleRef("top-level"), out)
	})
}

func TestModuleDaggerCallReturnTypes(t *testing.T) {
	t.Parallel()

	t.Run("return list objects", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main
type Minimal struct {}

type Foo struct {
	Bar int ` + "`" + `json:"bar"` + "`" + `
}

func (m *Minimal) Fn() []*Foo {
	var foos []*Foo
	for i := 0; i < 3; i++ {
		foos = append(foos, &Foo{Bar: i})
	}
	return foos
}
`,
			})

		logGen(ctx, t, modGen.Directory("."))
		expected := "0\n1\n2\n"

		t.Run("print", func(t *testing.T) {
			out, err := modGen.With(daggerCall("fn", "bar")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, expected, out)
		})

		t.Run("output", func(t *testing.T) {
			out, err := modGen.
				With(daggerCall("fn", "bar", "-o", "./outfile")).
				File("./outfile").
				Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, expected, out)
		})

		t.Run("json", func(t *testing.T) {
			out, err := modGen.
				With(daggerCall("fn", "bar", "--json")).
				Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `[{"bar": 0}, {"bar": 1}, {"bar": 2}]`, out)
		})
	})

	t.Run("return container", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

func New() *Test {
	return &Test{Ctr: dag.Container().From("alpine:3.18").WithExec([]string{"echo", "hello", "world"})}
}

type Test struct {
	Ctr *Container
}
`,
			})

		logGen(ctx, t, modGen.Directory("."))

		t.Run("default", func(t *testing.T) {
			ctr := modGen.With(daggerCall("ctr"))
			out, err := ctr.Stdout(ctx)
			require.NoError(t, err)
			require.Empty(t, out)
			out, err = ctr.Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "Container evaluated")
		})

		t.Run("output", func(t *testing.T) {
			modGen, err := modGen.With(daggerCall("ctr", "-o", "./container.tar")).Sync(ctx)
			require.NoError(t, err)
			size, err := modGen.File("./container.tar").Size(ctx)
			require.NoError(t, err)
			require.Greater(t, size, 0)
			_, err = modGen.WithExec([]string{"tar", "tf", "./container.tar", "oci-layout"}).Sync(ctx)
			require.NoError(t, err)
		})
	})

	t.Run("return directory", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

func New() *Test {
	return &Test{
		Dir: dag.Directory().WithNewFile("foo.txt", "foo").WithNewFile("bar.txt", "bar"),
	}
}

type Test struct {
	Dir *Directory
}
`,
			})

		logGen(ctx, t, modGen.Directory("."))

		t.Run("default", func(t *testing.T) {
			ctr := modGen.With(daggerCall("dir"))
			out, err := ctr.Stdout(ctx)
			require.NoError(t, err)
			require.Empty(t, out)
			out, err = ctr.Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "Directory evaluated")
		})

		t.Run("output", func(t *testing.T) {
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

	t.Run("return file", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

func New() *Test {
	return &Test{
		File: dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt"),
	}
}

type Test struct {
	File *File
}
`,
			})

		logGen(ctx, t, modGen.Directory("."))

		t.Run("default", func(t *testing.T) {
			ctr := modGen.With(daggerCall("file"))
			out, err := ctr.Stdout(ctx)
			require.NoError(t, err)
			require.Empty(t, out)
			out, err = ctr.Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "File evaluated")
		})

		t.Run("output", func(t *testing.T) {
			out, err := modGen.
				With(daggerCall("file", "-o", "./outfile")).
				File("./outfile").
				Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)
		})
	})

	t.Run("sync", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

func New() *Test {
	return &Test{Ctr: dag.Container().From("alpine:3.18").WithExec([]string{"echo", "hello", "world"})}
}

type Test struct {
	Ctr *Container
}
`,
			})

		// adding sync disables the default behavior of **not** printing the ID
		// just verify it works without error for now
		_, err := modGen.With(daggerCall("ctr", "sync")).Stdout(ctx)
		require.NoError(t, err)
	})
}

func TestModuleDaggerCallCoreChaining(t *testing.T) {
	t.Parallel()

	t.Run("container", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

func New() *Test {
	return &Test{Ctr: dag.Container().From("alpine:3.18.5")}
}

type Test struct {
	Ctr *Container
}
`,
			})

		t.Run("file", func(t *testing.T) {
			out, err := modGen.With(daggerCall("ctr", "file", "--path=/etc/alpine-release", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "3.18.5\n", out)
		})

		t.Run("export", func(t *testing.T) {
			modGen, err := modGen.With(daggerCall("ctr", "export", "--path=./container.tar.gz")).Sync(ctx)
			require.NoError(t, err)
			size, err := modGen.File("./container.tar.gz").Size(ctx)
			require.NoError(t, err)
			require.Greater(t, size, 0)
		})
	})

	t.Run("directory", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

func New() *Test {
	return &Test{
		Dir: dag.Directory().WithNewFile("foo.txt", "foo").WithNewFile("bar.txt", "bar"),
	}
}

type Test struct {
	Dir *Directory
}
`,
			})

		t.Run("file", func(t *testing.T) {
			out, err := modGen.With(daggerCall("dir", "file", "--path=foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", out)
		})

		t.Run("export", func(t *testing.T) {
			modGen, err := modGen.With(daggerCall("dir", "export", "--path=./outdir")).Sync(ctx)
			require.NoError(t, err)
			ents, err := modGen.Directory("./outdir").Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"bar.txt", "foo.txt"}, ents)
		})
	})

	t.Run("return file", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

func New() *Test {
	return &Test{
		File: dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt"),
	}
}

type Test struct {
	File *File
}
`,
			})

		t.Run("size", func(t *testing.T) {
			out, err := modGen.With(daggerCall("file", "size")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "3", out)
		})

		t.Run("export", func(t *testing.T) {
			modGen, err := modGen.With(daggerCall("file", "export", "--path=./outfile")).Sync(ctx)
			require.NoError(t, err)
			contents, err := modGen.File("./outfile").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", contents)
		})
	})
}

func TestModuleDaggerCallSaveOutput(t *testing.T) {
	// NB: Normal usage is tested in TestModuleDaggerCallReturnTypes.

	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Test struct {
}

func (t *Test) Hello() string {
    return "hello"
}

func (t *Test) File() *File {
    return dag.Directory().WithNewFile("foo.txt", "foo").File("foo.txt")
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	t.Run("truncate file", func(t *testing.T) {
		out, err := modGen.
			WithNewFile("foo.txt", dagger.ContainerWithNewFileOpts{
				Contents: "foobar",
			}).
			With(daggerCall("hello", "-o", "foo.txt")).
			File("foo.txt").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello", out)
	})

	t.Run("not a file", func(t *testing.T) {
		_, err := modGen.With(daggerCall("hello", "-o", ".")).Sync(ctx)
		require.ErrorContains(t, err, "is a directory")
	})

	t.Run("allow dir for file", func(t *testing.T) {
		out, err := modGen.
			With(daggerCall("file", "-o", ".")).
			File("foo.txt").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo", out)
	})

	t.Run("create parent dirs", func(t *testing.T) {
		ctr, err := modGen.With(daggerCall("hello", "-o", "foo/bar.txt")).Sync(ctx)
		require.NoError(t, err)

		t.Run("print success", func(t *testing.T) {
			// should print success to stderr so it doesn't interfere with piping output
			out, err := ctr.Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, out, `Saved output to "/work/foo/bar.txt"`)
		})

		t.Run("check directory permissions", func(t *testing.T) {
			out, err := ctr.WithExec([]string{"stat", "-c", "%a", "foo"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "755\n", out)
		})

		t.Run("check file permissions", func(t *testing.T) {
			out, err := ctr.WithExec([]string{"stat", "-c", "%a", "foo/bar.txt"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "644\n", out)
		})
	})

	t.Run("check umask", func(t *testing.T) {
		ctr, err := modGen.
			WithNewFile("/entrypoint.sh", dagger.ContainerWithNewFileOpts{
				Contents: `#!/bin/sh
umask 027
exec "$@"
`,
				Permissions: 0o750,
			}).
			WithEntrypoint([]string{"/entrypoint.sh"}).
			With(daggerCall("hello", "-o", "/tmp/foo/bar.txt")).
			Sync(ctx)
		require.NoError(t, err)

		t.Run("directory", func(t *testing.T) {
			out, err := ctr.WithExec([]string{"stat", "-c", "%a", "/tmp/foo"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "750\n", out)
		})

		t.Run("file", func(t *testing.T) {
			out, err := ctr.WithExec([]string{"stat", "-c", "%a", "/tmp/foo/bar.txt"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "640\n", out)
		})
	})
}

func TestModuleCallByName(t *testing.T) {
	t.Parallel()

	t.Run("local", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/mod-a").
			With(daggerExec("init", "--source=.", "--name=mod-a", "--sdk=go")).
			WithNewFile("/work/mod-a/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type ModA struct {}

			func (m *ModA) Fn(ctx context.Context) string { 
				return "hi from mod-a"
			}
			`,
			}).
			WithWorkdir("/work/mod-b").
			With(daggerExec("init", "--source=.", "--name=mod-b", "--sdk=go")).
			WithNewFile("/work/mod-b/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type ModB struct {}

			func (m *ModB) Fn(ctx context.Context) string { 
				return "hi from mod-b"
			}
			`,
			}).
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

	t.Run("git", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init")).
			With(daggerExec("install", "--name", "foo", testGitModuleRef(""))).
			With(daggerExec("install", "--name", "bar", testGitModuleRef("subdir/dep2")))

		out, err := ctr.With(daggerCallAt("foo", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from root hi from dep hi from dep2", strings.TrimSpace(out))

		out, err = ctr.With(daggerCallAt("bar", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep2", strings.TrimSpace(out))
	})
}

func TestModuleCallGitMod(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		With(daggerCallAt(testGitModuleRef("top-level"), "fn")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hi from top level hi from dep hi from dep2", strings.TrimSpace(out))
}

func TestModuleCallFindup(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

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
