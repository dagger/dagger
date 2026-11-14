package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
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

// An exported bundle of another version than an engine of this source merges,
// older or newer, is an unreadableError, which makes the tool exit 3 so an
// engine of the image's build makes the entries; one of its version passes.
func TestSameBundleVersion(t *testing.T) {
	for _, version := range []int{dagql.ValueBundleVersion - 1, dagql.ValueBundleVersion + 1} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, bundleFile), []byte(fmt.Sprintf(`{"version":%d,"roots":[]}`, version)), 0o644))
		var unreadable unreadableError
		require.True(t, errors.As(sameBundleVersion(dir), &unreadable), "bundle version %d", version)
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, bundleFile), []byte(fmt.Sprintf(`{"version":%d,"roots":[]}`, dagql.ValueBundleVersion)), 0o644))
	require.NoError(t, sameBundleVersion(dir))
}
