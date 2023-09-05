package templates

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModuleMainSrc(t *testing.T) {
	tmpdir := t.TempDir()

	funcs := goTemplateFuncs{
		moduleName:          "testMod",
		sourceDirectoryPath: tmpdir,
	}

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

	// TODO: assert on contents
	require.NotPanics(t, func() {
		generatedMain := funcs.moduleMainSrc()
		t.Log(generatedMain)
	})
}
