package cap

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// Launcher holds a configuration for executing an optional callback
// function and/or launching a child process with capability state
// different from the parent.
//
// Note, go1.10 is the earliest version of the Go toolchain that can
// support this abstraction.
type Launcher struct {
	mu sync.RWMutex

	// Note, path and args must be set, or callbackFn. They cannot
	// both be empty. In such cases .Launch() will error out.
	path string
	args []string
	env  []string

	callbackFn func(pa *syscall.ProcAttr, data interface{}) error

	// The following are only honored when path is non empty.
	changeUIDs bool
	uid        int

	changeGIDs bool
	gid        int
	groups     []int

	changeMode bool
	mode       Mode

	iab *IAB

	chroot string
}

// NewLauncher returns a new launcher for the specified program path
// and args with the specified environment.
func NewLauncher(path string, args []string, env []string) *Launcher {
	return &Launcher{
		path: path,
		args: args,
		env:  env,
	}
}

// FuncLauncher returns a new launcher whose purpose is to only
// execute fn in a disposable security context. This is a more bare
// bones variant of the more elaborate program launcher returned by
// cap.NewLauncher().
//
// Note, this launcher will fully ignore any overrides provided by the
// (*Launcher).SetUID() etc. methods. Should your fn() code want to
// run with a different capability state or other privilege, it should
// use the cap.*() functions to set them directly. The cap package
// will ensure that their effects are limited to the runtime of this
// individual function invocation. Warning: executing non-cap.*()
// syscall functions may corrupt the state of the program runtime and
// lead to unpredictable results.
//
// The properties of fn are similar to those supplied via
// (*Launcher).Callback(fn) method. However, this launcher is bare
// bones because, when launching, all privilege management performed
// by the fn() is fully discarded when the fn() completes
// execution. That is, it does not end by exec()ing some program.
func FuncLauncher(fn func(interface{}) error) *Launcher {
	return &Launcher{
		callbackFn: func(ignored *syscall.ProcAttr, data interface{}) error {
			return fn(data)
		},
	}
}

// Callback changes the callback function for Launch() to call before
// changing privilege. The only thing that is assumed is that the OS
// thread in use to call this callback function at launch time will be
// the one that ultimately calls fork to complete the launch of a path
// specified executable. Any returned error value of said function
// will terminate the launch process.
//
// A nil fn causes there to be no callback function invoked during a
// Launch() sequence - it will remove any pre-existing callback.
//
// If the non-nil fn requires any effective capabilities in order to
// run, they can be raised prior to calling .Launch() or inside the
// callback function itself.
//
// If the specified callback fn should call any "cap" package
// functions that change privilege state, these calls will only affect
// the launch goroutine itself. While the launch is in progress, other
// (non-launch) goroutines will block if they attempt to change
// privilege state. These routines will unblock once there are no
// in-flight launches.
//
// Note, the first argument provided to the callback function is the
// *syscall.ProcAttr value to be used when a process launch is taking
// place. A non-nil structure pointer can be modified by the callback
// to enhance the launch. For example, the .Files field can be
// overridden to affect how the launched process' stdin/out/err are
// handled.
//
// Further, the 2nd argument to the callback function is provided at
// Launch() invocation and can communicate contextual info to and from
// the callback and the main process.
func (attr *Launcher) Callback(fn func(*syscall.ProcAttr, interface{}) error) {
	if attr == nil {
		return
	}
	attr.mu.Lock()
	defer attr.mu.Unlock()
	attr.callbackFn = fn
}

// SetUID specifies the UID to be used by the launched command.
func (attr *Launcher) SetUID(uid int) {
	if attr == nil {
		return
	}
	attr.mu.Lock()
	defer attr.mu.Unlock()
	attr.changeUIDs = true
	attr.uid = uid
}

// SetGroups specifies the GID and supplementary groups for the
// launched command.
func (attr *Launcher) SetGroups(gid int, groups []int) {
	if attr == nil {
		return
	}
	attr.mu.Lock()
	defer attr.mu.Unlock()
	attr.changeGIDs = true
	attr.gid = gid
	attr.groups = groups
}

// SetMode specifies the libcap Mode to be used by the launched command.
func (attr *Launcher) SetMode(mode Mode) {
	if attr == nil {
		return
	}
	attr.mu.Lock()
	defer attr.mu.Unlock()
	attr.changeMode = true
	attr.mode = mode
}

// SetIAB specifies the IAB capability vectors to be inherited by the
// launched command. A nil value means the prevailing vectors of the
// parent will be inherited. Note, a duplicate of the provided IAB
// tuple is actually stored, so concurrent modification of the iab
// value does not affect the launcher.
func (attr *Launcher) SetIAB(iab *IAB) {
	if attr == nil {
		return
	}
	attr.mu.Lock()
	defer attr.mu.Unlock()
	attr.iab, _ = iab.Dup()
}

// SetChroot specifies the chroot value to be used by the launched
// command. An empty value means no-change from the prevailing value.
func (attr *Launcher) SetChroot(root string) {
	if attr == nil {
		return
	}
	attr.mu.Lock()
	defer attr.mu.Unlock()
	attr.chroot = root
}

// lResult is used to get the result from the doomed launcher thread.
type lResult struct {
	// tgid holds the thread group id, which is an alias for the
	// shared process id of the parent program.
	tgid int

	// tid holds the tid of the locked launching thread which dies
	// as the launch completes.
	tid int

	// pid is the pid of the launched program (path, args). In
	// the case of a FuncLaunch() this value is zero on success.
	// pid holds -1 in the case of error.
	pid int

	// err is nil on success, but otherwise holds the reason the
	// launch failed.
	err error
}

// ErrLaunchFailed is returned if a launch was aborted with no more
// specific error.
var ErrLaunchFailed = errors.New("launch failed")

// ErrNoLaunch indicates the go runtime available to this binary does
// not reliably support launching. See cap.LaunchSupported.
var ErrNoLaunch = errors.New("launch not supported")

// ErrAmbiguousChroot indicates that the Launcher is being used in
// addition to a callback supplied Chroot. The former should be used
// exclusively for this.
var ErrAmbiguousChroot = errors.New("use Launcher for chroot")

// ErrAmbiguousIDs indicates that the Launcher is being used in
// addition to a callback supplied Credentials. The former should be
// used exclusively for this.
var ErrAmbiguousIDs = errors.New("use Launcher for uids and gids")

// ErrAmbiguousAmbient indicates that the Launcher is being used in
// addition to a callback supplied ambient set and the former should
// be used exclusively in a Launch call.
var ErrAmbiguousAmbient = errors.New("use Launcher for ambient caps")

// lName is the name we temporarily give to the launcher thread. Note,
// this will likely stick around in the process tree if the Go runtime
// is not cleaning up locked launcher OS threads.
var lName = []byte("cap-launcher\000")

// <uapi/linux/prctl.h>
const prSetName = 15

//go:uintptrescapes
func launch(result chan<- lResult, attr *Launcher, data interface{}, quit chan<- struct{}) {
	if quit != nil {
		defer close(quit)
	}

	// Thread group ID is the process ID.
	tgid := syscall.Getpid()

	// This code waits until we are not scheduled on the parent
	// thread.  We will exit this thread once the child has
	// launched.
	runtime.LockOSThread()
	tid := syscall.Gettid()
	if tid == tgid {
		// Force the go runtime to find a new thread to run
		// on.  (It is really awkward to have a process'
		// PID=TID thread in effectively a zombie state. The
		// Go runtime has support for it, but pstree gives
		// ugly output since the prSetName value sticks around
		// after launch completion...
		//
		// (Optimize for time to debug by reducing ugly spam
		// like this.)
		quit := make(chan struct{})
		go launch(result, attr, data, quit)

		// Wait for that go routine to complete.
		<-quit
		runtime.UnlockOSThread()
		return
	}

	// Provide a way to serialize the caller on the thread
	// completing. This should be done by the one locked tid that
	// does the ForkExec(). All the other threads have a different
	// security context.
	defer close(result)

	// By never releasing the LockOSThread here, we guarantee that
	// the runtime will terminate the current OS thread once this
	// function returns.
	scwSetState(launchIdle, launchActive, tid)

	// Name the launcher thread - transient, but helps to debug if
	// the callbackFn or something else hangs up.
	singlesc.prctlrcall(prSetName, uintptr(unsafe.Pointer(&lName[0])), 0)

	var pa *syscall.ProcAttr
	var err error
	var needChroot bool

	// Only prepare a non-nil pa value if a path is provided.
	if attr.path != "" {
		// By default the following file descriptors are preserved for
		// the child. The user should modify them in the callback for
		// stdin/out/err redirection.
		pa = &syscall.ProcAttr{
			Files: []uintptr{0, 1, 2},
		}
		if len(attr.env) != 0 {
			pa.Env = attr.env
		} else {
			pa.Env = os.Environ()
		}
	}

	var pid int
	if attr.callbackFn != nil {
		if err = attr.callbackFn(pa, data); err != nil {
			goto abort
		}
		if attr.path == "" {
			goto abort
		}
	}

	if needChroot, err = validatePA(pa, attr.chroot); err != nil {
		goto abort
	}
	if attr.changeUIDs {
		if err = singlesc.setUID(attr.uid); err != nil {
			goto abort
		}
	}
	if attr.changeGIDs {
		if err = singlesc.setGroups(attr.gid, attr.groups); err != nil {
			goto abort
		}
	}
	if attr.changeMode {
		if err = singlesc.setMode(attr.mode); err != nil {
			goto abort
		}
	}
	if attr.iab != nil {
		// Note, since .iab is a private copy we don't need to
		// lock it around this call.
		if err = singlesc.iabSetProc(attr.iab); err != nil {
			goto abort
		}
	}

	if needChroot {
		c := GetProc()
		if err = c.SetFlag(Effective, true, SYS_CHROOT); err != nil {
			goto abort
		}
		if err = singlesc.setProc(c); err != nil {
			goto abort
		}
	}
	pid, err = syscall.ForkExec(attr.path, attr.args, pa)

abort:
	if err != nil {
		pid = -1
	}
	result <- lResult{
		tgid: tgid,
		tid:  tid,
		pid:  pid,
		err:  err,
	}
}

// pollForThreadExit waits for a thread to terminate. Only after the
// thread has safely exited is it safe to resume POSIX semantics
// security state mirroring for the rest of the process threads.
func (v lResult) pollForThreadExit() {
	if v.tid == -1 {
		return
	}
	for syscall.Tgkill(v.tgid, v.tid, 0) == nil {
		runtime.Gosched()
	}
	scwSetState(launchActive, launchIdle, v.tid)
}

// Launch performs a callback function and/or new program launch with
// a disposable security state. The data object, when not nil, can be
// used to communicate with the callback. It can also be used to
// return details from the callback function's execution.
//
// If the attr was created with NewLauncher(), this present function
// will return the pid of the launched process, or -1 and a non-nil
// error.
//
// If the attr was created with FuncLauncher(), this present function
// will return 0, nil if the callback function exits without
// error. Otherwise it will return -1 and the non-nil error of the
// callback return value.
//
// Note, while the disposable security state thread makes some
// operations seem more isolated - they are *not securely
// isolated*. Launching is inherently violating the POSIX semantics
// maintained by the rest of the "libcap/cap" package, so think of
// launching as a convenience wrapper around fork()ing.
//
// Advanced user note: if the caller of this function thinks they know
// what they are doing by using runtime.LockOSThread() before invoking
// this function, they should understand that the OS thread invoking
// (*Launcher).Launch() is *not* guaranteed to be the one used for the
// disposable security state to perform the launch. If said caller
// needs to run something on the disposable security state thread,
// they should do it via the launch callback function mechanism. (The
// Go runtime is complicated and this is why this Launch mechanism
// provides the optional callback function.)
func (attr *Launcher) Launch(data interface{}) (int, error) {
	if !LaunchSupported {
		return -1, ErrNoLaunch
	}
	if attr == nil {
		return -1, ErrLaunchFailed
	}
	attr.mu.RLock()
	defer attr.mu.RUnlock()
	if attr.callbackFn == nil && (attr.path == "" || len(attr.args) == 0) {
		return -1, ErrLaunchFailed
	}

	result := make(chan lResult)
	go launch(result, attr, data, nil)
	v, ok := <-result
	if !ok {
		return -1, ErrLaunchFailed
	}
	<-result // blocks until the launch() goroutine exits
	v.pollForThreadExit()
	return v.pid, v.err
}
