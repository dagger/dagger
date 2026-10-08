package engineutil

import (
	"bytes"
	"os"
	"path/filepath"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/wcprof"
)

// workloadStartWatch observes when the container's workload starts, for
// wcprof's split of container start-up from the user's process run.
//
// The go-runc started callback fires when the runc process is forked, before
// runc has created the container. `runc run --pid-file` writes the pid file
// only after it has created the container and released the container's
// process to exec the workload (runc's utils_linux.go runner.run: the file is
// written after container.Run, which ends by unblocking the init through the
// exec fifo), and writes it as a temporary file renamed into place. The
// rename is the boundary: before it, runc is setting the container up;
// after it, the workload runs.
type workloadStartWatch struct {
	file  *os.File
	name  string
	atNS  atomic.Int64
	ended chan struct{}
}

// watchWorkloadStart starts watching for runc to rename the pid file at
// pidFile into place. It must be called before runc starts.
func watchWorkloadStart(pidFile string) (*workloadStartWatch, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	if _, err := unix.InotifyAddWatch(fd, filepath.Dir(pidFile), unix.IN_MOVED_TO); err != nil {
		unix.Close(fd)
		return nil, err
	}
	w := &workloadStartWatch{
		// Non-blocking, so reads park in the runtime poller and Close
		// unblocks them.
		file:  os.NewFile(uintptr(fd), "inotify"),
		name:  filepath.Base(pidFile),
		ended: make(chan struct{}),
	}
	go w.read()
	return w, nil
}

func (w *workloadStartWatch) read() {
	defer close(w.ended)
	buf := make([]byte, 4096)
	for {
		n, err := w.file.Read(buf)
		if err != nil {
			return
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			nameStart := off + unix.SizeofInotifyEvent
			nameEnd := nameStart + int(ev.Len)
			if nameEnd > n {
				break
			}
			if string(bytes.TrimRight(buf[nameStart:nameEnd], "\x00")) == w.name {
				w.atNS.Store(wcprof.NowNS())
				return
			}
			off = nameEnd
		}
	}
}

// stop ends the watch and returns when the workload started (a wcprof
// timestamp), or 0 if that was not observed.
func (w *workloadStartWatch) stop() int64 {
	if w == nil {
		return 0
	}
	// Closing wakes a parked read, which then returns.
	w.file.Close()
	<-w.ended
	return w.atNS.Load()
}
