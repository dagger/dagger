// +build linux,!cgo
// +build go1.16

package psx // import "kernel.org/pub/linux/libs/security/libcap/psx"

import "syscall"

// Documentation for these functions are provided in the psx_cgo.go
// file.

//go:uintptrescapes

// Syscall3 performs a 3 argument syscall.  Syscall3 differs from
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
	return syscall.AllThreadsSyscall(syscallnr, arg1, arg2, arg3)
}

//go:uintptrescapes

// Syscall6 performs a 6 argument syscall on every thread of the
// combined Go and CGo runtimes. Other than the number of syscall
// arguments, its behavior is identical to that of Syscall3() - see
// above for the full documentation.
func Syscall6(syscallnr, arg1, arg2, arg3, arg4, arg5, arg6 uintptr) (uintptr, uintptr, syscall.Errno) {
	return syscall.AllThreadsSyscall6(syscallnr, arg1, arg2, arg3, arg4, arg5, arg6)
}
