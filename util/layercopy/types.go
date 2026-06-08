package layercopy

import (
	"os"
	"path/filepath"
	"strings"

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
	XAttrErrorHandler XAttrErrorHandler
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
