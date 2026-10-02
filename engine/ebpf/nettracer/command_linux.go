//go:build linux

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
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

// commandAccounting owns a subtree containing only engine subprocesses, never
// executor workloads. Its hooks survive individual command cgroups, so late
// socket traffic still contributes to the engine total after span completion.
type commandAccounting struct {
	path    string
	ingress link.Link
	egress  link.Link
	mu      sync.Mutex
	pending map[string]struct{}
	done    chan struct{}
	stopped chan struct{}
	closed  bool
}

func (t *Tracer) newCommandAccounting(parent string) (_ *commandAccounting, rerr error) {
	path, err := os.MkdirTemp(parent, "dagger-commands-")
	if err != nil {
		return nil, err
	}
	a := &commandAccounting{path: path, pending: map[string]struct{}{}, done: make(chan struct{}), stopped: make(chan struct{})}
	defer func() {
		if rerr != nil {
			if a.ingress != nil {
				_ = a.ingress.Close()
			}
			if a.egress != nil {
				_ = a.egress.Close()
			}
			_ = os.Remove(path)
		}
	}()
	// Probe clone-into-cgroup before touching any operation. Seccomp can allow
	// BPF and cgroup creation while denying clone3; metrics must stay optional.
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
	a.ingress, err = link.AttachCgroup(link.CgroupOptions{
		Path: path, Attach: ebpf.AttachCGroupInetIngress, Program: t.objs.CountOperationIngress,
	})
	if err != nil {
		return nil, err
	}
	a.egress, err = link.AttachCgroup(link.CgroupOptions{
		Path: path, Attach: ebpf.AttachCGroupInetEgress, Program: t.objs.CountOperationEgress,
	})
	if err != nil {
		return nil, err
	}
	go a.reap()
	return a, nil
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
		// A helper may outlive its parent (e.g. sshfs). Never kill it just to
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
	errs = append(errs, a.ingress.Close(), a.egress.Close())
	for path := range a.pending {
		errs = append(errs, os.Remove(path))
	}
	errs = append(errs, os.Remove(a.path))
	return errors.Join(errs...)
}

type Command struct {
	tracer    *Tracer
	path      string
	fd        *os.File
	id        uint64
	closeOnce sync.Once
	closeErr  error
}

// PrepareCommand places a command in its own accounting cgroup at clone time.
// Call before Start, and Close only after Wait (or after a daemon's lifetime).
// An error leaves cmd unchanged; callers can run it without network metrics.
func PrepareCommand(cmd *exec.Cmd) (_ *Command, rerr error) {
	t := Active()
	if t == nil || t.commands == nil {
		if t != nil && t.commandErr != nil {
			return nil, t.commandErr
		}
		return nil, errors.New("subprocess network accounting is unavailable")
	}
	if cmd.Process != nil || (cmd.SysProcAttr != nil && cmd.SysProcAttr.UseCgroupFD) {
		return nil, errors.New("command already started or has an assigned cgroup")
	}
	path, err := os.MkdirTemp(t.commands.path, "command-")
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
	c := &Command{tracer: t, path: path, fd: fd}
	defer func() {
		if rerr != nil {
			_ = fd.Close()
		}
	}()
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

func (c *Command) Sample() (Sample, error) {
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
		case <-c.tracer.commands.done:
			return errors.New("subprocess accounting stopped")
		case <-ticker.C:
		}
	}
}

func (c *Command) Close() error {
	c.closeOnce.Do(func() {
		var errs []error
		for _, key := range c.keys() {
			errs = append(errs, c.tracer.objs.OperationByteCounters.Delete(key))
		}
		errs = append(errs, c.fd.Close(), c.tracer.commands.remove(c.path))
		c.closeErr = errors.Join(errs...)
	})
	return c.closeErr
}
