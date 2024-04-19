// Package psx provides support for system calls that are run
// simultaneously on all threads under Linux.
//
// This property can be used to work around a historical lack of
// native Go support for such a feature. Something that is the subject
// of:
//
//   https://github.com/golang/go/issues/1435
//
// The package works differently depending on whether or not
// CGO_ENABLED is 0 or 1.
//
// In the former case, psx is a low overhead wrapper for the two
// native go calls: syscall.AllThreadsSyscall() and
// syscall.AllThreadsSyscall6() introduced in go1.16. We provide this
// wrapping to minimize client source code changes when compiling with
// or without CGo enabled.
//
// In the latter case, and toolchains prior to go1.16, it works via
// CGo wrappers for system call functions that call the C [lib]psx
// functions of these names. This ensures that the system calls
// execute simultaneously on all the pthreads of the Go (and CGo)
// combined runtime.
//
// With CGo, the psx support works in the following way: the pthread
// that is first asked to execute the syscall does so, and determines
// if it succeeds or fails. If it fails, it returns immediately
// without attempting the syscall on other pthreads. If the initial
// attempt succeeds, however, then the runtime is stopped in order for
// the same system call to be performed on all the remaining pthreads
// of the runtime. Once all pthreads have completed the syscall, the
// return codes are those obtained by the first pthread's invocation
// of the syscall.
//
// Note, there is no need to use this variant of syscall where the
// syscalls only read state from the kernel. However, since Go's
// runtime freely migrates code execution between pthreads, support of
// this type is required for any successful attempt to fully drop or
// modify the privilege of a running Go program under Linux.
//
// More info on how Linux privilege works and examples of using this
// package can be found here:
//
//    https://sites.google.com/site/fullycapable
//
// WARNING: For older go toolchains (prior to go1.15), correct
// compilation of this package may require an extra workaround step:
//
// The workaround is to build with the following CGO_LDFLAGS_ALLOW in
// effect (here the syntax is that of bash for defining an environment
// variable):
//
//    export CGO_LDFLAGS_ALLOW="-Wl,-?-wrap[=,][^-.@][^,]*"
//
//
// Copyright (c) 2019,20 Andrew G. Morgan <morgan@kernel.org>
//
// The psx package is licensed with a (you choose) BSD 3-clause or
// GPL2. See LICENSE file for details.
package psx // import "kernel.org/pub/linux/libs/security/libcap/psx"
