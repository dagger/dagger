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
	Quacks []string
	QuacksAtBystander []string
	QuacksAtGeese []string
}

type Duck interface {
	DaggerObject
	Quack(context.Context) (string, error)
	QuackAt(ctx context.Context, quackee string) (string, error)
	QuackAtAll(ctx context.Context, quackees []string) ([]string, error)
}

func (m Pond) WithDuck(ctx context.Context, duck Duck) (*Pond, error) {
	quack, err := duck.Quack(ctx)
	if err != nil {
		return nil, err
	}
	m.Quacks = append(m.Quacks, quack)

	quackAt, err := duck.QuackAt(ctx, "innocent bystander")
	if err != nil {
		return nil, err
	}
	m.QuacksAtBystander = append(m.QuacksAtBystander, quackAt)

	quacksAtGeese, err := duck.QuackAtAll(ctx, []string{"goose A", "goose B"})
	if err != nil {
		return nil, err
	}
	m.QuacksAtGeese = append(m.QuacksAtGeese, quacksAtGeese...)

	return &m, nil
}

func (m Pond) WithMaybeDuck(ctx context.Context, maybeDuck Optional[Duck]) (*Pond, error) {
	duck, ok := maybeDuck.Get()
	if !ok {
		return &m, nil
	}

	quack, err := duck.Quack(ctx)
	if err != nil {
		return nil, err
	}
	m.Quacks = append(m.Quacks, quack)

	quackAt, err := duck.QuackAt(ctx, "innocent bystander")
	if err != nil {
		return nil, err
	}
	m.QuacksAtBystander = append(m.QuacksAtBystander, quackAt)

	quacksAtGeese, err := duck.QuackAtAll(ctx, []string{"goose A", "goose B"})
	if err != nil {
		return nil, err
	}
	m.QuacksAtGeese = append(m.QuacksAtGeese, quacksAtGeese...)

	return &m, nil
}

func (m Pond) WithDucks(ctx context.Context, ducks []Duck) (*Pond, error) {
	for _, duck := range ducks {
		quack, err := duck.Quack(ctx)
		if err != nil {
			return nil, err
		}
		m.Quacks = append(m.Quacks, quack)
	}
	return &m, nil
}

func (m *Pond) QuackAll() string {
	return strings.Join(m.Quacks, "\n")
}

func (m *Pond) QuackAllAtBystander() string {
	return strings.Join(m.QuacksAtBystander, "\n")
}

func (m *Pond) QuackAllAtGeese() string {
	return strings.Join(m.QuacksAtGeese, "\n")
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

func (m *Top) TestQuackAt(ctx context.Context) (string, error) {
	return dag.Pond().
		WithDuck(dag.Mallard().AsPondDuck()).
		WithDuck(dag.Crested().AsPondDuck()).
		QuackAllAtBystander(ctx)
}

func (m *Top) TestQuackAtAll(ctx context.Context) (string, error) {
	return dag.Pond().
		WithDuck(dag.Mallard().AsPondDuck()).
		WithDuck(dag.Crested().AsPondDuck()).
		QuackAllAtGeese(ctx)
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
		out, err := ctr.With(daggerQuery(`{top{testQuackAt}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"top":{"testQuackAt":"to innocent bystander I say: mallard quack\nto innocent bystander I say: crested quack"}}`, out)
	})

	t.Run("iface with list of primitive arg and list of primitive return", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerQuery(`{top{testQuackAtAll}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"top":{"testQuackAtAll":"to goose A I say: mallard quack\nto goose B I say: mallard quack\nto goose A I say: crested quack\nto goose B I say: crested quack"}}`, out)
	})
}
