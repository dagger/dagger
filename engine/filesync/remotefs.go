package filesync

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/tonistiigi/fsutil/types"
)

type remoteFS struct {
	caller       session.Caller
	clientPath   string
	includes     []string
	excludes     []string
	useGitIgnore bool

	startOnce   sync.Once
	client      filesync.FileSync_DiffCopyClient
	filesMu     sync.RWMutex
	filesByPath map[string]*remoteFile
	filesByID   map[uint32]*remoteFile
}

func newRemoteFS(
	caller session.Caller,
	clientPath string,
	includes, excludes []string,
	useGitIgnore bool,
) *remoteFS {
	return &remoteFS{
		caller:       caller,
		clientPath:   clientPath,
		useGitIgnore: useGitIgnore,
		includes:     includes,
		excludes:     excludes,
	}
}

// Walk implements WalkFS for the remote client's filesystem. It can only be called once for a given remoteFS instance.
// The protocol is talking to an fsutil client. The gist of the idea is:
//   - The client starts walking its filesystem and sending stats for every path it hits. We receive those msgs in the same
//     order they were sent.
//   - It keeps track of an int ID for each file it sends a stat for, which can be consistent between us and the client due
//     the ordering guarantee mentioned above.
//   - We can ask for the contents of a given file by sending a msg to it with type PACKET_REQ and the ID of the file we want.
//     It will then send the file contents in chunks with type PACKET_DATA and the ID of the file.
func (fs *remoteFS) Walk(ctx context.Context, path string, walkFn fs.WalkDirFunc) error {
	var started bool
	fs.startOnce.Do(func() {
		started = true
	})
	if !started {
		return fmt.Errorf("walk already started")
	}

	var err error
	fs.client, err = filesync.NewFileSyncClient(fs.caller.Conn()).DiffCopy(engine.LocalImportOpts{
		Path:            fs.clientPath,
		UseGitIgnore:    fs.useGitIgnore,
		IncludePatterns: fs.includes,
		ExcludePatterns: fs.excludes,
	}.AppendToOutgoingContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to create diff copy client: %w", err)
	}

	fs.filesByPath = make(map[string]*remoteFile)
	fs.filesByID = make(map[uint32]*remoteFile)

	walkCh := make(chan *currentPath, 128)
	closeWalkCh := sync.OnceFunc(func() { close(walkCh) })

	errCh := make(chan error, 1)
	go func() (rerr error) {
		defer func() {
			if rerr != nil {
				errCh <- rerr
			}
			close(errCh)

			closeWalkCh()

			fs.filesMu.Lock()
			for _, rFile := range fs.filesByPath {
				rFile.CloseWrite(rerr)
			}
			fs.filesMu.Unlock()
		}()

		var pkt types.Packet
		var curFileID uint32
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

				// handle windows paths
				path := filepath.FromSlash(pkt.Stat.Path)
				if filepath.ToSlash(path) != pkt.Stat.Path {
					// e.g. a linux path foo/bar\baz cannot be represented on windows
					return &os.PathError{Path: pkt.Stat.Path, Err: syscall.EINVAL, Op: "unrepresentable path"}
				}

				pkt.Stat.Path = path
				pkt.Stat.Linkname = filepath.FromSlash(pkt.Stat.Linkname)

				if os.FileMode(pkt.Stat.Mode)&os.ModeType == 0 {
					r, w := io.Pipe()
					rFile := &remoteFile{
						id: curFileID,
						r:  r,
						w:  w,
					}
					fs.filesMu.Lock()
					fs.filesByPath[pkt.Stat.Path] = rFile
					fs.filesByID[curFileID] = rFile
					fs.filesMu.Unlock()
				}
				curFileID++

				select {
				case <-ctx.Done():
					return context.Cause(ctx)
				case walkCh <- &currentPath{path: path, stat: pkt.Stat}:
				}

			case types.PACKET_DATA:
				fs.filesMu.RLock()
				rFile, ok := fs.filesByID[pkt.ID]
				fs.filesMu.RUnlock()
				if !ok {
					return fmt.Errorf("invalid file request %d", pkt.ID)
				}
				if len(pkt.Data) == 0 {
					if err := rFile.CloseWrite(nil); err != nil {
						return fmt.Errorf("failed to close pipe %d: %w", pkt.ID, err)
					}
				} else {
					n, err := rFile.write(pkt.Data)
					if err != nil {
						err = fmt.Errorf("failed to write to pipe %d: %w", pkt.ID, err)
						rFile.CloseWrite(err)
						return err
					}
					if n != len(pkt.Data) {
						err := fmt.Errorf("short write %d/%d", n, len(pkt.Data))
						rFile.CloseWrite(err)
						return err
					}
				}
			}
		}
	}()

	for cp := range walkCh {
		if err := walkFn(cp.path, &StatInfo{cp.stat}, nil); err != nil {
			return err
		}
	}
	select {
	case err := <-errCh:
		return fmt.Errorf("receive failed: %w", err)
	default:
		return nil
	}
}

// ReadFile implements ReadFS for the remote client's filesystem. It can only be called while a Walk is in progress.
func (fs *remoteFS) ReadFile(ctx context.Context, path string) (io.ReadCloser, error) {
	fs.filesMu.RLock()
	rFile, ok := fs.filesByPath[path]
	fs.filesMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("invalid file request %s", path)
	}

	if err := fs.client.SendMsg(&types.Packet{ID: rFile.id, Type: types.PACKET_REQ}); err != nil {
		return nil, fmt.Errorf("failed to send request for file contents: %w", err)
	}

	return rFile, nil
}

type remoteFile struct {
	id uint32

	r *io.PipeReader
	w *io.PipeWriter
}

func (f *remoteFile) Read(p []byte) (n int, err error) {
	return f.r.Read(p)
}

func (f *remoteFile) write(p []byte) (n int, err error) {
	return f.w.Write(p)
}

func (f *remoteFile) CloseWrite(closeErr error) error {
	return f.w.CloseWithError(closeErr)
}

func (f *remoteFile) Close() error {
	return f.r.Close()
}
