package core

import (
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestModuleDaggerCallArgTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	t.Run("service args", func(t *testing.T) {
		t.Parallel()

		t.Run("used as service binding", func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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
			require.Equal(t, strings.TrimSpace(out), "im up")
		})

		t.Run("used directly", func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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
			require.Equal(t, strings.TrimSpace(out), "1 exposed ports:\n- TCP/8000")
		})
	})

	t.Run("list args", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
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
		require.Equal(t, strings.TrimSpace(out), "yo+my+friend")

		out, err = modGen.With(daggerCall("reads", "--files=foo.txt", "--files=foo.txt")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, strings.TrimSpace(out), "bar+bar")
	})

	t.Run("directory arg inputs", func(t *testing.T) {
		t.Parallel()

		t.Run("local dir", func(t *testing.T) {
			t.Parallel()
			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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

			out, err := modGen.With(daggerCall("fn", "--dir", "/dir/subdir")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.TrimSpace(out), "bar.txt\nfoo.txt")

			out, err = modGen.With(daggerCall("fn", "--dir", "file:///dir/subdir")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.TrimSpace(out), "bar.txt\nfoo.txt")
		})

		t.Run("git dir", func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main
type Test struct {}

func (m *Test) Fn(dir *Directory, subpath Optional[string]) *Directory {
	return dir.Directory(subpath.GetOr("."))
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
					t.Parallel()
					url := tc.baseURL + "#v0.9.1"
					if tc.subpath != "" {
						url += ":" + tc.subpath
					}

					args := []string{"fn", "--dir", url}
					if tc.subpath == "" {
						args = append(args, "--subpath", ".changes")
					}
					out, err := modGen.With(daggerCall(args...)).Stdout(ctx)
					require.NoError(t, err)

					require.Contains(t, out, "v0.9.1.md")
					require.NotContains(t, out, "v0.9.2.md")
				})
			}
		})
	})

	t.Run("secret args", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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
			t.Parallel()
			t.Run("happy", func(t *testing.T) {
				t.Parallel()
				out, err := modGen.With(daggerCall("insecure", "--token", "env:TOPSECRET")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "shhh", strings.TrimSpace(out))
			})
			t.Run("sad", func(t *testing.T) {
				t.Parallel()
				_, err := modGen.With(daggerCall("insecure", "--token", "env:NOWHERETOBEFOUND")).Stdout(ctx)
				require.ErrorContains(t, err, `secret env var not found: "NOW..."`)
			})
		})

		t.Run("implicit env", func(t *testing.T) {
			t.Parallel()
			t.Run("happy", func(t *testing.T) {
				t.Parallel()
				out, err := modGen.With(daggerCall("insecure", "--token", "TOPSECRET")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "shhh", strings.TrimSpace(out))
			})
			t.Run("sad", func(t *testing.T) {
				t.Parallel()
				_, err := modGen.With(daggerCall("insecure", "--token", "NOWHERETOBEFOUND")).Stdout(ctx)
				require.ErrorContains(t, err, `secret env var not found: "NOW..."`)
			})
		})

		t.Run("file", func(t *testing.T) {
			t.Parallel()
			t.Run("happy", func(t *testing.T) {
				t.Parallel()
				out, err := modGen.With(daggerCall("insecure", "--token", "file:/mysupersecret")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "file shhh", strings.TrimSpace(out))
			})
			t.Run("sad", func(t *testing.T) {
				t.Parallel()
				_, err := modGen.With(daggerCall("insecure", "--token", "file:/nowheretobefound")).Stdout(ctx)
				require.ErrorContains(t, err, `failed to read secret file "/nowheretobefound": open /nowheretobefound: no such file or directory`)
			})
		})

		t.Run("cmd", func(t *testing.T) {
			t.Parallel()
			t.Run("happy", func(t *testing.T) {
				t.Parallel()
				out, err := modGen.With(daggerCall("insecure", "--token", "cmd:echo -n cmd shhh")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "cmd shhh", strings.TrimSpace(out))
			})
			t.Run("sad", func(t *testing.T) {
				t.Parallel()
				_, err := modGen.With(daggerCall("insecure", "--token", "cmd:exit 1")).Stdout(ctx)
				require.ErrorContains(t, err, `failed to run secret command "exit 1": exit status 1`)
			})
		})

		t.Run("invalid source", func(t *testing.T) {
			t.Parallel()
			_, err := modGen.With(daggerCall("insecure", "--token", "wtf:HUH")).Stdout(ctx)
			require.ErrorContains(t, err, `unsupported secret arg source: "wtf"`)
		})
	})
}

func TestModuleDaggerCallReturnTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	t.Run("return list objects", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
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
		expected := "0\n1\n2"

		t.Run("print", func(t *testing.T) {
			t.Parallel()
			out, err := modGen.With(daggerCall("fn", "bar")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, expected, strings.TrimSpace(out))
		})

		t.Run("output", func(t *testing.T) {
			t.Parallel()
			out, err := modGen.
				With(daggerCall("fn", "bar", "-o", "./outfile")).
				File("./outfile").
				Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, expected, strings.TrimSpace(out))
		})
	})

	t.Run("return container", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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

		t.Run("print", func(t *testing.T) {
			t.Parallel()
			out, err := modGen.With(daggerCall("ctr")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello world", strings.TrimSpace(out))
		})

		t.Run("output", func(t *testing.T) {
			t.Parallel()
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

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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

		t.Run("print", func(t *testing.T) {
			t.Parallel()
			out, err := modGen.With(daggerCall("dir")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "bar.txt\nfoo.txt", strings.TrimSpace(out))
		})

		t.Run("output", func(t *testing.T) {
			t.Parallel()
			modGen, err := modGen.With(daggerCall("dir", "-o", "./outdir")).Sync(ctx)
			require.NoError(t, err)
			entries, err := modGen.Directory("./outdir").Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, "bar.txt\nfoo.txt", strings.Join(entries, "\n"))

			foo, err := modGen.Directory("./outdir").File("foo.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", strings.TrimSpace(foo))

			bar, err := modGen.Directory("./outdir").File("bar.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "bar", strings.TrimSpace(bar))
		})
	})

	t.Run("return file", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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

		t.Run("print", func(t *testing.T) {
			t.Parallel()
			out, err := modGen.With(daggerCall("file")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", strings.TrimSpace(out))
		})

		t.Run("output", func(t *testing.T) {
			t.Parallel()
			out, err := modGen.
				With(daggerCall("file", "-o", "./outfile")).
				File("./outfile").
				Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", strings.TrimSpace(out))
		})
	})

	t.Run("sync", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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

		// just verify it works without error for now
		_, err := modGen.With(daggerCall("ctr", "sync")).Stdout(ctx)
		require.NoError(t, err)
	})
}

func TestModuleDaggerCallCoreChaining(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	t.Run("container", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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
			t.Parallel()
			out, err := modGen.With(daggerCall("ctr", "file", "--path=/etc/alpine-release", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "3.18.5", strings.TrimSpace(out))
		})

		t.Run("export", func(t *testing.T) {
			t.Parallel()
			modGen, err := modGen.With(daggerCall("ctr", "export", "--path=./container.tar.gz")).Sync(ctx)
			require.NoError(t, err)
			size, err := modGen.File("./container.tar.gz").Size(ctx)
			require.NoError(t, err)
			require.Greater(t, size, 0)
		})
	})

	t.Run("directory", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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
			t.Parallel()
			out, err := modGen.With(daggerCall("dir", "file", "--path=foo.txt", "contents")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", strings.TrimSpace(out))
		})

		t.Run("export", func(t *testing.T) {
			t.Parallel()
			modGen, err := modGen.With(daggerCall("dir", "export", "--path=./outdir")).Sync(ctx)
			require.NoError(t, err)
			ents, err := modGen.Directory("./outdir").Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"bar.txt", "foo.txt"}, ents)
		})
	})

	t.Run("return file", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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
			t.Parallel()
			out, err := modGen.With(daggerCall("file", "size")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "3", strings.TrimSpace(out))
		})

		t.Run("export", func(t *testing.T) {
			t.Parallel()
			modGen, err := modGen.With(daggerCall("file", "export", "--path=./outfile")).Sync(ctx)
			require.NoError(t, err)
			contents, err := modGen.File("./outfile").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "foo", strings.TrimSpace(contents))
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
		With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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
		t.Parallel()
		out, err := modGen.
			WithNewFile("foo.txt", dagger.ContainerWithNewFileOpts{
				Contents: "foobar",
			}).
			With(daggerCall("hello", "-o", "foo.txt")).
			File("foo.txt").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello", strings.TrimSpace(out))
	})

	t.Run("not a file", func(t *testing.T) {
		t.Parallel()
		_, err := modGen.With(daggerCall("hello", "-o", ".")).Sync(ctx)
		require.ErrorContains(t, err, "is a directory")
	})

	t.Run("allow dir for file", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.
			With(daggerCall("file", "-o", ".")).
			File("foo.txt").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo", strings.TrimSpace(out))
	})

	t.Run("create parent dirs", func(t *testing.T) {
		t.Parallel()

		ctr, err := modGen.With(daggerCall("hello", "-o", "foo/bar.txt")).Sync(ctx)
		require.NoError(t, err)

		t.Run("print success", func(t *testing.T) {
			t.Parallel()
			// should print success to stderr so it doesn't interfere with piping output
			out, err := ctr.Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, out, `Saved output to "/work/foo/bar.txt"`)
		})

		t.Run("check directory permissions", func(t *testing.T) {
			t.Parallel()
			out, err := ctr.WithExec([]string{"stat", "-c", "%a", "foo"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "755", strings.TrimSpace(out))
		})

		t.Run("check file permissions", func(t *testing.T) {
			t.Parallel()
			out, err := ctr.WithExec([]string{"stat", "-c", "%a", "foo/bar.txt"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "644", strings.TrimSpace(out))
		})
	})
}
