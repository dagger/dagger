package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/dagger/dagger/engine"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/util/bklog"
	"github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
)

type remoteFS struct {
	client filesync.FileSync_DiffCopyClient

	eg errgroup.Group

	walkCh chan *currentPath

	filesByPath map[string]*remoteFile
	filesByID   map[uint32]*remoteFile
	filesMu     sync.RWMutex
}

type remoteFile struct {
	id uint32
	r  io.ReadCloser
	w  io.WriteCloser
}

func newRemoteFS(
	ctx context.Context,
	caller session.Caller,
	clientPath string,
	includes, excludes []string,
) (*remoteFS, error) {
	ctx = engine.LocalImportOpts{
		Path:            clientPath,
		IncludePatterns: includes,
		ExcludePatterns: excludes,
	}.AppendToOutgoingContext(ctx)

	diffCopyClient, err := filesync.NewFileSyncClient(caller.Conn()).DiffCopy(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create diff copy client: %w", err)
	}

	fs := &remoteFS{
		client: diffCopyClient,

		walkCh: make(chan *currentPath, 128),

		filesByPath: make(map[string]*remoteFile),
		filesByID:   make(map[uint32]*remoteFile),
	}

	fs.eg.Go(func() error {
		closeWalkCh := sync.OnceFunc(func() { close(fs.walkCh) })
		defer closeWalkCh()

		var pkt types.Packet
		var nextFileID uint32
		for {
			pkt = types.Packet{Data: pkt.Data[:0]}
			if err := fs.client.RecvMsg(&pkt); err != nil {
				return fmt.Errorf("failed to receive message: %w", err)
			}

			switch pkt.Type {
			case types.PACKET_ERR:
				return fmt.Errorf("error from sender: %s", pkt.Data)

			case types.PACKET_STAT:
				if pkt.Stat == nil {
					closeWalkCh()
					continue
				}

				// normalize unix wire-specific paths to platform-specific paths
				path := filepath.FromSlash(pkt.Stat.Path)
				if filepath.ToSlash(path) != pkt.Stat.Path {
					// e.g. a linux path foo/bar\baz cannot be represented on windows
					return &os.PathError{Path: pkt.Stat.Path, Err: syscall.EINVAL, Op: "unrepresentable path"}
				}
				pkt.Stat.Path = path
				pkt.Stat.Linkname = filepath.FromSlash(pkt.Stat.Linkname)

				if os.FileMode(pkt.Stat.Mode)&os.ModeType == 0 {
					fs.filesMu.Lock()
					r, w := io.Pipe()
					rFile := &remoteFile{
						id: nextFileID,
						r:  r,
						w:  w,
					}
					fs.filesByPath[pkt.Stat.Path] = rFile
					fs.filesByID[nextFileID] = rFile
					fs.filesMu.Unlock()
				}
				nextFileID++

				/* TODO: ? if we trust client do we need these?
				if err := r.orderValidator.HandleChange(ChangeKindAdd, cp.path, &StatInfo{cp.stat}, nil); err != nil {
					return err
				}
				if err := r.hlValidator.HandleChange(ChangeKindAdd, cp.path, &StatInfo{cp.stat}, nil); err != nil {
					return err
				}
				*/

				select {
				case <-ctx.Done():
					return context.Cause(ctx)
				case fs.walkCh <- &currentPath{path: path, stat: pkt.Stat}:
				}

			case types.PACKET_DATA:
				bklog.G(ctx).Debugf("RECV DATA %d", pkt.ID)
				fs.filesMu.RLock()
				rFile, ok := fs.filesByID[pkt.ID]
				fs.filesMu.RUnlock()
				if !ok {
					return fmt.Errorf("invalid file request %d", pkt.ID)
				}
				if len(pkt.Data) == 0 {
					bklog.G(ctx).Debugf("CLOSING PIPE %d", pkt.ID)

					if err := rFile.w.Close(); err != nil {
						return fmt.Errorf("failed to close pipe %d: %w", pkt.ID, err)
					}
				} else {
					n, err := rFile.w.Write(pkt.Data)
					if err != nil {
						return fmt.Errorf("failed to write to pipe %d: %w", pkt.ID, err)
					}
					if n != len(pkt.Data) {
						return fmt.Errorf("short write %d/%d", n, len(pkt.Data))
					}
				}

			case types.PACKET_FIN:
				for {
					var pkt types.Packet
					if err := fs.client.RecvMsg(&pkt); err != nil {
						if errors.Is(err, io.EOF) {
							return nil
						}
						return fmt.Errorf("failed to receive message after fin: %w", err)
					}
				}
			}
		}
	})

	return fs, nil
}

func (fs *remoteFS) Close() error {
	err := fs.client.SendMsg(&types.Packet{Type: types.PACKET_FIN})
	if err != nil {
		return fmt.Errorf("failed to send fin packet: %w", err)
	}
	return fs.eg.Wait()
}

func (fs *remoteFS) Walk(ctx context.Context, path string, walkFn fs.WalkDirFunc) error {
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case cp, ok := <-fs.walkCh:
			if !ok {
				return nil
			}
			if err := walkFn(cp.path, &StatInfo{cp.stat}, nil); err != nil {
				return err
			}
		}
	}
}

func (fs *remoteFS) ReadFile(ctx context.Context, path string) (io.ReadCloser, error) {
	fs.filesMu.RLock()
	// TODO : should we bother deleting from this map?
	rFile, ok := fs.filesByPath[path]
	fs.filesMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("invalid file request %s", path)
	}

	if err := fs.client.SendMsg(&types.Packet{ID: rFile.id, Type: types.PACKET_REQ}); err != nil {
		return nil, err
	}

	return rFile.r, nil
}
