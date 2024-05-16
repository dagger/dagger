package client

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/containerd/continuity/fs"
	"github.com/moby/buildkit/session/filesync"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/engine"
)

type Filesyncer struct {
	// Absolute path to the rootfs directory (defaults to "/")
	rootDir string
	// Absolute path to the cwd *under the rootfs* (defaults to os.Getwd()).
	// If rootDir is "/foo/bar" and the the real cwd is "/foo/bar/baz", then this
	// will be "/baz".
	cwdAbsPathUnderRoot string

	uid, gid uint32
}

func NewFilesyncer(rootDir, cwdPath string, uid, gid *uint32) (Filesyncer, error) {
	if rootDir == "" {
		rootDir = "/"
	}
	if cwdPath == "" {
		var err error
		cwdPath, err = os.Getwd()
		if err != nil {
			return Filesyncer{}, fmt.Errorf("get cwd: %w", err)
		}
	}

	if !filepath.IsAbs(rootDir) {
		return Filesyncer{}, errors.New("rootDir must be an absolute path")
	}
	if !filepath.IsAbs(cwdPath) {
		return Filesyncer{}, errors.New("cwdPath must be an absolute path")
	}

	cwdRelPath, err := filepath.Rel(rootDir, cwdPath)
	if err != nil {
		return Filesyncer{}, fmt.Errorf("get cwd rel path: %w", err)
	}
	if !filepath.IsLocal(cwdRelPath) {
		return Filesyncer{}, errors.New("cwdPath must be within rootDir")
	}

	f := Filesyncer{
		rootDir:             filepath.Clean(rootDir),
		cwdAbsPathUnderRoot: filepath.Join("/", cwdRelPath),
	}

	if uid == nil {
		f.uid = uint32(os.Getuid())
	} else {
		f.uid = *uid
	}
	if gid == nil {
		f.gid = uint32(os.Getgid())
	} else {
		f.gid = *gid
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

	// find the full path underneath the rootfs
	opts.Path = filepath.Clean(opts.Path)
	if !path.IsAbs(opts.Path) {
		opts.Path = filepath.Join(s.cwdAbsPathUnderRoot, opts.Path)
	}

	// save the original baseName before any symlinks are resolved in case we
	// want to Lstat it below
	baseName := path.Base(opts.Path)

	// resolve the full path on the system, *including* the parent rootfs, evaluating and bounding
	// symlinks under the rootfs
	opts.Path, err = fs.RootPath(s.rootDir, opts.Path)
	if err != nil {
		return fmt.Errorf("get root path: %w", err)
	}

	switch {
	case opts.StatPathOnly:
		// fsutil.Stat is actually Lstat, so be sure to not evaluate baseName in case
		// it's a symlink. Also important that the returned stat.Path is just the
		// base name of the path, not the full path provided.
		opts.Path = filepath.Join(filepath.Dir(opts.Path), baseName)
		stat, err := fsutil.Stat(opts.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return status.Errorf(codes.NotFound, "stat path: %s", err)
			}
			return fmt.Errorf("stat path: %w", err)
		}

		if opts.StatReturnAbsPath {
			stat.Path = strings.TrimPrefix(opts.Path, s.rootDir)
		}

		stat.Path = filepath.ToSlash(stat.Path)
		return stream.SendMsg(stat)

	case opts.ReadSingleFileOnly:
		// just stream the file bytes to the caller
		fileContents, err := os.ReadFile(opts.Path)
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
		fs, err := fsutil.NewFS(opts.Path)
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

	// find the full path underneath the rootfs
	opts.Path = filepath.Clean(opts.Path)
	if !path.IsAbs(opts.Path) {
		opts.Path = filepath.Join(t.cwdAbsPathUnderRoot, opts.Path)
	}

	// resolve the full path on the system, *including* the parent rootfs, evaluating and bounding
	// symlinks under the rootfs
	opts.Path, err = fs.RootPath(t.rootDir, opts.Path)
	if err != nil {
		return fmt.Errorf("get root path: %w", err)
	}

	if !opts.IsFileStream {
		// we're writing a full directory tree, normal fsutil.Receive is good
		if err := mkdirAllWithOwner(filepath.FromSlash(opts.Path), 0o700, t.uid, t.gid); err != nil {
			return fmt.Errorf("failed to create synctarget dest dir %s: %w", opts.Path, err)
		}

		err := fsutil.Receive(stream.Context(), stream, opts.Path, fsutil.ReceiveOpt{
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
	stat, err := os.Lstat(opts.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// we are writing the file to a new path
		destParentDir = filepath.Dir(opts.Path)
		finalDestPath = opts.Path
	case err != nil:
		// something went unrecoverably wrong if stat failed and it wasn't just because the path didn't exist
		return fmt.Errorf("failed to stat synctarget dest %s: %w", opts.Path, err)
	case !stat.IsDir():
		// we are overwriting an existing file
		destParentDir = filepath.Dir(opts.Path)
		finalDestPath = opts.Path
	case !allowParentDirPath:
		// we are writing to an existing directory, but allowParentDirPath is not set, so fail
		return fmt.Errorf("destination %q is a directory; must be a file path unless allowParentDirPath is set", opts.Path)
	default:
		// we are writing to an existing directory, and allowParentDirPath is set,
		// so write the file under the directory using the same file name as the source file
		if fileOriginalName == "" {
			// NOTE: we could instead just default to some name like container.tar or something if desired
			return fmt.Errorf("cannot export container tar to existing directory %q", opts.Path)
		}
		destParentDir = opts.Path
		finalDestPath = filepath.Join(destParentDir, fileOriginalName)
	}

	if err := mkdirAllWithOwner(destParentDir, 0o700, t.uid, t.gid); err != nil {
		return fmt.Errorf("failed to create synctarget dest dir %s: %w", destParentDir, err)
	}

	if opts.FileMode == 0 {
		opts.FileMode = 0o600
	}
	destF, err := os.OpenFile(finalDestPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, opts.FileMode)
	if err != nil {
		return fmt.Errorf("failed to create synctarget dest file %s: %w", finalDestPath, err)
	}
	defer destF.Close()
	if err := destF.Chown(int(t.uid), int(t.gid)); err != nil {
		return fmt.Errorf("failed to chown synctarget dest file %s: %w", finalDestPath, err)
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

// TODO: idtools.MkdirAllAndChown does this
// TODO: idtools.MkdirAllAndChown does this
// TODO: idtools.MkdirAllAndChown does this
// TODO: idtools.MkdirAllAndChown does this
func mkdirAllWithOwner(path string, perm os.FileMode, uid, gid uint32) error {
	alreadyUID := uid == uint32(os.Getuid())
	alreadyGID := gid == uint32(os.Getgid())
	if alreadyUID && alreadyGID {
		return os.MkdirAll(path, perm)
	}

	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute: %s", path)
	}

	split := strings.Split(path, "/")
	curPath := "/"
	for _, part := range split {
		if part == "" {
			continue
		}
		curPath = filepath.Join(curPath, part)

		stat, err := os.Stat(curPath) // purposely resolve symlinks here
		switch {
		case err == nil:
			if !stat.IsDir() {
				return fmt.Errorf("non-dir path part in mkdirAllWithOwner: %s %s", stat.Mode().Type(), curPath)
			}
		case errors.Is(err, os.ErrNotExist):
			if err := os.Mkdir(curPath, perm); err != nil {
				return err
			}
			if err := os.Chown(curPath, int(uid), int(gid)); err != nil {
				return err
			}
		default:
			return err
		}
	}
	return nil
}
