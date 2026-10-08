//go:build linux && (386 || amd64 || arm64)

package nettracer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

// commandAccounting owns sibling subtrees for engine subprocesses. Exec mount
// helpers instead attach private hooks below their owning execution.
type commandAccounting struct {
	paths   map[string]string
	links   []link.Link
	mu      sync.Mutex
	pending map[string]struct{}
	done    chan struct{}
	stopped chan struct{}
	closed  bool
}

var fallbackCommands atomic.Pointer[commandAccounting]

// InitCommandPlacement keeps resource isolation available when eBPF cannot
// load. The engine owns the returned cleanup; clients never install this.
func InitCommandPlacement() (func() error, error) {
	if t := Active(); t != nil && t.commands != nil {
		return func() error { return nil }, nil
	}
	if fallbackCommands.Load() != nil {
		return nil, errors.New("command placement already initialized")
	}
	path, err := currentCgroupPath()
	if err != nil {
		return nil, err
	}
	a, err := (*Tracer)(nil).newCommandAccounting(path)
	if err != nil {
		return nil, err
	}
	if !fallbackCommands.CompareAndSwap(nil, a) {
		_ = a.Close()
		return nil, errors.New("command placement already initialized")
	}
	return func() error {
		fallbackCommands.CompareAndSwap(a, nil)
		return a.Close()
	}, nil
}

func (t *Tracer) newCommandAccounting(parent string) (_ *commandAccounting, rerr error) {
	root, err := commandRoot(parent)
	if err != nil {
		return nil, err
	}
	a := &commandAccounting{paths: map[string]string{}, pending: map[string]struct{}{}, done: make(chan struct{}), stopped: make(chan struct{})}
	defer func() {
		if rerr != nil {
			for _, hook := range a.links {
				_ = hook.Close()
			}
			for _, path := range a.paths {
				_ = os.Remove(path)
			}
		}
	}()
	for _, name := range []string{"git", "rg"} {
		path := filepath.Join(root, name)
		// Do not attach to a category owned by another engine or left behind
		// by a live helper. That could mix totals or count traffic twice.
		if err := os.Mkdir(path, 0o755); err != nil {
			return nil, fmt.Errorf("reserve command cgroup %s: %w", path, err)
		}
		a.paths[name] = path
		// Keep these category cgroups empty. Delegate the available resource
		// controllers to the per-operation leaves for CPU and memory accounting.
		controllers, err := os.ReadFile(filepath.Join(path, "cgroup.controllers"))
		if err != nil {
			return nil, err
		}
		var enable []string
		for _, controller := range strings.Fields(string(controllers)) {
			enable = append(enable, "+"+controller)
		}
		if len(enable) > 0 {
			if err := os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), []byte(strings.Join(enable, " ")), 0o644); err != nil {
				return nil, err
			}
		}
		if t == nil {
			continue
		}
		for _, opts := range []link.CgroupOptions{
			{Path: path, Attach: ebpf.AttachCGroupInetIngress, Program: t.objs.CountOperationIngress},
			{Path: path, Attach: ebpf.AttachCGroupInetEgress, Program: t.objs.CountOperationEgress},
		} {
			hook, err := link.AttachCgroup(opts)
			if err != nil {
				return nil, err
			}
			a.links = append(a.links, hook)
		}
	}
	// Probe clone-into-cgroup before touching any operation. Seccomp can allow
	// BPF and cgroup creation while denying clone3; metrics must stay optional.
	path, err := os.MkdirTemp(a.paths["git"], "probe-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	fd, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	probe := exec.Command("true")
	probe.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(fd.Fd())}
	if err := probe.Run(); err != nil {
		return nil, fmt.Errorf("probe cgroup process placement: %w", err)
	}
	go a.reap()
	return a, nil
}

// Never escape the delegated hierarchy when the engine runs at its root.
// Custom layouts can use any leaf name, but must provide a sibling parent.
func commandRoot(enginePath string) (string, error) {
	enginePath = filepath.Clean(enginePath)
	if enginePath == "/sys/fs/cgroup" || !strings.HasPrefix(enginePath, "/sys/fs/cgroup/") {
		return "", errors.New("engine cgroup has no delegated sibling parent")
	}
	switch filepath.Base(enginePath) {
	case "git", "rg", "sshfs", "exec":
		return "", errors.New("engine is in a reserved workload cgroup")
	}
	return filepath.Dir(enginePath), nil
}

func (a *commandAccounting) reap() {
	defer close(a.stopped)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			a.mu.Lock()
			for path := range a.pending {
				if err := os.Remove(path); err == nil || os.IsNotExist(err) {
					delete(a.pending, path)
				}
			}
			a.mu.Unlock()
		}
	}
}

func (a *commandAccounting) remove(path string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	err := os.Remove(path)
	if !a.closed && (errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.ENOTEMPTY)) {
		// A helper may outlive its parent. Never kill it just to
		// remove an accounting cgroup. Retry after it exits.
		a.pending[path] = struct{}{}
		return nil
	}
	return err
}

func (a *commandAccounting) Close() error {
	close(a.done)
	<-a.stopped
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	var errs []error
	for _, hook := range a.links {
		errs = append(errs, hook.Close())
	}
	for path := range a.pending {
		errs = append(errs, os.Remove(path))
	}
	for _, path := range a.paths {
		errs = append(errs, os.Remove(path))
	}
	return errors.Join(errs...)
}

type Command struct {
	tracer     *Tracer
	accounting *commandAccounting
	path       string
	fd         *os.File
	id         uint64
	closeOnce  sync.Once
	closeErr   error
	links      []link.Link
}

// PrepareCommand places a command in its own accounting cgroup at clone time.
// Call before Start, and Close only after Wait (or after a daemon's lifetime).
// An error leaves cmd unchanged; callers can run it without network metrics.
func PrepareCommand(cmd *exec.Cmd) (_ *Command, rerr error) {
	return prepareCommand(cmd, "", false)
}

// PrepareCommandIn keeps a mount helper beneath its owning exec. Attach to
// the helper leaf, not the exec parent: container traffic is counted by TCX.
func PrepareCommandIn(cmd *exec.Cmd, parent string) (*Command, error) {
	command, err := prepareCommand(cmd, parent, false)
	if err != nil {
		// Preserve CPU/memory placement if loading the leaf BPF hooks fails.
		return prepareCommand(cmd, parent, true)
	}
	return command, nil
}

func prepareCommand(cmd *exec.Cmd, parent string, withoutNetwork bool) (_ *Command, rerr error) {
	t := Active()
	a := fallbackCommands.Load()
	if t != nil && t.commands != nil {
		a = t.commands
	} else {
		t = nil
	}
	if a == nil {
		return nil, errors.New("subprocess accounting is unavailable")
	}
	if withoutNetwork {
		t = nil
	}
	if cmd.Process != nil || (cmd.SysProcAttr != nil && cmd.SysProcAttr.UseCgroupFD) {
		return nil, errors.New("command already started or has an assigned cgroup")
	}
	privateParent := parent != ""
	if !privateParent {
		var ok bool
		parent, ok = a.paths[filepath.Base(cmd.Path)]
		if !ok {
			return nil, fmt.Errorf("no accounting cgroup for command %q", cmd.Path)
		}
	}
	prefix := "operation-"
	if privateParent {
		prefix = ""
	}
	path, err := os.MkdirTemp(parent, prefix)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil {
			_ = os.Remove(path)
		}
	}()
	fd, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	c := &Command{tracer: t, accounting: a, path: path, fd: fd}
	defer func() {
		if rerr != nil {
			_ = fd.Close()
		}
	}()
	if t != nil {
		c.id, err = cgroupID(path)
		if err != nil {
			return nil, err
		}
		values := make([]uint64, t.cpus)
		var reserved []netbytesOperationCounterKey
		for _, key := range c.keys() {
			if err := t.objs.OperationByteCounters.Update(key, values, ebpf.UpdateNoExist); err != nil {
				for _, key := range reserved {
					_ = t.objs.OperationByteCounters.Delete(key)
				}
				return nil, err
			}
			reserved = append(reserved, key)
		}
		if privateParent {
			defer func() {
				if rerr != nil {
					for _, hook := range c.links {
						_ = hook.Close()
					}
					for _, key := range reserved {
						_ = t.objs.OperationByteCounters.Delete(key)
					}
				}
			}()
			for _, opts := range []link.CgroupOptions{
				{Path: path, Attach: ebpf.AttachCGroupInetIngress, Program: t.objs.CountOperationIngress},
				{Path: path, Attach: ebpf.AttachCGroupInetEgress, Program: t.objs.CountOperationEgress},
			} {
				hook, err := link.AttachCgroup(opts)
				if err != nil {
					return nil, err
				}
				c.links = append(c.links, hook)
			}
		}
	}
	attrs := new(syscall.SysProcAttr)
	if cmd.SysProcAttr != nil {
		*attrs = *cmd.SysProcAttr
	}
	attrs.UseCgroupFD = true
	attrs.CgroupFD = int(fd.Fd())
	cmd.SysProcAttr = attrs
	return c, nil
}

func (c *Command) keys() []netbytesOperationCounterKey {
	keys := make([]netbytesOperationCounterKey, 0, 4)
	for direction := directionRX; direction <= directionTX; direction++ {
		for scope := scopeInternal; scope <= scopeExternal; scope++ {
			keys = append(keys, netbytesOperationCounterKey{CgroupId: c.id, Direction: direction, Scope: scope})
		}
	}
	return keys
}

// CgroupPath is the operation's private cgroup, retained until Close.
func (c *Command) CgroupPath() string { return c.path }

func (c *Command) Sample() (Sample, error) {
	if c.tracer == nil {
		return Sample{}, errors.New("subprocess network accounting is unavailable")
	}
	var sample Sample
	dst := []*uint64{&sample.InternalRX, &sample.ExternalRX, &sample.InternalTX, &sample.ExternalTX}
	for i, key := range c.keys() {
		values := make([]uint64, c.tracer.cpus)
		if err := c.tracer.objs.OperationByteCounters.Lookup(key, &values); err != nil {
			return Sample{}, err
		}
		for _, n := range values {
			*dst[i] += n
		}
	}
	return sample, nil
}

// WaitEmpty waits for the daemon and all descendants, not just its launcher.
// Keep sampling until this returns, before releasing the counters.
func (c *Command) WaitEmpty(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(filepath.Join(c.path, "cgroup.events"))
		if err != nil {
			return err
		}
		populated := ""
		for line := range strings.SplitSeq(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "populated" {
				populated = fields[1]
			}
		}
		if populated == "0" {
			return nil
		}
		if populated != "1" {
			return errors.New("missing or invalid populated value in cgroup.events")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.accounting.done:
			return errors.New("subprocess accounting stopped")
		case <-ticker.C:
		}
	}
}

func (c *Command) Close() error {
	c.closeOnce.Do(func() {
		var errs []error
		for _, hook := range c.links {
			errs = append(errs, hook.Close())
		}
		if c.tracer != nil {
			for _, key := range c.keys() {
				errs = append(errs, c.tracer.objs.OperationByteCounters.Delete(key))
			}
		}
		errs = append(errs, c.fd.Close(), c.accounting.remove(c.path))
		c.closeErr = errors.Join(errs...)
	})
	return c.closeErr
}
