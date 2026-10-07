package networkmetrics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/ebpf/nettracer"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
)

// Mount helpers live under sshfs/ alongside the execution's container cgroup.
// Reading that parent preserves the kernel's combined memory peak rather than
// adding independent peaks that may have occurred at different times.
type execMountResources struct {
	mu          sync.Mutex
	path        string
	active      bool
	unavailable bool
	commands    []commandNetworkCounters
	err         error
}

func NewExecMountResources(path string) enginetel.ExecMountResources {
	return &execMountResources{path: path}
}

func (r *execMountResources) Active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

func (r *execMountResources) Available() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.unavailable
}

func (r *execMountResources) Prepare(cmd *exec.Cmd) (_ func() error, rerr error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() {
		if rerr != nil {
			r.unavailable = true
		}
	}()
	if !r.active {
		// Systemd cgroup paths need a different manager. Do not reinterpret
		// them as filesystem paths or escape the engine's cgroup namespace.
		if !filepath.IsAbs(r.path) || strings.Contains(r.path, ":") || filepath.Clean(r.path) != r.path || r.path == "/" {
			return nil, fmt.Errorf("unsupported exec cgroup path %q", r.path)
		}
		parent := filepath.Join("/sys/fs/cgroup", r.path)
		if err := os.MkdirAll(filepath.Dir(parent), 0o755); err != nil {
			return nil, err
		}
		if err := enableControllers(filepath.Dir(parent)); err != nil {
			return nil, err
		}
		if err := os.Mkdir(parent, 0o755); err != nil {
			return nil, err
		}
		if err := enableControllers(parent); err != nil {
			_ = os.Remove(parent)
			return nil, err
		}
		mounts := filepath.Join(parent, "sshfs")
		if err := os.Mkdir(mounts, 0o755); err != nil {
			_ = os.Remove(parent)
			return nil, err
		}
		if err := enableControllers(mounts); err != nil {
			_ = os.Remove(mounts)
			_ = os.Remove(parent)
			return nil, err
		}
		r.active = true
	}
	command, err := nettracer.PrepareCommandIn(cmd, filepath.Join("/sys/fs/cgroup", r.path, "sshfs"))
	if err != nil {
		return nil, err
	}
	r.commands = append(r.commands, command)
	return r.finish(command), nil
}

func (r *execMountResources) finish(command commandNetworkCounters) func() error {
	return sync.OnceValue(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := command.WaitEmpty(ctx)
		if err != nil {
			// This daemon is exclusive to a released mount. A lazy unmount
			// must not leave it running after its session's telemetry closes.
			killErr := os.WriteFile(filepath.Join(command.CgroupPath(), "cgroup.kill"), []byte("1"), 0o600)
			waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer waitCancel()
			err = errors.Join(fmt.Errorf("sshfs did not exit after unmount: %w", err), killErr, command.WaitEmpty(waitCtx))
		}
		r.mu.Lock()
		r.err = errors.Join(r.err, err)
		r.mu.Unlock()
		// Retain the empty cgroup and BPF map until the executor's final
		// sample. Closing here would discard the mount's network counters.
		return err
	})
}

func enableControllers(path string) error {
	data, err := os.ReadFile(filepath.Join(path, "cgroup.controllers"))
	if err != nil {
		return err
	}
	var enabled []string
	for _, name := range strings.Fields(string(data)) {
		enabled = append(enabled, "+"+name)
	}
	if len(enabled) == 0 {
		return nil
	}
	return os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), []byte(strings.Join(enabled, " ")), 0o644)
}

func (r *execMountResources) Sample() (*resourcestypes.NetworkSample, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sample := &resourcestypes.NetworkSample{ScopeSupported: true}
	if r.unavailable {
		sample.ScopeSupported = false
		return sample, nil
	}
	for _, command := range r.commands {
		value, err := command.Sample()
		if err != nil {
			sample.ScopeSupported = false
			return sample, nil
		}
		sample.InternalRxBytes = addMountBytes(sample.InternalRxBytes, value.InternalRX)
		sample.InternalTxBytes = addMountBytes(sample.InternalTxBytes, value.InternalTX)
		sample.ExternalRxBytes = addMountBytes(sample.ExternalRxBytes, value.ExternalRX)
		sample.ExternalTxBytes = addMountBytes(sample.ExternalTxBytes, value.ExternalTX)
	}
	return sample, nil
}

func addMountBytes(total int64, value uint64) int64 {
	return total + int64(min(value, uint64(math.MaxInt64-total)))
}

func (r *execMountResources) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	err := r.err
	for _, command := range r.commands {
		err = errors.Join(err, command.Close())
	}
	if r.active {
		err = errors.Join(err, os.Remove(filepath.Join("/sys/fs/cgroup", r.path, "sshfs")))
		err = errors.Join(err, os.Remove(filepath.Join("/sys/fs/cgroup", r.path)))
	}
	return err
}
