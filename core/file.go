package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	bkcontenthash "github.com/dagger/dagger/engine/contenthash"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	telemetry "github.com/dagger/otel-go"
)

// File is a content-addressed file.
type File struct {
	File     string
	Platform Platform

	// Services necessary to provision the file.
	Services ServiceBindings

	LazyState
	snapshotMu        sync.RWMutex
	snapshotReady     bool
	snapshotSource    FileSnapshotSource
	Snapshot          bkcache.ImmutableRef
	persistedResultID uint64
}

type FileSnapshotSource struct {
	Directory dagql.ObjectResult[*Directory]
	File      dagql.ObjectResult[*File]
}

func (*File) Type() *ast.Type {
	return &ast.Type{
		NamedType: "File",
		NonNull:   true,
	}
}

func (*File) TypeDescription() string {
	return "A file."
}

func (file *File) PersistedResultID() uint64 {
	if file == nil {
		return 0
	}
	return file.persistedResultID
}

func (file *File) SetPersistedResultID(resultID uint64) {
	if file != nil {
		file.persistedResultID = resultID
	}
}

var _ dagql.OnReleaser = (*File)(nil)
var _ dagql.HasDependencyResults = (*File)(nil)

func (file *File) OnRelease(ctx context.Context) error {
	if file.Snapshot != nil {
		return file.Snapshot.Release(ctx)
	}
	return nil
}

func NewFileChild(parent dagql.ObjectResult[*File]) *File {
	if parent.Self() == nil {
		return nil
	}

	cp := *parent.Self()
	cp.Services = slices.Clone(cp.Services)
	cp.LazyState = NewLazyState()
	cp.snapshotMu = sync.RWMutex{}
	cp.snapshotReady = false
	cp.snapshotSource = FileSnapshotSource{}
	cp.Snapshot = nil

	return &cp
}

func NewFileWithSnapshot(filePath string, platform Platform, services ServiceBindings, snapshot bkcache.ImmutableRef) (*File, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("new file with snapshot: nil snapshot")
	}
	cloned := snapshot.Clone()
	file := &File{
		File:      filePath,
		Platform:  platform,
		Services:  slices.Clone(services),
		LazyState: NewLazyState(),
	}
	if err := file.setSnapshot(cloned); err != nil {
		_ = cloned.Release(context.Background())
		return nil, err
	}
	return file, nil
}

func (file *File) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if file == nil {
		return nil, nil
	}
	file.snapshotMu.RLock()
	source := file.snapshotSource
	file.snapshotMu.RUnlock()

	var deps []dagql.AnyResult
	if source.Directory.Self() != nil {
		attached, err := attach(source.Directory)
		if err != nil {
			return nil, fmt.Errorf("attach file directory snapshot source: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Directory])
		if !ok {
			return nil, fmt.Errorf("attach file directory snapshot source: unexpected result %T", attached)
		}
		file.snapshotMu.Lock()
		file.snapshotSource.Directory = typed
		file.snapshotMu.Unlock()
		deps = append(deps, typed)
	}
	if source.File.Self() != nil {
		attached, err := attach(source.File)
		if err != nil {
			return nil, fmt.Errorf("attach file snapshot source: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*File])
		if !ok {
			return nil, fmt.Errorf("attach file snapshot source: unexpected result %T", attached)
		}
		file.snapshotMu.Lock()
		file.snapshotSource.File = typed
		file.snapshotMu.Unlock()
		deps = append(deps, typed)
	}
	return deps, nil
}

func (file *File) getSnapshot() (bkcache.ImmutableRef, error) {
	file.snapshotMu.RLock()
	ready := file.snapshotReady
	snapshot := file.Snapshot
	source := file.snapshotSource
	file.snapshotMu.RUnlock()

	if !ready {
		return nil, fmt.Errorf("file snapshot not evaluated")
	}
	if snapshot != nil {
		return snapshot, nil
	}
	if source.File.Self() != nil {
		return source.File.Self().getSnapshot()
	}
	if source.Directory.Self() != nil {
		return source.Directory.Self().getSnapshot()
	}
	return nil, fmt.Errorf("file snapshot ready without snapshot or source")
}

func (file *File) setSnapshot(ref bkcache.ImmutableRef) error {
	file.snapshotMu.Lock()
	defer file.snapshotMu.Unlock()
	if file.snapshotReady {
		return fmt.Errorf("file snapshot already set")
	}
	file.Snapshot = ref
	file.snapshotSource = FileSnapshotSource{}
	file.snapshotReady = true
	file.LazyInit = nil
	return nil
}

func (file *File) setSnapshotSource(src FileSnapshotSource) error {
	if src.Directory.Self() != nil && src.File.Self() != nil {
		return fmt.Errorf("file snapshot source has both directory and file set")
	}
	if src.Directory.Self() == nil && src.File.Self() == nil {
		return fmt.Errorf("file snapshot source is empty")
	}
	file.snapshotMu.Lock()
	defer file.snapshotMu.Unlock()
	if file.snapshotReady {
		return fmt.Errorf("file snapshot already set")
	}
	file.Snapshot = nil
	file.snapshotSource = src
	file.snapshotReady = true
	file.LazyInit = nil
	return nil
}

func (file *File) CacheUsageSize(ctx context.Context) (int64, bool, error) {
	file.snapshotMu.RLock()
	snapshot := file.Snapshot
	file.snapshotMu.RUnlock()
	if file == nil || snapshot == nil {
		return 0, false, nil
	}
	size, err := snapshot.Size(ctx)
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

func (file *File) CacheUsageIdentity() (string, bool) {
	file.snapshotMu.RLock()
	snapshot := file.Snapshot
	file.snapshotMu.RUnlock()
	if file == nil || snapshot == nil {
		return "", false
	}
	return snapshot.ID(), true
}

func (file *File) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	file.snapshotMu.RLock()
	snapshot := file.Snapshot
	file.snapshotMu.RUnlock()
	if file == nil || snapshot == nil {
		return nil
	}
	return []dagql.PersistedSnapshotRefLink{
		{
			RefKey: snapshot.SnapshotID(),
			Role:   "snapshot",
		},
	}
}

const (
	persistedFileFormSnapshot = "snapshot"
	persistedFileFormSource   = "source"
)

type persistedFilePayload struct {
	Form                    string   `json:"form"`
	File                    string   `json:"file,omitempty"`
	Platform                Platform `json:"platform"`
	DirectorySourceResultID uint64   `json:"directorySourceResultID,omitempty"`
	FileSourceResultID      uint64   `json:"fileSourceResultID,omitempty"`
}

func (file *File) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	if file == nil {
		return nil, fmt.Errorf("encode persisted file: nil file")
	}
	payload := persistedFilePayload{
		File:     file.File,
		Platform: file.Platform,
	}
	file.snapshotMu.RLock()
	ready := file.snapshotReady
	snapshot := file.Snapshot
	source := file.snapshotSource
	file.snapshotMu.RUnlock()
	if !ready {
		return nil, fmt.Errorf("%w: encode persisted file: snapshot not ready", dagql.ErrPersistStateNotReady)
	}
	switch {
	case snapshot != nil:
		payload.Form = persistedFileFormSnapshot
	case source.Directory.Self() != nil:
		sourceID, err := encodePersistedObjectRef(cache, source.Directory, "file directory snapshot source")
		if err != nil {
			return nil, err
		}
		payload.Form = persistedFileFormSource
		payload.DirectorySourceResultID = sourceID
	case source.File.Self() != nil:
		sourceID, err := encodePersistedObjectRef(cache, source.File, "file snapshot source")
		if err != nil {
			return nil, err
		}
		payload.Form = persistedFileFormSource
		payload.FileSourceResultID = sourceID
	default:
		return nil, fmt.Errorf("%w: encode persisted file: invalid snapshot state", dagql.ErrPersistStateNotReady)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal persisted file payload: %w", err)
	}
	return payloadJSON, nil
}

func (*File) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedFilePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted file payload: %w", err)
	}

	file := &File{
		File:      persisted.File,
		Platform:  persisted.Platform,
		LazyState: NewLazyState(),
	}
	switch persisted.Form {
	case persistedFileFormSnapshot:
		snapshot, _, err := loadPersistedImmutableSnapshotByResultID(ctx, dag, resultID, "file", "snapshot")
		if err != nil {
			return nil, err
		}
		if err := file.setSnapshot(snapshot); err != nil {
			return nil, err
		}
		return file, nil
	case persistedFileFormSource:
		switch {
		case persisted.DirectorySourceResultID != 0 && persisted.FileSourceResultID != 0:
			return nil, fmt.Errorf("decode persisted file payload: both source result IDs set")
		case persisted.DirectorySourceResultID != 0:
			source, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.DirectorySourceResultID, "file directory snapshot source")
			if err != nil {
				return nil, err
			}
			if err := file.setSnapshotSource(FileSnapshotSource{Directory: source}); err != nil {
				return nil, err
			}
			if source.Self() != nil {
				file.Services = slices.Clone(source.Self().Services)
			}
		case persisted.FileSourceResultID != 0:
			source, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.FileSourceResultID, "file snapshot source")
			if err != nil {
				return nil, err
			}
			if err := file.setSnapshotSource(FileSnapshotSource{File: source}); err != nil {
				return nil, err
			}
			if source.Self() != nil {
				file.Services = slices.Clone(source.Self().Services)
			}
		default:
			return nil, fmt.Errorf("decode persisted file payload: missing source result ID")
		}
		return file, nil
	default:
		return nil, fmt.Errorf("decode persisted file payload: unsupported form %q", persisted.Form)
	}
}

func (file *File) WithContents(ctx context.Context, parent dagql.ObjectResult[*Directory], content []byte, permissions fs.FileMode, ownership *Ownership) (LazyInitFunc, error) {
	if dir, _ := filepath.Split(file.File); dir != "" {
		return nil, fmt.Errorf("file name %q must not contain a directory", file.File)
	}

	if permissions == 0 {
		permissions = 0o644
	}

	return func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return err
		}
		parentSnapshot, err := parent.Self().getSnapshot()
		if err != nil {
			return err
		}
		newRef, err := query.BuildkitCache().New(
			ctx,
			parentSnapshot,
			nil,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
			bkcache.WithDescription(fmt.Sprintf("newFile %s", file.File)),
		)
		if err != nil {
			return err
		}

		err = MountRef(ctx, newRef, nil, func(root string, _ *mount.Mount) error {
			resolvedDest, err := containerdfs.RootPath(root, file.File)
			if err != nil {
				return err
			}
			destPathDir, _ := filepath.Split(resolvedDest)
			err = os.MkdirAll(filepath.Dir(destPathDir), 0o755)
			if err != nil {
				return err
			}
			dst, err := os.OpenFile(resolvedDest, os.O_RDWR|os.O_CREATE|os.O_TRUNC, permissions)
			if err != nil {
				return err
			}
			defer func() {
				if dst != nil {
					_ = dst.Close()
				}
			}()

			if _, err := dst.Write(content); err != nil {
				return err
			}
			if err := dst.Close(); err != nil {
				return err
			}
			dst = nil

			if ownership != nil {
				if err := os.Chown(resolvedDest, ownership.UID, ownership.GID); err != nil {
					return fmt.Errorf("failed to set chown %s: err", resolvedDest)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}

		snapshot, err := newRef.Commit(ctx)
		if err != nil {
			return err
		}
		return file.setSnapshot(snapshot)
	}, nil
}

// Contents handles file content retrieval
func (file *File) Contents(ctx context.Context, offset, limit *int) ([]byte, error) {
	if limit != nil && *limit == 0 {
		// edge case: 0 limit, possibly from maths, just don't do anything
		return nil, nil
	}

	var buf bytes.Buffer
	w := &limitedWriter{
		Limit:  buildkit.MaxFileContentsSize,
		Writer: &buf,
	}

	snapshot, err := file.getSnapshot()
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, errEmptyResultRef
	}

	err = MountRef(ctx, snapshot, nil, func(root string, _ *mount.Mount) error {
		fullPath, err := containerdfs.RootPath(root, file.File)
		if err != nil {
			return err
		}

		r, err := os.Open(fullPath)
		if err != nil {
			return err
		}
		defer r.Close()

		if offset != nil || limit != nil {
			br := bufio.NewReader(r)
			lineNum := 1
			readLines := 0
			for {
				line, err := br.ReadBytes('\n')
				if err != nil && err != io.EOF {
					return err
				}

				if offset == nil || lineNum > *offset {
					w.Write(line)
					readLines++
					if limit != nil && readLines == *limit {
						break
					}
				}

				if err == io.EOF {
					break
				}

				lineNum++
			}
		} else {
			_, err := io.Copy(w, r)
			if err != nil {
				return err
			}
		}
		return err
	}, mountRefAsReadOnly)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type limitedWriter struct {
	Limit int
	io.Writer
	wrote int
}

func (cw *limitedWriter) Write(p []byte) (int, error) {
	if cw.wrote+len(p) > cw.Limit {
		return 0, fmt.Errorf("file size %d exceeds limit %d", cw.wrote+len(p), buildkit.MaxFileContentsSize)
	}
	n, err := cw.Writer.Write(p)
	if err != nil {
		return n, err
	}
	cw.wrote += n
	return n, nil
}

func (file *File) Search(ctx context.Context, opts SearchOpts, verbose bool) ([]*SearchResult, error) {
	ref, err := file.getSnapshot()
	if err != nil {
		return nil, err
	}
	if ref == nil {
		// empty directory, i.e. llb.Scratch()
		return []*SearchResult{}, nil
	}

	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContextFromContext(ctx))

	results := []*SearchResult{}
	err = MountRef(ctx, ref, nil, func(root string, _ *mount.Mount) error {
		resolvedDir, err := containerdfs.RootPath(root, filepath.Dir(file.File))
		if err != nil {
			return err
		}
		rgArgs := opts.RipgrepArgs()
		rgArgs = append(rgArgs, "--", filepath.Base(file.File))
		rg := exec.Command("rg", rgArgs...)
		rg.Dir = resolvedDir
		results, err = opts.RunRipgrep(ctx, rg, verbose)
		return err
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (file *File) WithReplaced(ctx context.Context, parent dagql.ObjectResult[*File], searchStr, replacementStr string, firstFrom *int, all bool) (LazyInitFunc, error) {
	return func(ctx context.Context) error {
		ctx = trace.ContextWithSpanContext(ctx, trace.SpanContextFromContext(ctx))

		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}

		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return err
		}
		parentSnapshot, err := parent.Self().getSnapshot()
		if err != nil {
			return err
		}

		sourceFile, err := NewFileWithSnapshot(file.File, file.Platform, file.Services, parentSnapshot)
		if err != nil {
			return err
		}

		// reuse Search internally so we get convenient line numbers for an error if
		// there are multiple matches
		matches, err := sourceFile.Search(ctx, SearchOpts{
			Pattern:   searchStr,
			Literal:   true,
			Multiline: strings.ContainsRune(searchStr, '\n'),
		}, false)
		if err != nil {
			return err
		}

		// Drop any matches before *firstFrom
		if firstFrom != nil {
			var matchesFrom []*SearchResult
			for _, match := range matches {
				if match.LineNumber >= *firstFrom {
					matchesFrom = append(matchesFrom, match)
				}
			}
			matches = matchesFrom
		}

		// Check for matches
		if len(matches) == 0 {
			if all {
				// If we're replacing all, it's not an error if there are no matches
				// (just a no-op)
				if parentSnapshot != nil {
					return file.setSnapshot(parentSnapshot.Clone())
				}
				return nil
			}
			return fmt.Errorf("search string not found")
		}

		var matchedLocs []string
		for _, match := range matches {
			for _, sub := range match.Submatches {
				matchedLocs = append(matchedLocs, fmt.Sprintf("line %d (%d-%d)", match.LineNumber, sub.Start, sub.End))
			}
		}

		// Load content into memory for simple bytes.Replace
		//
		// This is obviously less efficient than streaming, but:
		// 1. it is far simpler (I tried streaming text/transform and hit cryptic errors),
		// 2. we already faced the music on that with File.contents,
		// 3. this will mainly be used for code which is fine to hold in memory, and
		// 4. it is far simpler.
		var offset *int
		if firstFrom != nil {
			o := *firstFrom - 1
			offset = &o
		}
		contents, err := sourceFile.Contents(ctx, offset, nil)
		if err != nil {
			return err
		}
		search := []byte(searchStr)
		replacement := []byte(replacementStr)
		if all {
			contents = bytes.ReplaceAll(contents, search, replacement)
		} else if firstFrom != nil || len(matchedLocs) == 1 {
			contents = bytes.Replace(contents, search, replacement, 1)
		} else if len(matchedLocs) > 0 {
			return fmt.Errorf("search string found multiple times: %s", strings.Join(matchedLocs, ", "))
		}

		// If we replaced after a certain line, bring the content before it back
		if offset != nil && *offset > 0 {
			previous, err := sourceFile.Contents(ctx, nil, offset)
			if err != nil {
				return err
			}
			contents = append(previous, contents...)
		}

		// Create a new layer for the replaced content
		newRef, err := query.BuildkitCache().New(ctx, parentSnapshot, nil, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
			bkcache.WithDescription("patch"))
		if err != nil {
			return err
		}
		err = MountRef(ctx, newRef, nil, func(root string, _ *mount.Mount) (rerr error) {
			resolvedPath, err := containerdfs.RootPath(root, file.File)
			if err != nil {
				return err
			}
			// We're in a new copy-on-write layer, so truncating and rewriting in-place
			// should be fine; we don't need to worry about atomic writes, and this way
			// we preserve permissions and other metadata.
			if err := os.Truncate(resolvedPath, 0); err != nil {
				return err
			}
			f, err := os.OpenFile(resolvedPath, os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.Write(contents)
			return err
		})
		if err != nil {
			return err
		}
		snap, err := newRef.Commit(ctx)
		if err != nil {
			return err
		}
		return file.setSnapshot(snap)
	}, nil
}

func (file *File) Digest(ctx context.Context, excludeMetadata bool) (string, error) {
	// If metadata are included, directly compute the digest of the file
	if !excludeMetadata {
		snapshot, err := file.getSnapshot()
		if err != nil {
			return "", fmt.Errorf("failed to evaluate file: %w", err)
		}
		if snapshot == nil {
			return "", fmt.Errorf("failed to evaluate null file")
		}

		digest, err := bkcontenthash.Checksum(
			ctx,
			snapshot,
			file.File,
			bkcontenthash.ChecksumOpts{},
			nil,
		)
		if err != nil {
			return "", fmt.Errorf("failed to compute digest: %w", err)
		}

		return digest.String(), nil
	}

	// If metadata are excluded, compute the digest of the file from its content.
	reader, err := file.Open(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to open file to compute digest: %w", err)
	}

	defer reader.Close()

	h := sha256.New()
	if _, err := io.Copy(h, reader); err != nil {
		return "", fmt.Errorf("failed to copy file content into hasher: %w", err)
	}

	return digest.FromBytes(h.Sum(nil)).String(), nil
}

func (file *File) Stat(ctx context.Context) (*Stat, error) {
	immutableRef, err := file.getSnapshot()
	if err != nil {
		return nil, err
	}
	if immutableRef == nil {
		return nil, &os.PathError{Op: "stat", Path: file.File, Err: syscall.ENOENT}
	}

	osStatFunc := os.Stat
	rootPathFunc := containerdfs.RootPath
	// TODO Could there be a case where a File() is a symlink?
	// if doNotFollowSymlinks {
	// 	// symlink testing requires the Lstat call, which does NOT follow symlinks
	// 	osStatFunc = os.Lstat
	// 	// similarly, containerdfs.RootPath can't be used, since it follows symlinks
	// 	rootPathFunc = RootPathWithoutFinalSymlink
	// }

	var fileInfo os.FileInfo
	err = MountRef(ctx, immutableRef, nil, func(root string, _ *mount.Mount) error {
		resolvedPath, err := rootPathFunc(root, file.File)
		if err != nil {
			return err
		}
		fileInfo, err = osStatFunc(resolvedPath)
		return TrimErrPathPrefix(err, root)
	})
	if err != nil {
		return nil, err
	}

	m := fileInfo.Mode()

	stat := &Stat{
		Size:        int(fileInfo.Size()),
		Name:        fileInfo.Name(),
		Permissions: int(fileInfo.Mode().Perm()),
		FileType:    FileModeToFileType(m),
	}

	return stat, nil
}

func (file *File) WithName(ctx context.Context, parent dagql.ObjectResult[*File], filename string) (LazyInitFunc, error) {
	sourcePath := file.File
	file.File = filename

	return func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return err
		}
		parentSnapshot, err := parent.Self().getSnapshot()
		if err != nil {
			return err
		}
		newRef, err := query.BuildkitCache().New(
			ctx,
			parentSnapshot,
			nil,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
			bkcache.WithDescription(fmt.Sprintf("withName %s", filename)),
		)
		if err != nil {
			return err
		}
		err = MountRef(ctx, newRef, nil, func(root string, _ *mount.Mount) error {
			src, err := RootPathWithoutFinalSymlink(root, sourcePath)
			if err != nil {
				return err
			}
			dst, err := RootPathWithoutFinalSymlink(root, filename)
			if err != nil {
				return err
			}
			err = os.Rename(src, dst)
			if err != nil {
				return TrimErrPathPrefix(err, root)
			}
			return nil
		})
		if err != nil {
			return err
		}

		snapshot, err := newRef.Commit(ctx)
		if err != nil {
			return err
		}
		return file.setSnapshot(snapshot)
	}, nil
}

func (file *File) WithTimestamps(ctx context.Context, parent dagql.ObjectResult[*File], unix int) (LazyInitFunc, error) {
	return func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return err
		}
		parentSnapshot, err := parent.Self().getSnapshot()
		if err != nil {
			return err
		}
		newRef, err := query.BuildkitCache().New(
			ctx,
			parentSnapshot,
			nil,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
			bkcache.WithDescription(fmt.Sprintf("withTimestamps %d", unix)),
		)
		if err != nil {
			return err
		}
		err = MountRef(ctx, newRef, nil, func(root string, _ *mount.Mount) error {
			fullPath, err := RootPathWithoutFinalSymlink(root, file.File)
			if err != nil {
				return err
			}
			t := time.Unix(int64(unix), 0)
			err = os.Chtimes(fullPath, t, t)
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}

		snapshot, err := newRef.Commit(ctx)
		if err != nil {
			return err
		}
		return file.setSnapshot(snapshot)
	}, nil
}

type fileReadCloser struct {
	read  func(p []byte) (n int, err error)
	close func() error
}

func (frc fileReadCloser) Read(p []byte) (n int, err error) {
	return frc.read(p)
}

func (frc fileReadCloser) Close() error {
	return frc.close()
}

var _ io.ReadCloser = fileReadCloser{}

func (file *File) Open(ctx context.Context) (io.ReadCloser, error) {
	snapshot, err := file.getSnapshot()
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, errEmptyResultRef
	}

	root, _, closer, err := MountRefCloser(ctx, snapshot, nil, mountRefAsReadOnly)
	if err != nil {
		return nil, err
	}

	filePath, err := containerdfs.RootPath(root, file.File)
	if err != nil {
		closeErr := closer()
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return nil, err
	}

	r, err := os.Open(filePath)
	if err != nil {
		closeErr := closer()
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return nil, err
	}

	return &fileReadCloser{
		read: r.Read,
		close: func() error {
			var errs error
			if err := r.Close(); err != nil {
				errs = errors.Join(errs, err)
			}
			if err := closer(); err != nil {
				errs = errors.Join(errs, err)
			}
			return errs
		},
	}, nil
}

func (file *File) Export(ctx context.Context, dest string, allowParentDirPath bool) (rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("failed to get buildkit client: %w", err)
	}

	ctx, vtx := Tracer(ctx).Start(ctx, fmt.Sprintf("export file %s to host %s", filepath.Base(file.File), dest))
	defer telemetry.EndWithCause(vtx, &rerr)

	snapshot, err := file.getSnapshot()
	if err != nil {
		return fmt.Errorf("failed to evaluate file: %w", err)
	}
	if snapshot == nil {
		return errEmptyResultRef
	}

	return MountRef(ctx, snapshot, nil, func(root string, _ *mount.Mount) error {
		path, err := containerdfs.RootPath(root, file.File)
		if err != nil {
			return err
		}
		return bk.LocalFileExport(ctx, path, file.File, dest, allowParentDirPath)
	})
}

func (file *File) Mount(ctx context.Context, f func(string) error) error {
	snapshot, err := file.getSnapshot()
	if err != nil {
		return err
	}
	if snapshot == nil {
		return errEmptyResultRef
	}
	err = MountRef(ctx, snapshot, nil, func(root string, _ *mount.Mount) error {
		src, err := containerdfs.RootPath(root, file.File)
		if err != nil {
			return err
		}
		return f(src)
	}, mountRefAsReadOnly)
	return err
}

// AsJSON returns the file contents as JSON when possible, otherwise returns an error
func (file *File) AsJSON(ctx context.Context) (JSON, error) {
	contents, err := file.Contents(ctx, nil, nil)
	if err != nil {
		return nil, err
	}

	json := JSON(contents)
	if err := json.Validate(); err != nil {
		return nil, err
	}

	return json, nil
}

// AsEnvFile converts a File to an EnvFile by parsing its contents
func (file *File) AsEnvFile(ctx context.Context, expand bool) (*EnvFile, error) {
	contents, err := file.Contents(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	return (&EnvFile{
		Expand: expand,
	}).WithContents(string(contents))
}

func (file *File) Chown(ctx context.Context, parent dagql.ObjectResult[*File], owner string) (LazyInitFunc, error) {
	ownership, err := parseDirectoryOwner(owner)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ownership %s: %w", owner, err)
	}
	return func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, parent); err != nil {
			return err
		}
		parentSnapshot, err := parent.Self().getSnapshot()
		if err != nil {
			return err
		}
		newRef, err := query.BuildkitCache().New(
			ctx,
			parentSnapshot,
			nil,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
			bkcache.WithDescription(fmt.Sprintf("chown %s %s", file.File, owner)),
		)
		if err != nil {
			return err
		}
		err = MountRef(ctx, newRef, nil, func(root string, _ *mount.Mount) error {
			chownPath := file.File
			chownPath, err := containerdfs.RootPath(root, chownPath)
			if err != nil {
				return err
			}

			err = filepath.WalkDir(chownPath, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if err := os.Lchown(path, ownership.UID, ownership.GID); err != nil {
					return fmt.Errorf("failed to set chown %s: %w", path, err)
				}
				return nil
			})
			if err != nil {
				return fmt.Errorf("failed to walk %s: %w", chownPath, err)
			}
			return nil
		})
		if err != nil {
			return err
		}

		snapshot, err := newRef.Commit(ctx)
		if err != nil {
			return err
		}
		return file.setSnapshot(snapshot)
	}, nil
}
