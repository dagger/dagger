package cap

import (
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
	"sync"
)

// omask returns the offset and mask for a specific capability.
func omask(c Value) (uint, uint32) {
	u := uint(c)
	return u >> 5, uint32(1) << (u & 31)
}

// IAB holds a summary of all of the inheritable capability vectors:
// Inh, Amb and Bound. The Bound vector is the logical inverse (two's
// complement) of the process' Bounding set. That is, raising a Value
// in the Bound (think blocked) vector is equivalent to dropping that
// Value from the process' Bounding set. This convention is used to
// support the empty IAB as being mostly harmless.
type IAB struct {
	mu       sync.RWMutex
	a, i, nb []uint32
}

// Vector enumerates which of the inheritable IAB capability vectors
// is being manipulated.
type Vector uint

// Inh, Amb, Bound enumerate the IAB vector components. (Vector) Inh
// is equivalent to (Flag) Inheritable. They are named differently for
// syntax/type checking reasons.
const (
	Inh Vector = iota
	Amb
	Bound
)

// IABDiff holds the non-error result of an (*IAB).Cf()
// function call. It can be interpreted with the function
// (IABDiff).Has().
type IABDiff uint

// iBits, iBits and bBits track the (semi-)independent parts of an
// IABDiff.
const (
	iBits IABDiff = 1 << Inh
	aBits IABDiff = 1 << Amb
	bBits IABDiff = 1 << Bound
)

// Has determines if an IAB comparison differs in a specific vector.
func (d IABDiff) Has(v Vector) bool {
	return d&(1<<v) != 0
}

// String identifies a Vector value by its conventional I A or B
// string abbreviation.
func (v Vector) String() string {
	switch v {
	case Inh:
		return "I"
	case Amb:
		return "A"
	case Bound:
		return "B"
	default:
		return "<Error>"
	}
}

// NewIAB returns an empty IAB.
func NewIAB() *IAB {
	startUp.Do(multisc.cInit)
	return &IAB{
		i:  make([]uint32, words),
		a:  make([]uint32, words),
		nb: make([]uint32, words),
	}
}

// good confirms the iab looks to be initialized.
func (iab *IAB) good() error {
	if iab == nil || len(iab.i) == 0 || len(iab.i) != words || len(iab.a) != words || len(iab.nb) != words {
		return ErrBadValue
	}
	return nil
}

// Dup returns a duplicate copy of the IAB.
func (iab *IAB) Dup() (*IAB, error) {
	if err := iab.good(); err != nil {
		return nil, err
	}
	v := NewIAB()
	iab.mu.RLock()
	defer iab.mu.RUnlock()
	copy(v.i, iab.i)
	copy(v.a, iab.a)
	copy(v.nb, iab.nb)
	return v, nil
}

// IABInit allocates a new IAB tuple.
//
// Deprecated: Replace with NewIAB.
//
// Example, replace this:
//
//    iab := IABInit()
//
// with this:
//
//    iab := NewIAB()
func IABInit() *IAB {
	return NewIAB()
}

// IABGetProc summarizes the Inh, Amb and Bound capability vectors of
// the current process.
func IABGetProc() *IAB {
	iab := NewIAB()
	current := GetProc()
	iab.Fill(Inh, current, Inheritable)
	for c := MaxBits(); c > 0; {
		c--
		offset, mask := omask(c)
		if a, _ := GetAmbient(c); a {
			iab.a[offset] |= mask
		}
		if b, err := GetBound(c); err == nil && !b {
			iab.nb[offset] |= mask
		}
	}
	return iab
}

// IABFromText parses a string representing an IAB, as generated
// by IAB.String(), to generate an IAB.
func IABFromText(text string) (*IAB, error) {
	iab := NewIAB()
	if len(text) == 0 {
		return iab, nil
	}
	for _, f := range strings.Split(text, ",") {
		var i, a, nb bool
		var j int
		for j = 0; j < len(f); j++ {
			switch f[j : j+1] {
			case "!":
				nb = true
			case "^":
				i = true
				a = true
			case "%":
				i = true
			default:
				goto done
			}
		}
	done:
		c, err := FromName(f[j:])
		if err != nil {
			return nil, err
		}
		offset, mask := omask(c)
		if i || !nb {
			iab.i[offset] |= mask
		}
		if a {
			iab.a[offset] |= mask
		}
		if nb {
			iab.nb[offset] |= mask
		}
	}
	return iab, nil
}

// String serializes an IAB to a string format.
func (iab *IAB) String() string {
	if err := iab.good(); err != nil {
		return "<invalid>"
	}
	var vs []string
	iab.mu.RLock()
	defer iab.mu.RUnlock()
	for c := Value(0); c < Value(maxValues); c++ {
		offset, mask := omask(c)
		i := (iab.i[offset] & mask) != 0
		a := (iab.a[offset] & mask) != 0
		nb := (iab.nb[offset] & mask) != 0
		var cs []string
		if nb {
			cs = append(cs, "!")
		}
		if a {
			cs = append(cs, "^")
		} else if nb && i {
			cs = append(cs, "%")
		}
		if nb || a || i {
			vs = append(vs, strings.Join(cs, "")+c.String())
		}
	}
	return strings.Join(vs, ",")
}

// iabSetProc uses a syscaller to apply an IAB tuple to the process.
// The iab is known to be locked by the caller.
func (sc *syscaller) iabSetProc(iab *IAB) (err error) {
	temp := GetProc()
	var raising uint32
	for i := 0; i < words; i++ {
		newI := iab.i[i]
		oldIP := temp.flat[i][Inheritable] | temp.flat[i][Permitted]
		raising |= (newI & ^oldIP) | iab.a[i] | iab.nb[i]
		temp.flat[i][Inheritable] = newI
	}
	working, err2 := temp.Dup()
	if err2 != nil {
		err = err2
		return
	}
	if raising != 0 {
		if err = working.SetFlag(Effective, true, SETPCAP); err != nil {
			return
		}
		if err = sc.setProc(working); err != nil {
			return
		}
	}
	defer func() {
		if err2 := sc.setProc(temp); err == nil {
			err = err2
		}
	}()
	if err = sc.resetAmbient(); err != nil {
		return
	}
	for c := Value(maxValues); c > 0; {
		c--
		offset, mask := omask(c)
		if iab.a[offset]&mask != 0 {
			err = sc.setAmbient(true, c)
		}
		if err == nil && iab.nb[offset]&mask != 0 {
			err = sc.dropBound(c)
		}
		if err != nil {
			return
		}
	}
	return
}

// SetProc attempts to change the Inheritable, Ambient and Bounding
// capability vectors of the current process using the content,
// iab. The Bounding vector strongly affects the potential for setting
// other bits, so this function carefully performs the combined
// operation in the most flexible manner.
func (iab *IAB) SetProc() error {
	if err := iab.good(); err != nil {
		return err
	}
	state, sc := scwStateSC()
	defer scwSetState(launchBlocked, state, -1)
	iab.mu.RLock()
	defer iab.mu.RUnlock()
	return sc.iabSetProc(iab)
}

// GetVector returns the raised state of the specific capability bit
// of the indicated vector.
func (iab *IAB) GetVector(vec Vector, val Value) (bool, error) {
	if err := iab.good(); err != nil {
		return false, err
	}
	if val >= MaxBits() {
		return false, ErrBadValue
	}
	iab.mu.RLock()
	defer iab.mu.RUnlock()
	offset, mask := omask(val)
	switch vec {
	case Inh:
		return (iab.i[offset] & mask) != 0, nil
	case Amb:
		return (iab.a[offset] & mask) != 0, nil
	case Bound:
		return (iab.nb[offset] & mask) != 0, nil
	default:
		return false, ErrBadValue
	}
}

// SetVector sets all of the vals in the specified vector to the
// raised value.  Note, the Ambient vector cannot contain values not raised
// in the Inh vector, so setting values directly in one vector may have
// the side effect of mirroring the value in the other vector to
// maintain this constraint. Note, raising a Bound vector bit is
// equivalent to lowering the Bounding vector of the process (when
// successfully applied with (*IAB).SetProc()).
func (iab *IAB) SetVector(vec Vector, raised bool, vals ...Value) error {
	if err := iab.good(); err != nil {
		return err
	}
	iab.mu.Lock()
	defer iab.mu.Unlock()
	for _, val := range vals {
		if val >= Value(maxValues) {
			return ErrBadValue
		}
		offset, mask := omask(val)
		switch vec {
		case Inh:
			if raised {
				iab.i[offset] |= mask
			} else {
				iab.i[offset] &= ^mask
				iab.a[offset] &= ^mask
			}
		case Amb:
			if raised {
				iab.a[offset] |= mask
				iab.i[offset] |= mask
			} else {
				iab.a[offset] &= ^mask
			}
		case Bound:
			if raised {
				iab.nb[offset] |= mask
			} else {
				iab.nb[offset] &= ^mask
			}
		default:
			return ErrBadValue
		}
	}
	return nil
}

// Fill fills one of the Inh, Amb and Bound capability vectors from
// one of the flag vectors of a Set.  Note, filling the Inh vector
// will mask the Amb vector, and filling the Amb vector may raise
// entries in the Inh vector. Further, when filling the Bound vector,
// the bits are inverted from what you might expect - that is lowered
// bits from the Set will be raised in the Bound vector.
func (iab *IAB) Fill(vec Vector, c *Set, flag Flag) error {
	if err := iab.good(); err != nil {
		return err
	}
	// work with a copy to avoid potential deadlock.
	s, err := c.Dup()
	if err != nil {
		return err
	}
	iab.mu.Lock()
	defer iab.mu.Unlock()
	for i := 0; i < words; i++ {
		flat := s.flat[i][flag]
		switch vec {
		case Inh:
			iab.i[i] = flat
			iab.a[i] &= flat
		case Amb:
			iab.a[i] = flat
			iab.i[i] |= flat
		case Bound:
			iab.nb[i] = ^flat
		default:
			return ErrBadSet
		}
	}
	return nil
}

// Cf compares two IAB values. Its return value is 0 if the compared
// tuples are considered identical. The macroscopic differences can be
// investigated with (IABDiff).Has().
func (iab *IAB) Cf(alt *IAB) (IABDiff, error) {
	if err := iab.good(); err != nil {
		return 0, err
	}
	if iab == alt {
		return 0, nil
	}
	// Avoid holding two locks at once.
	ref, err := alt.Dup()
	if err != nil {
		return 0, err
	}
	iab.mu.RLock()
	defer iab.mu.RUnlock()

	var cf IABDiff
	for i := 0; i < words; i++ {
		if iab.i[i] != ref.i[i] {
			cf |= iBits
		}
		if iab.a[i] != ref.a[i] {
			cf |= aBits
		}
		if iab.nb[i] != ref.nb[i] {
			cf |= bBits
		}
	}
	return cf, nil
}

// parseHex converts the /proc/*/status string into an array of
// uint32s suitable for storage in an IAB structure.
func parseHex(hex string, invert bool) []uint32 {
	if len(hex) != 8*words {
		// Invalid string
		return nil
	}
	var result []uint32
	for i := 0; i < words; i++ {
		upper := 8 * (words - i)
		raw, err := strconv.ParseUint(hex[upper-8:upper], 16, 32)
		if err != nil {
			return nil
		}
		if invert {
			raw = ^raw
		}
		bits := allMask(uint(i)) & uint32(raw)
		result = append(result, bits)
	}
	return result
}

var procRoot = "/proc"

// ProcRoot sets the local mount point for the Linux /proc filesystem.
// It defaults to "/proc", but might be mounted elsewhere on any given
// system. The function returns the previous value of the local mount
// point. If the user attempts to set it to "", the value is left
// unchanged.
func ProcRoot(path string) string {
	was := procRoot
	if path != "" {
		procRoot = path
	}
	return was
}

// IABGetPID returns the IAB tuple of a specified process. The kernel
// ABI does not support this query via system calls, so the function
// works by parsing the /proc/<pid>/status file content.
func IABGetPID(pid int) (*IAB, error) {
	tf := fmt.Sprintf("%s/%d/status", procRoot, pid)
	d, err := ioutil.ReadFile(tf)
	if err != nil {
		return nil, err
	}
	iab := &IAB{}
	for _, line := range strings.Split(string(d), "\n") {
		if !strings.HasPrefix(line, "Cap") {
			continue
		}
		flavor := line[3:]
		if strings.HasPrefix(flavor, "Inh:\t") {
			iab.i = parseHex(line[8:], false)
			continue
		}
		if strings.HasPrefix(flavor, "Bnd:\t") {
			iab.nb = parseHex(line[8:], true)
			continue
		}
		if strings.HasPrefix(flavor, "Amb:\t") {
			iab.a = parseHex(line[8:], false)
			continue
		}
	}
	if len(iab.i) != words || len(iab.a) != words || len(iab.nb) != words {
		return nil, ErrBadValue
	}
	return iab, nil
}
