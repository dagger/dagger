//go:build linux && (386 || amd64 || arm64)

package nettracer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

// Executor workloads are accounted at cgroup socket-buffer hooks attached once
// to the cgroup containing every workload's cgroup. Per-veth TCX links are not
// used: attaching or releasing one holds RTNL across an RCU grace period, which
// serialized every container's network setup and teardown on the host.

// WorkloadParentPath returns the cgroup that contains every executor
// workload's cgroup for cgroupParent, as the OCI spec generator places them.
// Systemd slice parents name no path the engine can attach to.
func WorkloadParentPath(cgroupParent string) (string, bool) {
	if cgroupParent == "" {
		cgroupParent = "/"
	}
	if strings.Contains(cgroupParent, ".slice") && strings.HasSuffix(cgroupParent, ":") {
		return "", false
	}
	return filepath.Join("/sys/fs/cgroup", cgroupParent, "exec"), true
}

// AttachWorkloads attaches the workload programs to parent, creating it if
// needed. Every workload cgroup created below it inherits them.
func (t *Tracer) AttachWorkloads(parent string) (rerr error) {
	t.workloadMu.Lock()
	defer t.workloadMu.Unlock()
	if t.workloadParent != "" {
		return errors.New("workload network accounting already attached")
	}
	parent = filepath.Clean(parent)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("creating workload parent cgroup %s: %w", parent, err)
	}
	id, err := cgroupID(parent)
	if err != nil {
		return err
	}
	var links []link.Link
	defer func() {
		if rerr != nil {
			for _, l := range links {
				_ = l.Close()
			}
		}
	}()
	for _, opts := range []link.CgroupOptions{
		{Path: parent, Attach: ebpf.AttachCGroupInetIngress, Program: t.objs.CountWorkloadIngress},
		{Path: parent, Attach: ebpf.AttachCGroupInetEgress, Program: t.objs.CountWorkloadEgress},
	} {
		l, err := link.AttachCgroup(opts)
		if err != nil {
			return fmt.Errorf("attaching workload accounting to %s: %w", parent, err)
		}
		links = append(links, l)
	}
	if err := t.objs.WorkloadParentCgroupId.Put(uint32(0), id); err != nil {
		return fmt.Errorf("setting workload parent cgroup ID: %w", err)
	}
	t.workloadParent = parent
	t.workloadLinks = links
	return nil
}

func (t *Tracer) closeWorkloads() error {
	t.workloadMu.Lock()
	defer t.workloadMu.Unlock()
	var errs []error
	for _, l := range t.workloadLinks {
		errs = append(errs, l.Close())
	}
	t.workloadLinks = nil
	return errors.Join(errs...)
}

// Workload holds the counters of one executor workload's cgroup.
type Workload struct {
	tracer    *Tracer
	id        uint64
	closeOnce sync.Once
	closeErr  error
}

// Workload reserves counters for the workload cgroup at path, which must be a
// direct child of the attached workload parent. It creates the cgroup if the
// runtime has not yet, so the counters exist before the workload's first
// packet; the runtime adopts an existing, empty cgroup.
func (t *Tracer) Workload(path string) (_ *Workload, rerr error) {
	t.workloadMu.Lock()
	parent := t.workloadParent
	t.workloadMu.Unlock()
	if parent == "" {
		return nil, errors.New("workload network accounting is unavailable")
	}
	path = filepath.Clean(path)
	if filepath.Dir(path) != parent {
		return nil, fmt.Errorf("workload cgroup %s is not a child of %s", path, parent)
	}
	if err := os.Mkdir(path, 0o755); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("creating workload cgroup %s: %w", path, err)
	}
	id, err := cgroupID(path)
	if err != nil {
		return nil, err
	}
	w := &Workload{tracer: t, id: id}
	values := make([]uint64, t.cpus)
	var reserved []netbytesOperationCounterKey
	for _, key := range w.keys() {
		if err := t.objs.WorkloadByteCounters.Update(key, values, ebpf.UpdateNoExist); err != nil {
			for _, key := range reserved {
				_ = t.objs.WorkloadByteCounters.Delete(key)
			}
			return nil, fmt.Errorf("reserving workload counters: %w", err)
		}
		reserved = append(reserved, key)
	}
	return w, nil
}

func (w *Workload) keys() []netbytesOperationCounterKey {
	keys := make([]netbytesOperationCounterKey, 0, 4)
	for direction := directionRX; direction <= directionTX; direction++ {
		for scope := scopeInternal; scope <= scopeExternal; scope++ {
			keys = append(keys, netbytesOperationCounterKey{CgroupId: w.id, Direction: direction, Scope: scope})
		}
	}
	return keys
}

// Sample returns the workload's cumulative counters.
func (w *Workload) Sample() (Sample, error) {
	var sample Sample
	dst := []*uint64{&sample.InternalRX, &sample.ExternalRX, &sample.InternalTX, &sample.ExternalTX}
	for i, key := range w.keys() {
		values := make([]uint64, w.tracer.cpus)
		if err := w.tracer.objs.WorkloadByteCounters.Lookup(key, &values); err != nil {
			return Sample{}, err
		}
		for _, n := range values {
			*dst[i] += n
		}
	}
	return sample, nil
}

// Close releases the workload's counters. Sample the workload for the last
// time first.
func (w *Workload) Close() error {
	w.closeOnce.Do(func() {
		var errs []error
		for _, key := range w.keys() {
			errs = append(errs, w.tracer.objs.WorkloadByteCounters.Delete(key))
		}
		w.closeErr = errors.Join(errs...)
	})
	return w.closeErr
}
