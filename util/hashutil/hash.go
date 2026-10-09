package hashutil

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"sync"

	"github.com/opencontainers/go-digest"
	"github.com/zeebo/xxh3"
)

const (
	XXH3 digest.Algorithm = "xxh3"
)

const (
	// directWriteThreshold is the input size at or above which WithString and
	// WithBytes write straight into the xxh3 hasher instead of copying the
	// input into the buffer. Below it, batching small inputs into one buffer
	// saves per-Write overhead; above it, that overhead (a flush plus one
	// extra Write call) is negligible next to copying the input, and copying
	// would grow the buffer to the input's size. Recipes can carry
	// multi-megabyte literals that get re-hashed often, so copying them is
	// a real memory cost.
	directWriteThreshold = 8 << 10

	// maxBufferLen bounds how much the buffer accumulates before it is
	// flushed into the xxh3 hasher, so many medium-sized inputs can't grow
	// it unboundedly either.
	maxBufferLen = 32 << 10

	// maxPooledBufCap is the largest buffer capacity returned to bufPool;
	// larger buffers are dropped so the pool doesn't pin big allocations.
	maxPooledBufCap = 64 << 10
)

var bufPool = &sync.Pool{New: func() any {
	b := make([]byte, 0, 128)
	return &b
}}

var hasherPool = &sync.Pool{New: func() any {
	return xxh3.New()
}}

func NewHasher() *Hasher {
	// re-use buffers to save some allocations and work for the go GC
	bufPtr := bufPool.Get().(*[]byte)
	*bufPtr = (*bufPtr)[:0]

	// also re-use xxh3 hashers since the struct has some large arrays (not slices), which
	// are expensive to allocate
	xxh3Hasher := hasherPool.Get().(*xxh3.Hasher)

	return &Hasher{
		bufPtr: bufPtr,
		xxh3:   xxh3Hasher,
	}
}

// Hasher enables efficient hashing of mixed inputs of various types. It's intended for hot
// codepaths where minimizing allocations and overhead is important.
//
// Inputs are separated by null bytes to avoid collisions (e.g. "ab" + "c" vs "a" + "bc").
//
// Small inputs are batched in a buffer that is written to the streaming xxh3
// hasher when it fills up or when the digest is computed; large inputs are
// written to the xxh3 hasher directly. Since xxh3 streaming yields the same
// hash for the same byte sequence however it's split across writes, the
// digest only depends on the sequence of inputs.
//
// NOTE: all the With* methods are mutating Hasher, so it can't "branch" off to compute different
// hashes. It also is not safe to use after Close or DigestAndClose have been called.
type Hasher struct {
	bufPtr *[]byte
	xxh3   *xxh3.Hasher
}

// flush writes the buffered bytes into the xxh3 hasher and empties the buffer.
func (h *Hasher) flush() {
	if len(*h.bufPtr) == 0 {
		return
	}
	_, _ = h.xxh3.Write(*h.bufPtr) // docs say it never errors
	*h.bufPtr = (*h.bufPtr)[:0]
}

// reserve flushes the buffer if appending n more bytes would exceed
// maxBufferLen.
func (h *Hasher) reserve(n int) {
	if len(*h.bufPtr)+n > maxBufferLen {
		h.flush()
	}
}

func (h *Hasher) WithString(s string) *Hasher {
	if len(s) >= directWriteThreshold {
		h.flush()
		_, _ = h.xxh3.WriteString(s) // docs say it never errors
	} else {
		h.reserve(len(s) + 1)
		*h.bufPtr = append(*h.bufPtr, s...)
	}
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) WithBytes(bs ...byte) *Hasher {
	if len(bs) >= directWriteThreshold {
		h.flush()
		_, _ = h.xxh3.Write(bs) // docs say it never errors
	} else {
		h.reserve(len(bs) + 1)
		*h.bufPtr = append(*h.bufPtr, bs...)
	}
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) WithByte(b byte) *Hasher {
	h.reserve(2)
	*h.bufPtr = append(*h.bufPtr, b, 0)
	return h
}

func (h *Hasher) WithInt64(i int64) *Hasher {
	h.reserve(9)
	*h.bufPtr = binary.BigEndian.AppendUint64(*h.bufPtr, uint64(i))
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) WithUint64(i uint64) *Hasher {
	h.reserve(9)
	*h.bufPtr = binary.BigEndian.AppendUint64(*h.bufPtr, i)
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) WithInt32(i int32) *Hasher {
	h.reserve(5)
	*h.bufPtr = binary.BigEndian.AppendUint32(*h.bufPtr, uint32(i))
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) WithFloat64(f float64) *Hasher {
	h.reserve(9)
	*h.bufPtr = binary.BigEndian.AppendUint64(*h.bufPtr, math.Float64bits(f))
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) WithDelim() *Hasher {
	h.reserve(1)
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) Close() {
	if cap(*h.bufPtr) <= maxPooledBufCap {
		bufPool.Put(h.bufPtr)
	}
	h.bufPtr = nil

	h.xxh3.Reset()
	hasherPool.Put(h.xxh3)
	h.xxh3 = nil
}

func (h *Hasher) DigestAndClose() string {
	// format as a hex string; do it the efficient way rather than fmt.Sprintf
	h.flush()
	var hashBuf [8]byte
	binary.BigEndian.PutUint64(hashBuf[:], h.xxh3.Sum64())
	var hexStr [5 + 16]byte // 5 for "xxh3:" + 16 for the hex
	hexStr[0], hexStr[1], hexStr[2], hexStr[3], hexStr[4] = 'x', 'x', 'h', '3', ':'
	hex.Encode(hexStr[5:], hashBuf[:])

	h.Close()
	return string(hexStr[:])
}

// HashStrings returns the xxh3 digest of the concatenation of the input
// strings, separated by null bytes to avoid collisions. It's more convenient
// than NewHasher when all inputs are already strings.
func HashStrings(ins ...string) digest.Digest {
	h := hasherPool.Get().(*xxh3.Hasher)
	for _, in := range ins {
		h.WriteString(in)
		h.Write([]byte{0})
	}

	dgst := digest.NewDigest(XXH3, h)
	h.Reset()
	hasherPool.Put(h)
	return dgst
}
