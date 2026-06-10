package layercopy

import (
	"os"

	"github.com/containerd/containerd/v2/core/mount"
)

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
