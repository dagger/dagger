package clientdb

import (
	"encoding/hex"
	"strings"
)

// idTable interns IDs as dense uint32s, in the order they are first seen.
// A canonical ID -- prefix then 16 lowercase hex digits, as OTel renders a
// span ID (prefix "") and dagql an xxh3 digest (prefix "xxh3:") -- is keyed
// by its 8 bytes and rebuilt from them; anything else (a malformed or test
// ID) is kept as written in a side table, so every ID round-trips exactly.
// A string-keyed table holds each ID's string twice over, with headers: the
// bulk of an index whose every row names one.
type idTable struct {
	prefix  string
	hex     map[[8]byte]uint32
	keys    [][8]byte
	foreign map[string]uint32
	names   map[uint32]string // the foreign IDs' names
}

// canonical parses a canonical ID.
func (t *idTable) canonical(id string) (key [8]byte, ok bool) {
	if len(id) != len(t.prefix)+16 || !strings.HasPrefix(id, t.prefix) {
		return key, false
	}
	digits := id[len(t.prefix):]
	for i := range len(digits) {
		c := digits[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return key, false
		}
	}
	_, err := hex.Decode(key[:], []byte(digits))
	return key, err == nil
}

// lookup returns id's interned ID, if it has one.
func (t *idTable) lookup(id string) (uint32, bool) {
	if key, ok := t.canonical(id); ok {
		n, found := t.hex[key]
		return n, found
	}
	n, found := t.foreign[id]
	return n, found
}

// intern returns id's interned ID, interning it if needed.
func (t *idTable) intern(id string) uint32 {
	if key, ok := t.canonical(id); ok {
		if n, found := t.hex[key]; found {
			return n
		}
		if t.hex == nil {
			t.hex = map[[8]byte]uint32{}
		}
		n := uint32(len(t.keys))
		t.hex[key] = n
		t.keys = append(t.keys, key)
		return n
	}
	if n, found := t.foreign[id]; found {
		return n
	}
	if t.foreign == nil {
		t.foreign, t.names = map[string]uint32{}, map[uint32]string{}
	}
	n := uint32(len(t.keys))
	t.foreign[id] = n
	t.names[n] = id
	t.keys = append(t.keys, [8]byte{})
	return n
}

// count is how many IDs are interned.
func (t *idTable) count() int { return len(t.keys) }

// name returns the ID interned as n.
func (t *idTable) name(n uint32) string {
	if name, ok := t.names[n]; ok {
		return name
	}
	buf := make([]byte, len(t.prefix)+16)
	copy(buf, t.prefix)
	hex.Encode(buf[len(t.prefix):], t.keys[n][:])
	return string(buf)
}

// empty reports whether n is the empty ID.
func (t *idTable) empty(n uint32) bool {
	name, ok := t.names[n]
	return ok && name == ""
}

// in reports whether the ID interned as n is in set, without allocating its
// name.
func (t *idTable) in(n uint32, set map[string]bool) bool {
	if name, ok := t.names[n]; ok {
		return set[name]
	}
	var buf [64]byte
	b := append(buf[:0], t.prefix...)
	b = hex.AppendEncode(b, t.keys[n][:])
	return set[string(b)]
}
