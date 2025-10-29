package filesync

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/tonistiigi/fsutil/types"
)

type StatInfo struct {
	*types.Stat
}

func (s *StatInfo) Name() string {
	return filepath.Base(s.Stat.Path)
}

func (s *StatInfo) Size() int64 {
	return s.Stat.Size_
}

func (s *StatInfo) Mode() os.FileMode {
	return os.FileMode(s.Stat.Mode)
}

func (s *StatInfo) ModTime() time.Time {
	return time.Unix(s.Stat.ModTime/1e9, s.Stat.ModTime%1e9)
}

func (s *StatInfo) IsDir() bool {
	return s.Mode().IsDir()
}

func (s *StatInfo) Sys() any {
	return s.Stat
}

func (s *StatInfo) Type() fs.FileMode {
	return fs.FileMode(s.Stat.Mode)
}

func (s *StatInfo) Info() (fs.FileInfo, error) {
	return s, nil
}

type HashedStatInfo struct {
	StatInfo
	dgst digest.Digest
}

func (s *HashedStatInfo) Digest() digest.Digest {
	return s.dgst
}
