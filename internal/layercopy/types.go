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
	CopyDirContents   bool
	ReplaceExisting   bool
	DestPathHintIsDir bool
	DisableHardlinks  bool
}

type Copier struct {
	dest *destination
}
