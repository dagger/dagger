package workers

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/moby/buildkit/util/testutil/integration"
)

func withOTELSocketPath(socketPath string) integration.ConfigUpdater {
	return otelSocketPath(socketPath)
}

type otelSocketPath string

func (osp otelSocketPath) UpdateConfigFile(in string) string {
	return fmt.Sprintf(`%s

[otel]
  socketPath = %q
`, in, osp)
}

func runBuildkitd(
	ctx context.Context,
	conf *integration.BackendConfig,
	args []string,
	logs map[string]*bytes.Buffer,
	uid, gid int,
	extraEnv []string,
) (address string, cl func() error, err error) {
	deferF := &integration.MultiCloser{}
	cl = deferF.F()

	defer func() {
		if err != nil {
			deferF.F()()
			cl = nil
		}
	}()

	tmpdir, err := os.MkdirTemp("", "bktest_buildkitd")
	if err != nil {
		return "", nil, err
	}

	if err := chown(tmpdir, uid, gid); err != nil {
		return "", nil, err
	}

	if err := os.MkdirAll(filepath.Join(tmpdir, "tmp"), 0711); err != nil {
		return "", nil, err
	}

	if err := chown(filepath.Join(tmpdir, "tmp"), uid, gid); err != nil {
		return "", nil, err
	}
	deferF.Append(func() error { return os.RemoveAll(tmpdir) })

	cfgfile, err := integration.WriteConfig(
		append(conf.DaemonConfig, withOTELSocketPath(getTraceSocketPath(tmpdir))))
	if err != nil {
		return "", nil, err
	}
	deferF.Append(func() error {
		return os.RemoveAll(filepath.Dir(cfgfile))
	})

	args = append(args, "--config="+cfgfile)
	address = getBuildkitdAddr(tmpdir)

	args = append(args, "--root", tmpdir, "--addr", address, "--debug")
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // test utility
	cmd.Env = append(
		os.Environ(),
		"BUILDKIT_DEBUG_EXEC_OUTPUT=1",
		"BUILDKIT_DEBUG_PANIC_ON_ERROR=1",
		"TMPDIR="+filepath.Join(tmpdir, "tmp"))
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.SysProcAttr = getSysProcAttr()

	stop, err := integration.StartCmd(cmd, logs)
	if err != nil {
		return "", nil, err
	}
	deferF.Append(stop)

	if err := integration.WaitSocket(address, 15*time.Second, cmd); err != nil {
		return "", nil, err
	}

	// separated out since it's not required in windows
	deferF.Append(func() error {
		return mountInfo(tmpdir)
	})

	return address, cl, err
}
