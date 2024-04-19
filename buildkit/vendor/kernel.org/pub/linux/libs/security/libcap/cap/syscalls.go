package cap

import (
	"runtime"
	"sync"
	"syscall"

	"kernel.org/pub/linux/libs/security/libcap/psx"
)

// multisc provides syscalls overridable for testing purposes that
// support a single kernel security state for all OS threads.
// We use this version when we are cgo compiling because
// we need to manage the native C pthreads too.
var multisc = &syscaller{
	w3: psx.Syscall3,
	w6: psx.Syscall6,
	r3: syscall.RawSyscall,
	r6: syscall.RawSyscall6,
}

// singlesc provides a single threaded implementation. Users should
// take care to ensure the thread is locked and marked nogc.
var singlesc = &syscaller{
	w3: syscall.RawSyscall,
	w6: syscall.RawSyscall6,
	r3: syscall.RawSyscall,
	r6: syscall.RawSyscall6,
}

// launchState is used to track which variant of the write syscalls
// should execute.
type launchState int

// these states are used to understand when a launch is in progress.
const (
	launchIdle launchState = iota
	launchActive
	launchBlocked
)

// scwMu is used to fully serialize the write system calls. Note, this
// would generally not be necessary, but in the case of Launch we get
// into a situation where the launching thread is temporarily allowed
// to deviate from the kernel state of the rest of the runtime and
// allowing other threads to perform w* syscalls will potentially
// interfere with the launching process. In pure Go binaries, this
// will lead inevitably to a panic when the AllThreadsSyscall
// discovers inconsistent thread state.
//
// scwMu protects scwTIDs and scwState
var scwMu sync.Mutex

// scwTIDs holds the thread IDs of the threads that are executing a
// launch it is empty when no launches are occurring.
var scwTIDs = make(map[int]bool)

// scwState captures whether a launch is in progress or not.
var scwState = launchIdle

// scwCond is used to announce when scwState changes to other
// goroutines waiting for it to change.
var scwCond = sync.NewCond(&scwMu)

// scwSetState blocks until a launch state change between states from
// and to occurs. We use this for more context specific syscaller
// use. In the case that the caller is requesting a launchActive ->
// launchIdle transition they are declaring that tid is no longer
// launching. If another thread is also launching the call will
// complete, but the launchState will remain launchActive.
func scwSetState(from, to launchState, tid int) {
	scwMu.Lock()
	for scwState != from {
		if scwState == launchActive && from == launchIdle && to == launchActive {
			break // This "transition" is also allowed.
		}
		scwCond.Wait()
	}
	if from == launchIdle && to == launchActive {
		scwTIDs[tid] = true
	} else if from == launchActive && to == launchIdle {
		delete(scwTIDs, tid)
		if len(scwTIDs) != 0 {
			to = from // not actually idle
		}
	}
	scwState = to
	scwCond.Broadcast()
	scwMu.Unlock()
}

// scwStateSC blocks until the current syscaller is available for
// writes, and then marks launchBlocked. Use scwSetState to perform
// the reverse transition (blocked->returned state value).
func scwStateSC() (launchState, *syscaller) {
	sc := multisc
	scwMu.Lock()
	for {
		if scwState == launchIdle {
			break
		}
		runtime.LockOSThread()
		if scwState == launchActive && scwTIDs[syscall.Gettid()] {
			sc = singlesc
			// note, we don't runtime.UnlockOSThread()
			// here because we have no reason to ever
			// allow this thread to return to normal use -
			// we need it dead before we can return to the
			// launchIdle state.
			break
		}
		runtime.UnlockOSThread()
		scwCond.Wait()
	}
	old := scwState
	scwState = launchBlocked
	scwCond.Broadcast()
	scwMu.Unlock()

	return old, sc
}
