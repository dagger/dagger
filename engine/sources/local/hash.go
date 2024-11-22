package local

import (
	"archive/tar"
	"hash"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/opencontainers/go-digest"
	fstypes "github.com/tonistiigi/fsutil/types"
	"github.com/zeebo/xxh3"
)

const (
	XXH3 digest.Algorithm = "xxh3"
)

func NewFromStat(stat *fstypes.Stat) (hash.Hash, error) {
	// Clear the socket bit since archive/tar.FileInfoHeader does not handle it
	stat.Mode &^= uint32(os.ModeSocket)

	fi := &StatInfo{stat}
	hdr, err := tar.FileInfoHeader(fi, stat.Linkname)
	if err != nil {
		return nil, err
	}
	hdr.Name = "" // note: empty name is different from current has in docker build. Name is added on recursive directory scan instead
	hdr.Devmajor = stat.Devmajor
	hdr.Devminor = stat.Devminor
	hdr.Uid = int(stat.Uid)
	hdr.Gid = int(stat.Gid)

	if len(stat.Xattrs) > 0 {
		hdr.PAXRecords = make(map[string]string, len(stat.Xattrs))
		for k, v := range stat.Xattrs {
			hdr.PAXRecords["SCHILY.xattr."+k] = string(v)
		}
	}
	tsh := &tarsumHash{hdr: hdr, Hash: xxh3.New()}
	tsh.Reset() // initialize header
	return tsh, nil
}

type tarsumHash struct {
	hash.Hash
	hdr *tar.Header
}

// Reset resets the Hash to its initial state.
func (tsh *tarsumHash) Reset() {
	// comply with hash.Hash and reset to the state hash had before any writes
	tsh.Hash.Reset()
	WriteV1TarsumHeaders(tsh.hdr, tsh.Hash)
}

// WriteV1TarsumHeaders writes a tar header to a writer in V1 tarsum format.
func WriteV1TarsumHeaders(h *tar.Header, w io.Writer) {
	for _, elem := range v1TarHeaderSelect(h) {
		w.Write([]byte(elem[0] + elem[1]))
	}
}

// Functions below are from docker legacy tarsum implementation.
// There is no valid technical reason to continue using them.

func v0TarHeaderSelect(h *tar.Header) (orderedHeaders [][2]string) {
	return [][2]string{
		{"name", h.Name},
		{"mode", strconv.FormatInt(h.Mode, 10)},
		{"uid", strconv.Itoa(h.Uid)},
		{"gid", strconv.Itoa(h.Gid)},
		{"size", strconv.FormatInt(h.Size, 10)},
		{"mtime", strconv.FormatInt(h.ModTime.UTC().Unix(), 10)},
		{"typeflag", string([]byte{h.Typeflag})},
		{"linkname", h.Linkname},
		{"uname", h.Uname},
		{"gname", h.Gname},
		{"devmajor", strconv.FormatInt(h.Devmajor, 10)},
		{"devminor", strconv.FormatInt(h.Devminor, 10)},
	}
}

func v1TarHeaderSelect(h *tar.Header) (orderedHeaders [][2]string) {
	pax := h.PAXRecords
	if len(h.Xattrs) > 0 { //nolint:staticcheck // field deprecated in stdlib
		if pax == nil {
			pax = map[string]string{}
			for k, v := range h.Xattrs { //nolint:staticcheck // field deprecated in stdlib
				pax["SCHILY.xattr."+k] = v
			}
		}
	}

	// Get extended attributes.
	xAttrKeys := make([]string, 0, len(h.PAXRecords))
	for k := range pax {
		if strings.HasPrefix(k, "SCHILY.xattr.") {
			k = strings.TrimPrefix(k, "SCHILY.xattr.")
			if k == "security.capability" || !strings.HasPrefix(k, "security.") && !strings.HasPrefix(k, "system.") {
				xAttrKeys = append(xAttrKeys, k)
			}
		}
	}
	sort.Strings(xAttrKeys)

	// Make the slice with enough capacity to hold the 11 basic headers
	// we want from the v0 selector plus however many xattrs we have.
	orderedHeaders = make([][2]string, 0, 11+len(xAttrKeys))

	// Copy all headers from v0 excluding the 'mtime' header (the 5th element).
	v0headers := v0TarHeaderSelect(h)
	orderedHeaders = append(orderedHeaders, v0headers[0:5]...)
	orderedHeaders = append(orderedHeaders, v0headers[6:]...)

	// Finally, append the sorted xattrs.
	for _, k := range xAttrKeys {
		orderedHeaders = append(orderedHeaders, [2]string{k, h.PAXRecords["SCHILY.xattr."+k]})
	}

	return
}
