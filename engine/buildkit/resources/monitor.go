package resources

import (
	"path/filepath"

	resourcestypes "github.com/dagger/dagger/engine/buildkit/resources/types"
	bkresourcestypes "github.com/moby/buildkit/executor/resources/types"
	"github.com/prometheus/procfs"
)

const (
	cgroupProcsFile       = "cgroup.procs"
	cgroupControllersFile = "cgroup.controllers"
	cgroupSubtreeFile     = "cgroup.subtree_control"
	defaultMountpoint     = "/sys/fs/cgroup"
	initGroup             = "init"
)

type Monitor struct {
	isCgroupV2 bool
	proc       procfs.FS
}

func NewMonitor() (*Monitor, error) {
	procfs, err := procfs.NewDefaultFS()
	if err != nil {
		return nil, err
	}

	return &Monitor{
		isCgroupV2: true,
		proc:       procfs,
	}, nil
}

type NetworkSampler interface {
	Sample() (*bkresourcestypes.NetworkSample, error)
}

type RecordOpt struct {
	NetworkSampler NetworkSampler
	SampleCh       chan<- *resourcestypes.Sample
}

func (m *Monitor) RecordNamespace(cgroupNSPath string, opt RecordOpt) (resourcestypes.Recorder, error) {
	if !m.isCgroupV2 {
		return &nopRecorder{}, nil
	}
	return &cgroupRecorder{
		cgroupNSPath: filepath.Join(defaultMountpoint, cgroupNSPath),
		procfs:       m.proc,
		netSampler:   opt.NetworkSampler,
		sampleCh:     opt.SampleCh,
	}, nil
}

/* TODO: not needed, right?
func prepareCgroupControllers() error {
	v, ok := os.LookupEnv("BUILDKIT_SETUP_CGROUPV2_ROOT")
	if !ok {
		return nil
	}
	if b, _ := strconv.ParseBool(v); !b {
		return nil
	}
	// move current process to init cgroup
	if err := os.MkdirAll(filepath.Join(defaultMountpoint, initGroup), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(defaultMountpoint, cgroupProcsFile), os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	s := bufio.NewScanner(f)
	for s.Scan() {
		if err := os.WriteFile(filepath.Join(defaultMountpoint, initGroup, cgroupProcsFile), s.Bytes(), 0); err != nil {
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	dt, err := os.ReadFile(filepath.Join(defaultMountpoint, cgroupControllersFile))
	if err != nil {
		return err
	}
	for _, c := range strings.Split(string(dt), " ") {
		if c == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(defaultMountpoint, cgroupSubtreeFile), []byte("+"+c), 0); err != nil {
			// ignore error
			bklog.L.Warnf("failed to enable cgroup controller %q: %+v", c, err)
		}
	}
	return nil
}
*/
