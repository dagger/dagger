package solver

import (
	"context"
	"errors"
	"io/fs"
	"time"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	fstypes "github.com/tonistiigi/fsutil/types"
)

// BuildkitFS is a io/fs.FS adapter for Buildkit
// BuildkitFS implements the ReadFileFS, StatFS and ReadDirFS interfaces.
type BuildkitFS struct {
	ref bkgw.Reference
}

func NewBuildkitFS(ref bkgw.Reference) *BuildkitFS {
	return &BuildkitFS{
		ref: ref,
	}
}

// Open is not supported.
func (f *BuildkitFS) Open(name string) (fs.File, error) {
	return nil, errors.New("not implemented")
}

func (f *BuildkitFS) Stat(name string) (fs.FileInfo, error) {
	st, err := f.ref.StatFile(context.TODO(), bkgw.StatRequest{
		Path: name,
	})
	if err != nil {
		return nil, err
	}
	return bkFileInfo{st}, nil
}

func (f *BuildkitFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := f.ref.ReadDir(context.TODO(), bkgw.ReadDirRequest{
		Path: name,
	})
	if err != nil {
		return nil, err
	}
	res := make([]fs.DirEntry, 0, len(entries))
	for _, st := range entries {
		res = append(res, bkDirEntry{
			bkFileInfo: bkFileInfo{
				st: st,
			},
		})
	}
	return res, nil
}

func (f *BuildkitFS) ReadFile(name string) ([]byte, error) {
	return f.ref.ReadFile(context.TODO(), bkgw.ReadRequest{
		Filename: name,
	})
}

// bkFileInfo is a fs.FileInfo adapter for fstypes.Stat
type bkFileInfo struct {
	st *fstypes.Stat
}

func (s bkFileInfo) Name() string {
	return s.st.GetPath()
}

func (s bkFileInfo) Size() int64 {
	return s.st.GetSize_()
}

func (s bkFileInfo) IsDir() bool {
	return s.st.IsDir()
}

func (s bkFileInfo) ModTime() time.Time {
	return time.Unix(s.st.GetModTime(), 0)
}

func (s bkFileInfo) Mode() fs.FileMode {
	return fs.FileMode(s.st.Mode)
}

func (s bkFileInfo) Sys() interface{} {
	return s.st
}

// bkDirEntry is a fs.DirEntry adapter for fstypes.Stat
type bkDirEntry struct {
	bkFileInfo
}

func (s bkDirEntry) Info() (fs.FileInfo, error) {
	return s.bkFileInfo, nil
}

func (s bkDirEntry) Type() fs.FileMode {
	return s.Mode()
}
