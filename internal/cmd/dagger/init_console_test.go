package daggercmd

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// Drive the actual CLI's interactive frontend without a terminal. Both
// operations are skipped, so this test needs an engine but no Cloud account.
func TestInitConsoleShowsCommands(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	bin := os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN")
	if bin == "" {
		bin = "dagger"
	}
	root := t.TempDir()
	git := exec.CommandContext(t.Context(), "git", "init", root)
	gitOutput, err := git.CombinedOutput()
	require.NoError(t, err, string(gitOutput))
	configRoot := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, bin, "-W", root, "init")
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + configRoot,
		"XDG_CONFIG_HOME=" + filepath.Join(configRoot, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(configRoot, ".cache"),
		"XDG_STATE_HOME=" + filepath.Join(configRoot, ".local", "state"),
		"DAGGER_TUI_CONSOLE=" + address,
	}
	// Use the dev engine selected by the test harness.
	for _, name := range []string{"_EXPERIMENTAL_DAGGER_RUNNER_HOST", "DAGGER_ENGINE"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	require.NoError(t, command.Start())
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()

	httpClient := &http.Client{Timeout: 5 * time.Second}
	screen := func() string {
		response, err := httpClient.Get("http://" + address + "/screen")
		if err != nil {
			return ""
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return string(data)
	}
	send := func(endpoint, value string) {
		response, err := httpClient.Post("http://"+address+endpoint, "text/plain", strings.NewReader(value))
		require.NoError(t, err)
		defer response.Body.Close()
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
	}
	var first string
	require.Eventually(t, func() bool {
		first = screen()
		return strings.Contains(first, "module recommend")
	}, 45*time.Second, 100*time.Millisecond)
	require.Contains(t, first, "Find and install suitable modules?")
	require.NotContains(t, first, "Enable cloud checks?")
	t.Logf("First prompt:\n%s", first)
	send("/key", "enter") // Skip module recommendations.
	var second string
	defer func() {
		if t.Failed() {
			t.Logf("Last prompt:\n%s", second)
		}
	}()
	require.Eventually(t, func() bool {
		second = screen()
		return strings.Contains(second, "Enable cloud checks?")
	}, 5*time.Second, 100*time.Millisecond)
	require.Contains(t, second, "cloud checks on")
	send("/key", "enter") // Skip Cloud checks.
	// The console deliberately stays open after the command completes.
	// Wait for the last form to close, then stop the console with SIGINT.
	require.Eventually(t, func() bool {
		final := screen()
		return !strings.Contains(final, "Enable cloud checks?") && strings.Contains(final, "cloud checks on")
	}, 5*time.Second, 100*time.Millisecond)
	require.NoError(t, command.Process.Signal(os.Interrupt))
	select {
	case err := <-finished:
		require.NoError(t, err, stderr.String())
	case <-ctx.Done():
		t.Fatal("init did not finish after both optional operations were skipped")
	}
	// Commands must also survive in the completed output, after forms close.
	output := ansi.Strip(stdout.String() + stderr.String())
	require.Contains(t, output, "module recommend")
	require.Contains(t, output, "cloud checks on")
	require.Contains(t, output, "Skipped command:")
}
