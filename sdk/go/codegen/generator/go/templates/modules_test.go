package templates

import (
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"dagger.io/dagger/modules"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

func TestModuleMainSrc(t *testing.T) {
	tmpdir := t.TempDir()

	testMain := `package main

// My Cool Test module
type TestMod struct {}

// Foo does a thing
func (m *TestMod) Foo(s string, i int, b bool) (string, error) {
	return "", nil
}

func main() {}
`
	err := os.WriteFile(filepath.Join(tmpdir, "main.go"), []byte(testMain), 0644)
	require.NoError(t, err)

	testGoMod := `module testMod

go 1.20
`
	err = os.WriteFile(filepath.Join(tmpdir, "go.mod"), []byte(testGoMod), 0644)
	require.NoError(t, err)

	fset := token.NewFileSet()
	pkgs, err := packages.Load(&packages.Config{
		Dir: tmpdir,
	}, ".")
	require.NoError(t, err)

	funcs := goTemplateFuncs{
		module: &modules.Config{
			Name: "testMod",
		},
		modulePkg:  pkgs[0],
		moduleFset: fset,
	}

	// TODO: assert on contents
	require.NotPanics(t, func() {
		generatedMain := funcs.moduleMainSrc()
		t.Log(generatedMain)
	})
}
