package resources

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadUsage(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(cpuStatFile, "usage_usec 1500\nuser_usec 1200\nsystem_usec 300\nnr_periods 0\n")
	user, sys, peak, err := ReadUsage(dir)
	if err != nil || user != 1200 || sys != 300 || peak != 0 {
		t.Fatalf("without memory.peak: %d %d %d %v", user, sys, peak, err)
	}
	write(memoryPeakFile, "4096\n")
	if _, _, peak, err = ReadUsage(dir); err != nil || peak != 4096 {
		t.Fatalf("with memory.peak: %d %v", peak, err)
	}
	write(cpuStatFile, "usage_usec 1500\n")
	if _, _, _, err = ReadUsage(dir); err == nil {
		t.Error("cpu.stat without user and system: no error")
	}
	if _, _, _, err = ReadUsage(filepath.Join(dir, "gone")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing cgroup: %v, want not exist", err)
	}
}

func TestReadIO(t *testing.T) {
	dir := t.TempDir()
	stat := "259:0 rbytes=4096 wbytes=1024 rios=1 wios=1 dbytes=0 dios=0\n" +
		"259:1 rbytes=8192 wbytes=0 rios=2 wios=0 dbytes=0 dios=0\n"
	if err := os.WriteFile(filepath.Join(dir, ioStatFile), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	read, written, err := ReadIO(dir)
	if err != nil || read != 12288 || written != 1024 {
		t.Fatalf("summed over devices: %d %d %v", read, written, err)
	}
	if _, _, err = ReadIO(filepath.Join(dir, "gone")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing cgroup: %v, want not exist", err)
	}
}

func TestReadMemory(t *testing.T) {
	dir := t.TempDir()
	stat := "anon 1048576\nfile 4194304\nkernel 65536\nshmem 0\n" +
		"anon_thp 524288\nfile_thp 2097152\nshmem_thp 0\ninactive_file 1024\n"
	if err := os.WriteFile(filepath.Join(dir, memoryStatFile), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMemory(dir)
	if err != nil || m != (Memory{AnonBytes: 1048576, FileBytes: 4194304, HugeBytes: 2621440}) {
		t.Fatalf("memory: %+v %v", m, err)
	}
	if err := os.WriteFile(filepath.Join(dir, memoryStatFile), []byte("kernel 65536\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadMemory(dir); err == nil {
		t.Error("memory.stat without anon: no error")
	}
}
