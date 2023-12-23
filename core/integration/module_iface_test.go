package core

import (
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestModuleGoIfaces(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/mallard").
		WithNewFile("./main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "fmt"

type Mallard struct {}

func (m *Mallard) Quack() string {
	return "mallard quack"
}

func (m Mallard) QuackAt(quackee string) string {
	return fmt.Sprintf("to %s I say: %s", quackee, m.Quack())
}

func (m Mallard) QuackAtAll(quackees []string) []string {
	var quacks []string
	for _, quackee := range quackees {
		quacks = append(quacks, m.QuackAt(quackee))
	}
	return quacks
}
			`,
		}).
		With(daggerExec("mod", "init", "--name=mallard", "--sdk=go")).
		WithWorkdir("/work/crested").
		WithNewFile("./main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "fmt"

type Crested struct {}

func (m *Crested) Quack() string {
	return "crested quack"
}

func (m Crested) QuackAt(quackee string) string {
	return fmt.Sprintf("to %s I say: %s", quackee, m.Quack())
}

func (m Crested) QuackAtAll(quackees []string) []string {
	var quacks []string
	for _, quackee := range quackees {
		quacks = append(quacks, m.QuackAt(quackee))
	}
	return quacks
}
			`,
		}).
		With(daggerExec("mod", "init", "--name=crested", "--sdk=go")).
		WithWorkdir("/work/pond").
		WithNewFile("./main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import (
	"context"
	"strings"
)

type Pond struct {
	Ducks []Duck
}

type Duck interface {
	DaggerObject
	Quack(context.Context) (string, error)
	QuackAt(ctx context.Context, quackee string) (string, error)
	QuackAtAll(ctx context.Context, quackees []string) ([]string, error)
}

func (m Pond) WithDuck(duck Duck) *Pond {
	m.Ducks = append(m.Ducks, duck)
	return &m
}

func (m *Pond) WithMaybeDuck(maybeDuck Optional[Duck]) *Pond {
	duck, ok := maybeDuck.Get()
	if !ok {
		return m
	}
	m.Ducks = append(m.Ducks, duck)
	return m
}

func (m *Pond) WithDucks(ducks []Duck) *Pond {
	for _, duck := range ducks {
		m = m.WithDuck(duck)
	}
	return m
}

func (m *Pond) QuackAll(ctx context.Context) (string, error) {
	var quacks []string
	for _, duck := range m.Ducks {
		quack, err := duck.Quack(ctx)
		if err != nil {
			return "", err
		}
		quacks = append(quacks, quack)
	}
	return strings.Join(quacks, "\n"), nil
}

func (m *Pond) QuackAllAt(ctx context.Context, quackee string) (string, error) {
	var quacks []string
	for _, duck := range m.Ducks {
		quack, err := duck.QuackAt(ctx, quackee)
		if err != nil {
			return "", err
		}
		quacks = append(quacks, quack)
	}
	return strings.Join(quacks, "\n"), nil
}

func (m *Pond) QuackAllAtMany(ctx context.Context, quackees []string) (string, error) {
	var quacks []string
	for _, duck := range m.Ducks {
		newQuacks, err := duck.QuackAtAll(ctx, quackees)
		if err != nil {
			return "", err
		}
		quacks = append(quacks, newQuacks...)
	}
	return strings.Join(quacks, "\n"), nil
}
			`,
		}).
		With(daggerExec("mod", "init", "--name=pond", "--sdk=go")).
		WithWorkdir("/work").
		WithNewFile("./main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import (
	"context"
)

type Top struct{}

func (m *Top) Test(ctx context.Context) (string, error) {
	return dag.Pond().
		WithDuck(dag.Mallard().AsPondDuck()).
		WithDuck(dag.Crested().AsPondDuck()).
		QuackAll(ctx)
}

func (m *Top) TestList(ctx context.Context) (string, error) {
	return dag.Pond().
		WithDucks([]*PondDuck{dag.Mallard().AsPondDuck(), dag.Crested().AsPondDuck()}).
		QuackAll(ctx)
}

func (m *Top) TestOptional(ctx context.Context) (string, error) {
	return dag.Pond().
		WithMaybeDuck().
		WithDuck(dag.Crested().AsPondDuck()).
		QuackAll(ctx)
}

func (m *Top) TestQuackAt(ctx context.Context, quackee string) (string, error) {
	return dag.Pond().
		WithDuck(dag.Mallard().AsPondDuck()).
		WithDuck(dag.Crested().AsPondDuck()).
		QuackAllAt(ctx, quackee)
}

func (m *Top) TestQuackAtAll(ctx context.Context, quackees []string) (string, error) {
	return dag.Pond().
		WithDuck(dag.Mallard().AsPondDuck()).
		WithDuck(dag.Crested().AsPondDuck()).
		QuackAllAtMany(ctx, quackees)
}
			`,
		}).
		With(daggerExec("mod", "init", "--name=top", "--sdk=go")).
		With(daggerExec("mod", "install", "./mallard")).
		With(daggerExec("mod", "install", "./crested")).
		With(daggerExec("mod", "install", "./pond"))

	t.Run("basic", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerQuery(`{top{test}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"top":{"test":"mallard quack\ncrested quack"}}`, out)
	})

	t.Run("function arg as list of ifaces", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerQuery(`{top{testList}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"top":{"testList":"mallard quack\ncrested quack"}}`, out)
	})

	t.Run("function arg as optional iface", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerQuery(`{top{testOptional}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"top":{"testOptional":"crested quack"}}`, out)
	})

	t.Run("iface with primitive arg", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerQuery(`{top{testQuackAt(quackee: "innocent bystander")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"top":{"testQuackAt":"to innocent bystander I say: mallard quack\nto innocent bystander I say: crested quack"}}`, out)
	})

	t.Run("iface with list of primitive arg and list of primitive return", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerQuery(`{top{testQuackAtAll(quackees: ["Mushu", "Sammy"])}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"top":{"testQuackAtAll":"to Mushu I say: mallard quack\nto Sammy I say: mallard quack\nto Mushu I say: crested quack\nto Sammy I say: crested quack"}}`, out)
	})
}
