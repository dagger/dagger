package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetDriverMissingRuntimes(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	for _, scheme := range []string{"image", "image+podman", "container+podman"} {
		t.Run(scheme, func(t *testing.T) {
			_, err := GetDriver(t.Context(), scheme)
			require.ErrorContains(t, err, scheme)
			var unavailable *runtimeUnavailableError
			require.ErrorAs(t, err, &unavailable)
			require.Equal(t, true, unavailable.Extensions()["_quiet"])
			require.Equal(t, unavailable.message, unavailable.Extensions()["_message"])
			require.Contains(t, unavailable.message, "Install a compatible container runtime")
			require.Contains(t, unavailable.message, "PATH")
			require.Contains(t, unavailable.message, "dagger help engine")
		})
	}
}

func TestGetDriverSelection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		commands []string
		want     Driver
	}{
		{"skip missing runtimes", []string{"podman"}, podmanImageDriver},
		{"prefer first available runtime", []string{"docker", "podman"}, dockerImageDriver},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := runtimeTestPath(t)
			for _, command := range tc.commands {
				writeRuntimeCommand(t, dir, command, `test "$*" = version`)
			}
			driver, err := GetDriver(t.Context(), "image")
			require.NoError(t, err)
			require.Same(t, tc.want, driver)
		})
	}
}

func TestGetDriverUnavailableRuntime(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    string
	}{
		{"docker", "version"},
		{"container", "system status"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			dir := runtimeTestPath(t)
			writeRuntimeCommand(t, dir, tc.command, fmt.Sprintf("test \"$*\" = %q || exit 2\nexit 1", tc.args))
			writeRuntimeCommand(t, dir, "podman", "exit 0")

			// A failed probe must not silently select another backend.
			driver, err := GetDriver(t.Context(), "image")
			require.Nil(t, driver)
			var unavailable *runtimeUnavailableError
			require.ErrorAs(t, err, &unavailable)
			require.Equal(t, true, unavailable.Extensions()["_quiet"])
			require.Equal(t, unavailable.message, unavailable.Extensions()["_message"])
			require.Contains(t, unavailable.message, "using "+tc.command)
			require.Contains(t, unavailable.message, "Run `"+tc.command+" "+tc.args+"`")
			require.NotContains(t, unavailable.message, "exit status")
			require.ErrorContains(t, err, "exit status 1")
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			require.Equal(t, 1, exitErr.ExitCode())
		})
	}
}

func TestContainerRuntimeLookupFailure(t *testing.T) {
	dir := runtimeTestPath(t)
	// Preserve the original false, nil result for non-executable files too.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"), nil, 0644))
	available, err := containerRuntimeAvailable(t.Context(), "docker", "version")
	require.False(t, available)
	require.NoError(t, err)
}

func TestContainerRuntimeCanceled(t *testing.T) {
	dir := runtimeTestPath(t)
	writeRuntimeCommand(t, dir, "docker", "exit 0")
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("user canceled setup")
	cancel(cause)

	available, err := containerRuntimeAvailable(ctx, "docker", "version")
	require.False(t, available)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, cause)
	require.NotContains(t, err.Error(), "diagnose")
	var unavailable *runtimeUnavailableError
	require.False(t, errors.As(err, &unavailable))
}

func runtimeTestPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake runtime commands use /bin/sh")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	return dir
}

func writeRuntimeCommand(t *testing.T, dir, name, script string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0755))
}
