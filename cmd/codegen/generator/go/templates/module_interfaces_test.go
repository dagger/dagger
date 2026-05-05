package templates

import (
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

func TestLegacyModuleInterfaceIDSurface(t *testing.T) {
	spec := &parsedIfaceType{
		name:              "CustomIface",
		moduleName:        "test",
		legacyGoSDKCompat: true,
	}

	code, err := spec.ImplementationCode()
	require.NoError(t, err)
	got := fmt.Sprintf("%#v", code)

	require.Contains(t, got, "type CustomIfaceID = dagger.ID")
	require.Contains(t, got, "func LoadCustomIfaceFromID(r *dagger.Client, id CustomIfaceID) CustomIface")
	require.Contains(t, got, "func (r *customIfaceImpl) ID(ctx context.Context) (CustomIfaceID, error)")
}

func TestParseGoIfaceAcceptsImportedDaggerObject(t *testing.T) {
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		fullPath := filepath.Join(dir, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
		require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o644))
	}

	write("go.mod", `module example.com/test

go 1.25
`)
	write("internal/dagger/dagger.go", `package dagger

import (
	"context"
	"encoding/json"
)

type ID string

type DaggerObject interface {
	XXX_GraphQLType() string
	XXX_GraphQLIDType() string
	XXX_GraphQLID(ctx context.Context) (string, error)
	MarshalJSON() ([]byte, error)
	ID(ctx context.Context) (ID, error)
}

var _ json.Marshaler
`)
	write("dagger.gen.go", `package main

import (
	"context"
	"encoding/json"

	"example.com/test/internal/dagger"
)

type DaggerObject interface {
	XXX_GraphQLType() string
	XXX_GraphQLIDType() string
	XXX_GraphQLID(ctx context.Context) (string, error)
	MarshalJSON() ([]byte, error)
	ID(ctx context.Context) (dagger.ID, error)
}

var _ json.Marshaler
`)
	write("main.go", `package main

import (
	"context"

	"example.com/test/internal/dagger"
)

type Test struct{}

func (m *Test) Fn() LocalOtherIface {
	return nil
}

type LocalOtherIface interface {
	dagger.DaggerObject
	Foo(ctx context.Context) (string, error)
}
`)

	fset := token.NewFileSet()
	pkgs, err := packages.Load(&packages.Config{
		Dir:   dir,
		Fset:  fset,
		Mode:  packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedModule,
		Tests: false,
	}, ".")
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	require.Empty(t, packages.PrintErrors(pkgs))

	funcs := goTemplateFuncs{
		cfg: generator.Config{
			ModuleConfig: &generator.ModuleGeneratorConfig{
				ModuleName: "test",
			},
		},
		modulePkg:  pkgs[0],
		moduleFset: fset,
	}

	var ifaceNames []string
	err = funcs.visitTypes(true, &visitorFuncs{
		RootVisitor: func(string) error { return nil },
		StructVisitor: func(*parseState, *types.Named, *types.TypeName, *parsedObjectType, *types.Struct) error {
			return nil
		},
		IfaceVisitor: func(_ *parseState, _ *types.Named, obj *types.TypeName, _ *parsedIfaceType, _ *types.Interface) error {
			ifaceNames = append(ifaceNames, obj.Name())
			return nil
		},
		EnumVisitor: func(*parseState, *types.Named, *types.TypeName, *parsedEnumType, *types.Basic) error {
			return nil
		},
	})
	require.NoError(t, err)
	require.Contains(t, ifaceNames, "LocalOtherIface")
}
