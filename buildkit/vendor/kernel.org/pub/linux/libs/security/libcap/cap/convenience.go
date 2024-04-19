package cap

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// This file contains convenience functions for libcap, to help
// users do the right thing with respect to capabilities for
// common actions.

// Secbits capture the prctl settable secure-bits of a process.
type Secbits uint

// SecbitNoRoot etc are the bitmasks associated with the supported
// Secbit masks.  Source: uapi/linux/securebits.h
const (
	SecbitNoRoot Secbits = 1 << iota
	SecbitNoRootLocked
	SecbitNoSetUIDFixup
	SecbitNoSetUIDFixupLocked
	SecbitKeepCaps
	SecbitKeepCapsLocked
	SecbitNoCapAmbientRaise
	SecbitNoCapAmbientRaiseLocked
)

const (
	securedBasicBits   = SecbitNoRoot | SecbitNoRootLocked | SecbitNoSetUIDFixup | SecbitNoSetUIDFixupLocked | SecbitKeepCapsLocked
	securedAmbientBits = securedBasicBits | SecbitNoCapAmbientRaise | SecbitNoCapAmbientRaiseLocked
)

// defines from uapi/linux/prctl.h
const (
	prGetKeepCaps   = 7
	prSetKeepCaps   = 8
	prGetSecureBits = 27
	prSetSecureBits = 28
	prSetNoNewPrivs = 38
)

// GetSecbits returns the current setting of the process' Secbits.
func GetSecbits() Secbits {
	v, err := multisc.prctlrcall(prGetSecureBits, 0, 0)
	if err != nil {
		panic(err)
	}
	return Secbits(v)
}

func (sc *syscaller) setSecbits(s Secbits) error {
	_, err := sc.prctlwcall(prSetSecureBits, uintptr(s), 0)
	return err
}

// Set attempts to force the process Secbits to a value. This function
// will raise cap.SETPCAP in order to achieve this operation, and will
// completely lower the Effective Flag of the process upon returning.
func (s Secbits) Set() error {
	state, sc := scwStateSC()
	defer scwSetState(launchBlocked, state, -1)
	return sc.setSecbits(s)
}

// Mode summarizes a complicated secure-bits and capability mode in a
// libcap preferred way.
type Mode uint

// ModeUncertain etc are how libcap summarizes security modes
// involving capabilities and secure-bits.
const (
	ModeUncertain Mode = iota
	ModeNoPriv
	ModePure1EInit
	ModePure1E
	ModeHybrid
)

// GetMode assesses the current process state and summarizes it as
// a Mode. This function always succeeds. Unfamiliar modes are
// declared ModeUncertain.
func GetMode() Mode {
	b := GetSecbits()
	if b == 0 {
		return ModeHybrid
	}
	if b&securedBasicBits != securedBasicBits {
		return ModeUncertain
	}

	for c := Value(0); ; c++ {
		v, err := GetAmbient(c)
		if err != nil {
			if c != 0 && b != securedAmbientBits {
				return ModeUncertain
			}
			break
		}
		if v {
			return ModeUncertain
		}
	}

	w := GetProc()
	e := NewSet()
	cf, _ := w.Cf(e)

	if cf.Has(Inheritable) {
		return ModePure1E
	}
	if cf.Has(Permitted) || cf.Has(Effective) {
		return ModePure1EInit
	}

	for c := Value(0); ; c++ {
		v, err := GetBound(c)
		if err != nil {
			break
		}
		if v {
			return ModePure1EInit
		}
	}

	return ModeNoPriv
}

// ErrBadMode is the error returned when an attempt is made to set an
// unrecognized libcap security mode.
var ErrBadMode = errors.New("unsupported mode")

func (sc *syscaller) setMode(m Mode) error {
	w := GetProc()
	defer func() {
		w.ClearFlag(Effective)
		sc.setProc(w)
	}()

	if err := w.SetFlag(Effective, true, SETPCAP); err != nil {
		return err
	}
	if err := sc.setProc(w); err != nil {
		return err
	}

	if m == ModeHybrid {
		return sc.setSecbits(0)
	}

	if m == ModeNoPriv || m == ModePure1EInit {
		w.ClearFlag(Inheritable)
	} else if m != ModePure1E {
		return ErrBadMode
	}

	sb := securedAmbientBits
	if _, err := GetAmbient(0); err != nil {
		sb = securedBasicBits
	} else if err := sc.resetAmbient(); err != nil {
		return err
	}

	if err := sc.setSecbits(sb); err != nil {
		return err
	}

	if m != ModeNoPriv {
		return nil
	}

	for c := Value(0); sc.dropBound(c) == nil; c++ {
	}
	w.ClearFlag(Permitted)

	// For good measure.
	sc.prctlwcall6(prSetNoNewPrivs, 1, 0, 0, 0, 0)

	return nil
}

// Set attempts to enter the specified mode. An attempt is made to
// enter the mode, so if you prefer this operation to be a no-op if
// entering the same mode, call only if CurrentMode() disagrees with
// the desired mode.
//
// This function will raise cap.SETPCAP in order to achieve this
// operation, and will completely lower the Effective Flag of the
// process' Set before returning. This function may fail for lack of
// permission or because (some of) the Secbits are already locked for
// the current process.
func (m Mode) Set() error {
	state, sc := scwStateSC()
	defer scwSetState(launchBlocked, state, -1)
	return sc.setMode(m)
}

// String returns the libcap conventional string for this mode.
func (m Mode) String() string {
	switch m {
	case ModeUncertain:
		return "UNCERTAIN"
	case ModeNoPriv:
		return "NOPRIV"
	case ModePure1EInit:
		return "PURE1E_INIT"
	case ModePure1E:
		return "PURE1E"
	case ModeHybrid:
		return "HYBRID"
	default:
		return "UNKNOWN"
	}
}

func (sc *syscaller) setUID(uid int) error {
	w := GetProc()
	defer func() {
		w.ClearFlag(Effective)
		sc.setProc(w)
	}()

	if err := w.SetFlag(Effective, true, SETUID); err != nil {
		return err
	}

	// these may or may not work depending on whether or not they
	// are locked. We try them just in case.
	sc.prctlwcall(prSetKeepCaps, 1, 0)
	defer sc.prctlwcall(prSetKeepCaps, 0, 0)

	if err := sc.setProc(w); err != nil {
		return err
	}

	if _, _, err := sc.w3(syscall.SYS_SETUID, uintptr(uid), 0, 0); err != 0 {
		return err
	}
	return nil
}

// SetUID is a convenience function for robustly setting the UID and
// all other variants of UID (EUID etc) to the specified value without
// dropping the privilege of the current process. This function will
// raise cap.SETUID in order to achieve this operation, and will
// completely lower the Effective Flag of the process before
// returning. Unlike the traditional method of dropping privilege when
// changing from [E]UID=0 to some other UID, this function only can
// perform any change of UID if cap.SETUID is available, and this
// operation will not alter the Permitted Flag of the process' Set.
func SetUID(uid int) error {
	state, sc := scwStateSC()
	defer scwSetState(launchBlocked, state, -1)
	return sc.setUID(uid)
}

//go:uintptrescapes
func (sc *syscaller) setGroups(gid int, suppl []int) error {
	w := GetProc()
	defer func() {
		w.ClearFlag(Effective)
		sc.setProc(w)
	}()

	if err := w.SetFlag(Effective, true, SETGID); err != nil {
		return err
	}
	if err := sc.setProc(w); err != nil {
		return err
	}

	if _, _, err := sc.w3(syscall.SYS_SETGID, uintptr(gid), 0, 0); err != 0 {
		return err
	}
	if len(suppl) == 0 {
		if _, _, err := sc.w3(sysSetGroupsVariant, 0, 0, 0); err != 0 {
			return err
		}
		return nil
	}

	// On linux gid values are 32-bits.
	gs := make([]uint32, len(suppl))
	for i, g := range suppl {
		gs[i] = uint32(g)
	}
	if _, _, err := sc.w3(sysSetGroupsVariant, uintptr(len(suppl)), uintptr(unsafe.Pointer(&gs[0])), 0); err != 0 {
		return err
	}
	return nil
}

// SetGroups is a convenience function for robustly setting the GID
// and all other variants of GID (EGID etc) to the specified value, as
// well as setting all of the supplementary groups. This function will
// raise cap.SETGID in order to achieve this operation, and will
// completely lower the Effective Flag of the process Set before
// returning.
func SetGroups(gid int, suppl ...int) error {
	state, sc := scwStateSC()
	defer scwSetState(launchBlocked, state, -1)
	return sc.setGroups(gid, suppl)
}

//go:uintptrescapes

// Prctlw is a convenience function for performing a syscall.Prctl()
// call that executes on all the threads of the process. It is called
// Prctlw because it is only appropriate to call this function when it
// is writing thread state that the caller wants to set on all OS
// threads of the process to observe POSIX semantics when Linux
// doesn't natively honor them. (Check prctl documentation for when it
// is appropriate to use this vs. a normal syscall.Prctl() call.)
func Prctlw(prVal uintptr, args ...uintptr) (int, error) {
	if n := len(args); n > 5 {
		return -1, fmt.Errorf("prctl supports up to 5 arguments (not %d)", n)
	}
	state, sc := scwStateSC()
	defer scwSetState(launchBlocked, state, -1)
	as := make([]uintptr, 5)
	copy(as, args)
	return sc.prctlwcall6(prVal, as[0], as[1], as[2], as[3], as[4])
}

//go:uintptrescapes

// Prctl is a convenience function that performs a syscall.Prctl()
// that either reads state using a single OS thread, or performs a
// Prctl that is treated as a process wide setting. It is provided for
// symmetry reasons, but is equivalent to simply calling the
// corresponding syscall function.
func Prctl(prVal uintptr, args ...uintptr) (int, error) {
	if n := len(args); n > 5 {
		return -1, fmt.Errorf("prctl supports up to 5 arguments (not %d)", n)
	}
	as := make([]uintptr, 5)
	copy(as, args)
	return singlesc.prctlrcall6(prVal, as[0], as[1], as[2], as[3], as[4])
}
