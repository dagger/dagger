package core

// originally imported from buildkit's exec_binfmt.go so we can call
// getEmulator, a private function

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/engine/engineutil"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/util/archutil"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	copy "github.com/dagger/dagger/internal/fsutil/copy"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

var qemuArchMap = map[string]string{
	"arm64":   "aarch64",
	"amd64":   "x86_64",
	"riscv64": "riscv64",
	"arm":     "arm",
	"s390x":   "s390x",
	"ppc64":   "ppc64",
	"ppc64le": "ppc64le",
	"386":     "i386",
}

type emulator struct {
	path string
}

func (e *emulator) Mount(ctx context.Context, readonly bool) (bkcache.MountableRef, error) {
	return &staticEmulatorMount{path: e.path}, nil
}

type staticEmulatorMount struct {
	path string
}

func (m *staticEmulatorMount) Mount() ([]mount.Mount, func() error, error) {
	tmpdir, err := os.MkdirTemp("", "buildkit-qemu-emulator")
	if err != nil {
		return nil, nil, err
	}
	var ret bool
	defer func() {
		if !ret {
			os.RemoveAll(tmpdir)
		}
	}()

	if err := copy.Copy(context.TODO(), filepath.Dir(m.path), filepath.Base(m.path), tmpdir, engineutil.BuildkitQemuEmulatorMountPoint, func(ci *copy.CopyInfo) {
		m := 0555
		ci.Mode = &m
	}); err != nil {
		return nil, nil, err
	}

	ret = true
	return []mount.Mount{{
			Type:    "bind",
			Source:  filepath.Join(tmpdir, engineutil.BuildkitQemuEmulatorMountPoint),
			Options: []string{"ro", "bind"},
		}}, func() error {
			return os.RemoveAll(tmpdir)
		}, nil
}

func getEmulator(ctx context.Context, pp ocispecs.Platform) (*emulator, error) {
	all := archutil.SupportedPlatforms(false)
	pp = platforms.Normalize(pp)
	for _, p := range all {
		if platforms.Only(p).Match(pp) {
			return nil, nil
		}
	}

	if pp.Architecture == "amd64" {
		if pp.Variant != "" && pp.Variant != "v2" {
			var supported []string
			for _, p := range all {
				if p.Architecture == "amd64" {
					supported = append(supported, platforms.Format(p))
				}
			}
			return nil, errors.Errorf("no support for running processes with %s platform, supported: %s", platforms.Format(pp), strings.Join(supported, ", "))
		}
	}

	a, ok := qemuArchMap[pp.Architecture]
	if !ok {
		a = pp.Architecture
	}

	fn, err := exec.LookPath("buildkit-qemu-" + a)
	if err != nil {
		bklog.G(ctx).Warn(err.Error()) // TODO: remove this with pull support
		return nil, nil                // no emulator available
	}

	return &emulator{path: fn}, nil
}
