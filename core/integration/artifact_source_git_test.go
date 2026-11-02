package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ArtifactsSuite) TestSourceGitSelectors(ctx context.Context, t *testctx.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	write := func(path, contents string) {
		t.Helper()
		target := filepath.Join(repo, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		require.NoError(t, os.WriteFile(target, []byte(contents), 0o644))
	}
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := hostDaggerCommandRaw(ctx, t, dir, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		require.NoError(t, err, stderr.String())
		return strings.TrimSpace(string(out))
	}
	list := func(dir string, flags ...string) []string {
		t.Helper()
		out := run(dir, append([]string{"list", "--type=Check", "-f=link"}, flags...)...)
		if out == "" {
			return nil
		}
		return strings.Split(out, "\n")
	}
	git("init", "-b", "main")
	git("config", "user.name", "Dagger Tests")
	git("config", "user.email", "dagger@example.com")
	write("dagger.toml", "[modules.probe]\nsource = './probe'\nmain = true\n")
	write("probe/dagger-module.toml", "name = 'probe'\nengineVersion = 'v1.0.0-beta.20'\n[runtime]\nsource = 'dang'\n")
	write("probe/main.dang", "# "+rand.Text()+`
type Probe {
 pub staged(ws: Workspace!): Void @check { ws.file("/src/staged.txt").contents; null }
 pub other(ws: Workspace!): Void @check { ws.file("/other.txt").contents; null }
 pub cross(ws: Workspace!): Void @check {
   ws.file("/src/shared.txt").contents
   ws.file("/README.md").contents
   null
 }
}`)
	for _, path := range []string{"src/staged.txt", "src/shared.txt", "other.txt", "README.md"} {
		write(path, "initial\n")
	}
	git("add", ".")
	git("commit", "-m", "initial")
	write("src/staged.txt", "committed\n")
	git("add", "src/staged.txt")
	git("commit", "-m", "change staged input")

	all := []string{"dag+check://?check=staged", "dag+check://?check=other", "dag+check://?check=cross"}
	require.ElementsMatch(t, all, list(repo, "--commit", "HEAD"), "cold checks remain selected")
	run(repo, "check", "--generated=false", "--uncommitted")
	require.ElementsMatch(t, []string{"dag+check://?check=staged"}, list(repo, "--commit", "HEAD"))
	require.Empty(t, list(repo, "--uncommitted"))

	write("README.md", "documentation change\n")
	// Record current reads so the assertions test path matching, not unknown inputs.
	run(repo, "check", "--generated=false", "--uncommitted")
	require.ElementsMatch(t, []string{"dag+check://?check=cross"}, list(repo, "--uncommitted"))
	require.ElementsMatch(t, []string{"dag+check://?check=staged", "dag+check://?check=cross"}, list(repo, "--commit", "HEAD", "--uncommitted"))
	require.Empty(t, list(repo, "--uncommitted", "--path", "src"), "scope changes before matching cross dependencies")

	write("src/staged.txt", "staged change\n")
	git("add", "src/staged.txt")
	write("other.txt", "unstaged change\n")
	run(repo, "check", "--generated=false", "--uncommitted")
	require.ElementsMatch(t, all, list(repo, "--uncommitted"))
	src := filepath.Join(repo, "src")
	run(src, "check", "--generated=false", "--uncommitted")
	require.ElementsMatch(t, []string{"dag+check://?check=staged"}, list(src, "--uncommitted", "--path", "."))
	require.ElementsMatch(t, []string{"dag+check://?check=other"}, list(src, "--uncommitted", "--path", "/other.txt"))
	run(repo, "check", "--uncommitted", "--path", "missing")
}
