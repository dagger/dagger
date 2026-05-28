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

type Filter struct {
	Include   []string
	Exclude   []string
	Gitignore bool
}

type CopyOptions struct {
	Filter            Filter
	Chown             *Ownership
	Mode              *os.FileMode
	CopyDirContents   bool
	ReplaceExisting   bool
	DestPathHintIsDir bool
}

type Copier struct {
	dest *destination
}
