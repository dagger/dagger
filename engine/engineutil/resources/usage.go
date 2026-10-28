package resources

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// ReadIO reads a cgroup's bytes read from and written to block devices
// since it was created: io.stat's rbytes and wbytes, summed over devices.
func ReadIO(cgroupPath string) (readBytes, writeBytes int64, err error) {
	bs, err := os.ReadFile(filepath.Join(cgroupPath, ioStatFile))
	if err != nil {
		return 0, 0, err
	}
	for _, kvs := range nestedKeyValuesInt64(bs) {
		for k, v := range kvs {
			switch k {
			case ioReadBytes:
				readBytes += v
			case ioWriteBytes:
				writeBytes += v
			}
		}
	}
	return readBytes, writeBytes, nil
}

// Memory is a cgroup's memory at one moment, from memory.stat: anonymous
// memory, file pages (the page cache), and the part of both in transparent
// huge pages.
type Memory struct {
	AnonBytes int64
	FileBytes int64
	HugeBytes int64
}

// memoryStatFileKey is memory.stat's page cache, and memoryStatHugeKeys its
// transparent huge page counters, each part of anon or file.
const memoryStatFileKey = "file"

var memoryStatHugeKeys = []string{"anon_thp", "file_thp", "shmem_thp"}

// ReadMemory reads a cgroup's current memory.
func ReadMemory(cgroupPath string) (Memory, error) {
	bs, err := os.ReadFile(filepath.Join(cgroupPath, memoryStatFile))
	if err != nil {
		return Memory{}, err
	}
	var m Memory
	found := false
	for key, value := range flatKeyValuesInt64(bs) {
		switch {
		case key == memoryStatAnonKey:
			m.AnonBytes, found = value, true
		case key == memoryStatFileKey:
			m.FileBytes = value
		case slices.Contains(memoryStatHugeKeys, key):
			m.HugeBytes += value
		}
	}
	if !found {
		return Memory{}, fmt.Errorf("%s: no %s", filepath.Join(cgroupPath, memoryStatFile), memoryStatAnonKey)
	}
	return m, nil
}
