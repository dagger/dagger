//go:build linux

package cgroupmetrics

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	defaultProcRoot   = "/proc"
	defaultCgroupRoot = "/sys/fs/cgroup"
)

var errUnsupportedPlatform = errors.New("cgroup v2 metrics are not supported on this platform")

type reader struct {
	dir   string
	inode uint64
}

func newReader(procRoot, cgroupRoot string) (*reader, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, "self", "cgroup"))
	if err != nil {
		return nil, fmt.Errorf("read self cgroup: %w", err)
	}
	cgroupPath, err := parseUnifiedPath(string(data))
	if err != nil {
		return nil, err
	}
	dir, err := resolveCgroupPath(cgroupRoot, cgroupPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, "cgroup.controllers")); err != nil {
		return nil, fmt.Errorf("verify cgroup v2 boundary: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat cgroup boundary: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("get cgroup boundary inode")
	}
	return &reader{dir: dir, inode: stat.Ino}, nil
}

func parseUnifiedPath(data string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			if err := validateCgroupPath(parts[2]); err != nil {
				return "", err
			}
			return parts[2], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan self cgroup: %w", err)
	}
	return "", errors.New("unified cgroup v2 entry not found")
}

func validateCgroupPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return fmt.Errorf("invalid unified cgroup path %q", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return fmt.Errorf("invalid unified cgroup path %q", path)
		}
	}
	if filepath.Clean(path) != path {
		return fmt.Errorf("unclean unified cgroup path %q", path)
	}
	return nil
}

func resolveCgroupPath(cgroupRoot, cgroupPath string) (string, error) {
	if err := validateCgroupPath(cgroupPath); err != nil {
		return "", err
	}
	root, err := filepath.Abs(cgroupRoot)
	if err != nil {
		return "", fmt.Errorf("resolve cgroup mount: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve cgroup mount symlinks: %w", err)
	}
	candidate := filepath.Join(root, strings.TrimPrefix(cgroupPath, "/"))
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve cgroup boundary: %w", err)
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", fmt.Errorf("validate cgroup boundary: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("cgroup boundary %q escapes mount %q", candidate, root)
	}
	return candidate, nil
}

func (r *reader) read(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(r.dir, name))
}
