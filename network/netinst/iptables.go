package netinst

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagger/dagger/internal/buildkit/util/bklog"
)

const iptablesBinDir = "/usr/bin"

var iptablesSymlinkOnce sync.Once
var iptablesSymlinkErr error

// EnsureIptablesSymlinks makes sure iptables/ip6tables resolve to the right backend.
func EnsureIptablesSymlinks(ctx context.Context) error {
	iptablesSymlinkOnce.Do(func() {
		iptablesSymlinkErr = ensureIptablesSymlinks(ctx)
	})
	return iptablesSymlinkErr
}

func ensureIptablesSymlinks(ctx context.Context) error {
	backend, target, err := pickIptablesTarget()
	if err != nil {
		return err
	}

	bklog.G(ctx).Infof("configuring iptables backend: %s", backend)

	for _, name := range []string{
		"iptables",
		"iptables-save",
		"iptables-restore",
		"ip6tables",
		"ip6tables-save",
		"ip6tables-restore",
	} {
		path := filepath.Join(iptablesBinDir, name)
		if err := ensureSymlink(path, target); err != nil {
			return fmt.Errorf("ensure %s: %w", path, err)
		}
		bklog.G(ctx).Infof("set %s -> %s", path, target)
	}

	return nil
}

func pickIptablesTarget() (string, string, error) {
	legacyTarget := filepath.Join(iptablesBinDir, "xtables-legacy-multi")
	nftTarget := filepath.Join(iptablesBinDir, "xtables-nft-multi")

	backend := "nft"
	target := nftTarget
	if legacyXtablesAvailable() {
		backend = "legacy"
		target = legacyTarget
	}

	if _, err := os.Stat(target); err != nil {
		return "", "", fmt.Errorf("%s missing: %w", target, err)
	}
	return backend, target, nil
}

func ensureSymlink(path, target string) error {
	if path == "" || target == "" {
		return fmt.Errorf("invalid symlink target")
	}

	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("path is a directory: %s", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}

	return os.Symlink(target, path)
}

func legacyXtablesAvailable() bool {
	// /proc/net/ip_tables_names only exists when legacy iptables kernel modules are available.
	if _, err := os.Stat("/proc/net/ip_tables_names"); err == nil {
		return true
	} else if os.IsNotExist(err) {
		return false
	}
	return false
}
