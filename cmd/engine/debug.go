package main

import (
	"context"
	"encoding/json"
	"expvar"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/mackerelio/go-osstat/cpu"
	"github.com/mackerelio/go-osstat/loadavg"
	"github.com/mackerelio/go-osstat/memory"
	"github.com/mackerelio/go-osstat/uptime"
	"github.com/prometheus/procfs"
	"github.com/prometheus/procfs/blockdevice"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/constraints"
	"golang.org/x/net/trace"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/server"
	"github.com/dagger/dagger/engine/wcprof"
)

func setupDebugHandlers(addr string, eng *server.Server) error {
	m := http.NewServeMux()
	m.Handle("/debug/vars", expvar.Handler())
	m.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	m.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	m.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	m.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	m.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	m.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	m.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
	m.Handle("/debug/pprof/block", pprof.Handler("block"))
	m.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	m.Handle("/debug/requests", http.HandlerFunc(trace.Traces))
	m.Handle("/debug/events", http.HandlerFunc(trace.Events))
	// m.Handle("/debug/fgtrace", fgtrace.Config{})

	// uncomment these to get data from /mutex and /block
	// runtime.SetMutexProfileFraction(1)
	// runtime.SetBlockProfileRate(1)

	// Every process the engine can see, with its state, wait channel and
	// kernel stack, for diagnosing a stuck shutdown.
	m.Handle("/debug/processes", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, ent := range entries {
			if _, err := strconv.Atoi(ent.Name()); err != nil {
				continue
			}
			dir := filepath.Join("/proc", ent.Name())
			stat, _ := os.ReadFile(filepath.Join(dir, "stat"))
			wchan, _ := os.ReadFile(filepath.Join(dir, "wchan"))
			cmdline, _ := os.ReadFile(filepath.Join(dir, "cmdline"))
			// at most 16 frames of the kernel stack, as is enough to see
			// where a process waits
			stack, _ := os.ReadFile(filepath.Join(dir, "stack"))
			if lines := strings.SplitAfter(string(stack), "\n"); len(lines) > 16 {
				stack = []byte(strings.Join(lines[:16], ""))
			}
			fmt.Fprintf(rw, "== %s\nstat: %s\nwchan: %s\ncmdline: %s\n%s\n",
				ent.Name(), strings.TrimSpace(string(stat)), wchan,
				strings.ReplaceAll(string(cmdline), "\x00", " "), stack)
		}
	}))
	m.Handle("/debug/gc", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		runtime.GC()
		debug.FreeOSMemory()
		logrus.Debugf("triggered GC from debug endpoint")
	}))
	m.Handle("/debug/dagql/egraph", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if eng == nil {
			http.Error(rw, "engine server not available", http.StatusServiceUnavailable)
			return
		}
		snapshot := eng.DagqlDebugSnapshot()
		if snapshot == nil {
			http.Error(rw, "dagql cache not available", http.StatusServiceUnavailable)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(rw)
		enc.SetIndent("", "  ")
		if err := enc.Encode(snapshot); err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
	}))
	m.Handle("/debug/dagql/cache", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if eng == nil {
			http.Error(rw, "engine server not available", http.StatusServiceUnavailable)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		if err := eng.WriteDagqlCacheDebugSnapshot(rw); err != nil {
			logrus.WithError(err).Warn("failed streaming dagql cache debug snapshot")
		}
	}))
	m.Handle("/debug/client-lifecycle", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if eng == nil {
			http.Error(rw, "engine server not available", http.StatusServiceUnavailable)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(rw)
		enc.SetIndent("", "  ")
		if err := enc.Encode(eng.ClientLifecycleDebugSnapshot()); err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
		}
	}))
	// Engine-global profiling toggle: GET reports state, POST "on"/"off"
	// (or any strconv.ParseBool value) flips it. Disabling keeps buffered
	// events dumpable and does not stop sessions that opted in via --profile.
	m.Handle("/debug/wcprof/enabled", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			body, err := io.ReadAll(io.LimitReader(req.Body, 64))
			if err != nil {
				http.Error(rw, err.Error(), http.StatusBadRequest)
				return
			}
			val := strings.TrimSpace(string(body))
			if val == "" {
				val = req.URL.Query().Get("v")
			}
			var on bool
			switch strings.ToLower(val) {
			case "on":
				on = true
			case "off":
				on = false
			default:
				var err error
				on, err = strconv.ParseBool(val)
				if err != nil {
					http.Error(rw, fmt.Sprintf("POST body must be on/off or a boolean, got %q", val), http.StatusBadRequest)
					return
				}
			}
			if on {
				wcprof.EnableGlobal()
				logrus.Info("wcprof global recording enabled via debug endpoint")
			} else {
				wcprof.DisableGlobal()
				logrus.Info("wcprof global recording disabled via debug endpoint")
			}
		}
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(map[string]bool{ //nolint:errcheck,errchkjson
			"global_enabled":  wcprof.GloballyEnabled(),
			"recorder_active": wcprof.Active() != nil,
		})
	}))
	m.Handle("/debug/wcprof/dump", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rec := wcprof.Active()
		if rec == nil {
			http.Error(rw, "wcprof was never enabled (POST 'on' to /debug/wcprof/enabled, connect a client with --profile, start the engine with --wcprof, or set _DAGGER_WCPROF=1)", http.StatusServiceUnavailable)
			return
		}
		// flush by default; pass ?flush=0 to keep events buffered
		flush := req.URL.Query().Get("flush") != "0"
		rw.Header().Set("Content-Type", "application/x-ndjson")
		if err := rec.WriteDump(rw, flush); err != nil {
			logrus.WithError(err).Warn("failed streaming wcprof dump")
		}
	}))

	// setting debugaddr is opt-in. permission is defined by listener address
	trace.AuthRequest = func(_ *http.Request) (bool, bool) {
		return true, true
	}

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	logrus.Debugf("debug handlers listening at %s", addr)
	go http.Serve(l, m)
	return nil
}

// logTraceMetrics logs information useful for debugging but too expensive for the
// default debug log level.
func logTraceMetrics(ctx context.Context) {
	for range time.Tick(5 * time.Minute) {
		l := bklog.G(ctx)

		// This is the same implementation as runtime/debug.Stack(), but with all=true.
		// It should be used with caution as each call results in a stop-the-world for
		// the gc, could take a lot of memory, and has to do extra work to ensure
		// the full stack is collected
		buf := make([]byte, 1024)
		for {
			n := runtime.Stack(buf, true)
			if n < len(buf) {
				buf = buf[:n]
				break
			}
			buf = make([]byte, 2*len(buf))
		}
		l = l.WithField("goroutine-stacks", string(buf))

		l.Trace("engine trace metrics")
	}
}

func logMetrics(ctx context.Context, engineStateRootDir string, eng *server.Server) {
	for range time.Tick(60 * time.Second) {
		l := bklog.G(ctx)

		// controller stats
		l = eng.LogMetrics(l)

		// goroutine stats
		l = l.WithField("goroutine-count", runtime.NumGoroutine())

		// system cpu stats
		cpuStats, err := cpu.Get()
		if err == nil {
			l = withUnsignedIntField(l, "cpu-total", cpuStats.Total)
			l = withUnsignedIntField(l, "cpu-user", cpuStats.User)
			l = withUnsignedIntField(l, "cpu-nice", cpuStats.Nice)
			l = withUnsignedIntField(l, "cpu-system", cpuStats.System)
			l = withUnsignedIntField(l, "cpu-idle", cpuStats.Idle)
			l = withUnsignedIntField(l, "cpu-iowait", cpuStats.Iowait)
			l = withUnsignedIntField(l, "cpu-irq", cpuStats.Irq)
			l = withUnsignedIntField(l, "cpu-softirq", cpuStats.Softirq)
			l = withUnsignedIntField(l, "cpu-steal", cpuStats.Steal)
			l = withSignedIntField(l, "cpu-count", cpuStats.CPUCount)
		} else {
			l = l.WithField("cpu-error", err.Error())
		}

		// system loadavg stats
		loadAvgStats, err := loadavg.Get()
		if err == nil {
			l = withFloatField(l, "loadavg-1", loadAvgStats.Loadavg1)
			l = withFloatField(l, "loadavg-5", loadAvgStats.Loadavg5)
			l = withFloatField(l, "loadavg-15", loadAvgStats.Loadavg15)
		} else {
			l = l.WithField("loadavg-error", err.Error())
		}

		// system memory stats
		memStats, err := memory.Get()
		if err == nil {
			l = withUnsignedIntField(l, "mem-total", memStats.Total)
			l = withUnsignedIntField(l, "mem-free", memStats.Free)
			l = withUnsignedIntField(l, "mem-available", memStats.Available)
			l = withUnsignedIntField(l, "mem-buffers", memStats.Buffers)
			l = withUnsignedIntField(l, "mem-cached", memStats.Cached)
			l = withUnsignedIntField(l, "mem-active", memStats.Active)
			l = withUnsignedIntField(l, "mem-inactive", memStats.Inactive)
			l = withUnsignedIntField(l, "mem-swap-cached", memStats.SwapCached)
			l = withUnsignedIntField(l, "mem-swap-total", memStats.SwapTotal)
			l = withUnsignedIntField(l, "mem-swap-free", memStats.SwapFree)
			l = withUnsignedIntField(l, "mem-mapped", memStats.Mapped)
			l = withUnsignedIntField(l, "mem-shmem", memStats.Shmem)
			l = withUnsignedIntField(l, "mem-slab", memStats.Slab)
			l = withUnsignedIntField(l, "mem-page-tables", memStats.PageTables)
			l = withUnsignedIntField(l, "mem-committed", memStats.Committed)
			l = withUnsignedIntField(l, "mem-vmalloc-used", memStats.VmallocUsed)
		} else {
			l = l.WithField("mem-error", err.Error())
		}

		// system uptime
		uptimeDuration, err := uptime.Get()
		if err == nil {
			l = withDurationField(l, "uptime", uptimeDuration)
		} else {
			l = l.WithField("uptime-error", err.Error())
		}

		// self stats
		procSelf, err := procfs.Self()
		if err == nil {
			// self memory stats
			smapsRollup, err := procSelf.ProcSMapsRollup()
			if err == nil {
				l = withUnsignedIntField(l, "proc-self-mem-rss", smapsRollup.Rss)
				l = withUnsignedIntField(l, "proc-self-mem-pss", smapsRollup.Pss)
				l = withUnsignedIntField(l, "proc-self-mem-shared-clean", smapsRollup.SharedClean)
				l = withUnsignedIntField(l, "proc-self-mem-shared-dirty", smapsRollup.SharedDirty)
				l = withUnsignedIntField(l, "proc-self-mem-private-clean", smapsRollup.PrivateClean)
				l = withUnsignedIntField(l, "proc-self-mem-private-dirty", smapsRollup.PrivateDirty)
				l = withUnsignedIntField(l, "proc-self-mem-referenced", smapsRollup.Referenced)
				l = withUnsignedIntField(l, "proc-self-mem-anonymous", smapsRollup.Anonymous)
				l = withUnsignedIntField(l, "proc-self-mem-swap", smapsRollup.Swap)
				l = withUnsignedIntField(l, "proc-self-mem-swap-pss", smapsRollup.SwapPss)
			} else {
				l = l.WithField("proc-self-mem-error", err.Error())
			}
			// TODO: self cpu stats
		} else {
			l = l.WithField("proc-self-error", err.Error())
		}

		// disk usage stats
		for _, dir := range []string{"/", engineStateRootDir} {
			var statfs unix.Statfs_t
			err := unix.Statfs(dir, &statfs)
			if err == nil {
				l = withUnsignedIntField(l, fmt.Sprintf("disk-size-%s", dir), statfs.Blocks*uint64(statfs.Bsize))
				l = withUnsignedIntField(l, fmt.Sprintf("disk-free-%s", dir), statfs.Bfree*uint64(statfs.Bsize))
				l = withUnsignedIntField(l, fmt.Sprintf("disk-available-%s", dir), statfs.Bavail*uint64(statfs.Bsize))
			} else {
				l = l.WithField(fmt.Sprintf("disk-error-%s", dir), err.Error())
			}
		}

		// disk I/O, pressure stall and writeback stats, to tell a slow or
		// stalled disk apart from a busy workload
		l = withDiskIOStats(l, engineStateRootDir)
		procFS, err := procfs.NewDefaultFS()
		if err == nil {
			l = withPressureStats(l, procFS)
			meminfo, err := procFS.Meminfo()
			if err == nil && meminfo.DirtyBytes != nil && meminfo.WritebackBytes != nil {
				l = withUnsignedIntField(l, "mem-dirty", *meminfo.DirtyBytes)
				l = withUnsignedIntField(l, "mem-writeback", *meminfo.WritebackBytes)
			}
		} else {
			l = l.WithField("procfs-error", err.Error())
		}

		l.Info("engine metrics")
	}
}

// withDiskIOStats adds the /proc/diskstats stats of the block device that
// holds dir. All are cumulative counters except disk-io-in-progress, which is
// the number of I/Os in flight when sampled.
func withDiskIOStats(l *logrus.Entry, dir string) *logrus.Entry {
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return l.WithField("disk-io-error", err.Error())
	}
	fs, err := blockdevice.NewDefaultFS()
	if err != nil {
		return l.WithField("disk-io-error", err.Error())
	}
	diskstats, err := fs.ProcDiskstats()
	if err != nil {
		return l.WithField("disk-io-error", err.Error())
	}
	major, minor := unix.Major(st.Dev), unix.Minor(st.Dev)
	for _, s := range diskstats {
		if s.MajorNumber != major || s.MinorNumber != minor {
			continue
		}
		l = l.WithField("disk-io-device", s.DeviceName)
		l = withUnsignedIntField(l, "disk-io-reads", s.ReadIOs)
		l = withUnsignedIntField(l, "disk-io-read-bytes", s.ReadSectors*512)
		l = withUnsignedIntField(l, "disk-io-read-ms", s.ReadTicks)
		l = withUnsignedIntField(l, "disk-io-writes", s.WriteIOs)
		l = withUnsignedIntField(l, "disk-io-write-bytes", s.WriteSectors*512)
		l = withUnsignedIntField(l, "disk-io-write-ms", s.WriteTicks)
		l = withUnsignedIntField(l, "disk-io-in-progress", s.IOsInProgress)
		l = withUnsignedIntField(l, "disk-io-busy-ms", s.IOsTotalTicks)
		l = withUnsignedIntField(l, "disk-io-weighted-ms", s.WeightedIOTicks)
		return l
	}
	return l.WithField("disk-io-error", fmt.Sprintf("no block device %d:%d for %s", major, minor, dir))
}

// withPressureStats adds the cumulative pressure stall totals, in
// microseconds, from /proc/pressure.
func withPressureStats(l *logrus.Entry, fs procfs.FS) *logrus.Entry {
	for _, resource := range []string{"cpu", "io", "memory"} {
		psi, err := fs.PSIStatsForResource(resource)
		if err != nil {
			l = l.WithField(fmt.Sprintf("pressure-%s-error", resource), err.Error())
			continue
		}
		if psi.Some != nil {
			l = withUnsignedIntField(l, fmt.Sprintf("pressure-%s-some-us", resource), psi.Some.Total)
		}
		if psi.Full != nil {
			l = withUnsignedIntField(l, fmt.Sprintf("pressure-%s-full-us", resource), psi.Full.Total)
		}
	}
	return l
}

func withUnsignedIntField[T constraints.Unsigned](l *logrus.Entry, name string, value T) *logrus.Entry {
	return l.WithField(name, strconv.FormatUint(uint64(value), 10))
}

func withSignedIntField[T constraints.Signed](l *logrus.Entry, name string, value T) *logrus.Entry {
	return l.WithField(name, strconv.FormatInt(int64(value), 10))
}

func withFloatField[T constraints.Float](l *logrus.Entry, name string, value T) *logrus.Entry {
	return l.WithField(name, strconv.FormatFloat(float64(value), 'f', -1, 64))
}

func withDurationField(l *logrus.Entry, name string, value time.Duration) *logrus.Entry {
	return l.WithField(name, value.String())
}
