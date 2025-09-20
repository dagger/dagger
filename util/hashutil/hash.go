package hashutil

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"sync"

	"github.com/zeebo/xxh3"
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

type Hasher struct {
	bufPtr *[]byte
	xxh3   *xxh3.Hasher
}

func (h *Hasher) WithString(s string) *Hasher {
	*h.bufPtr = append(*h.bufPtr, s...)
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) WithBytes(bs ...byte) *Hasher {
	*h.bufPtr = append(*h.bufPtr, bs...)
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) WithByte(b byte) *Hasher {
	*h.bufPtr = append(*h.bufPtr, b, 0)
	return h
}

func (h *Hasher) WithInt64(i int64) *Hasher {
	*h.bufPtr = binary.BigEndian.AppendUint64(*h.bufPtr, uint64(i))
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) WithInt32(i int32) *Hasher {
	*h.bufPtr = binary.BigEndian.AppendUint32(*h.bufPtr, uint32(i))
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) WithFloat64(f float64) *Hasher {
	*h.bufPtr = binary.BigEndian.AppendUint64(*h.bufPtr, math.Float64bits(f))
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) WithDelim() *Hasher {
	*h.bufPtr = append(*h.bufPtr, 0)
	return h
}

func (h *Hasher) Close() {
	bufPool.Put(h.bufPtr)
	h.bufPtr = nil

	h.xxh3.Reset()
	hasherPool.Put(h.xxh3)
	h.xxh3 = nil
}

func (h *Hasher) DigestAndClose() string {
	// format as a hex string; do it the efficient way rather than fmt.Sprintf
	_, _ = h.xxh3.Write(*h.bufPtr) // docs say it never errors
	hashBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(hashBuf, h.xxh3.Sum64())
	hexStr := make([]byte, 5+16) // 5 for "xxh3:" + 16 for the hex
	hexStr[0], hexStr[1], hexStr[2], hexStr[3], hexStr[4] = 'x', 'x', 'h', '3', ':'
	hex.Encode(hexStr[5:], hashBuf)

	h.Close()
	return string(hexStr)
}
