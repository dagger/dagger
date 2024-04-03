package reffs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	fstypes "github.com/tonistiigi/fsutil/types"

	"github.com/dagger/dagger/engine/buildkit"
)

type FS struct {
	ctx context.Context
	ref bkgw.Reference
}

func ReferenceFS(ctx context.Context, ref bkgw.Reference) fs.FS {
	return &FS{ctx: ctx, ref: ref}
}

func OpenState(ctx context.Context, bk *buildkit.Client, st llb.State, opts ...llb.ConstraintsOpt) (fs.FS, error) {
	def, err := st.Marshal(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return OpenDef(ctx, bk, def.ToPB())
}

func OpenDef(ctx context.Context, bk *buildkit.Client, def *pb.Definition) (fs.FS, error) {
	res, err := bk.Solve(ctx, bkgw.SolveRequest{
		Definition: def,
		Evaluate:   true,
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	if ref == nil {
		return nil, fmt.Errorf("no ref returned")
	}

	return &FS{ctx: ctx, ref: ref}, nil
}

func (fs *FS) Open(name string) (fs.File, error) {
	stat, err := fs.ref.StatFile(fs.ctx, bkgw.StatRequest{Path: name})
	if err != nil {
		return nil, err
	}

	return &File{ctx: fs.ctx, ref: fs.ref, stat: stat, name: name}, nil
}

type File struct {
	ctx    context.Context
	ref    bkgw.Reference
	name   string
	stat   *fstypes.Stat
	offset int64
}

func (f *File) Stat() (fs.FileInfo, error) {
	return &refFileInfo{stat: f.stat}, nil
}

func (f *File) Read(p []byte) (int, error) {
	if f.offset >= f.stat.Size_ {
		return 0, io.EOF
	}

	content, err := f.ref.ReadFile(f.ctx, bkgw.ReadRequest{
		Filename: f.name,
		Range: &bkgw.FileRange{
			Offset: int(f.offset),
			Length: len(p),
		},
	})
	if err != nil {
		return 0, err
	}
	n := copy(p, content)
	f.offset += int64(n)
	return n, nil
}

func (f *File) Close() error {
	return nil
}

type refFileInfo struct {
	stat *fstypes.Stat
}

func (fi *refFileInfo) Name() string {
	return fi.stat.Path
}

func (fi *refFileInfo) Size() int64 {
	return fi.stat.Size_ // NB: *NOT* Size()!
}

func (fi *refFileInfo) Mode() fs.FileMode {
	return fs.FileMode(fi.stat.Mode)
}

func (fi *refFileInfo) ModTime() time.Time {
	return time.Unix(fi.stat.ModTime/int64(time.Second), fi.stat.ModTime%int64(time.Second))
}

func (fi *refFileInfo) IsDir() bool {
	return fi.stat.IsDir()
}

func (fi *refFileInfo) Sys() interface{} {
	return nil
}
