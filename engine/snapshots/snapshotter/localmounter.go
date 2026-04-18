package snapshotter

import (
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
)

type Mounter interface {
	Mount() (string, error)
	Unmount() error
}

type LocalMounterOpt func(*localMounter)

func LocalMounter(mountable Mountable, opts ...LocalMounterOpt) Mounter {
	lm := &localMounter{mountable: mountable}
	for _, opt := range opts {
		opt(lm)
	}
	return lm
}

func LocalMounterWithMounts(mounts []mount.Mount, opts ...LocalMounterOpt) Mounter {
	lm := &localMounter{mounts: mounts}
	for _, opt := range opts {
		opt(lm)
	}
	return lm
}

type localMounter struct {
	mu                  sync.Mutex
	mounts              []mount.Mount
	mountable           Mountable
	target              string
	release             func() error
	forceRemount        bool
	tmpDir              string
	overlayIncompatDirs []string
}

func ForceRemount() LocalMounterOpt {
	return func(lm *localMounter) {
		lm.forceRemount = true
	}
}
