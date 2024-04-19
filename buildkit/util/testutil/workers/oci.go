package workers

import (
	"context"
	"fmt"
	"log"
	"runtime"

	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/pkg/errors"
)

// InitOCIWorker registers an integration test worker, which enables the --oci-worker
// flag in the test buildkitd instance and disables the --containerd-worker flag. This
// integration test worker is not supported on Windows.
func InitOCIWorker() {
	// calling platform specific
	initOCIWorker()
}

type OCI struct {
	ID                     string
	UID                    int
	GID                    int
	Snapshotter            string
	RootlessKitNet         string // e.g., "slirp4netns"
	RootlessKitDetachNetNS bool   // needs RootlessKitNet to be non-host network
}

func (s *OCI) Name() string {
	return s.ID
}

func (s *OCI) Rootless() bool {
	return s.UID != 0
}

func (s *OCI) NetNSDetached() bool {
	return s.Rootless() && s.RootlessKitDetachNetNS
}

func (s *OCI) New(ctx context.Context, cfg *integration.BackendConfig) (integration.Backend, func() error, error) {
	if err := integration.LookupBinary("buildkitd"); err != nil {
		return nil, nil, err
	}
	if err := requireRoot(); err != nil {
		return nil, nil, err
	}
	// Include use of --oci-worker-labels to trigger https://github.com/moby/buildkit/pull/603
	buildkitdArgs := []string{"buildkitd", "--oci-worker=true", "--containerd-worker=false", "--oci-worker-gc=false", "--oci-worker-labels=org.mobyproject.buildkit.worker.sandbox=true"}

	if s.Snapshotter != "" {
		buildkitdArgs = append(buildkitdArgs,
			fmt.Sprintf("--oci-worker-snapshotter=%s", s.Snapshotter))
	}

	if s.UID != 0 {
		if s.GID == 0 {
			return nil, nil, errors.Errorf("unsupported id pair: uid=%d, gid=%d", s.UID, s.GID)
		}
		var rootlessKitArgs []string
		switch s.RootlessKitNet {
		case "", "host":
		// NOP
		default:
			// See docs/rootless.md
			rootlessKitArgs = append(rootlessKitArgs, "--net="+s.RootlessKitNet, "--copy-up=/etc", "--disable-host-loopback")
		}
		if s.RootlessKitDetachNetNS {
			rootlessKitArgs = append(rootlessKitArgs, "--detach-netns")
		}
		// TODO: make sure the user exists and subuid/subgid are configured.
		buildkitdArgs = append(append([]string{"sudo", "-u", fmt.Sprintf("#%d", s.UID), "-i", "--", "exec", "rootlesskit"}, rootlessKitArgs...), buildkitdArgs...)
	}

	var extraEnv []string
	if runtime.GOOS != "windows" && s.Snapshotter != "native" {
		extraEnv = append(extraEnv, "BUILDKIT_DEBUG_FORCE_OVERLAY_DIFF=true")
	}
	buildkitdSock, stop, err := runBuildkitd(ctx, cfg, buildkitdArgs, cfg.Logs, s.UID, s.GID, extraEnv)
	if err != nil {
		integration.PrintLogs(cfg.Logs, log.Println)
		return nil, nil, err
	}

	return backend{
		address:       buildkitdSock,
		rootless:      s.UID != 0,
		netnsDetached: s.NetNSDetached(),
		snapshotter:   s.Snapshotter,
	}, stop, nil
}

func (s *OCI) Close() error {
	return nil
}
