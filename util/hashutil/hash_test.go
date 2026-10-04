package hashutil

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zeebo/xxh3"
)

// hasherOp applies one input to a Hasher and appends the same bytes the
// Hasher is expected to hash to a reference stream.
type hasherOp struct {
	name  string
	apply func(*Hasher)
	bytes func([]byte) []byte
}

func opString(s string) hasherOp {
	return hasherOp{
		name:  fmt.Sprintf("WithString(len=%d)", len(s)),
		apply: func(h *Hasher) { h.WithString(s) },
		bytes: func(b []byte) []byte { return append(append(b, s...), 0) },
	}
}

func opBytes(bs []byte) hasherOp {
	return hasherOp{
		name:  fmt.Sprintf("WithBytes(len=%d)", len(bs)),
		apply: func(h *Hasher) { h.WithBytes(bs...) },
		bytes: func(b []byte) []byte { return append(append(b, bs...), 0) },
	}
}

func opByte(c byte) hasherOp {
	return hasherOp{
		name:  "WithByte",
		apply: func(h *Hasher) { h.WithByte(c) },
		bytes: func(b []byte) []byte { return append(b, c, 0) },
	}
}

func opInt64(i int64) hasherOp {
	return hasherOp{
		name:  "WithInt64",
		apply: func(h *Hasher) { h.WithInt64(i) },
		bytes: func(b []byte) []byte { return append(binary.BigEndian.AppendUint64(b, uint64(i)), 0) },
	}
}

func opUint64(i uint64) hasherOp {
	return hasherOp{
		name:  "WithUint64",
		apply: func(h *Hasher) { h.WithUint64(i) },
		bytes: func(b []byte) []byte { return append(binary.BigEndian.AppendUint64(b, i), 0) },
	}
}

func opInt32(i int32) hasherOp {
	return hasherOp{
		name:  "WithInt32",
		apply: func(h *Hasher) { h.WithInt32(i) },
		bytes: func(b []byte) []byte { return append(binary.BigEndian.AppendUint32(b, uint32(i)), 0) },
	}
}

func opFloat64(f float64) hasherOp {
	return hasherOp{
		name:  "WithFloat64",
		apply: func(h *Hasher) { h.WithFloat64(f) },
		bytes: func(b []byte) []byte { return append(binary.BigEndian.AppendUint64(b, math.Float64bits(f)), 0) },
	}
}

func opDelim() hasherOp {
	return hasherOp{
		name:  "WithDelim",
		apply: func(h *Hasher) { h.WithDelim() },
		bytes: func(b []byte) []byte { return append(b, 0) },
	}
}

// referenceDigest hashes the whole expected byte stream in one shot, which is
// what Hasher did before it started streaming large inputs.
func referenceDigest(stream []byte) string {
	var sum [8]byte
	binary.BigEndian.PutUint64(sum[:], xxh3.Hash(stream))
	return "xxh3:" + hex.EncodeToString(sum[:])
}

func runOps(ops []hasherOp) (got, want string) {
	h := NewHasher()
	var stream []byte
	for _, op := range ops {
		op.apply(h)
		stream = op.bytes(stream)
	}
	return h.DigestAndClose(), referenceDigest(stream)
}

func opNames(ops []hasherOp) string {
	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = op.name
	}
	return strings.Join(names, ", ")
}

func payload(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	_, _ = rng.Read(b)
	return b
}

func TestHasherDigestEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	sizes := []int{
		0, 1, 7, 128,
		directWriteThreshold - 1, directWriteThreshold, directWriteThreshold + 1,
		maxBufferLen - 1, maxBufferLen, maxBufferLen + 1,
		maxPooledBufCap + 1,
		1 << 20,
	}

	small := func() []hasherOp {
		return []hasherOp{
			opString("hello"),
			opInt64(-42),
			opUint64(math.MaxUint64),
			opInt32(7),
			opFloat64(3.14),
			opByte('x'),
			opDelim(),
			opBytes([]byte("world")),
		}
	}

	var cases [][]hasherOp
	cases = append(cases, nil, small())
	for _, n := range sizes {
		s := string(payload(rng, n))
		bs := payload(rng, n)
		for _, big := range []hasherOp{opString(s), opBytes(bs)} {
			// alone
			cases = append(cases, []hasherOp{big})
			// large input first
			cases = append(cases, append([]hasherOp{big}, small()...))
			// large input last
			cases = append(cases, append(small(), big))
			// large input in the middle
			mid := append(small(), big)
			cases = append(cases, append(mid, small()...))
			// back to back
			cases = append(cases, []hasherOp{big, big, opDelim(), big})
		}
	}

	// sequences of mixed sizes, enough to fill the buffer several times over
	for range 50 {
		var ops []hasherOp
		for range 1 + rng.Intn(200) {
			switch rng.Intn(10) {
			case 0:
				ops = append(ops, opString(string(payload(rng, sizes[rng.Intn(len(sizes))]))))
			case 1:
				ops = append(ops, opBytes(payload(rng, sizes[rng.Intn(len(sizes))])))
			case 2:
				ops = append(ops, opString(string(payload(rng, rng.Intn(2*directWriteThreshold)))))
			case 3:
				ops = append(ops, opBytes(payload(rng, rng.Intn(2*directWriteThreshold))))
			case 4:
				ops = append(ops, opInt64(rng.Int63()))
			case 5:
				ops = append(ops, opUint64(rng.Uint64()))
			case 6:
				ops = append(ops, opInt32(rng.Int31()))
			case 7:
				ops = append(ops, opFloat64(rng.Float64()))
			case 8:
				ops = append(ops, opByte(byte(rng.Intn(256))))
			case 9:
				ops = append(ops, opDelim())
			}
		}
		cases = append(cases, ops)
	}

	for i, ops := range cases {
		got, want := runOps(ops)
		require.Equal(t, want, got, "case %d: %s", i, opNames(ops))
	}
}

func TestHasherKnownDigests(t *testing.T) {
	// Pinned digests: these are persisted cache keys, so they must never
	// change. The values were produced by the buffer-everything
	// implementation that predates streaming large inputs.
	require.Equal(t, "xxh3:2d06800538d394c2", NewHasher().DigestAndClose())
	require.Equal(t, "xxh3:ae2f7592407a19a0",
		NewHasher().WithString("a").WithString("bc").DigestAndClose())
	big := strings.Repeat("x", 1<<20)
	require.Equal(t, "xxh3:ba0beb9d9e24f819",
		NewHasher().WithString("a").WithString(big).WithDelim().DigestAndClose())
	require.Equal(t, "xxh3:8d6bc6ccb968f3ed",
		NewHasher().
			WithBytes([]byte(big)...).
			WithInt64(-1).
			WithString(big[:directWriteThreshold-1]).
			WithString(big[:directWriteThreshold]).
			DigestAndClose())
}

func TestHasherDropsOversizedBuffers(t *testing.T) {
	h := NewHasher()
	bufPtr := h.bufPtr
	*bufPtr = make([]byte, 0, maxPooledBufCap+1)
	h.Close()
	for range 100 {
		got := bufPool.Get().(*[]byte)
		require.NotSame(t, bufPtr, got)
		require.LessOrEqual(t, cap(*got), maxPooledBufCap)
	}
}

func TestHasherBufferStaysBounded(t *testing.T) {
	h := NewHasher()
	defer h.Close()
	medium := strings.Repeat("m", directWriteThreshold-1)
	for range 100 {
		h.WithString(medium).WithInt64(1)
		require.LessOrEqual(t, len(*h.bufPtr), maxBufferLen)
	}
	h.WithString(strings.Repeat("l", 1<<20))
	require.LessOrEqual(t, cap(*h.bufPtr), maxPooledBufCap)
}

func BenchmarkHasher(b *testing.B) {
	for _, size := range []int{16, 1 << 10, 64 << 10, 1 << 20} {
		s := strings.Repeat("x", size)
		// WarmPool reuses pooled buffers across iterations.
		b.Run(fmt.Sprintf("WarmPool/%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for range b.N {
				NewHasher().WithString("Query").WithString(s).WithInt64(1).DigestAndClose()
			}
		})
		// ColdPool starts every iteration with an empty buffer pool, as after
		// a GC cycle clears it or when many hashers run concurrently. This
		// shows what a buffered input costs; the fresh pool itself accounts
		// for a few KiB per op.
		b.Run(fmt.Sprintf("ColdPool/%d", size), func(b *testing.B) {
			orig := bufPool
			defer func() { bufPool = orig }()
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for range b.N {
				bufPool = &sync.Pool{New: orig.New}
				NewHasher().WithString("Query").WithString(s).WithInt64(1).DigestAndClose()
			}
		})
	}
}
