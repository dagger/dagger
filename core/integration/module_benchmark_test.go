package core

import (
	"context"
	"fmt"
	"go/format"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/testctx"
	"github.com/google/uuid"
	"github.com/iancoleman/strcase"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func BenchmarkModule(b *testing.B) {
	testctx.Bench(testCtx, b, ModuleSuite{}, BenchMiddleware()...)
}

func (ModuleSuite) BenchmarkLotsOfFunctions(ctx context.Context, t *testctx.B) {
	const funcCount = 100

	t.Run("go sdk", func(ctx context.Context, t *testctx.B) {
		c := connect(ctx, t)

		mainSrc := `
		package main

		type PotatoSack struct {}
		`

		for i := 0; i < funcCount; i++ {
			mainSrc += fmt.Sprintf(`
			func (m *PotatoSack) Potato%d() string {
				return "potato #%d"
			}
			`, i, i)
		}

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/main.go", mainSrc).
			With(daggerExec("init", "--source=.", "--name=potatoSack", "--sdk=go"))

		var eg errgroup.Group
		for i := 0; i < funcCount; i++ {
			i := i
			// just verify a subset work
			if i%10 != 0 {
				continue
			}
			eg.Go(func() error {
				_, err := modGen.
					With(daggerCall(fmt.Sprintf("potato-%d", i))).
					Sync(ctx)
				return err
			})
		}
		require.NoError(t, eg.Wait())
	})

	t.Run("python sdk", func(ctx context.Context, t *testctx.B) {
		c := connect(ctx, t)

		mainSrc := `import dagger

@dagger.object_type
class PotatoSack:
`

		for i := 0; i < funcCount; i++ {
			mainSrc += fmt.Sprintf(`
    @dagger.function
    def potato_%d(self) -> str:
        return "potato #%d"
`, i, i)
		}

		modGen := c.Container().
			From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(fileContents("src/potato_sack/__init__.py", mainSrc)).
			With(daggerExec("init", "--source=.", "--name=potatoSack", "--sdk=python"))

		var eg errgroup.Group
		for i := 0; i < funcCount; i++ {
			i := i
			// just verify a subset work
			if i%10 != 0 {
				continue
			}
			eg.Go(func() error {
				_, err := modGen.
					WithEnvVariable("CACHE_BUST", uuid.NewString()).
					With(daggerCall(fmt.Sprintf("potato-%d", i))).
					Sync(ctx)
				return err
			})
		}
		require.NoError(t, eg.Wait())
	})

	t.Run("typescript sdk", func(ctx context.Context, t *testctx.B) {
		c := connect(ctx, t)

		mainSrc := `
		import { object, func } from "@dagger.io/dagger"

@object()
export class PotatoSack {
		`

		for i := 0; i < funcCount; i++ {
			mainSrc += fmt.Sprintf(`
  @func()
  potato_%d(): string {
    return "potato #%d"
  }
			`, i, i)
		}

		mainSrc += "\n}"

		modGen := c.
			Container().
			From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(sdkSource("typescript", mainSrc)).
			With(daggerExec("init", "--name=potatoSack", "--sdk=typescript", "--source=."))

		var eg errgroup.Group
		for i := 0; i < funcCount; i++ {
			i := i
			// just verify a subset work
			if i%10 != 0 {
				continue
			}
			eg.Go(func() error {
				_, err := modGen.
					WithEnvVariable("CACHE_BUST", uuid.NewString()).
					With(daggerCall(fmt.Sprintf("potato-%d", i))).
					Sync(ctx)
				return err
			})
		}
		require.NoError(t, eg.Wait())
	})
}

func (ModuleSuite) BenchmarkLotsOfDeps(ctx context.Context, t *testctx.B) {
	c := connect(ctx, t)

	modGen := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work")

	modCount := 0

	getModMainSrc := func(name string, depNames []string) string {
		t.Helper()
		mainSrc := fmt.Sprintf(`package main
	import "context"

	type %s struct {}

	func (m *%s) Fn(ctx context.Context) (string, error) {
		s := "%s"
		var depS string
		_ = depS
		var err error
		_ = err
	`, strcase.ToCamel(name), strcase.ToCamel(name), name)
		for _, depName := range depNames {
			mainSrc += fmt.Sprintf(`
	depS, err = dag.%s().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	`, strcase.ToCamel(depName))
		}
		mainSrc += "return s, nil\n}\n"
		fmted, err := format.Source([]byte(mainSrc))
		require.NoError(t, err)
		return string(fmted)
	}

	// need to construct dagger.json directly in order to avoid excessive
	// `dagger mod use` calls while constructing the huge DAG of deps
	var rootCfg modules.ModuleConfig

	addModulesWithDeps := func(newMods int, depNames []string) []string {
		t.Helper()

		var newModNames []string
		for i := 0; i < newMods; i++ {
			name := fmt.Sprintf("mod%d", modCount)
			modCount++
			newModNames = append(newModNames, name)
			modGen = modGen.
				WithWorkdir("/work/"+name).
				WithEnvVariable("CACHE_BUST", uuid.NewString()).
				WithNewFile("./main.go", getModMainSrc(name, depNames))

			var depCfgs []*modules.ModuleConfigDependency
			for _, depName := range depNames {
				depCfgs = append(depCfgs, &modules.ModuleConfigDependency{
					Name:   depName,
					Source: filepath.Join("..", depName),
				})
			}
			modGen = modGen.With(configFile(".", &modules.ModuleConfig{
				Name:         name,
				SDK:          "go",
				Dependencies: depCfgs,
			}))
		}
		return newModNames
	}

	// Create a base module, then add 6 layers of deps, where each layer has one more module
	// than the previous layer and each module within the layer has a dep on each module
	// from the previous layer. Finally add a single module at the top that depends on all
	// modules from the last layer and call that.
	// Basically, this creates a quadratically growing DAG of modules and verifies we
	// handle it efficiently enough to be callable.
	curDeps := addModulesWithDeps(1, nil)
	for i := 0; i < 6; i++ {
		curDeps = addModulesWithDeps(len(curDeps)+1, curDeps)
	}
	addModulesWithDeps(1, curDeps)

	modGen = modGen.With(configFile("..", &rootCfg))

	_, err := modGen.With(daggerCall("fn")).Sync(ctx)
	require.NoError(t, err)
}

func (ModuleSuite) BenchmarkLargeObjectFieldVal(ctx context.Context, t testctx.ITB) {
	// make sure we don't hit any limits when an object field value is large

	c := connect(ctx, t)

	// put a timeout on this since failures modes could result in hangs
	t = t.WithTimeout(60 * time.Second)

	_, err := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go")).
		With(sdkSource("go", `package main

import "strings"

type Test struct {
	BigVal string
}

func New() *Test {
	return &Test{
		BigVal: strings.Repeat("a", 30*1024*1024),
	}
}

// add a func for returning the val in order to test mode codepaths that
// involve serializing and passing the object around
func (m *Test) Fn() string {
	return m.BigVal
}
`)).
		With(daggerCall("fn")).
		Sync(ctx)
	require.NoError(t, err)
}

// regression test for https://github.com/dagger/dagger/issues/7334
// and https://github.com/dagger/dagger/pull/7336
func (ModuleSuite) BenchmarkCallSameModuleInParallel(ctx context.Context, t *testctx.B) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--name=dep", "--sdk=go")).
		With(sdkSource("go", `package main

import (
	"github.com/moby/buildkit/identity"
	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (m *Dep) DepFn(s *dagger.Secret) string {
	return identity.NewID()
}
`)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		With(sdkSource("go", `package main

import (
	"context"
	"golang.org/x/sync/errgroup"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context) ([]string, error) {
	var eg errgroup.Group
	results := make([]string, 10)
	for i := 0; i < 10; i++ {
		i := i
		eg.Go(func() error {
			res, err := dag.Dep().DepFn(ctx, dag.SetSecret("foo", "bar"))
			if err != nil {
				return err
			}
			results[i] = res
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}
`)).
		With(daggerExec("install", "./dep")).
		With(daggerCall("fn"))

	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	results := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, results, 10)
	expectedRes := results[0]
	for _, res := range results {
		require.Equal(t, expectedRes, res)
	}
}
