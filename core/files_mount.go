package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/v2/core/mount"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/util/layercopy"
)

// FilesMountDir is where an exec's files mounts are assembled, under the
// engine's root: the same filesystem as the snapshots, so the files are hard
// links. The engine empties it when it starts.
const FilesMountDir = "files-mounts"

type execFilesMountConfig struct {
	Files []execMountedFile
}

type execMountedFile struct {
	Name string
	Path string
	Ref  bkcache.ImmutableRef
}

func prepareExecFilesMount(ctx context.Context, cfg *execFilesMountConfig) (bkcache.Mountable, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query for files mount: %w", err)
	}
	state := query.EngineVolumeState()
	if state.RootDir == "" {
		return nil, fmt.Errorf("engine root is not configured")
	}
	return &execFilesMount{
		files:             cfg.Files,
		parent:            filepath.Join(state.RootDir, FilesMountDir),
		recursiveReadOnly: state.RecursiveReadOnlySupported,
	}, nil
}

type execFilesMount struct {
	files             []execMountedFile
	parent            string
	recursiveReadOnly bool
}

// Mount links each file into a new directory, as Directory.withFiles copies
// them into a snapshot, and binds that directory read-only. Releasing the
// mount removes the directory.
func (mnt *execFilesMount) Mount(ctx context.Context, _ bool) (_ bkcache.MountableRef, rerr error) {
	if err := os.MkdirAll(mnt.parent, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(mnt.parent, "")
	if err != nil {
		return nil, err
	}
	release := func() error { return os.RemoveAll(dir) }
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, release())
		}
	}()
	if err := os.Chmod(dir, 0o755); err != nil {
		return nil, err
	}

	copier, err := layercopy.NewCopier(layercopy.Mount{Root: dir})
	if err != nil {
		return nil, err
	}
	defer copier.Close()
	for _, f := range mnt.files {
		if err := MountRef(ctx, f.Ref, func(srcRoot string, srcMnt *mount.Mount) error {
			return copier.CopyFile(ctx, layercopy.Mount{Root: srcRoot, Mount: srcMnt}, f.Path, f.Name, layercopy.CopyOptions{})
		}, mountRefAsReadOnly); err != nil {
			return nil, fmt.Errorf("files mount %q: %w", f.Name, err)
		}
	}

	options := []string{"rbind"}
	if mnt.recursiveReadOnly {
		options = append(options, "rro")
	} else {
		options = append(options, "ro")
	}
	return &execVolumeMountInstance{mounts: []mount.Mount{{
		Type:    "bind",
		Source:  dir,
		Options: options,
	}}, release: release}, nil
}

// execMountedFiles evaluates a files mount's files and resolves each to its
// snapshot and path, as Directory.withFiles does for its sources.
func execMountedFiles(ctx context.Context, src *FilesMountSource) (*execFilesMountConfig, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	inputs := make([]dagql.AnyResult, 0, len(src.Files))
	for _, f := range src.Files {
		if f.File.Self() == nil {
			return nil, fmt.Errorf("files mount %q: nil file", f.Name)
		}
		inputs = append(inputs, f.File)
	}
	if err := cache.Evaluate(ctx, inputs...); err != nil {
		return nil, err
	}
	cfg := &execFilesMountConfig{Files: make([]execMountedFile, len(src.Files))}
	for i, f := range src.Files {
		path, err := f.File.Self().File.GetOrEval(ctx, f.File.Result)
		if err != nil {
			return nil, err
		}
		ref, err := f.File.Self().Snapshot.GetOrEval(ctx, f.File.Result)
		if err != nil {
			return nil, err
		}
		if ref == nil {
			return nil, fmt.Errorf("files mount %q: nil source snapshot", f.Name)
		}
		cfg.Files[i] = execMountedFile{Name: f.Name, Path: path, Ref: ref}
	}
	return cfg, nil
}
