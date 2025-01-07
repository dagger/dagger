package client

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/docker/docker/pkg/idtools"
	"github.com/moby/buildkit/session/filesync"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/engine"
)

type Filesyncer struct {
	uid, gid uint32
}

func NewFilesyncer() (Filesyncer, error) {
	f := Filesyncer{
		uid: uint32(os.Getuid()),
		gid: uint32(os.Getgid()),
	}

	return f, nil
}

func (f Filesyncer) AsSource() FilesyncSource {
	return FilesyncSource(f)
}

func (f Filesyncer) AsTarget() FilesyncTarget {
	return FilesyncTarget(f)
}

type FilesyncSource Filesyncer

func (s FilesyncSource) Register(server *grpc.Server) {
	filesync.RegisterFileSyncServer(server, s)
}

func (s FilesyncSource) TarStream(stream filesync.FileSync_TarStreamServer) error {
	return fmt.Errorf("tarstream not supported")
}

func (s FilesyncSource) DiffCopy(stream filesync.FileSync_DiffCopyServer) error {
	opts, err := engine.LocalImportOptsFromContext(stream.Context())
	if err != nil {
		return fmt.Errorf("get local import opts: %w", err)
	}

	absPath, err := Filesyncer(s).fullRootPathAndBaseName(opts.Path, opts.StatResolvePath)
	if err != nil {
		return fmt.Errorf("get full root path: %w", err)
	}

	switch {
	case opts.StatPathOnly:
		stat, err := fsutil.Stat(absPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return status.Errorf(codes.NotFound, "stat path: %s", err)
			}
			return fmt.Errorf("stat path: %w", err)
		}

		if opts.StatReturnAbsPath {
			stat.Path = absPath
		}

		stat.Path = filepath.ToSlash(stat.Path)
		return stream.SendMsg(stat)

	case opts.ReadSingleFileOnly:
		// just stream the file bytes to the caller
		fileContents, err := os.ReadFile(absPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return status.Errorf(codes.NotFound, "read path: %s", err)
			}
			return fmt.Errorf("read file: %w", err)
		}
		if len(fileContents) > int(opts.MaxFileSize) {
			// NOTE: can lift this size restriction by chunking if ever needed
			return fmt.Errorf("file contents too large: %d > %d", len(fileContents), opts.MaxFileSize)
		}
		return stream.SendMsg(&filesync.BytesMessage{Data: fileContents})

	default:
		// otherwise, do the whole directory sync back to the caller
		fs, err := fsutil.NewFS(absPath)
		if err != nil {
			return err
		}
		fs, err = fsutil.NewFilterFS(fs, &fsutil.FilterOpt{
			IncludePatterns: opts.IncludePatterns,
			ExcludePatterns: opts.ExcludePatterns,
			FollowPaths:     opts.FollowPaths,
			Map: func(p string, st *fstypes.Stat) fsutil.MapResult {
				st.Uid = 0
				st.Gid = 0
				return fsutil.MapResultKeep
			},
		})
		if err != nil {
			return err
		}
		return fsutil.Send(stream.Context(), stream, fs, nil)
	}
}

type FilesyncTarget Filesyncer

func (t FilesyncTarget) Register(server *grpc.Server) {
	filesync.RegisterFileSendServer(server, t)
}

func (t FilesyncTarget) DiffCopy(stream filesync.FileSend_DiffCopyServer) (rerr error) {
	opts, err := engine.LocalExportOptsFromContext(stream.Context())
	if err != nil {
		return fmt.Errorf("get local export opts: %w", err)
	}

	absPath, err := Filesyncer(t).fullRootPathAndBaseName(opts.Path, false)
	if err != nil {
		return fmt.Errorf("get full root path: %w", err)
	}

	if !opts.IsFileStream {
		// we're writing a full directory tree, normal fsutil.Receive is good
		if err := idtools.MkdirAllAndChownNew(filepath.FromSlash(absPath), 0o700, idtools.Identity{
			UID: int(t.uid),
			GID: int(t.gid),
		}); err != nil {
			return fmt.Errorf("failed to create synctarget dest dir %s: %w", absPath, err)
		}

		err := fsutil.Receive(stream.Context(), stream, absPath, fsutil.ReceiveOpt{
			Merge: opts.Merge,
			Filter: func(path string, stat *fstypes.Stat) bool {
				stat.Uid = t.uid
				stat.Gid = t.gid
				return true
			},
		})
		if err != nil {
			return fmt.Errorf("failed to receive fs changes: %w", err)
		}
		return nil
	}

	// This is either a file export or a container tarball export, we'll just be receiving BytesMessages with
	// the contents and can write them directly to the destination path.

	// If the dest is a directory that already exists, we will never delete it and replace it with the file.
	// However, if allowParentDirPath is set, we will write the file underneath that existing directory.
	// But if allowParentDirPath is not set, which is the default setting in our API right now, we will return
	// an error when path is a pre-existing directory.
	allowParentDirPath := opts.AllowParentDirPath

	// File exports specifically (as opposed to container tar exports) have an original filename that we will
	// use in the case where dest is a directory and allowParentDirPath is set, in which case we need to know
	// what to name the file underneath the pre-existing directory.
	fileOriginalName := opts.FileOriginalName

	var destParentDir string
	var finalDestPath string
	stat, err := os.Stat(absPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// we are writing the file to a new path
		destParentDir = filepath.Dir(absPath)
		finalDestPath = absPath
	case err != nil:
		// something went unrecoverably wrong if stat failed and it wasn't just because the path didn't exist
		return fmt.Errorf("failed to stat synctarget dest %s: %w", absPath, err)
	case !stat.IsDir():
		// we are overwriting an existing file
		destParentDir = filepath.Dir(absPath)
		finalDestPath = absPath
	case !allowParentDirPath:
		// we are writing to an existing directory, but allowParentDirPath is not set, so fail
		return fmt.Errorf("destination %q is a directory; must be a file path unless allowParentDirPath is set", absPath)
	default:
		// we are writing to an existing directory, and allowParentDirPath is set,
		// so write the file under the directory using the same file name as the source file
		if fileOriginalName == "" {
			// NOTE: we could instead just default to some name like container.tar or something if desired
			return fmt.Errorf("cannot export container tar to existing directory %q", absPath)
		}
		destParentDir = absPath
		finalDestPath = filepath.Join(destParentDir, fileOriginalName)
	}

	if err := idtools.MkdirAllAndChownNew(filepath.FromSlash(destParentDir), 0o700, idtools.Identity{
		UID: int(t.uid),
		GID: int(t.gid),
	}); err != nil {
		return fmt.Errorf("failed to create synctarget dest dir %s: %w", absPath, err)
	}

	if opts.FileMode == 0 {
		opts.FileMode = 0o600
	}
	destF, err := os.OpenFile(finalDestPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, opts.FileMode)
	if err != nil {
		return fmt.Errorf("failed to create synctarget dest file %s: %w", finalDestPath, err)
	}
	defer destF.Close()
	if runtime.GOOS != "windows" {
		if err := destF.Chown(int(t.uid), int(t.gid)); err != nil {
			return fmt.Errorf("failed to chown synctarget dest file %s: %w", finalDestPath, err)
		}
	}

	for {
		msg := filesync.BytesMessage{}
		if err := stream.RecvMsg(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if _, err := destF.Write(msg.Data); err != nil {
			return err
		}
	}
}

func (f Filesyncer) fullRootPathAndBaseName(reqPath string, fullyResolvePath bool) (_ string, err error) {
	// NOTE: filepath.Clean also handles calling FromSlash (relevant when this is a Windows client)
	reqPath = filepath.Clean(reqPath)

	rootPath, err := Abs(reqPath)
	if err != nil {
		return "", fmt.Errorf("get abs path: %w", err)
	}
	if fullyResolvePath {
		rootPath, err = filepath.EvalSymlinks(rootPath)
		if err != nil {
			return "", fmt.Errorf("eval symlinks: %w", err)
		}
	}
	return rootPath, nil
}
