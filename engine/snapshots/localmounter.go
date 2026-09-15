package snapshots

import (
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
)

type Mounter interface {
	Mount() (string, error)
	Unmount() error
}

type LocalMounterOpt func(*localMounter)

func LocalMounter(mountable MountableRef, opts ...LocalMounterOpt) Mounter {
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
	mountable           MountableRef
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
