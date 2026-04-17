package rootlessmountopts

import (
	"github.com/containerd/containerd/v2/core/mount"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// UnprivilegedMountFlags gets the set of mount flags that are set on the mount that contains the given
// path and are locked by CL_UNPRIVILEGED. This is necessary to ensure that
// bind-mounting "with options" will not fail with user namespaces, due to
// kernel restrictions that require user namespace mounts to preserve
// CL_UNPRIVILEGED locked flags.
//
// From https://github.com/moby/moby/blob/v23.0.1/daemon/oci_linux.go#L430-L460
func UnprivilegedMountFlags(path string) ([]string, error) {
	var statfs unix.Statfs_t
	if err := unix.Statfs(path, &statfs); err != nil {
		return nil, err
	}

	unprivilegedFlags := map[uint64]string{
		unix.MS_RDONLY:     "ro",
		unix.MS_NODEV:      "nodev",
		unix.MS_NOEXEC:     "noexec",
		unix.MS_NOSUID:     "nosuid",
		unix.MS_NOATIME:    "noatime",
		unix.MS_RELATIME:   "relatime",
		unix.MS_NODIRATIME: "nodiratime",
	}

	var flags []string
	for mask, flag := range unprivilegedFlags {
		if uint64(statfs.Flags)&mask == mask {
			flags = append(flags, flag)
		}
	}

	return flags, nil
}

func FixUp(mounts []mount.Mount) ([]mount.Mount, error) {
	for i, m := range mounts {
		var isBind bool
		for _, o := range m.Options {
			switch o {
			case "bind", "rbind":
				isBind = true
			}
		}
		if !isBind {
			continue
		}
		unpriv, err := UnprivilegedMountFlags(m.Source)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get unprivileged mount flags for %+v", m)
		}
		m.Options = dedupeStrings(append(m.Options, unpriv...))
		mounts[i] = m
	}
	return mounts, nil
}

func FixUpOCI(mounts []specs.Mount) ([]specs.Mount, error) {
	for i, m := range mounts {
		var isBind bool
		for _, o := range m.Options {
			switch o {
			case "bind", "rbind":
				isBind = true
			}
		}
		if !isBind {
			continue
		}
		unpriv, err := UnprivilegedMountFlags(m.Source)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get unprivileged mount flags for %+v", m)
		}
		m.Options = dedupeStrings(append(m.Options, unpriv...))
		mounts[i] = m
	}
	return mounts, nil
}

func dedupeStrings(in []string) []string {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
