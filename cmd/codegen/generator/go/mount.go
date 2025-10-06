package gogenerator

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
)

// MountedFS takes a target FS and mounts it at Name
type MountedFS struct {
	FS   fs.FS
	Name string
}

func (fs *MountedFS) Open(name string) (fs.File, error) {
	name = filepath.Clean(name)
	if name == "." {
		return &staticFile{name: fs.Name}, nil
	}

	name, ok := strings.CutPrefix(name, fs.Name)
	if !ok {
		return nil, os.ErrNotExist
	}
	name = filepath.Clean(strings.TrimPrefix(name, "/"))
	return fs.FS.Open(name)
}

var _ fs.FS = &MountedFS{}

type staticFile struct {
	name string
}

// impls for fs.File

func (f *staticFile) Stat() (fs.FileInfo, error) {
	return &fsutil.StatInfo{
		Stat: &fstypes.Stat{
			Mode: uint32(fs.ModeDir) | 0o755,
		},
	}, nil
}
func (f *staticFile) Read([]byte) (int, error) {
	return 0, io.EOF
}
func (f *staticFile) Close() error {
	return nil
}
func (f *staticFile) ReadDir(n int) ([]fs.DirEntry, error) {
	return []fs.DirEntry{f}, nil
}

// impls for fs.DirEntry

func (f *staticFile) Name() string {
	return f.name
}
func (f *staticFile) IsDir() bool {
	return true
}
func (f *staticFile) Type() fs.FileMode {
	stat, _ := f.Stat()
	return stat.Mode().Type()
}
func (f *staticFile) Info() (fs.FileInfo, error) {
	return f.Stat()
}
