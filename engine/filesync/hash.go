package filesync

import (
	"encoding/binary"
	"hash"
	"slices"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	fstypes "github.com/tonistiigi/fsutil/types"
	"github.com/zeebo/xxh3"
)

const (
	XXH3 digest.Algorithm = "xxh3"
)

var statHashBufPool = &sync.Pool{
	New: func() any {
		buffer := make([]byte, 0, 64)
		return &buffer
	},
}

func newHashFromStat(stat *fstypes.Stat) hash.Hash {
	h := &statHash{Hash: xxh3.New(), stat: stat}
	h.Reset()
	return h
}

type statHash struct {
	hash.Hash
	stat *fstypes.Stat
}

func (h *statHash) Reset() {
	h.Hash.Reset()

	// this is similar to upstream's NewFromStat func but avoids overhead of creating tar headers
	// https://github.com/dagger/dagger/internal/buildkit/blob/44504feda1ce39bb8578537a6e6a93f90bdf4220/cache/contenthash/filehash.go#L42-L42

	// skip name of file since contenthash includes that on its own
	// skip mtime since all relevant metadata + file contents that impact modtime are included in the hash
	// skip size since it will inherently be included when writing file contents to the hash

	buf := *(statHashBufPool.Get().(*[]byte))
	buf = buf[:0]

	buf = binary.LittleEndian.AppendUint32(buf, h.stat.Mode)
	buf = binary.LittleEndian.AppendUint32(buf, h.stat.Uid)
	buf = binary.LittleEndian.AppendUint32(buf, h.stat.Gid)
	buf = binary.LittleEndian.AppendUint64(buf, uint64(h.stat.Devmajor))
	buf = binary.LittleEndian.AppendUint64(buf, uint64(h.stat.Devminor))
	buf = append(buf, []byte("\x00"+h.stat.Linkname+"\x00")...)

	xattrs := make([]string, 0, len(h.stat.Xattrs))
	for k, v := range h.stat.Xattrs {
		if strings.HasPrefix(k, "system.") {
			continue
		}
		if strings.HasPrefix(k, "security.") && k != "security.capability" {
			continue
		}
		xattrs = append(xattrs, k+string(v))
	}
	slices.Sort(xattrs)
	for _, xattr := range xattrs {
		buf = append(buf, []byte("\x00"+xattr+"\x00")...)
	}

	h.Write(buf)
	statHashBufPool.Put(&buf)
}
