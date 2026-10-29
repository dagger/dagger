package engineutil

import (
	"errors"
	"sync"

	"golang.org/x/sys/unix"
)

// Transparent huge pages per exec.
//
// The kernel can switch transparent huge pages off for a process
// (PR_SET_THP_DISABLE), never on, and a process passes the setting on to the
// processes it starts, through exec. So execs get huge pages only on a host
// set to "always", and an exec that didn't ask for them is started with them
// switched off except where its program asks for them itself
// (PR_THP_DISABLE_EXCEPT_ADVISED, Linux 6.18): the same as on a host set to
// "madvise", the usual default. The engine sets that mode on itself while the
// runtime starts, under a lock, and puts its own mode back once the runtime
// has started. Kernels before 6.18 don't have the mode; there nothing is
// changed.

const prTHPDisableExceptAdvised = 1 << 1

var thpLaunch struct {
	sync.Mutex
	once sync.Once
	// own is the engine's own mode as PR_GET_THP_DISABLE reads it: 0, 1 (off),
	// or 3 (off except where advised). ok is false where the kernel lacks the
	// "except where advised" mode.
	own int
	ok  bool
}

// startWithTHP runs start with the engine's transparent huge page mode set for
// an exec that did or didn't ask for huge pages. start starts the runtime: like
// go-runc's Run and Exec, it sends the runtime's pid on the channel it is
// given once the runtime has started, and closes the channel when it returns.
// The engine's own mode is put back as soon as the runtime has started, or
// start has returned. The pid, and the close, are passed on to started if it
// is not nil.
func startWithTHP(hugePages bool, started chan<- int, start func(started chan<- int) error) error {
	l := &thpLaunch
	l.Lock()
	l.once.Do(func() {
		own, err := unix.PrctlRetInt(unix.PR_GET_THP_DISABLE, 0, 0, 0, 0)
		if err != nil {
			return
		}
		l.own = own
		if setTHPMode(3) == nil {
			l.ok = true
			setTHPMode(own)
		}
	})
	if !l.ok {
		l.Unlock()
		return start(started)
	}
	mode := 3
	if hugePages {
		mode = 0
	}
	setTHPMode(mode)
	var restore sync.Once
	release := func() {
		restore.Do(func() {
			setTHPMode(l.own)
			l.Unlock()
		})
	}
	runtimeStarted := make(chan int, 1)
	go func() {
		if pid, ok := <-runtimeStarted; ok {
			release()
			if started != nil {
				started <- pid
			}
		}
		if started != nil {
			close(started)
		}
	}()
	err := start(runtimeStarted)
	release()
	return err
}

// setTHPMode sets the engine's mode, as PR_GET_THP_DISABLE reads it.
func setTHPMode(mode int) error {
	switch mode {
	case 0:
		return unix.Prctl(unix.PR_SET_THP_DISABLE, 0, 0, 0, 0)
	case 1:
		return unix.Prctl(unix.PR_SET_THP_DISABLE, 1, 0, 0, 0)
	case 3:
		return unix.Prctl(unix.PR_SET_THP_DISABLE, 1, prTHPDisableExceptAdvised, 0, 0)
	}
	return errors.New("unknown transparent huge page mode")
}
