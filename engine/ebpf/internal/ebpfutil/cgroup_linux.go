package ebpfutil

import (
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/unix"
)

// CgroupID returns the ID of the cgroup at path, as eBPF helpers such as
// bpf_get_current_cgroup_id and the kernel's cgroup structures name it.
func CgroupID(path string) (uint64, error) {
	handle, _, err := unix.NameToHandleAt(unix.AT_FDCWD, path, 0)
	if err != nil {
		return 0, fmt.Errorf("getting cgroup ID for %s: %w", path, err)
	}
	data := handle.Bytes()
	if len(data) < 8 {
		return 0, fmt.Errorf(
			"getting cgroup ID for %s: handle is %d bytes",
			path,
			len(data),
		)
	}
	return binary.NativeEndian.Uint64(data[:8]), nil
}
