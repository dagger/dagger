package dagql

import (
	"encoding/binary"
	"iter"
)

// storedRefs are the entries a stored record's references name, in their
// order, each as its difference from the one before it in a signed varint: a
// record's references are mostly entries numbered near each other, so most
// take one or two bytes in place of eight.
type storedRefs string

// packStoredRefs returns refs as storedRefs, or "" for none.
func packStoredRefs(refs []uint64) storedRefs {
	if len(refs) == 0 {
		return ""
	}
	packed := make([]byte, 0, len(refs)*2)
	var prev uint64
	for _, ref := range refs {
		packed = binary.AppendVarint(packed, int64(ref-prev))
		prev = ref
	}
	return storedRefs(packed)
}

// all yields the entries, in their order.
func (r storedRefs) all() iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		var prev uint64
		for rest := string(r); rest != ""; {
			delta, n := storedRefsVarint(rest)
			if n <= 0 {
				return
			}
			rest = rest[n:]
			prev += uint64(delta)
			if !yield(prev) {
				return
			}
		}
	}
}

// list returns the entries, in their order, in a slice of the caller's own,
// or nil for none.
func (r storedRefs) list() []uint64 {
	var refs []uint64
	for ref := range r.all() {
		refs = append(refs, ref)
	}
	return refs
}

// storedRefsVarint reads a signed varint from the start of s, as
// binary.Varint reads one from a byte slice.
func storedRefsVarint(s string) (int64, int) {
	var ux uint64
	var shift uint
	for i := 0; i < len(s) && i < binary.MaxVarintLen64; i++ {
		b := s[i]
		if b < 0x80 {
			if i == binary.MaxVarintLen64-1 && b > 1 {
				return 0, -(i + 1)
			}
			ux |= uint64(b) << shift
			x := int64(ux >> 1)
			if ux&1 != 0 {
				x = ^x
			}
			return x, i + 1
		}
		ux |= uint64(b&0x7f) << shift
		shift += 7
	}
	return 0, 0
}
