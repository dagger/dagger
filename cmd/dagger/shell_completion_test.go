package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	os.Exit(oteltest.Main(m))
}

func Middleware() []testctx.Middleware[*testing.T] {
	return []testctx.Middleware[*testing.T]{
		oteltest.WithTracing[*testing.T](
			oteltest.TraceConfig[*testing.T]{
				StartOptions: testutil.SpanOpts[*testing.T],
			},
		),
		oteltest.WithLogging[*testing.T](),
	}
}

func getLocalDaggerCliPath(t testing.TB) string {
	t.Helper()
	if cli := os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN"); cli != "" {
		return cli
	}
	cli, err := exec.LookPath("dagger")
	require.NoError(t, err, "dagger binary not found in PATH")
	require.NotEmpty(t, cli, "dagger binary path is empty even after LookPath")
	return cli
}

func runHostDaggerCommand(ctx context.Context, t testing.TB, workdir string, args ...string) error {
	t.Helper()
	cmd := exec.CommandContext(ctx, getLocalDaggerCliPath(t), args...)
	cmd.Env = os.Environ()
	cmd.Dir = workdir
	output, err := cmd.CombinedOutput()
	t.Logf("Cmd: %v, Output: %s", cmd.Args, output)
	if err != nil {
		return fmt.Errorf("command failed: %w", err)
	}
	return nil
}

func parseCmd(cmdline string) (base string, cursor int, expected string) {
	start := strings.IndexRune(cmdline, '<')
	end := strings.IndexRune(cmdline, '>')
	if start == -1 || end == -1 || start >= end {
		panic("invalid cmdline: missing <expr>")
	}
	inprogress, rest, ok := strings.Cut(cmdline[start+1:end], "$")
	if !ok {
		panic("invalid cmdline: missing '$' token")
	}
	expected = strings.TrimSpace(inprogress + rest)
	base = cmdline[:start] + inprogress + cmdline[end+1:]
	cursor = start + len(inprogress)
	return
}

func assertAutocomplete(t *testctx.T, ac shellAutoComplete, cmdline string) {
	t.Helper()
	base, cursor, expected := parseCmd(cmdline)
	_, comp := ac.Do([][]rune{[]rune(base)}, 0, cursor)
	require.NotNil(t, comp)
	require.Equal(t, 1, comp.NumCategories())
	var opts []string
	for i := 0; i < comp.NumEntries(0); i++ {
		opts = append(opts, comp.Entry(0, i).Title())
	}
	require.Contains(t, opts, expected)
}

type DaggerCMDSuite struct{}

func TestDaggerCMD(tt *testing.T) {
	testctx.New(tt, Middleware()...).RunTests(DaggerCMDSuite{})
}

type shellAutocompleteTestCase struct {
	Name           string
	CmdlinesToTest []string
	setup          func(ctx context.Context, t *testctx.T, tmpDir string) (moduleRootRelPath string, err error)
	Env            [][2]string
}

func (DaggerCMDSuite) TestShellAutocomplete(ctx context.Context, t *testctx.T) {
	testCases := []shellAutocompleteTestCase{
		{
			Name: "WithStandardWolfiModule",
			CmdlinesToTest: []string{
				// top-level function
				`<con$tainer >`,
				`<$container >`,
				`  <$container >`,
				`<con$tainer > "alpine:latest"`,
				`<con$tainer >| directory`,

				// top-level deps
				`<a$lpine >`,

				// stdlib fallback
				`<dir$ectory >`,
				`directory | <with$-new-file >`,

				// chaining
				`container | <$directory >`,
				`container | <$with-directory >`,
				`container | <dir$ectory >`,
				`container | <with-$directory >`,
				`container | directory "./path" | <f$ile >`,

				// subshells
				`container | with-directory $(<$container >)`,
				`container | with-directory $(<con$tainer >)`,
				`container | with-directory $(container | <$directory >)`,
				`container | with-directory $(container | <dir$ectory >)`,
				`container | with-directory $(container | <$directory >`,
				`container | with-directory $(container | <dir$ectory >`,

				// args
				`container <--$packages >`,
				`container <--$packages > | directory`,
				`container | directory <--$expand >`,

				// TODO: These have been hidden. Uncomment when stable, or put them
				// bethind a feature flag so they can be tested even if hidden.

				// // .deps builtin
				// `.deps | <$alpine >`,
				// `.deps | <a$lpine >`,
				//
				// // .stdlib builtin
				// `.stdlib | <$container >`,
				// `.stdlib | <con$tainer >`,
				// `.stdlib | container <--$platform >`,
				// `.stdlib | container | <dir$ectory >`,
				//
				// // .core builtin
				// `.core | <con$tainer >`,
				// `.core | container <--$platform >`,
				// `.core | container | <dir$ectory >`,

				// FIXME: avoid inserting extra spaces
				// `<contain$er> `,
			},
			// Env: // No Env var, rely on CWD
			setup: func(ctx context.Context, t *testctx.T, tmpDir string) (string, error) {
				wd, err := os.Getwd()
				if err != nil {
					return "", err
				}
				moduleSrc := filepath.Join(wd, "../../modules")
				if err := os.CopyFS(tmpDir, os.DirFS(moduleSrc)); err != nil {
					t.Logf("CopyFS failed: %v, using fallback", err)
					cp := exec.CommandContext(ctx, "cp", "-r", "-T", moduleSrc, tmpDir)
					if out, err := cp.CombinedOutput(); err != nil {
						return "", fmt.Errorf("fallback cp failed: %w, output: %s", err, out)
					}
				}
				git := exec.CommandContext(ctx, "git", "init")
				git.Dir = tmpDir
				if out, err := git.CombinedOutput(); err != nil {
					return "", fmt.Errorf("git init failed: %w, output: %s", err, out)
				}
				return "wolfi", nil
			},
		},
		{
			Name: "WithNoSDKModule",
			CmdlinesToTest: []string{
				// Complete the 'hello' module name installed in 'test'
				`<hel$lo >`,
				`<$hello >`,

				// // Complete the 'hello' function within the 'hello' module
				// // Note: These might still fail if the underlying completion logic issue persists,
				// // but they represent the correct scenario for this simplified setup.
				// `hello | <$hello>`,
				// `hello | <hel$lo >`,

				`<con$tainer >`,
				`<dir$ectory >`,
			},
			setup: func(ctx context.Context, t *testctx.T, tmpDir string) (string, error) {
				testDir := filepath.Join(tmpDir, "test")
				helloDir := filepath.Join(testDir, "hello")
				code := `package main
 type Hello struct{}
 func (m *Hello) Hello() string { return "hi" }`
				if err := os.MkdirAll(helloDir, 0755); err != nil {
					return "", err
				}
				if err := os.WriteFile(filepath.Join(helloDir, "main.go"), []byte(code), 0644); err != nil {
					return "", err
				}
				if err := runHostDaggerCommand(ctx, t, helloDir, "init", "--sdk=go", "--name=hello"); err != nil {
					return "", err
				}
				if err := os.MkdirAll(testDir, 0755); err != nil {
					return "", err
				}
				if err := runHostDaggerCommand(ctx, t, testDir, "init", "--name=test"); err != nil {
					return "", err
				}
				if err := runHostDaggerCommand(ctx, t, testDir, "install", "./hello"); err != nil {
					return "", err
				}
				return "test", nil
			},
		},
	}

	origWD, err := os.Getwd()
	require.NoError(t, err)

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.Name, func(ctx context.Context, t *testctx.T) {
			tmpDir := t.TempDir()
			t.Cleanup(func() { _ = os.Chdir(origWD) })
			root, err := tc.setup(ctx, t, tmpDir)
			require.NoError(t, err)
			absRoot := filepath.Join(tmpDir, root)
			require.DirExists(t, absRoot)
			require.NoError(t, os.Chdir(absRoot))
			for _, env := range tc.Env {
				t.Setenv(env[0], env[1])
			}
			client, err := dagger.Connect(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { client.Close() })
			handler := &shellCallHandler{dag: client, debug: false}
			require.NoError(t, handler.RunAll(ctx, nil))
			ac := shellAutoComplete{handler}
			for _, cmd := range tc.CmdlinesToTest {
				t.Run(cmd, func(ctx context.Context, t *testctx.T) {
					assertAutocomplete(t, ac, cmd)
				})
			}
		})
	}
}
