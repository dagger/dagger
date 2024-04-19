// +build linux,cgo

package psx // import "kernel.org/pub/linux/libs/security/libcap/psx"

import (
	"runtime"
	"sync"
	"syscall"
)

// #cgo LDFLAGS: -lpthread -Wl,-wrap,pthread_create
//
// #include <errno.h>
// #include "psx_syscall.h"
//
// long __errno_too(long set_errno) {
//     long v = errno;
//     if (set_errno >= 0) {
//       errno = set_errno;
//     }
//     return v;
// }
import "C"

// setErrno returns the current C.errno value and, if v >= 0, sets the
// CGo errno for a random pthread to value v. If you want some
// consistency, this needs to be called from runtime.LockOSThread()
// code. This function is only defined for testing purposes. The psx.c
// code should properly handle the case that a non-zero errno is saved
// and restored independently of what these Syscall[36]() functions
// observe.
func setErrno(v int) int {
	return int(C.__errno_too(C.long(v)))
}

var makeFatal sync.Once

// forceFatal configures the psx_syscall mechanism to PSX_ERROR.
func forceFatal() {
	makeFatal.Do(func() {
		C.psx_set_sensitivity(C.PSX_ERROR)
	})
}

//go:uintptrescapes

// Syscall3 performs a 3 argument syscall. Syscall3 differs from
// syscall.[Raw]Syscall() insofar as it is simultaneously executed on
// every thread of the combined Go and CGo runtimes. It works
// differently depending on whether CGO_ENABLED is 1 or 0 at compile
// time.
//
// If CGO_ENABLED=1 it uses the libpsx function C.psx_syscall3().
//
// If CGO_ENABLED=0 it redirects to the go1.16+
// syscall.AllThreadsSyscall() function.
func Syscall3(syscallnr, arg1, arg2, arg3 uintptr) (uintptr, uintptr, syscall.Errno) {
	forceFatal()
	// We lock to the OSThread here because we may need errno to
	// be the one for this thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	v := C.psx_syscall3(C.long(syscallnr), C.long(arg1), C.long(arg2), C.long(arg3))
	var errno syscall.Errno
	if v < 0 {
		errno = syscall.Errno(C.__errno_too(-1))
	}
	return uintptr(v), uintptr(v), errno
}

//go:uintptrescapes

// Syscall6 performs a 6 argument syscall on every thread of the
// combined Go and CGo runtimes. Other than the number of syscall
// arguments, its behavior is identical to that of Syscall3() - see
// above for the full documentation.
func Syscall6(syscallnr, arg1, arg2, arg3, arg4, arg5, arg6 uintptr) (uintptr, uintptr, syscall.Errno) {
	forceFatal()
	// We lock to the OSThread here because we may need errno to
	// be the one for this thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	v := C.psx_syscall6(C.long(syscallnr), C.long(arg1), C.long(arg2), C.long(arg3), C.long(arg4), C.long(arg5), C.long(arg6))
	var errno syscall.Errno
	if v < 0 {
		errno = syscall.Errno(C.__errno_too(-1))
	}
	return uintptr(v), uintptr(v), errno
}
