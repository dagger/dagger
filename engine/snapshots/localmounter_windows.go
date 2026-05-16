package snapshots

import (
	"os"

	"github.com/Microsoft/go-winio/pkg/bindfilter"
	"github.com/containerd/containerd/v2/core/mount"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/pkg/errors"
	"golang.org/x/sys/windows"
)

func (lm *localMounter) Mount() (string, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if lm.mounts == nil && lm.mountable != nil {
		mounts, release, err := lm.mountable.Mount()
		if err != nil {
			return "", err
		}
		lm.mounts = mounts
		lm.release = release
	}

	if len(lm.mounts) != 1 {
		return "", errors.Wrapf(cerrdefs.ErrNotImplemented, "request to mount %d layers, only 1 is supported", len(lm.mounts))
	}

	m := lm.mounts[0]
	dir, err := os.MkdirTemp("", "dagger-mount")
	if err != nil {
		return "", errors.Wrap(err, "failed to create temp dir")
	}

	if m.Type == "bind" || m.Type == "rbind" {
		if !m.ReadOnly() {
			return m.Source, nil
		}
		if err := bindfilter.ApplyFileBinding(dir, m.Source, m.ReadOnly()); err != nil {
			return "", errors.Wrapf(err, "failed to mount %v: %+v", m, err)
		}
	} else {
		if err := m.Mount(dir); err != nil {
			return "", errors.Wrapf(err, "failed to mount %v: %+v", m, err)
		}
	}

	lm.target = dir
	return lm.target, nil
}

func (lm *localMounter) Unmount() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if len(lm.mounts) != 1 {
		return errors.Wrapf(cerrdefs.ErrNotImplemented, "request to mount %d layers, only 1 is supported", len(lm.mounts))
	}
	m := lm.mounts[0]

	if lm.target != "" {
		if m.Type == "bind" || m.Type == "rbind" {
			if err := bindfilter.RemoveFileBinding(lm.target); err != nil {
				if !errors.Is(err, windows.ERROR_INVALID_PARAMETER) && !errors.Is(err, windows.ERROR_NOT_FOUND) {
					return errors.Wrapf(err, "failed to unmount %v: %+v", lm.target, err)
				}
			}
		} else {
			if err := mount.Unmount(lm.target, 0); err != nil {
				return errors.Wrapf(err, "failed to unmount %v: %+v", lm.target, err)
			}
		}
		os.RemoveAll(lm.target)
		lm.target = ""
	}

	if lm.release != nil {
		return lm.release()
	}

	return nil
}
