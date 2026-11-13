package main

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/stretchr/testify/require"
)

// A runtime's compiles generate no debug information its link would drop, its
// generated client compiles without inlining, and the standard library's compile lines stay those of a build without the
// flags, so that its archives in the engine image serve every other build.
func TestGoSDKBuildFlagsOnCompileLines(t *testing.T) {
	require.Contains(t, distconsts.GoSDKBuildFlags(), "-ldflags=-s -w", "debug information is only left out of a binary that drops it")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/seed\n\ngo 1.26\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport (\n\t_ \"fmt\"\n\n\t_ \"example.com/seed/internal/dagger\"\n)\n\nfunc main() {}\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal", "dagger"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "dagger", "dagger.go"), []byte("package dagger\n"), 0o644))
	compiles := func(flags ...string) map[string]string {
		cmd := exec.Command("go", append(append([]string{"build", "-n", "-a"}, flags...), "-o", os.DevNull, ".")...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOCACHE="+t.TempDir(), "GOFLAGS=")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		lines := map[string]string{}
		for _, l := range strings.Split(string(out), "\n") {
			if m := regexp.MustCompile(`/compile .* -p (\S+) `).FindStringSubmatch(l); m != nil {
				// Each build numbers its work directories and has its own build IDs.
				lines[m[1]] = regexp.MustCompile(`-buildid \S+`).ReplaceAllString(regexp.MustCompile(`\$WORK/b\d+`).ReplaceAllString(l, "$$WORK"), "")
			}
		}
		return lines
	}
	plain := compiles()
	flagged := compiles(distconsts.GoSDKBuildFlags()...)
	require.Contains(t, flagged, "fmt")
	require.Equal(t, plain["fmt"], flagged["fmt"])
	require.NotContains(t, plain["main"], "-dwarf=false")
	require.Contains(t, flagged["main"], "-dwarf=false")
	require.NotContains(t, flagged["main"], " -l ", "only the generated client compiles without inlining")
	client := flagged["example.com/seed/internal/dagger"]
	require.Contains(t, client, "-dwarf=false")
	require.Contains(t, client, " -l ")
}

// The Go SDK's shared imports name every third-party package generated code
// imports, which sdk/go/internal/codegendeps pins: a module's dependency calls
// for what they reach are the image's only when the list covers them all.
func TestGoSDKSharedImportsCoverGeneratedCode(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "sdk", "go", "internal", "codegendeps", "codegendeps.go"), nil, parser.ImportsOnly)
	require.NoError(t, err)
	require.NotEmpty(t, f.Imports)
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		require.Contains(t, distconsts.GoSDKSharedImports(), path, "generated code imports %s, which the Go SDK's shared imports leave out", path)
	}
}
