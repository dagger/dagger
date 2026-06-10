package layercopy

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
)

// cleanRel normalizes a path into a slash-relative form: it cleans the path
// and strips a leading separator so it can be used as a map key shared between
// the matcher, source, and destination implementations.
func cleanRel(p string) string {
	p = filepath.Clean(p)
	if p == "." || p == string(filepath.Separator) {
		return ""
	}
	return strings.TrimPrefix(p, string(filepath.Separator))
}

type Mount struct {
	Root  string
	Mount *mount.Mount
}

type Ownership struct {
	UID int
	GID int
}

type XAttrErrorHandler func(dst, src, xattrKey string, err error) error

type Filter struct {
	Only      map[string]struct{}
	Include   []string
	Exclude   []string
	Gitignore bool
}

type CopyOptions struct {
	Filter            Filter
	Chown             *Ownership
	Mode              *os.FileMode
	Stats             *CopyStats
	XAttrErrorHandler XAttrErrorHandler
	DisableXAttrs     bool
	CopyDirContents   bool
	ReplaceExisting   bool
	DestPathHintIsDir bool

	// DisableHardlinks disables all hardlink creation, including hardlink
	// preservation between entries created by this copy.
	DisableHardlinks bool

	// DisableSourceHardlinks disables hardlinking from source paths into the
	// destination while still preserving hardlinks within this copy.
	DisableSourceHardlinks bool
}

type Copier struct {
	dest *destination
}

type CopyStats struct {
	EntriesVisited int64
	Included       int64
	Skipped        int64

	Dirs         int64
	RegularFiles int64
	Symlinks     int64
	SpecialFiles int64

	ReadDirCalls        int64
	ReadDirDuration     time.Duration
	EnsureDirCalls      int64
	EnsureDirDuration   time.Duration
	CreatedDirs         int64
	MaterializedDirs    int64
	RemoveCalls         int64
	RemoveDuration      time.Duration
	ContentCopyCalls    int64
	ContentCopyDuration time.Duration
	BytesCopied         int64
	MetadataCalls       int64
	MetadataDuration    time.Duration
	XAttrListCalls      int64
	XAttrGetCalls       int64
	XAttrSetCalls       int64
	XAttrDuration       time.Duration
}
