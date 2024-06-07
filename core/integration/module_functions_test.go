package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func TestModuleDaggerCLIFunctions(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import (
	"context"
)

type Test struct{}

// doc for FnA
func (m *Test) FnA() *Container {
	return nil
}

// doc for FnB
func (m *Test) FnB() Duck {
	return nil
}

type Duck interface {
	DaggerObject
	// quack that thang
	Quack(ctx context.Context) (string, error)
}

// doc for FnC
func (m *Test) FnC() *Obj {
	return nil
}

// doc for Prim
func (m *Test) Prim() string {
	return "yo"
}

type Obj struct {
	// doc for FieldA
	FieldA *Container
	// doc for FieldB
	FieldB string
	// doc for FieldC
	FieldC *Obj
	// doc for FieldD
	FieldD *OtherObj
}

// doc for FnD
func (m *Obj) FnD() *Container {
	return nil
}

type OtherObj struct {
	// doc for OtherFieldA
	OtherFieldA *Container
	// doc for OtherFieldB
	OtherFieldB string
	// doc for OtherFieldC
	OtherFieldC *Obj
	// doc for OtherFieldD
	OtherFieldD *OtherObj
}

// doc for FnE
func (m *OtherObj) FnE() *Container {
	return nil
}

`,
		})

	t.Run("top-level", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn-a   doc for FnA")
		require.Contains(t, lines, "fn-b   doc for FnB")
		require.Contains(t, lines, "fn-c   doc for FnC")
		require.Contains(t, lines, "prim   doc for Prim")
	})

	t.Run("top-level from subdir", func(t *testing.T) {
		t.Parallel()
		// find-up should kick in
		out, err := ctr.
			WithWorkdir("/work/some/subdir").
			With(daggerFunctions()).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn-a   doc for FnA")
		require.Contains(t, lines, "fn-b   doc for FnB")
		require.Contains(t, lines, "fn-c   doc for FnC")
		require.Contains(t, lines, "prim   doc for Prim")
	})

	t.Run("return core object", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerFunctions("fn-a")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "file                          Retrieves a file at the given path.")
		require.Contains(t, lines, "as-tarball                    Returns a File representing the container serialized to a tarball.")
	})

	t.Run("return primitive", func(t *testing.T) {
		t.Parallel()
		_, err := ctr.With(daggerFunctions("prim")).Stdout(ctx)
		require.ErrorContains(t, err, `function "prim" returns type "STRING_KIND" with no further functions available`)
	})

	t.Run("alt casing", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerFunctions("fnA")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "file                          Retrieves a file at the given path.")
		require.Contains(t, lines, "as-tarball                    Returns a File representing the container serialized to a tarball.")
	})

	t.Run("return user interface", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerFunctions("fn-b")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "quack   quack that thang")
	})

	t.Run("return user object", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerFunctions("fn-c")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "field-a   doc for FieldA")
		require.Contains(t, lines, "field-b   doc for FieldB")
		require.Contains(t, lines, "field-c   doc for FieldC")
		require.Contains(t, lines, "field-d   doc for FieldD")
		require.Contains(t, lines, "fn-d      doc for FnD")
	})

	t.Run("return user object nested", func(t *testing.T) {
		t.Parallel()
		out, err := ctr.With(daggerFunctions("fn-c", "field-d")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "other-field-a   doc for OtherFieldA")
		require.Contains(t, lines, "other-field-b   doc for OtherFieldB")
		require.Contains(t, lines, "other-field-c   doc for OtherFieldC")
		require.Contains(t, lines, "other-field-d   doc for OtherFieldD")
		require.Contains(t, lines, "fn-e            doc for FnE")
	})
}
