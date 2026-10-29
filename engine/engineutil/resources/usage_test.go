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
