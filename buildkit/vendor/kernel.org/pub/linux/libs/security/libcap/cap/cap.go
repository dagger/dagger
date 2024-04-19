// Package cap provides all the Linux Capabilities userspace library API
// bindings in native Go.
//
// Capabilities are a feature of the Linux kernel that allow fine
// grain permissions to perform privileged operations. Privileged
// operations are required to do irregular system level operations
// from code. You can read more about how Capabilities are intended to
// work here:
//
//   https://static.googleusercontent.com/media/research.google.com/en//pubs/archive/33528.pdf
//
// This package supports native Go bindings for all the features
// described in that paper as well as supporting subsequent changes to
// the kernel for other styles of inheritable Capability.
//
// Some simple things you can do with this package are:
//
//   // Read and display the capabilities of the running process
//   c := cap.GetProc()
//   log.Printf("this process has these caps:", c)
//
//   // Drop any privilege a process might have (including for root,
//   // but note root 'owns' a lot of system files so a cap-limited
//   // root can still do considerable damage to a running system).
//   old := cap.GetProc()
//   empty := cap.NewSet()
//   if err := empty.SetProc(); err != nil {
//       log.Fatalf("failed to drop privilege: %q -> %q: %v", old, empty, err)
//   }
//   now := cap.GetProc()
//   if cf, _ := now.Cf(empty); cf != 0 {
//       log.Fatalf("failed to fully drop privilege: have=%q, wanted=%q", now, empty)
//   }
//
// The "cap" package operates with POSIX semantics for security
// state. That is all OS threads are kept in sync at all times. The
// package "kernel.org/pub/linux/libs/security/libcap/psx" is used to
// implement POSIX semantics system calls that manipulate thread state
// uniformly over the whole Go (and any CGo linked) process runtime.
//
// Note, if the Go runtime syscall interface contains the Linux
// variant syscall.AllThreadsSyscall() API (it debuted in go1.16 see
// https://github.com/golang/go/issues/1435 for its history) then the
// "libcap/psx" package will use that to invoke Capability setting
// system calls in pure Go binaries. With such an enhanced Go runtime,
// to force this behavior, use the CGO_ENABLED=0 environment variable.
//
// POSIX semantics are more secure than trying to manage privilege at
// a thread level when those threads share a common memory image as
// they do under Linux: it is trivial to exploit a vulnerability in
// one thread of a process to cause execution on any another
// thread. So, any imbalance in security state, in such cases will
// readily create an opportunity for a privilege escalation
// vulnerability.
//
// POSIX semantics also work well with Go, which deliberately tries to
// insulate the user from worrying about the number of OS threads that
// are actually running in their program. Indeed, Go can efficiently
// launch and manage tens of thousands of concurrent goroutines
// without bogging the program or wider system down. It does this by
// aggressively migrating idle threads to make progress on unblocked
// goroutines. So, inconsistent security state across OS threads can
// also lead to program misbehavior.
//
// The only exception to this process-wide common security state is
// the cap.Launcher related functionality. This briefly locks an OS
// thread to a goroutine in order to launch another executable - the
// robust implementation of this kind of support is quite subtle, so
// please read its documentation carefully, if you find that you need
// it.
//
// See https://sites.google.com/site/fullycapable/ for recent updates,
// some more complete walk-through examples of ways of using
// 'cap.Set's etc and information on how to file bugs.
//
// Copyright (c) 2019-21 Andrew G. Morgan <morgan@kernel.org>
//
// The cap and psx packages are licensed with a (you choose) BSD
// 3-clause or GPL2. See LICENSE file for details.
package cap // import "kernel.org/pub/linux/libs/security/libcap/cap"

import (
	"errors"
	"sort"
	"sync"
	"syscall"
	"unsafe"
)

// Value is the type of a single capability (or permission) bit.
type Value uint

// Flag is the type of one of the three Value dimensions held in a
// Set.  It is also used in the (*IAB).Fill() method for changing the
// Bounding and Ambient Vectors.
type Flag uint

// Effective, Permitted, Inheritable are the three Flags of Values
// held in a Set.
const (
	Effective Flag = iota
	Permitted
	Inheritable
)

// Diff summarizes the result of the (*Set).Cf() function.
type Diff uint

const (
	effectiveDiff   Diff = 1 << Effective
	permittedDiff   Diff = 1 << Permitted
	inheritableDiff Diff = 1 << Inheritable
)

// String identifies a Flag value by its conventional "e", "p" or "i"
// string abbreviation.
func (f Flag) String() string {
	switch f {
	case Effective:
		return "e"
	case Permitted:
		return "p"
	case Inheritable:
		return "i"
	default:
		return "<Error>"
	}
}

// data holds a 32-bit slice of the compressed bitmaps of capability
// sets as understood by the kernel.
type data [Inheritable + 1]uint32

// Set is an opaque capabilities container for a set of system
// capbilities. It holds individually addressable capability Value's
// for the three capability Flag's. See GetFlag() and SetFlag() for
// how to adjust them individually, and Clear() and ClearFlag() for
// how to do bulk operations.
//
// For admin tasks associated with managing namespace specific file
// capabilities, Set can also support a namespace-root-UID value which
// defaults to zero. See GetNSOwner() and SetNSOwner().
type Set struct {
	// mu protects all other members of a Set.
	mu sync.RWMutex

	// flat holds Flag Value bitmaps for all capabilities
	// associated with this Set.
	flat []data

	// Linux specific
	nsRoot int
}

// Various known kernel magic values.
const (
	kv1 = 0x19980330 // First iteration of process capabilities (32 bits).
	kv2 = 0x20071026 // First iteration of process and file capabilities (64 bits) - deprecated.
	kv3 = 0x20080522 // Most recently supported process and file capabilities (64 bits).
)

var (
	// startUp protects setting of the following values: magic,
	// words, maxValues.
	startUp sync.Once

	// magic holds the preferred magic number for the kernel ABI.
	magic uint32

	// words holds the number of uint32's associated with each
	// capability Flag for this session.
	words int

	// maxValues holds the number of bit values that are named by
	// the running kernel. This is generally expected to match
	// ValueCount which is autogenerated at packaging time.
	maxValues uint
)

type header struct {
	magic uint32
	pid   int32
}

// syscaller is a type for abstracting syscalls. The r* variants are
// for reading state, and can be parallelized, the w* variants need to
// be serialized so all OS threads can share state.
type syscaller struct {
	r3 func(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)
	w3 func(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)
	r6 func(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)
	w6 func(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)
}

// caprcall provides a pointer etc wrapper for the system calls
// associated with getcap.
//go:uintptrescapes
func (sc *syscaller) caprcall(call uintptr, h *header, d []data) error {
	x := uintptr(0)
	if d != nil {
		x = uintptr(unsafe.Pointer(&d[0]))
	}
	_, _, err := sc.r3(call, uintptr(unsafe.Pointer(h)), x, 0)
	if err != 0 {
		return err
	}
	return nil
}

// capwcall provides a pointer etc wrapper for the system calls
// associated with setcap.
//go:uintptrescapes
func (sc *syscaller) capwcall(call uintptr, h *header, d []data) error {
	x := uintptr(0)
	if d != nil {
		x = uintptr(unsafe.Pointer(&d[0]))
	}
	_, _, err := sc.w3(call, uintptr(unsafe.Pointer(h)), x, 0)
	if err != 0 {
		return err
	}
	return nil
}

// prctlrcall provides a wrapper for the prctl systemcalls that only
// read kernel state. There is a limited number of arguments needed
// and the caller should use 0 for those not needed.
func (sc *syscaller) prctlrcall(prVal, v1, v2 uintptr) (int, error) {
	r, _, err := sc.r3(syscall.SYS_PRCTL, prVal, v1, v2)
	if err != 0 {
		return int(r), err
	}
	return int(r), nil
}

// prctlrcall6 provides a wrapper for the prctl systemcalls that only
// read kernel state and require 6 arguments - ambient cap API, I'm
// looking at you. There is a limited number of arguments needed and
// the caller should use 0 for those not needed.
func (sc *syscaller) prctlrcall6(prVal, v1, v2, v3, v4, v5 uintptr) (int, error) {
	r, _, err := sc.r6(syscall.SYS_PRCTL, prVal, v1, v2, v3, v4, v5)
	if err != 0 {
		return int(r), err
	}
	return int(r), nil
}

// prctlwcall provides a wrapper for the prctl systemcalls that
// write/modify kernel state. Where available, these will use the
// POSIX semantics fixup system calls. There is a limited number of
// arguments needed and the caller should use 0 for those not needed.
func (sc *syscaller) prctlwcall(prVal, v1, v2 uintptr) (int, error) {
	r, _, err := sc.w3(syscall.SYS_PRCTL, prVal, v1, v2)
	if err != 0 {
		return int(r), err
	}
	return int(r), nil
}

// prctlwcall6 provides a wrapper for the prctl systemcalls that
// write/modify kernel state and require 6 arguments - ambient cap
// API, I'm looking at you. (Where available, these will use the POSIX
// semantics fixup system calls). There is a limited number of
// arguments needed and the caller should use 0 for those not needed.
func (sc *syscaller) prctlwcall6(prVal, v1, v2, v3, v4, v5 uintptr) (int, error) {
	r, _, err := sc.w6(syscall.SYS_PRCTL, prVal, v1, v2, v3, v4, v5)
	if err != 0 {
		return int(r), err
	}
	return int(r), nil
}

// cInit performs the lazy identification of the capability vintage of
// the running system.
func (sc *syscaller) cInit() {
	h := &header{
		magic: kv3,
	}
	sc.caprcall(syscall.SYS_CAPGET, h, nil)
	magic = h.magic
	switch magic {
	case kv1:
		words = 1
	case kv2, kv3:
		words = 2
	default:
		// Fall back to a known good version.
		magic = kv3
		words = 2
	}
	// Use the bounding set to evaluate which capabilities exist.
	maxValues = uint(sort.Search(32*words, func(n int) bool {
		_, err := GetBound(Value(n))
		return err != nil
	}))
	if maxValues == 0 {
		// Fall back to using the largest value defined at build time.
		maxValues = NamedCount
	}
}

// MaxBits returns the number of kernel-named capabilities discovered
// at runtime in the current system.
func MaxBits() Value {
	startUp.Do(multisc.cInit)
	return Value(maxValues)
}

// NewSet returns an empty capability set.
func NewSet() *Set {
	startUp.Do(multisc.cInit)
	return &Set{
		flat: make([]data, words),
	}
}

// ErrBadSet indicates a nil pointer was used for a *Set, or the
// request of the Set is invalid in some way.
var ErrBadSet = errors.New("bad capability set")

// good confirms that c looks valid.
func (c *Set) good() error {
	if c == nil || len(c.flat) == 0 {
		return ErrBadSet
	}
	return nil
}

// Dup returns a copy of the specified capability set.
func (c *Set) Dup() (*Set, error) {
	if err := c.good(); err != nil {
		return nil, err
	}
	n := NewSet()
	c.mu.RLock()
	defer c.mu.RUnlock()
	copy(n.flat, c.flat)
	n.nsRoot = c.nsRoot
	return n, nil
}

// GetPID returns the capability set associated with the target process
// id; pid=0 is an alias for current.
func GetPID(pid int) (*Set, error) {
	v := NewSet()
	if err := multisc.caprcall(syscall.SYS_CAPGET, &header{magic: magic, pid: int32(pid)}, v.flat); err != nil {
		return nil, err
	}
	return v, nil
}

// GetProc returns the capability Set of the current process. If the
// kernel is unable to determine the Set associated with the current
// process, the function panic()s.
func GetProc() *Set {
	c, err := GetPID(0)
	if err != nil {
		panic(err)
	}
	return c
}

// setProc uses syscaller to set process capabilities.  Note, c is
// either private to or (read) locked by the caller.
func (sc *syscaller) setProc(c *Set) error {
	return sc.capwcall(syscall.SYS_CAPSET, &header{magic: magic}, c.flat)
}

// SetProc attempts to set the capability Set of the current
// process. The kernel will perform permission checks and an error
// will be returned if the attempt fails. Should the attempt fail
// no process capabilities will have been modified.
//
// Note, the general behavior of this call is to set the
// process-shared capabilities. However, when called from a callback
// function as part of a (*Launcher).Launch(), the call only sets the
// capabilities of the thread being used to perform the launch.
func (c *Set) SetProc() error {
	if err := c.good(); err != nil {
		return err
	}
	state, sc := scwStateSC()
	defer scwSetState(launchBlocked, state, -1)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return sc.setProc(c)
}

// defines from uapi/linux/prctl.h
const (
	prCapBSetRead = 23
	prCapBSetDrop = 24
)

// GetBound determines if a specific capability is currently part of
// the local bounding set. On systems where the bounding set Value is
// not present, this function returns an error.
func GetBound(val Value) (bool, error) {
	v, err := multisc.prctlrcall(prCapBSetRead, uintptr(val), 0)
	if err != nil {
		return false, err
	}
	return v > 0, nil
}

//go:uintptrescapes
func (sc *syscaller) dropBound(val ...Value) error {
	for _, v := range val {
		if _, err := sc.prctlwcall(prCapBSetDrop, uintptr(v), 0); err != nil {
			return err
		}
	}
	return nil
}

// DropBound attempts to suppress bounding set Values. The kernel will
// never allow a bounding set Value bit to be raised once successfully
// dropped. However, dropping requires the current process is
// sufficiently capable (usually via cap.SETPCAP being raised in the
// Effective flag of the process' Set). Note, the drops are performed
// in order and if one bounding value cannot be dropped, the function
// returns immediately with an error which may leave the system in an
// ill-defined state. The caller can determine where things went wrong
// using GetBound().
func DropBound(val ...Value) error {
	state, sc := scwStateSC()
	defer scwSetState(launchBlocked, state, -1)
	return sc.dropBound(val...)
}

// defines from uapi/linux/prctl.h
const (
	prCapAmbient = 47

	prCapAmbientIsSet    = 1
	prCapAmbientRaise    = 2
	prCapAmbientLower    = 3
	prCapAmbientClearAll = 4
)

// GetAmbient determines if a specific capability is currently part of
// the local ambient set. On systems where the ambient set Value is
// not present, this function returns an error.
func GetAmbient(val Value) (bool, error) {
	r, err := multisc.prctlrcall6(prCapAmbient, prCapAmbientIsSet, uintptr(val), 0, 0, 0)
	return r > 0, err
}

//go:uintptrescapes
func (sc *syscaller) setAmbient(enable bool, val ...Value) error {
	dir := uintptr(prCapAmbientLower)
	if enable {
		dir = prCapAmbientRaise
	}
	for _, v := range val {
		_, err := sc.prctlwcall6(prCapAmbient, dir, uintptr(v), 0, 0, 0)
		if err != nil {
			return err
		}
	}
	return nil
}

// SetAmbient attempts to set a specific Value bit to the state,
// enable. This function will return an error if insufficient
// permission is available to perform this task. The settings are
// performed in order and the function returns immediately an error is
// detected. Use GetAmbient() to unravel where things went
// wrong. Note, the cap package manages an abstraction IAB that
// captures all three inheritable vectors in a single type. Consider
// using that.
func SetAmbient(enable bool, val ...Value) error {
	state, sc := scwStateSC()
	defer scwSetState(launchBlocked, state, -1)
	return sc.setAmbient(enable, val...)
}

func (sc *syscaller) resetAmbient() error {
	var v bool
	var err error

	for c := Value(0); !v; c++ {
		if v, err = GetAmbient(c); err != nil {
			// no non-zero values found.
			return nil
		}
	}
	_, err = sc.prctlwcall6(prCapAmbient, prCapAmbientClearAll, 0, 0, 0, 0)
	return err
}

// ResetAmbient attempts to ensure the Ambient set is fully
// cleared. It works by first reading the set and if it finds any bits
// raised it will attempt a reset. The test before attempting a reset
// behavior is a workaround for situations where the Ambient API is
// locked, but a reset is not actually needed. No Ambient bit not
// already raised in both the Permitted and Inheritable Set is allowed
// to be raised by the kernel.
func ResetAmbient() error {
	state, sc := scwStateSC()
	defer scwSetState(launchBlocked, state, -1)
	return sc.resetAmbient()
}
