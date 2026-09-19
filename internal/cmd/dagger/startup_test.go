package daggercmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// Exercise Main in a fresh process: the bug happened before Cobra, so testing
// command handlers alone would miss both the failure and unwanted startup work.
func TestCommandStartup(t *testing.T) {
	const argsEnv = "DAGGER_TEST_STARTUP_ARGS"
	if encoded := os.Getenv(argsEnv); encoded != "" {
		var args []string
		require.NoError(t, json.Unmarshal([]byte(encoded), &args))
		os.Args = append([]string{"dagger"}, args...)
		Main()
		os.Exit(0)
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	// SDK names must be discovered before Cobra renders help. Listing names
	// needs only configuration, even when the selected engine is unreachable.
	sdkDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sdkDir, "dagger.toml"), []byte(`
[modules.example-sdk]
source = "github.com/dagger/go-sdk"
[sdks.example]
module = "example-sdk"
`), 0o600))
	for _, tc := range []struct {
		name   string
		args   []string
		want   string
		status int
	}{
		{"remote workspace does not block help", []string{"-W", "github.com/dagger/dagger@pull/14159/head", "api", "--help"}, "Dagger API", 0},
		{"help command does not start profiling", []string{"help", "api"}, "Dagger API", 0},
		{"help command skips setup", []string{"-W", ".", "help", "api"}, "Dagger API", 0},
		{"help accepts unused workspace flag", []string{"-W", ".", "version", "--help"}, "Print dagger version", 0},
		{"unknown flag takes precedence", []string{"-W", ".", "api", "--typo"}, "unknown flag", 1},
		{"unknown flag still fails with help", []string{"-W", ".", "api", "--help", "--typo"}, "unknown flag", 1},
		{"missing workspace value", []string{"-W", ".", "api", "--workspace"}, "flag needs an argument", 1},
		{"invalid help value", []string{"-W", ".", "api", "--help=invalid"}, "invalid argument", 1},
		{"execution rejects leading workspace flag", []string{"-W", ".", "version"}, "not supported", 1},
		{"execution rejects trailing workspace flag", []string{"version", "-W", "."}, "not supported", 1},
		{"version stays local", []string{"version", "-q"}, "", 0},
		{"bare API group shows help", []string{"-W", ".", "api"}, "Dagger API", 0},
		{"bare module group shows help", []string{"-W", ".", "module"}, "Dagger modules", 0},
		{"nested module group shows help", []string{"-W", ".", "module", "client"}, "generated clients", 0},
		{"cloud group skips authentication", []string{"-W", ".", "cloud", "org"}, "organizations", 0},
		{"API group typo keeps existing help behavior", []string{"-W", ".", "api", "typo"}, "Dagger API", 0},
		{"module group typo keeps existing error behavior", []string{"-W", ".", "module", "typo"}, "unknown command", 1},
		{"root typo fails before setup", []string{"-W", ".", "typo"}, "unknown command", 1},
		{"bare root shows usage", []string{"-W", "."}, "USAGE", 0},
		{"short help flag", []string{"-W", ".", "api", "-h"}, "Dagger API", 0},
		{"help through module alias", []string{"-W", ".", "mod", "--help"}, "Dagger modules", 0},
		{"last help value wins", []string{"version", "-y", "--help=false", "--help"}, "Print dagger version", 0},
		{"false help still enforces execution policy", []string{"version", "-y", "--help", "--help=false"}, "not supported", 1},
		{"workspace value is not a help flag", []string{"version", "--workspace=--help"}, "not supported", 1},
		{"help after separator stays an argument", []string{"-W", ".", "api", "--", "--help"}, "Dagger API", 0},
		{"help needs no interactive terminal", []string{"--shell-on-error", "check", "--help"}, "Verify your project", 0},
		{"help needs no tty progress frontend", []string{"--progress=tty", "check", "--help"}, "Verify your project", 0},
		{"help needs no working directory", []string{"--workdir=/no-such-directory-for-startup-test", "check", "--help"}, "Verify your project", 0},
		// The shell sends this when completing `dagger -W . api <Tab>`.
		{"tab completion after workspace selection", []string{"__complete", "-W", ".", "api", ""}, "functions", 0},
		{"completion skips shared setup", []string{"__complete", "api", ""}, "functions", 0},
		{"SDK names are discovered for module init help", []string{"--workdir", sdkDir, "module", "init", "--help"}, "example", 0},
		{"SDK names are discovered for the help command", []string{"--workdir", sdkDir, "help", "module", "init"}, "example", 0},
		{"SDK names are discovered for client help", []string{"--workdir", sdkDir, "module", "client", "add", "--help"}, "example", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A profile file would reveal that shared execution setup ran.
			profile := filepath.Join(t.TempDir(), "profile")
			encoded, err := json.Marshal(tc.args)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCommandStartup$")
			cmd.Env = append(os.Environ(),
				argsEnv+"="+string(encoded),
				"DAGGER_X_RELEASE=",
				"DAGGER_ENGINE=tcp://127.0.0.1:1",
				"DAGGER_CLOUD_TOKEN=",
				"XDG_CONFIG_HOME="+t.TempDir(),
				"CPUPROFILE="+profile,
				"PPROF=",
				"DAGGER_NO_UPDATE_CHECK=1",
			)
			output, err := cmd.CombinedOutput()
			if tc.status == 0 {
				require.NoError(t, err, string(output))
			} else {
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit, string(output))
				require.Equal(t, tc.status, exit.ExitCode(), string(output))
			}
			require.Contains(t, string(output), tc.want)
			require.NoFileExists(t, profile, "help and invalid invocations must not run shared setup")
			require.NotContains(t, string(output), "OAuth")
		})
	}
}

func TestRootArgsRespectWorkdir(t *testing.T) {
	oldWorkdir := workdir
	t.Cleanup(func() { workdir = oldWorkdir })
	workdir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "script.dsh"), []byte("version\n"), 0o600))
	cmd := &cobra.Command{Use: "dagger"}
	require.NoError(t, validateRootArgs(cmd, []string{"script.dsh"}))
	require.ErrorContains(t, validateRootArgs(cmd, []string{"missing.dsh"}), "unknown command or file")
}
