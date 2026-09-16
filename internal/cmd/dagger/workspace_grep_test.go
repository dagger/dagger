package daggercmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/idtui"
)

func TestWorkspaceGrepCommand(t *testing.T) {
	for _, name := range []string{"workspace", "ws"} {
		cmd, _, err := rootCmd.Find([]string{name, "grep"})
		require.NoError(t, err)
		require.Same(t, workspaceGrepCmd, cmd)
		require.NoError(t, cmd.ValidateArgs([]string{"pattern"}))
		require.NoError(t, cmd.ValidateArgs([]string{"pattern", "src", "/README.md"}))
	}
	require.NoError(t, validateFlagCapabilities(testRootCommand(), []string{"ws", "grep", "-i", "pattern", "-W", "github.com/dagger/dagger"}))

	for _, args := range [][]string{nil, {"--json", "-l", "pattern"}, {"--unknown", "pattern"}} {
		cmd := newWorkspaceGrepCmd()
		cmd.SetOut(io.Discard)
		var stderr bytes.Buffer
		cmd.SetErr(&stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		var exit idtui.ExitError
		require.ErrorAs(t, err, &exit)
		require.Equal(t, 2, exit.Code())
		require.NotEmpty(t, stderr.String())
	}

	// StringArray keeps commas within one glob, such as a brace expression.
	cmd := newWorkspaceGrepCmd()
	require.NoError(t, cmd.ParseFlags([]string{"-g", "*.{go,mod}", "-g", "!vendor/**"}))
	globs, err := cmd.Flags().GetStringArray("glob")
	require.NoError(t, err)
	require.Equal(t, []string{"*.{go,mod}", "!vendor/**"}, globs)
}

func TestWorkspaceGrepFlagErrors(t *testing.T) {
	// Exercise Main in a fresh process so this covers the early global flag
	// pass as well as Cobra's command parser, without starting an engine.
	const argsEnv = "DAGGER_TEST_GREP_FLAG_ARGS"
	if encoded := os.Getenv(argsEnv); encoded != "" {
		var args []string
		require.NoError(t, json.Unmarshal([]byte(encoded), &args))
		os.Args = append([]string{"dagger"}, args...)
		Main()
		return
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		args   []string
		want   string
		status int
	}{
		{"unknown flag", []string{"ws", "grep", "--unknown", "needle"}, "unknown flag", 2},
		{"missing glob value", []string{"ws", "grep", "needle", "--glob"}, "flag needs an argument", 2},
		{"missing short glob value", []string{"ws", "grep", "needle", "-g"}, "flag needs an argument", 2},
		{"invalid local boolean", []string{"ws", "grep", "needle", "--ignore-case=wat"}, "invalid argument", 2},
		{"invalid global boolean", []string{"ws", "grep", "needle", "--debug=wat"}, "invalid argument", 2},
		{"unavailable flag", []string{"ws", "grep", "needle", "--env=ci"}, "is not supported", 2},
		{"ordinary command parsing", []string{"ws", "ls", "--debug=wat"}, "invalid argument", 1},
		{"ordinary command capability", []string{"version", "--engine=cloud"}, "is not supported", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.args)
			require.NoError(t, err)
			cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestWorkspaceGrepFlagErrors$")
			cmd.Env = append(os.Environ(), argsEnv+"="+string(encoded))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err = cmd.Run()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit)
			require.Equal(t, tc.status, exit.ExitCode(), stderr.String())
			require.Empty(t, stdout.String())
			require.Contains(t, stderr.String(), tc.want)
		})
	}
}

func TestWorkspaceGrepPaths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cwd     string
		targets []string
		want    []string
	}{
		{"root default", "/", nil, []string{"."}},
		{"nested default", "/services/api", nil, []string{"services/api"}},
		{"relative and absolute", "/services/api", []string{"src", "../web", "/README.md", "/"}, []string{"services/api/src", "services/web", "README.md", "."}},
		{"leading dash", "/", []string{"-config", "./-config"}, []string{"./-config", "./-config"}},
		{"dot segments", "/services/api", []string{"./src/../main.go", "../../LICENSE"}, []string{"services/api/main.go", "LICENSE"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := workspaceGrepPaths(tc.cwd, tc.targets)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	for _, target := range []string{"../secret", "/../secret", "//../secret"} {
		_, err := workspaceGrepPaths("/", []string{target})
		require.ErrorContains(t, err, "escapes workspace root")
	}
	_, err := workspaceGrepPaths("/../secret", nil)
	require.ErrorContains(t, err, "escapes workspace root")
}

func TestWorkspaceGrepGlobalShortFlag(t *testing.T) {
	previous := shellOnError
	t.Cleanup(func() { shellOnError = previous })
	for _, tc := range []struct {
		args        []string
		shellOnFail bool
	}{
		{[]string{"grep", "-i", "needle"}, false},
		{[]string{"grep", "--shell-on-error", "needle"}, true},
		{[]string{"--shell-on-error", "grep", "needle"}, true},
	} {
		root := &cobra.Command{Use: "dagger"}
		root.PersistentFlags().BoolVarP(&shellOnError, "shell-on-error", "i", false, "Open a shell on failure")
		cmd := newWorkspaceGrepCmd()
		root.AddCommand(cmd)
		parseGlobalFlags(root, tc.args)
		require.Equal(t, tc.shellOnFail, shellOnError, tc.args)
	}
}

func TestWorkspaceGrepDisplayPath(t *testing.T) {
	for _, tc := range []struct {
		cwd, file, want string
	}{
		{"/", "./README.md", "README.md"},
		{"/services/api", "services/api/src/main.go", "src/main.go"},
		{"/services/api", "./README.md", "../../README.md"},
		{"/services/api", "/services/web/main.go", "../web/main.go"},
	} {
		got, err := workspaceGrepDisplayPath(tc.cwd, tc.file)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
}

func TestPrintWorkspaceGrep(t *testing.T) {
	results := []workspaceGrepResult{
		{FilePath: "src/main.go", LineNumber: 42, MatchedLines: "\tfirst match\n  second match\n"},
		{FilePath: "src/main.go", LineNumber: 58, MatchedLines: "\n"},
		{FilePath: "README.md", LineNumber: 3, MatchedLines: "no final newline"},
		{FilePath: "windows.txt", LineNumber: 1, MatchedLines: "match\r\n"},
	}
	var out bytes.Buffer
	require.NoError(t, printWorkspaceGrep(&out, results, workspaceGrepOptions{}, false))
	require.Equal(t, "src/main.go:42:\tfirst match\nsrc/main.go:43:  second match\nsrc/main.go:58:\nREADME.md:3:no final newline\nwindows.txt:1:match\r\n", out.String())

	out.Reset()
	require.NoError(t, printWorkspaceGrep(&out, results, workspaceGrepOptions{filesOnly: true}, false))
	require.Equal(t, "src/main.go\nREADME.md\nwindows.txt\n", out.String())

	out.Reset()
	require.NoError(t, printWorkspaceGrep(&out, nil, workspaceGrepOptions{}, false))
	require.Empty(t, out.String())
}

func TestPrintWorkspaceGrepJSON(t *testing.T) {
	result := workspaceGrepResult{
		FilePath: "src/main.go", LineNumber: 42, MatchedLines: "\tmatch\n", AbsoluteOffset: 128,
		Submatches: []workspaceGrepSubmatch{{Text: "match", Start: 1, End: 6}},
	}
	var out bytes.Buffer
	require.NoError(t, printWorkspaceGrep(&out, []workspaceGrepResult{result}, workspaceGrepOptions{json: true}, true))
	require.JSONEq(t, `[{"filePath":"src/main.go","lineNumber":42,"matchedLines":"\tmatch\n","absoluteOffset":128,"submatches":[{"text":"match","start":1,"end":6}]}]`, out.String())
	var decoded []workspaceGrepResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	require.Equal(t, []workspaceGrepResult{result}, decoded)

	out.Reset()
	require.NoError(t, printWorkspaceGrep(&out, nil, workspaceGrepOptions{json: true}, false))
	require.Equal(t, "[]\n", out.String())

	out.Reset()
	require.NoError(t, printWorkspaceGrep(&out, []workspaceGrepResult{{}}, workspaceGrepOptions{json: true}, false))
	require.Contains(t, out.String(), `"submatches":[]`)
}

func TestPrintWorkspaceGrepHighlight(t *testing.T) {
	results := []workspaceGrepResult{{
		FilePath: "example.txt", LineNumber: 5, MatchedLines: "é first\n  second match\n",
		Submatches: []workspaceGrepSubmatch{{Text: "first\n  second", Start: 3, End: 17}},
	}}
	// Use byte positions, including the multibyte character and newline.
	var out bytes.Buffer
	require.NoError(t, printWorkspaceGrep(&out, results, workspaceGrepOptions{}, true))
	require.Equal(t, "example.txt:5:é \x1b[1;31mfirst\x1b[0m\nexample.txt:6:\x1b[1;31m  second\x1b[0m match\n", out.String())

	out.Reset()
	require.NoError(t, printWorkspaceGrep(&out, results, workspaceGrepOptions{}, false))
	require.Equal(t, "example.txt:5:é first\nexample.txt:6:  second match\n", out.String())
}

func TestHighlightWorkspaceGrepLineBounds(t *testing.T) {
	require.Equal(t, "\x1b[1;31mabc\x1b[0m", highlightWorkspaceGrepLine("abc", 10, []workspaceGrepSubmatch{{Start: 0, End: 99}}))
	require.Equal(t, "abc", highlightWorkspaceGrepLine("abc", 10, []workspaceGrepSubmatch{{Start: 0, End: 5}, {Start: 50, End: 90}}))
	require.Equal(t, "a\x1b[1;31mb\x1b[0m\x1b[1;31mc\x1b[0m", highlightWorkspaceGrepLine("abc", 0, []workspaceGrepSubmatch{{Start: 2, End: 3}, {Start: 1, End: 2}}))
}

func TestWorkspaceGrepWriteError(t *testing.T) {
	for _, opts := range []workspaceGrepOptions{{}, {filesOnly: true}, {json: true}} {
		err := printWorkspaceGrep(workspaceGrepFailWriter{}, []workspaceGrepResult{{FilePath: "file"}}, opts, false)
		require.ErrorIs(t, err, io.ErrClosedPipe)
	}
}

type workspaceGrepFailWriter struct{}

func (workspaceGrepFailWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestWorkspaceGrepError(t *testing.T) {
	cmd := &cobra.Command{}
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	original := errors.New("search failed")
	err := workspaceGrepError(cmd, original)
	var exit idtui.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 2, exit.Code())
	require.ErrorIs(t, err, original)
	require.Contains(t, stderr.String(), "search failed")

	stderr.Reset()
	err = workspaceGrepError(cmd, idtui.ExitError{OriginalCode: 1, Original: original})
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 2, exit.Code())
	require.Empty(t, stderr.String())
}
