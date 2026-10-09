package engineutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/distconsts"
)

// runc passes the extra files on from fd 3 in order; each variable names its
// file's fd, and nil files are skipped.
func TestInitFDs(t *testing.T) {
	r1, w1, err := os.Pipe()
	require.NoError(t, err)
	defer r1.Close()
	defer w1.Close()
	r2, w2, err := os.Pipe()
	require.NoError(t, err)
	defer r2.Close()
	defer w2.Close()

	var fds initFDs
	fds.add(distconsts.SessionHelperStatusFDEnv, w1)
	fds.add("UNUSED", nil)
	fds.add(distconsts.InitTimingFDEnv, w2)
	require.Equal(t, []*os.File{w1, w2}, fds.files)

	spec := &specs.Spec{Process: &specs.Process{Args: []string{initPath, "true"}, Env: []string{"A=1"}}}
	got := fds.withEnv(spec)
	require.Equal(t, []string{"A=1", distconsts.SessionHelperStatusFDEnv + "=3", distconsts.InitTimingFDEnv + "=4"}, got.Process.Env)
	require.Equal(t, []string{"A=1"}, spec.Process.Env, "the exec's own spec is unchanged")

	var none initFDs
	require.Same(t, spec, none.withEnv(spec))
	require.Nil(t, none.files)
}

func TestReadBundleSpecWithoutInitFDEnv(t *testing.T) {
	_, w, err := os.Pipe()
	require.NoError(t, err)
	defer w.Close()
	var fds initFDs
	fds.add(distconsts.SessionHelperStatusFDEnv, w)
	fds.add(distconsts.InitTimingFDEnv, w)
	spec := &specs.Spec{Process: &specs.Process{Args: []string{initPath, "sleep", "100"}, Env: []string{"A=1", "PATH=/bin"}}}

	// The bundle's config.json is written with the variables; a process
	// exec'd into the running container starts from it without /.init.
	bundle := t.TempDir()
	b, err := json.Marshal(fds.withEnv(spec))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(bundle, "config.json"), b, 0o600))
	got, err := readBundleSpec(bundle)
	require.NoError(t, err)
	require.Equal(t, []string{"A=1", "PATH=/bin"}, got.Process.Env)
}

// /.init's report that the session helper failed reaches the engine as an
// error naming what happened.
func TestSessionHelperStatus(t *testing.T) {
	for _, tc := range []struct {
		report string
		want   string
	}{
		{"exited 3\n", "the Dagger session helper in this container exited with status 3"},
		{"start-failed failed to start session subprocess: fork/exec /proc/self/exe: no such file or directory\n", "the Dagger session helper in this container failed to start: failed to start session subprocess"},
	} {
		status, err := newSessionHelperStatus()
		require.NoError(t, err)
		got := make(chan error, 1)
		go status.watch(func(err error) { got <- err })
		_, err = fmt.Fprint(status.w, tc.report)
		require.NoError(t, err)
		select {
		case err := <-got:
			require.ErrorContains(t, err, tc.want)
		case <-time.After(5 * time.Second):
			t.Fatalf("no failure for report %q", tc.report)
		}
		status.close()
	}
}

// watch returns when the exec's cleanup closes the pipe, without a report.
func TestSessionHelperStatusClosed(t *testing.T) {
	status, err := newSessionHelperStatus()
	require.NoError(t, err)
	returned := make(chan struct{})
	go func() {
		status.watch(func(err error) { panic(errors.Join(errors.New("unexpected failure"), err)) })
		close(returned)
	}()
	status.close()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not return when the pipe closed")
	}
}
