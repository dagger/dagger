package templates

import (
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core/modules"
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
	generatedMain, err := funcs.moduleMainSrc()
	require.NoError(t, err)
	t.Log(generatedMain)
}

func TestParsePragmaComment(t *testing.T) {
	tests := []struct {
		name     string
		comment  string
		expected map[string]string
		rest     string
	}{
		{
			name:    "single key-value",
			comment: "dagger:foo=bar",
			expected: map[string]string{
				"foo": "bar",
			},
			rest: "",
		},
		{
			name:    "single key-value with trailing",
			comment: "dagger:foo=bar\n",
			expected: map[string]string{
				"foo": "bar",
			},
			rest: "",
		},
		{
			name:    "multiple key-value",
			comment: "dagger:foo=bar\ndagger:baz=qux",
			expected: map[string]string{
				"foo": "bar",
				"baz": "qux",
			},
			rest: "",
		},
		{
			name:    "interpolated key-value",
			comment: "line 1\ndagger:foo=bar\nline 2\ndagger:baz=qux\nline 3",
			expected: map[string]string{
				"foo": "bar",
				"baz": "qux",
			},
			rest: "line 1\nline 2\nline 3",
		},
		{
			name:    "interpolated key-value with trailing",
			comment: "line 1\ndagger:foo=bar\nline 2\ndagger:baz=qux\nline 3\n",
			expected: map[string]string{
				"foo": "bar",
				"baz": "qux",
			},
			rest: "line 1\nline 2\nline 3\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, rest := parsePragmaComment(test.comment)
			require.Equal(t, test.expected, actual)
			require.Equal(t, test.rest, rest)
		})
	}
}
