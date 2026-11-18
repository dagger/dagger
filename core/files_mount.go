package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/containerd/containerd/v2/core/mount"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/wcprof"
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
	endLink := recordFilesMountStep(ctx, "exec.filesMount.link", len(mnt.files))
	counts := map[string]int{}
	for _, f := range mnt.files {
		how, err := linkMountedFile(ctx, copier, f)
		if err != nil {
			return nil, fmt.Errorf("files mount %q: %w", f.Name, err)
		}
		counts[how]++
	}
	endLink()
	for how, n := range counts {
		recordFilesMountStep(ctx, how, n)()
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

// How linkMountedFile linked a file, each the profile op that counts them.
const (
	filesMountLinkedFromLayer = "exec.filesMount.link.fromLayer"
	filesMountNotInLayer      = "exec.filesMount.link.notInLayer"
	filesMountLayerUnknown    = "exec.filesMount.link.layerUnknown"
)

// linkMountedFile links f into the mount and says how it did. Linking only needs
// the file's real path, and an idle mount of the snapshot is gone soon after its
// last use, so a mount between builds would be made again for every file. So a
// non-empty file in its snapshot's topmost layer is linked from there, with no
// mount or view of the snapshot. Anything else goes through a shared mount.
//
// What holds the snapshot without a view: not f.Ref, because an immutable ref
// holds nothing (snapshotManager.get, immutableRef.release). The owner lease of
// the file's result row does (File.PersistedSnapshotRefLinks, attached by
// Cache.syncResultSnapshotLeasesLocked), or a dependency's lease if the row's
// link is borrowed from it. The exec keeps that row alive through its
// container's dependency on the file (ContainerWithMountedFilesLazy.AttachDependencies).
//
// For which window: only from resolving the path to the hard link. The link
// shares the file's inode, so nothing needs the snapshot after it, with or
// without a view: its bytes stay until the mount is released, whatever happens to
// the snapshot. If the file has gone from the layer anyway, it falls back to the
// shared mount, which reports that as it always has.
func linkMountedFile(ctx context.Context, copier *layercopy.Copier, f execMountedFile) (how string, _ error) {
	how = filesMountLayerUnknown
	if shared, ok := f.Ref.(bkcache.SharedMounter); ok {
		if layer, ok := shared.SharedMountTopLayer(); ok {
			linked, err := copier.CopyFileFromLayer(ctx, layer, f.Path, f.Name, layercopy.CopyOptions{})
			if err != nil {
				return "", err
			}
			if linked {
				return filesMountLinkedFromLayer, nil
			}
			how = filesMountNotInLayer
		}
	}
	return how, MountRef(ctx, f.Ref, func(srcRoot string, srcMnt *mount.Mount) error {
		return copier.CopyFile(ctx, layercopy.Mount{Root: srcRoot, Mount: srcMnt}, f.Path, f.Name, layercopy.CopyOptions{})
	}, mountRefAsReadOnly)
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
	endEvaluate := recordFilesMountStep(ctx, "withExec.filesMount.evaluate", len(inputs))
	err = cache.Evaluate(ctx, inputs...)
	endEvaluate()
	if err != nil {
		return nil, err
	}
	defer recordFilesMountStep(ctx, "withExec.filesMount.read", len(inputs))()
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

// recordFilesMountStep times one step of a files mount in the profile, with the
// number of files it covers as its ident. The returned func ends it.
func recordFilesMountStep(ctx context.Context, class string, files int) func() {
	if !wcprof.Enabled(ctx) {
		return func() {}
	}
	start := wcprof.NowNS()
	return func() {
		wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, class, wcprof.OpOpts{Ident: strconv.Itoa(files)}, start, wcprof.NowNS(), wcprof.OutcomeOK)
	}
}
