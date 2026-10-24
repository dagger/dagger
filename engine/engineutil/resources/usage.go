package resources

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ReadUsage reads a cgroup's total CPU time (cpu.stat's user_usec and
// system_usec) and its peak memory (memory.peak, 0 when the kernel doesn't
// provide it), for a reading taken once the cgroup's work is done.
func ReadUsage(cgroupPath string) (userUS, systemUS, memPeakBytes int64, err error) {
	bs, err := os.ReadFile(filepath.Join(cgroupPath, cpuStatFile))
	if err != nil {
		return 0, 0, 0, err
	}
	found := 0
	for key, value := range flatKeyValuesInt64(bs) {
		switch key {
		case cpuUserKey:
			userUS = value
			found++
		case cpuSystemKey:
			systemUS = value
			found++
		}
	}
	if found < 2 {
		return 0, 0, 0, fmt.Errorf("%s: no %s and %s", filepath.Join(cgroupPath, cpuStatFile), cpuUserKey, cpuSystemKey)
	}
	switch bs, err := os.ReadFile(filepath.Join(cgroupPath, memoryPeakFile)); {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return 0, 0, 0, err
	default:
		if memPeakBytes, err = singleValue(bs); err != nil {
			return 0, 0, 0, fmt.Errorf("%s: %w", filepath.Join(cgroupPath, memoryPeakFile), err)
		}
	}
	return userUS, systemUS, memPeakBytes, nil
}
