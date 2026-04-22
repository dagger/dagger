package main

import (
	"context"
	"expvar"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/mackerelio/go-osstat/cpu"
	"github.com/mackerelio/go-osstat/loadavg"
	"github.com/mackerelio/go-osstat/memory"
	"github.com/mackerelio/go-osstat/uptime"
	"github.com/prometheus/procfs"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/constraints"
	"golang.org/x/net/trace"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/server"
)

func setupDebugHandlers(addr string) error {
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

	m.Handle("/debug/gc", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		runtime.GC()
		debug.FreeOSMemory()
		logrus.Debugf("triggered GC from debug endpoint")
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

		l.Info("engine metrics")
	}
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
