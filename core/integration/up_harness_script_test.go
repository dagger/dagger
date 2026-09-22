package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDaggerUpVerifyHarness(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode, want string
		prepared   bool
	}{
		{"success", "OK: fixture service responded", true},
		{"prepare-error", "FAIL: module-loading preparation failed after", false},
		{"prepare-timeout", "FAIL: module-loading preparation failed after", false},
		{"probe-timeout", "FAIL: service readiness timed out after", true},
		{"body-timeout", "FAIL: service response read failed after", true},
		{"wrong-body", "FAIL: expected nginx in response, got: wrong", true},
		{"shutdown-timeout", "FAIL: dagger up shutdown timed out after", true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			state := t.TempDir()
			require.NoError(t, syscall.Mkfifo(filepath.Join(state, "started"), 0600))
			bin := filepath.Join(state, "bin")
			require.NoError(t, os.Mkdir(bin, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(bin, "dagger"), []byte(`#!/bin/sh
if [ "$2" = -l ]; then
	case "$UP_TEST_MODE" in
		prepare-error) exit 17 ;;
		prepare-timeout) exec sleep 30 ;;
	esac
	touch "$UP_TEST_STATE/prepared"
	exit 0
fi
[ -f "$UP_TEST_STATE/prepared" ] || exit 18
if [ "$UP_TEST_MODE" = shutdown-timeout ]; then
	trap '' TERM
fi
touch "$UP_TEST_STATE/launched"
echo started > "$UP_TEST_STATE/started"
exec sleep 30
`), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(bin, "wget"), []byte(`#!/bin/sh
touch "$UP_TEST_STATE/probed"
if [ "$2" = --spider ]; then
	[ "$UP_TEST_MODE" != probe-timeout ] || exec sleep 30
	if [ ! -f "$UP_TEST_STATE/ready" ]; then
		read -r STARTED < "$UP_TEST_STATE/started"
		touch "$UP_TEST_STATE/ready"
	fi
	exit 0
fi
[ "$UP_TEST_MODE" != body-timeout ] || exec sleep 30
if [ "$UP_TEST_MODE" = wrong-body ]; then
	echo wrong
else
	echo nginx
fi
`), 0700))

			// Leave scheduling slack in stages that are not being forced to time out.
			bounds := upVerifyBounds{prepare: 5, ready: 5, probe: 5, shutdown: 5}
			switch tc.mode {
			case "prepare-timeout":
				bounds.prepare = 1
			case "probe-timeout":
				bounds.ready, bounds.probe = 1, 1
			case "body-timeout":
				bounds.probe = 1
			case "shutdown-timeout":
				bounds.shutdown = 1
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", "-c", upVerifyScript("web", "http://localhost:80", "nginx", "OK: fixture service responded", bounds))
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "UP_TEST_STATE="+state, "UP_TEST_MODE="+tc.mode)
			// The outer deadline kills the command's process group if a regression
			// prevents the script's own bounded cleanup from completing.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
			cmd.WaitDelay = time.Second
			out, err := cmd.CombinedOutput()
			require.NoError(t, ctx.Err(), "harness exceeded outer bound: %s", out)
			if tc.mode == "success" {
				require.NoError(t, err, "%s", out)
			} else {
				require.Error(t, err, "%s", out)
				require.NotContains(t, string(out), "OK: fixture service responded")
			}
			require.Contains(t, string(out), tc.want)
			if tc.prepared {
				require.FileExists(t, filepath.Join(state, "prepared"))
				require.Contains(t, string(out), "DONE: module-loading preparation after")
			} else {
				require.NoFileExists(t, filepath.Join(state, "launched"))
				require.NoFileExists(t, filepath.Join(state, "probed"))
			}
			t.Logf("%s", out)
		})
	}
}
