package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The engine image's builds are the module runtimes' only if both are made
// with one set of flags.
func TestGoSDKBuildFlagsMatchRuntime(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "sdk", "go", "runtime", "main.dang"))
	require.NoError(t, err)
	m := regexp.MustCompile(`buildFlags: \[(.*)\]`).FindSubmatch(src)
	require.NotNil(t, m, "the Go SDK's buildFlags")
	var flags []string
	for _, f := range strings.Split(string(m[1]), ", ") {
		flag, err := strconv.Unquote(f)
		require.NoError(t, err)
		flags = append(flags, flag)
	}
	require.Equal(t, goSDKBuildFlags, flags)
}

// A runtime's compiles generate no debug information its link would drop,
// and the standard library's compile lines stay those of a build without the
// flags, so that its archives in the engine image serve every other build.
func TestGoSDKBuildFlagsOnCompileLines(t *testing.T) {
	require.Contains(t, goSDKBuildFlags, "-ldflags=-s -w", "debug information is only left out of a binary that drops it")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/seed\n\ngo 1.26\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport _ \"fmt\"\n\nfunc main() {}\n"), 0o644))
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
	flagged := compiles(goSDKBuildFlags...)
	require.Contains(t, flagged, "fmt")
	require.Equal(t, plain["fmt"], flagged["fmt"])
	require.NotContains(t, plain["main"], "-dwarf=false")
	require.Contains(t, flagged["main"], "-dwarf=false")
}
