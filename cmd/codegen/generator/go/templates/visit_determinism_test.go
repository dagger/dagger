package templates

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	"github.com/dagger/dagger/cmd/codegen/generator"
)

var deterministicVisitTestSources = map[string]string{
	"main.go": `package main
type GitRepo struct{}

func (m *GitRepo) RemoteA() *RemoteA { return &RemoteA{} }
func (m *GitRepo) RemoteB() *RemoteB { return &RemoteB{} }
`,
	"remote_a.go": `package main
type RemoteA struct{}
`,
	"remote_b.go": `package main
type RemoteB struct{}
`,
}

func loadVisitDeterminismTestPackage(t *testing.T, fileOrder ...string) *packages.Package {
	t.Helper()

	fset := token.NewFileSet()
	syntax := make([]*ast.File, 0, len(fileOrder))
	for _, fileName := range fileOrder {
		src, ok := deterministicVisitTestSources[fileName]
		require.Truef(t, ok, "missing source for %q", fileName)

		file, err := parser.ParseFile(fset, fileName, src, 0)
		require.NoErrorf(t, err, "parse %q", fileName)
		syntax = append(syntax, file)
	}

	typesPkg, err := (&types.Config{}).Check("example.com/testmodule", fset, syntax, nil)
	require.NoError(t, err)

	return &packages.Package{
		Types:  typesPkg,
		Syntax: syntax,
		Fset:   fset,
		Module: &packages.Module{Dir: "."},
	}
}

func structVisitOrder(t *testing.T, fileOrder ...string) []string {
	t.Helper()

	testPkg := loadVisitDeterminismTestPackage(t, fileOrder...)
	funcs := goTemplateFuncs{
		cfg: generator.Config{
			ModuleConfig: &generator.ModuleGeneratorConfig{
				ModuleName: "git-repo",
			},
		},
		modulePkg:  testPkg,
		moduleFset: testPkg.Fset,
	}

	visited := []string{}
	err := funcs.visitTypes(false, &visitorFuncs{
		RootVisitor: func(string) error {
			return nil
		},
		StructVisitor: func(_ *parseState, _ *types.Named, obj *types.TypeName, _ *parsedObjectType, _ *types.Struct) error {
			visited = append(visited, obj.Name())
			return nil
		},
		IfaceVisitor: func(_ *parseState, _ *types.Named, _ *types.TypeName, _ *parsedIfaceType, _ *types.Interface) error {
			return nil
		},
		EnumVisitor: func(_ *parseState, _ *types.Named, _ *types.TypeName, _ *parsedEnumType, _ *types.Basic) error {
			return nil
		},
	})
	require.NoError(t, err)

	return visited
}

func TestVisitTypesDeterministicAcrossFiles(t *testing.T) {
	t.Parallel()

	orderAFirst := structVisitOrder(t, "main.go", "remote_a.go", "remote_b.go")
	orderBFirst := structVisitOrder(t, "main.go", "remote_b.go", "remote_a.go")

	// Mock the package-loading race by changing file insertion order in the
	// file set; codegen should still visit subtypes in a stable order.
	require.Equal(t, orderAFirst, orderBFirst)
}
