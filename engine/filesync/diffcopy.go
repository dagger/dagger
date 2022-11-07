package filesync

import (
	"context"
	io "io"
	"os"

	"github.com/pkg/errors"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"google.golang.org/grpc"
)

type Stream interface {
	Context() context.Context
	SendMsg(m interface{}) error
	RecvMsg(m interface{}) error
}

func sendDiffCopy(stream Stream, fs fsutil.FS, progress progressCb) error {
	return errors.WithStack(fsutil.Send(stream.Context(), stream, fs, progress))
}

func recvDiffCopy(ds grpc.ClientStream, dest string, cu CacheUpdater, progress progressCb, differ fsutil.DiffType, filter func(string, *fstypes.Stat) bool) (err error) {
	var cf fsutil.ChangeFunc
	var ch fsutil.ContentHasher
	if cu != nil {
		cu.MarkSupported(true)
		cf = cu.HandleChange
		ch = cu.ContentHasher()
	}
	defer func() {
		// tracing wrapper requires close trigger even on clean eof
		if err == nil {
			ds.CloseSend()
		}
	}()
	return errors.WithStack(fsutil.Receive(ds.Context(), ds, dest, fsutil.ReceiveOpt{
		NotifyHashed:  cf,
		ContentHasher: ch,
		ProgressCb:    progress,
		Filter:        fsutil.FilterFunc(filter),
		Differ:        differ,
	}))
}

func syncTargetDiffCopy(ds grpc.ServerStream, dest string) error {
	if err := os.MkdirAll(dest, 0700); err != nil {
		return errors.Wrapf(err, "failed to create synctarget dest dir %s", dest)
	}
	return errors.WithStack(fsutil.Receive(ds.Context(), ds, dest, fsutil.ReceiveOpt{
		Merge: true,
		Filter: func() func(string, *fstypes.Stat) bool {
			uid := os.Getuid()
			gid := os.Getgid()
			return func(p string, st *fstypes.Stat) bool {
				st.Uid = uint32(uid)
				st.Gid = uint32(gid)
				return true
			}
		}(),
	}))
}

func writeTargetFile(ds grpc.ServerStream, wc io.WriteCloser) error {
	for {
		bm := BytesMessage{}
		if err := ds.RecvMsg(&bm); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return errors.WithStack(err)
		}
		if _, err := wc.Write(bm.Data); err != nil {
			return errors.WithStack(err)
		}
	}
}
