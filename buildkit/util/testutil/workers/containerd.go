package workers

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/pkg/errors"
)

func InitContainerdWorker() {
	integration.Register(&Containerd{
		ID:         "containerd",
		Containerd: "containerd",
	})
	// defined in Dockerfile
	// e.g. `containerd-1.1=/opt/containerd-1.1/bin,containerd-42.0=/opt/containerd-42.0/bin`
	if s := os.Getenv("BUILDKIT_INTEGRATION_CONTAINERD_EXTRA"); s != "" {
		entries := strings.Split(s, ",")
		for _, entry := range entries {
			pair := strings.Split(strings.TrimSpace(entry), "=")
			if len(pair) != 2 {
				panic(errors.Errorf("unexpected BUILDKIT_INTEGRATION_CONTAINERD_EXTRA: %q", s))
			}
			name, bin := pair[0], pair[1]
			integration.Register(&Containerd{
				ID:         name,
				Containerd: filepath.Join(bin, "containerd"),
				// override PATH to make sure that the expected version of the shim binary is used
				ExtraEnv: []string{fmt.Sprintf("PATH=%s:%s", bin, os.Getenv("PATH"))},
			})
		}
	}

	// the rootless uid is defined in Dockerfile
	if s := os.Getenv("BUILDKIT_INTEGRATION_ROOTLESS_IDPAIR"); s != "" {
		var uid, gid int
		if _, err := fmt.Sscanf(s, "%d:%d", &uid, &gid); err != nil {
			bklog.L.Fatalf("unexpected BUILDKIT_INTEGRATION_ROOTLESS_IDPAIR: %q", s)
		}
		if integration.RootlessSupported(uid) {
			integration.Register(&Containerd{
				ID:          "containerd-rootless",
				Containerd:  "containerd",
				UID:         uid,
				GID:         gid,
				Snapshotter: "native", // TODO: test with fuse-overlayfs as well, or automatically determine snapshotter
			})

			// TODO: add RootlessKitDetachNetNS after updating containerd-rootless.sh to include https://github.com/containerd/nerdctl/pull/2723
		}
	}

	if s := os.Getenv("BUILDKIT_INTEGRATION_SNAPSHOTTER"); s != "" {
		integration.Register(&Containerd{
			ID:          fmt.Sprintf("containerd-snapshotter-%s", s),
			Containerd:  "containerd",
			Snapshotter: s,
		})
	}
}

type Containerd struct {
	ID          string
	Containerd  string
	Snapshotter string
	UID         int
	GID         int
	ExtraEnv    []string // e.g. "PATH=/opt/containerd-1.4/bin:/usr/bin:..."
}

func (c *Containerd) Name() string {
	return c.ID
}

func (c *Containerd) Rootless() bool {
	return c.UID != 0
}

func (c *Containerd) NetNSDetached() bool {
	return false
}

func (c *Containerd) New(ctx context.Context, cfg *integration.BackendConfig) (b integration.Backend, cl func() error, err error) {
	if err := integration.LookupBinary(c.Containerd); err != nil {
		return nil, nil, err
	}

	if err := integration.LookupBinary("buildkitd"); err != nil {
		return nil, nil, err
	}

	if err := requireRoot(); err != nil {
		return nil, nil, err
	}

	deferF := &integration.MultiCloser{}
	cl = deferF.F()

	defer func() {
		if err != nil {
			deferF.F()()
			cl = nil
		}
	}()

	rootless := false
	if c.UID != 0 {
		if c.GID == 0 {
			return nil, nil, errors.Errorf("unsupported id pair: uid=%d, gid=%d", c.UID, c.GID)
		}
		rootless = true
	}

	tmpdir, err := os.MkdirTemp("", "bktest_containerd")
	if err != nil {
		return nil, nil, err
	}

	if rootless {
		if err := os.Chown(tmpdir, c.UID, c.GID); err != nil {
			return nil, nil, err
		}
	}

	deferF.Append(func() error { return os.RemoveAll(tmpdir) })

	address := getContainerdSock(tmpdir)
	config := fmt.Sprintf(`root = %q
state = %q
# CRI plugins listens on 10010/tcp for stream server.
# We disable CRI plugin so that multiple instance can run simultaneously.
disabled_plugins = ["cri"]

[grpc]
  address = %q

[debug]
  level = "debug"
  address = %q`,
		filepath.Join(tmpdir, "root"),
		filepath.Join(tmpdir, "state"),
		address, getContainerdDebugSock(tmpdir),
	)

	var snBuildkitdArgs []string
	if c.Snapshotter != "" {
		snBuildkitdArgs = append(snBuildkitdArgs,
			fmt.Sprintf("--containerd-worker-snapshotter=%s", c.Snapshotter))
		if c.Snapshotter == "stargz" {
			snPath, snCl, err := runStargzSnapshotter(cfg)
			if err != nil {
				return nil, nil, err
			}
			deferF.Append(snCl)
			config = fmt.Sprintf(`%s

[proxy_plugins]
  [proxy_plugins.stargz]
    type = "snapshot"
    address = %q
`, config, snPath)
		}
	}

	configFile := filepath.Join(tmpdir, "config.toml")
	if err := os.WriteFile(configFile, []byte(config), 0644); err != nil {
		return nil, nil, err
	}

	containerdArgs := []string{c.Containerd, "--config", configFile}
	rootlessKitState := filepath.Join(tmpdir, "rootlesskit-containerd")
	if rootless {
		containerdArgs = append(append([]string{"sudo", "-u", fmt.Sprintf("#%d", c.UID), "-i",
			fmt.Sprintf("CONTAINERD_ROOTLESS_ROOTLESSKIT_STATE_DIR=%s", rootlessKitState),
			// Integration test requires the access to localhost of the host network namespace.
			// TODO: remove these configurations
			"CONTAINERD_ROOTLESS_ROOTLESSKIT_NET=host",
			"CONTAINERD_ROOTLESS_ROOTLESSKIT_PORT_DRIVER=none",
			"CONTAINERD_ROOTLESS_ROOTLESSKIT_FLAGS=--mtu=0",
		}, c.ExtraEnv...), "containerd-rootless.sh", "-c", configFile)
	}

	cmd := exec.Command(containerdArgs[0], containerdArgs[1:]...) //nolint:gosec // test utility
	cmd.Env = append(os.Environ(), c.ExtraEnv...)

	ctdStop, err := integration.StartCmd(cmd, cfg.Logs)
	if err != nil {
		return nil, nil, err
	}
	if err := integration.WaitSocket(address, 10*time.Second, cmd); err != nil {
		ctdStop()
		return nil, nil, errors.Wrapf(err, "containerd did not start up: %s", integration.FormatLogs(cfg.Logs))
	}
	deferF.Append(ctdStop)

	// handles only windows case, no effect on unix
	address = normalizeAddress(address)
	buildkitdArgs := []string{
		"buildkitd",
		"--containerd-worker-gc=false",
		"--containerd-worker=true",
		"--containerd-worker-addr", address,
		"--containerd-worker-labels=org.mobyproject.buildkit.worker.sandbox=true", // Include use of --containerd-worker-labels to trigger https://github.com/moby/buildkit/pull/603
	}
	buildkitdArgs = applyBuildkitdPlatformFlags(buildkitdArgs)
	buildkitdArgs = append(buildkitdArgs, snBuildkitdArgs...)

	if runtime.GOOS != "windows" && c.Snapshotter != "native" {
		c.ExtraEnv = append(c.ExtraEnv, "BUILDKIT_DEBUG_FORCE_OVERLAY_DIFF=true")
	}
	if rootless {
		pidStr, err := os.ReadFile(filepath.Join(rootlessKitState, "child_pid"))
		if err != nil {
			return nil, nil, err
		}
		pid, err := strconv.ParseInt(string(pidStr), 10, 64)
		if err != nil {
			return nil, nil, err
		}
		buildkitdArgs = append([]string{"sudo", "-u", fmt.Sprintf("#%d", c.UID), "-i", "--", "exec",
			"nsenter", "-U", "--preserve-credentials", "-m", "-t", fmt.Sprintf("%d", pid)},
			append(buildkitdArgs, "--containerd-worker-snapshotter=native")...)
	}
	buildkitdSock, stop, err := runBuildkitd(ctx, cfg, buildkitdArgs, cfg.Logs, c.UID, c.GID, c.ExtraEnv)
	if err != nil {
		integration.PrintLogs(cfg.Logs, log.Println)
		return nil, nil, err
	}
	deferF.Append(stop)

	return backend{
		address:           buildkitdSock,
		containerdAddress: address,
		rootless:          rootless,
		netnsDetached:     false,
		snapshotter:       c.Snapshotter,
		extraEnv:          c.ExtraEnv,
	}, cl, nil
}

func (c *Containerd) Close() error {
	return nil
}

func runStargzSnapshotter(cfg *integration.BackendConfig) (address string, cl func() error, err error) {
	binary := "containerd-stargz-grpc"
	if err := integration.LookupBinary(binary); err != nil {
		return "", nil, err
	}

	deferF := &integration.MultiCloser{}
	cl = deferF.F()

	defer func() {
		if err != nil {
			deferF.F()()
			cl = nil
		}
	}()

	tmpStargzDir, err := os.MkdirTemp("", "bktest_containerd_stargz_grpc")
	if err != nil {
		return "", nil, err
	}
	deferF.Append(func() error { return os.RemoveAll(tmpStargzDir) })

	address = filepath.Join(tmpStargzDir, "containerd-stargz-grpc.sock")
	stargzRootDir := filepath.Join(tmpStargzDir, "root")
	cmd := exec.Command(binary,
		"--log-level", "debug",
		"--address", address,
		"--root", stargzRootDir)
	snStop, err := integration.StartCmd(cmd, cfg.Logs)
	if err != nil {
		return "", nil, err
	}
	if err = integration.WaitSocket(address, 10*time.Second, cmd); err != nil {
		snStop()
		return "", nil, errors.Wrapf(err, "containerd-stargz-grpc did not start up: %s", integration.FormatLogs(cfg.Logs))
	}
	deferF.Append(snStop)

	return
}
