package resources

import (
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/procfs"
	"github.com/sourcegraph/conc/pool"

	resourcestypes "github.com/dagger/dagger/engine/buildkit/resources/types"
	"github.com/dagger/dagger/engine/slog"
)

const (
	sampleInterval = 2 * time.Second
)

type cgroupRecorder struct {
	cgroupNSPath string // path to the cgroup namespace being monitored (e.g. /sys/fs/cgroup/buildkit/abcdef123456)
	procfs       procfs.FS
	netSampler   NetworkSampler
	sampleCh     chan<- *resourcestypes.Sample

	startCPUStat *procfs.CPUStat
	samplerLoop  *pool.ErrorPool

	closeOnce func() error
	closeCh   chan struct{}
}

func (r *cgroupRecorder) Start() {
	if stat, err := r.procfs.Stat(); err == nil {
		r.startCPUStat = &stat.CPUTotal
	} else {
		slog.Error("failed to get initial CPU stat", "err", err)
	}

	r.samplerLoop = pool.New().WithErrors()
	r.samplerLoop.Go(r.run)

	r.closeCh = make(chan struct{})
	r.closeOnce = sync.OnceValue(r.close)
}

func (r *cgroupRecorder) run() error {
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()

	defer close(r.sampleCh)
loop:
	for {
		select {
		case <-r.closeCh:
			break loop
		case <-ticker.C:
			now := time.Now()
			sample, err := r.sample(now)
			if err != nil {
				slog.Error("failed to sample cgroup", "err", err)
				continue
			}
			select {
			case r.sampleCh <- sample:
			case <-r.closeCh:
				break loop
			}
		}
	}

	// try a final sample before closing
	sample, err := r.sample(time.Now())
	if err != nil {
		slog.Error("failed to sample cgroup", "err", err)
		return nil
	}
	select {
	case r.sampleCh <- sample:
	default:
	}
	return nil
}

func (r *cgroupRecorder) sample(tm time.Time) (*resourcestypes.Sample, error) {
	cpu, err := getCgroupCPUStat(r.cgroupNSPath)
	if err != nil {
		return nil, err
	}
	memory, err := getCgroupMemoryStat(r.cgroupNSPath)
	if err != nil {
		return nil, err
	}
	io, err := getCgroupIOStat(r.cgroupNSPath)
	if err != nil {
		return nil, err
	}
	pids, err := getCgroupPIDsStat(r.cgroupNSPath)
	if err != nil {
		return nil, err
	}
	sample := &resourcestypes.Sample{
		Timestamp_: tm,
		CPUStat:    cpu,
		MemoryStat: memory,
		IOStat:     io,
		PIDsStat:   pids,
	}
	if r.netSampler != nil {
		net, err := r.netSampler.Sample()
		if err != nil {
			return nil, fmt.Errorf("failed to sample network: %w", err)
		}
		sample.NetStat = (*resourcestypes.NetworkSample)(net)
	}
	return sample, nil
}

func (r *cgroupRecorder) Close() error {
	if r.closeOnce == nil {
		return nil
	}
	return r.closeOnce()
}

func (r *cgroupRecorder) close() error {
	close(r.closeCh)
	// TODO: technically racy if Close and Start are called at same time... ?
	if r.samplerLoop == nil {
		return nil
	}
	return r.samplerLoop.Wait()
}

type nopRecorder struct {
}

func (r *nopRecorder) Start() {
}

func (r *nopRecorder) Close() error {
	return nil
}

/* TODO:
func (r *cgroupRecord) close() {
	r.once.Do(func() {
		defer close(r.done)
		go func() {
			r.monitor.mu.Lock()
			delete(r.monitor.records, r.ns)
			r.monitor.mu.Unlock()
		}()
		if r.sampler == nil {
			return
		}
		s, err := r.sampler.Close(true)
		if err != nil {
			r.err = err
		} else {
			r.samples = s
		}
		r.closeSampler()

		if r.startCPUStat != nil {
			stat, err := r.monitor.proc.Stat()
			if err == nil {
				cpu := &resourcestypes.SysCPUStat{
					User:      stat.CPUTotal.User - r.startCPUStat.User,
					Nice:      stat.CPUTotal.Nice - r.startCPUStat.Nice,
					System:    stat.CPUTotal.System - r.startCPUStat.System,
					Idle:      stat.CPUTotal.Idle - r.startCPUStat.Idle,
					Iowait:    stat.CPUTotal.Iowait - r.startCPUStat.Iowait,
					IRQ:       stat.CPUTotal.IRQ - r.startCPUStat.IRQ,
					SoftIRQ:   stat.CPUTotal.SoftIRQ - r.startCPUStat.SoftIRQ,
					Steal:     stat.CPUTotal.Steal - r.startCPUStat.Steal,
					Guest:     stat.CPUTotal.Guest - r.startCPUStat.Guest,
					GuestNice: stat.CPUTotal.GuestNice - r.startCPUStat.GuestNice,
				}
				r.sysCPUStat = cpu
			}
		}
	})
}

*/
