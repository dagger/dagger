package core

// Workspace alignment: mostly aligned; coverage targets post-workspace module constructor semantics, but setup still relies on historical module helpers.
// Scope: Constructor argument handling, constructor-populated fields, constructor error behavior, and SDK-specific constructor parity.
// Intent: Keep constructor behavior explicit and isolated from the remaining runtime and validation coverage in the historical umbrella suite.

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestConstructor(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

func New(
	ctx context.Context,
	foo string,
	bar *int, // +optional
	baz []string,
	dir *dagger.Directory,
) *Test {
	bar2 := 42
	if bar != nil {
		bar2 = *bar
	}
	return &Test{
		Foo: foo,
		Bar: bar2,
		Baz: baz,
		Dir: dir,
	}
}

type Test struct {
	Foo         string
	Bar         int
	Baz         []string
	Dir         *dagger.Directory
	NeverSetDir *dagger.Directory
}

func (m *Test) GimmeFoo() string {
	return m.Foo
}

func (m *Test) GimmeBar() int {
	return m.Bar
}

func (m *Test) GimmeBaz() []string {
	return m.Baz
}

func (m *Test) GimmeDirEnts(ctx context.Context) ([]string, error) {
	return m.Dir.Entries(ctx)
}
`,
			},
			{
				sdk: "python",
				source: `import dagger
from dagger import field, function, object_type

@object_type
class Test:
    foo: str = field()
    dir: dagger.Directory = field()
    bar: int = field(default=42)
    baz: list[str] = field(default=list)
    never_set_dir: dagger.Directory | None = field(default=None)

    @function
    def gimme_foo(self) -> str:
        return self.foo

    @function
    def gimme_bar(self) -> int:
        return self.bar

    @function
    def gimme_baz(self) -> list[str]:
        return self.baz

    @function
    async def gimme_dir_ents(self) -> list[str]:
        return await self.dir.entries()
`,
			},
			{
				sdk: "typescript",
				source: `
import { Directory, object, func } from '@dagger.io/dagger';

@object()
export class Test {
	@func()
	foo: string

	@func()
	dir: Directory

	@func()
	bar: number

	@func()
	baz: string[]

	@func()
	neverSetDir?: Directory

	constructor(foo: string, dir: Directory, bar = 42, baz: string[] = []) {
		this.foo = foo;
		this.dir = dir;
		this.bar = bar;
		this.baz = baz;
	}

	@func()
	gimmeFoo(): string {
		return this.foo;
	}

	@func()
	gimmeBar(): number {
		return this.bar;
	}

	@func()
	gimmeBaz(): string[] {
		return this.baz;
	}

	@func()
	async gimmeDirEnts(): Promise<string[]> {
		return this.dir.entries();
	}
}
`,
			},
		} {
			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				ctr := modInit(t, c, tc.sdk, tc.source)

				out, err := ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "foo")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "abc")

				out, err = ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "gimme-foo")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "abc")

				out, err = ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "42")

				out, err = ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "gimme-bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "42")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "123")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "gimme-bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "123")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "baz")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "x\ny\nz")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "gimme-baz")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "x\ny\nz")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "gimme-dir-ents")).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, strings.TrimSpace(out), "dagger.json")
			})
		}
	})

	t.Run("fields only", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"context"
)

func New(ctx context.Context) (Test, error) {
	v, err := dag.Container().From("%s").File("/etc/alpine-release").Contents(ctx)
	if err != nil {
		return Test{}, err
	}
	return Test{
		AlpineVersion: v,
	}, nil
}

type Test struct {
	AlpineVersion string
}
`,
			},
			{
				sdk: "python",
				source: `from dagger import dag, field, function, object_type

@object_type
class Test:
    alpine_version: str = field()

    @classmethod
    async def create(cls) -> "Test":
        return cls(alpine_version=await (
            dag.container()
            .from_("%s")
            .file("/etc/alpine-release")
            .contents()
        ))
`,
			},
			{
				sdk: "typescript",
				source: `
import { dag, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  alpineVersion: string

  // NOTE: this is not standard to do async operations in the constructor.
  // This is only for testing purpose but it shouldn't be done in real usage.
  constructor() {
    return (async () => {
      this.alpineVersion = await dag.container().from("%s").file("/etc/alpine-release").contents()

      return this; // Return the newly-created instance
    })();
  }
}
`,
			},
		} {
			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				ctr := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/test").
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
					With(sdkSource(tc.sdk, fmt.Sprintf(tc.source, alpineImage)))

				out, err := ctr.With(daggerCall("alpine-version")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, distconsts.AlpineVersion, strings.TrimSpace(out))
			})
		}
	})

	t.Run("return error", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"fmt"
)

func New() (*Test, error) {
	return nil, fmt.Errorf("too bad: %s", "so sad")
}

type Test struct {
	Foo string
}
`,
			},
			{
				sdk: "python",
				source: `from dagger import object_type, field

@object_type
class Test:
    foo: str = field()

    def __init__(self):
        raise ValueError("too bad: " + "so sad")
`,
			},
			{
				sdk: "typescript",
				source: `
import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  foo: string

  constructor() {
    throw new Error("too bad: " + "so sad")
  }
}
`,
			},
		} {
			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				var logs safeBuffer
				c := connect(ctx, t, dagger.WithLogOutput(&logs))

				ctr := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/test").
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
					With(sdkSource(tc.sdk, tc.source))

				_, err := ctr.With(daggerCall("foo")).Stdout(ctx)
				require.Error(t, err)

				require.NoError(t, c.Close())

				t.Log(logs.String())
				require.Regexp(t, "too bad: so sad", logs.String())
			})
		}
	})

	t.Run("python: with default factory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		content := identity.NewID()

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/test").
			With(daggerExec("init", "--name=test", "--sdk=python")).
			With(sdkSource("python", fmt.Sprintf(`import dagger
from dagger import dag, object_type, field

@object_type
class Test:
    foo: dagger.File = field(default=lambda: (
        dag.directory()
        .with_new_file("foo.txt", "%s")
        .file("foo.txt")
    ))
    bar: list[str] = field(default=list)
`, content),
			))

		out, err := ctr.With(daggerCall("foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("--foo=dagger.json", "foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"source": "python"`)

		_, err = ctr.With(daggerCall("bar")).Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("typescript: with default factory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		content := identity.NewID()

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/test").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", fmt.Sprintf(`
import { dag, File, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  foo: File = dag.directory().withNewFile("foo.txt", "%s").file("foo.txt")

  @func()
  bar: string[] = []

  // Allow foo to be set through the constructor
  constructor(foo?: File) {
    if (foo) {
      this.foo = foo
    }
  }
}
`, content),
			))

		out, err := ctr.With(daggerCall("foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("--foo=dagger.json", "foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"source": "typescript"`)

		_, err = ctr.With(daggerCall("bar")).Sync(ctx)
		require.NoError(t, err)
	})
}
