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

// Keep GitRepo methods in separate files to emulate non-deterministic package file load order.
var visitDeterminismSources = map[string]string{
	"main.go": `package main
type GitRepo struct{}
`,
	"remote_a.go": `package main
type RemoteA struct{}

func (m *GitRepo) RemoteA() *RemoteA { return &RemoteA{} }
`,
	"remote_b.go": `package main
type RemoteB struct{}

func (m *GitRepo) RemoteB() *RemoteB { return &RemoteB{} }
`,
}

// loadVisitDeterminismTestPackage builds the same package with caller-controlled source file order.
func loadVisitDeterminismTestPackage(t *testing.T, fileOrder ...string) *packages.Package {
	t.Helper()

	// Build the same package with controlled file insertion order to simulate loader variability.
	fset := token.NewFileSet()
	syntax := make([]*ast.File, 0, len(fileOrder))
	for _, fileName := range fileOrder {
		src, ok := visitDeterminismSources[fileName]
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

// newVisitDeterminismTemplateFuncs creates the minimal template context needed to call visitTypes.
func newVisitDeterminismTemplateFuncs(testPkg *packages.Package) goTemplateFuncs {
	return goTemplateFuncs{
		cfg: generator.Config{
			ModuleConfig: &generator.ModuleGeneratorConfig{
				ModuleName: "git-repo",
			},
		},
		modulePkg:  testPkg,
		moduleFset: testPkg.Fset,
	}
}

type visitSnapshot struct {
	structNames        []string
	gitRepoMethodNames []string
}

// snapshotVisit runs visitTypes once and captures the struct and GitRepo method traversal order.
func snapshotVisit(t *testing.T, fileOrder ...string) visitSnapshot {
	t.Helper()

	testPkg := loadVisitDeterminismTestPackage(t, fileOrder...)
	funcs := newVisitDeterminismTemplateFuncs(testPkg)
	snapshot := visitSnapshot{}

	// This test exercises struct traversal only.
	noopRoot := func(string) error { return nil }
	noopIface := func(_ *parseState, _ *types.Named, _ *types.TypeName, _ *parsedIfaceType, _ *types.Interface) error {
		return nil
	}
	noopEnum := func(_ *parseState, _ *types.Named, _ *types.TypeName, _ *parsedEnumType, _ *types.Basic) error {
		return nil
	}
	// Capture both top-level struct order and GitRepo method order in one pass.
	captureStruct := func(_ *parseState, _ *types.Named, obj *types.TypeName, objTypeSpec *parsedObjectType, _ *types.Struct) error {
		snapshot.structNames = append(snapshot.structNames, obj.Name())
		if obj.Name() != "GitRepo" {
			return nil
		}
		for _, method := range objTypeSpec.methods {
			snapshot.gitRepoMethodNames = append(snapshot.gitRepoMethodNames, method.name)
		}
		return nil
	}

	err := funcs.visitTypes(false, &visitorFuncs{
		RootVisitor:   noopRoot,
		StructVisitor: captureStruct,
		IfaceVisitor:  noopIface,
		EnumVisitor:   noopEnum,
	})
	require.NoError(t, err)

	return snapshot
}

// TestVisitTypesDeterministicAcrossFiles verifies traversal order is stable across file load permutations.
func TestVisitTypesDeterministicAcrossFiles(t *testing.T) {
	t.Parallel()

	orderAFirst := snapshotVisit(t, "main.go", "remote_a.go", "remote_b.go")
	orderBFirst := snapshotVisit(t, "main.go", "remote_b.go", "remote_a.go")

	t.Run("struct order", func(t *testing.T) {
		// Mock the package-loading race by changing file insertion order in the
		// file set; codegen should still visit subtypes in a stable order.
		require.Equal(t, orderAFirst.structNames, orderBFirst.structNames)
	})

	t.Run("gitrepo method order", func(t *testing.T) {
		require.Equal(t, []string{"RemoteA", "RemoteB"}, orderAFirst.gitRepoMethodNames)
		require.Equal(t, orderAFirst.gitRepoMethodNames, orderBFirst.gitRepoMethodNames)
	})
}
