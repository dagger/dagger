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
	"github.com/dagger/dagger/engine/engineutil"
	telemetry "github.com/dagger/otel-go"
)

// File is a content-addressed file.
type File struct {
	Platform Platform

	// Services necessary to provision the file.
	Services ServiceBindings

	Lazy     Lazy[*File]
	File     *LazyAccessor[string, *File]
	Snapshot *LazyAccessor[bkcache.ImmutableRef, *File]
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

var _ dagql.OnReleaser = (*File)(nil)
var _ dagql.HasDependencyResults = (*File)(nil)
var _ dagql.HasLazyEvaluation = (*File)(nil)

func (file *File) OnRelease(ctx context.Context) error {
	if file == nil || file.Snapshot == nil {
		return nil
	}
	snapshot, ok := file.Snapshot.Peek()
	if ok && snapshot != nil {
		return snapshot.Release(ctx)
	}
	return nil
}

func (file *File) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if file == nil {
		return nil, nil
	}
	owned, err := file.Services.AttachDependencyResults("file", attach)
	if err != nil {
		return nil, err
	}
	if file.Lazy == nil {
		return owned, nil
	}
	lazyDeps, err := file.Lazy.AttachDependencies(ctx, attach)
	if err != nil {
		return nil, err
	}
	return append(owned, lazyDeps...), nil
}

func (file *File) LazyEvalFunc() dagql.LazyEvalFunc {
	if file == nil || file.Lazy == nil {
		return nil
	}
	return func(ctx context.Context) error {
		// Successful lazy evaluation materializes the file into a plain value.
		// Clearing Lazy keeps Lazy != nil as a truthful signal that the file
		// still has deferred work.
		lazy := file.Lazy
		if err := lazy.Evaluate(ctx, file); err != nil {
			return err
		}
		if file.Lazy == lazy {
			file.Lazy = nil
		}
		return nil
	}
}

func ParseFileOwner(owner string) (*Ownership, error) {
	return ParseDirectoryOwner(owner)
}

func (file *File) CacheUsageSize(ctx context.Context, identity string) (int64, bool, error) {
	if file == nil {
		return 0, false, nil
	}
	if file.Snapshot == nil {
		return 0, false, nil
	}
	snapshot, ok := file.Snapshot.Peek()
	if !ok || snapshot == nil {
		return 0, false, nil
	}
	if snapshot.SnapshotID() != identity {
		return 0, false, nil
	}
	size, err := snapshot.Size(ctx)
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

func (file *File) CacheUsageIdentities() []string {
	if file == nil || file.Snapshot == nil {
		return nil
	}
	snapshot, ok := file.Snapshot.Peek()
	if !ok || snapshot == nil {
		return nil
	}
	return []string{snapshot.SnapshotID()}
}

func (file *File) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	if file == nil || file.Snapshot == nil {
		return nil
	}
	snapshot, ok := file.Snapshot.Peek()
	if !ok || snapshot == nil {
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
	persistedFileFormLazy     = "lazy"
)

type persistedFilePayload struct {
	Form     string          `json:"form"`
	File     string          `json:"file,omitempty"`
	Platform Platform        `json:"platform"`
	Services ServiceBindings `json:"services,omitempty"`
	LazyJSON json.RawMessage `json:"lazyJSON,omitempty"`
}

func (file *File) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	if file == nil {
		return nil, fmt.Errorf("encode persisted file: nil file")
	}
	filePath := ""
	if file.File != nil {
		if peekedPath, ok := file.File.Peek(); ok {
			filePath = peekedPath
		}
	}
	payload := persistedFilePayload{
		File:     filePath,
		Platform: file.Platform,
		Services: slices.Clone(file.Services),
	}
	if file.Snapshot != nil {
		if snapshot, ok := file.Snapshot.Peek(); ok && snapshot != nil {
			payload.Form = persistedFileFormSnapshot
			payloadJSON, err := json.Marshal(payload)
			if err != nil {
				return nil, fmt.Errorf("marshal persisted file payload: %w", err)
			}
			return payloadJSON, nil
		}
	}
	if file.Lazy != nil {
		payload.Form = persistedFileFormLazy
		lazyJSON, err := file.Lazy.EncodePersisted(ctx, cache)
		if err != nil {
			return nil, err
		}
		payload.LazyJSON = lazyJSON
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal persisted file payload: %w", err)
		}
		return payloadJSON, nil
	}
	return nil, fmt.Errorf("%w: encode persisted file: missing snapshot and lazy op", dagql.ErrPersistStateNotReady)
}

//nolint:dupl // symmetric with decodePersistedDirectoryWithSnapshotRole in directory.go; sharing hides type specifics
func decodePersistedFileWithSnapshotRole(ctx context.Context, dag *dagql.Server, resultID uint64, call *dagql.ResultCall, payload json.RawMessage, snapshotRole string) (*File, error) {
	var persisted persistedFilePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted file payload: %w", err)
	}

	file := &File{
		Platform: persisted.Platform,
		Services: slices.Clone(persisted.Services),
		File:     new(LazyAccessor[string, *File]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
	}
	if persisted.File != "" {
		file.File.setValue(persisted.File)
	}
	switch persisted.Form {
	case persistedFileFormSnapshot:
		snapshot, err := loadPersistedImmutableSnapshotByResultID(ctx, dag, resultID, "file", snapshotRole)
		if err != nil {
			return nil, err
		}
		file.Snapshot.setValue(snapshot)
		return file, nil
	case persistedFileFormLazy:
		if call == nil {
			return nil, fmt.Errorf("decode persisted file payload: missing call for lazy form")
		}
		lazy, err := decodePersistedFileLazy(ctx, dag, call, persisted.LazyJSON)
		if err != nil {
			return nil, err
		}
		file.Lazy = lazy
		return file, nil
	default:
		return nil, fmt.Errorf("decode persisted file payload: unsupported form %q", persisted.Form)
	}
}

func (*File) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, call *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	return decodePersistedFileWithSnapshotRole(ctx, dag, resultID, call, payload, "snapshot")
}

type FileWithReplacedLazy struct {
	LazyState
	Parent      dagql.ObjectResult[*File]
	Search      string
	Replacement string
	FirstFrom   *int
	All         bool
}

type FileWithNameLazy struct {
	LazyState
	Parent   dagql.ObjectResult[*File]
	Filename string
}

type FileWithTimestampsLazy struct {
	LazyState
	Parent    dagql.ObjectResult[*File]
	Timestamp int
}

type FileChownLazy struct {
	LazyState
	Parent dagql.ObjectResult[*File]
	Owner  string
}

type FileSubfileLazy struct {
	LazyState
	Parent dagql.ObjectResult[*Directory]
	Path   string
}

type persistedFileWithReplacedLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Search         string `json:"search"`
	Replacement    string `json:"replacement"`
	FirstFrom      *int   `json:"firstFrom,omitempty"`
	All            bool   `json:"all,omitempty"`
}

type persistedFileWithNameLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Filename       string `json:"filename"`
}

type persistedFileWithTimestampsLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Timestamp      int    `json:"timestamp"`
}

type persistedFileChownLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Owner          string `json:"owner"`
}

type persistedFileSubfileLazy struct {
	ParentResultID uint64 `json:"parentResultID"`
	Path           string `json:"path"`
}

func decodePersistedFileLazy(ctx context.Context, dag *dagql.Server, call *dagql.ResultCall, payload json.RawMessage) (Lazy[*File], error) {
	switch call.Field {
	case "file":
		var persisted persistedFileSubfileLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted file subfile lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.ParentResultID, "file subfile parent")
		if err != nil {
			return nil, err
		}
		return &FileSubfileLazy{LazyState: NewLazyState(), Parent: parent, Path: persisted.Path}, nil
	case "withName":
		var persisted persistedFileWithNameLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted file withName lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.ParentResultID, "file withName parent")
		if err != nil {
			return nil, err
		}
		return &FileWithNameLazy{LazyState: NewLazyState(), Parent: parent, Filename: persisted.Filename}, nil
	case "withReplaced":
		var persisted persistedFileWithReplacedLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted file withReplaced lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.ParentResultID, "file withReplaced parent")
		if err != nil {
			return nil, err
		}
		return &FileWithReplacedLazy{
			LazyState:   NewLazyState(),
			Parent:      parent,
			Search:      persisted.Search,
			Replacement: persisted.Replacement,
			FirstFrom:   persisted.FirstFrom,
			All:         persisted.All,
		}, nil
	case "withTimestamps":
		var persisted persistedFileWithTimestampsLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted file withTimestamps lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.ParentResultID, "file withTimestamps parent")
		if err != nil {
			return nil, err
		}
		return &FileWithTimestampsLazy{LazyState: NewLazyState(), Parent: parent, Timestamp: persisted.Timestamp}, nil
	case "chown":
		var persisted persistedFileChownLazy
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted file chown lazy: %w", err)
		}
		parent, err := loadPersistedObjectResultByResultID[*File](ctx, dag, persisted.ParentResultID, "file chown parent")
		if err != nil {
			return nil, err
		}
		return &FileChownLazy{LazyState: NewLazyState(), Parent: parent, Owner: persisted.Owner}, nil
	default:
		return nil, fmt.Errorf("decode persisted file lazy payload: unsupported field %q", call.Field)
	}
}

func (lazy *FileSubfileLazy) Evaluate(ctx context.Context, file *File) error {
	return lazy.LazyState.Evaluate(ctx, "File.file", func(ctx context.Context) error {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := cache.Evaluate(ctx, lazy.Parent); err != nil {
			return err
		}

		parentPath, err := lazy.Parent.Self().Dir.GetOrEval(ctx, lazy.Parent.Result)
		if err != nil {
			return fmt.Errorf("failed to get parent directory path: %w", err)
		}
		finalPath := filepath.Join(parentPath, lazy.Path)

		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		srv, err := query.Server.Server(ctx)
		if err != nil {
			return err
		}

		info, err := lazy.Parent.Self().Stat(ctx, lazy.Parent, srv, lazy.Path, false)
		if err != nil {
			return err
		}
		if info.FileType == FileTypeDirectory {
			return notAFileError{fmt.Errorf("path %s is a directory, not a file", lazy.Path)}
		}

		parentSnapshot, err := lazy.Parent.Self().Snapshot.GetOrEval(ctx, lazy.Parent.Result)
		if err != nil {
			return fmt.Errorf("failed to get parent directory snapshot: %w", err)
		}
		if parentSnapshot == nil {
			return fmt.Errorf("file subfile parent snapshot is nil")
		}

		reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, parentSnapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
		if err != nil {
			return err
		}
		file.File.setValue(finalPath)
		file.Snapshot.setValue(reopened)
		return nil
	})
}

func (lazy *FileSubfileLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachDirectoryResult(attach, lazy.Parent, "attach file subfile parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *FileSubfileLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "file subfile parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedFileSubfileLazy{ParentResultID: parentID, Path: lazy.Path})
}

func (lazy *FileWithReplacedLazy) Evaluate(ctx context.Context, file *File) error {
	return lazy.LazyState.Evaluate(ctx, "File.withReplaced", func(ctx context.Context) error {
		return file.WithReplaced(ctx, lazy.Parent, lazy.Search, lazy.Replacement, lazy.FirstFrom, lazy.All)
	})
}

func (lazy *FileWithReplacedLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	attached, err := attachFileResult(attach, lazy.Parent, "attach file withReplaced parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = attached
	return []dagql.AnyResult{attached}, nil
}

func (lazy *FileWithReplacedLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "file withReplaced parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedFileWithReplacedLazy{
		ParentResultID: parentID,
		Search:         lazy.Search,
		Replacement:    lazy.Replacement,
		FirstFrom:      lazy.FirstFrom,
		All:            lazy.All,
	})
}

func (lazy *FileWithNameLazy) Evaluate(ctx context.Context, file *File) error {
	return lazy.LazyState.Evaluate(ctx, "File.withName", func(ctx context.Context) error {
		return file.WithName(ctx, lazy.Parent, lazy.Filename)
	})
}

func (lazy *FileWithNameLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	attached, err := attachFileResult(attach, lazy.Parent, "attach file withName parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = attached
	return []dagql.AnyResult{attached}, nil
}

func (lazy *FileWithNameLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "file withName parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedFileWithNameLazy{ParentResultID: parentID, Filename: lazy.Filename})
}

func (lazy *FileWithTimestampsLazy) Evaluate(ctx context.Context, file *File) error {
	return lazy.LazyState.Evaluate(ctx, "File.withTimestamps", func(ctx context.Context) error {
		return file.WithTimestamps(ctx, lazy.Parent, lazy.Timestamp)
	})
}

func (lazy *FileWithTimestampsLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	attached, err := attachFileResult(attach, lazy.Parent, "attach file withTimestamps parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = attached
	return []dagql.AnyResult{attached}, nil
}

func (lazy *FileWithTimestampsLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "file withTimestamps parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedFileWithTimestampsLazy{ParentResultID: parentID, Timestamp: lazy.Timestamp})
}

func (lazy *FileChownLazy) Evaluate(ctx context.Context, file *File) error {
	return lazy.LazyState.Evaluate(ctx, "File.chown", func(ctx context.Context) error {
		return file.Chown(ctx, lazy.Parent, lazy.Owner)
	})
}

func (lazy *FileChownLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	attached, err := attachFileResult(attach, lazy.Parent, "attach file chown parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = attached
	return []dagql.AnyResult{attached}, nil
}

func (lazy *FileChownLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "file chown parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedFileChownLazy{ParentResultID: parentID, Owner: lazy.Owner})
}

func (file *File) WithContents(ctx context.Context, parent dagql.ObjectResult[*Directory], filePath string, content []byte, permissions fs.FileMode, ownership *Ownership) error {
	if dir, _ := filepath.Split(filePath); dir != "" {
		return fmt.Errorf("file name %q must not contain a directory", filePath)
	}

	if permissions == 0 {
		permissions = 0o644
	}

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
	parentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	if parentSnapshot == nil {
		return fmt.Errorf("file withContents: nil parent snapshot")
	}
	file.File.setValue(filePath)
	newRef, err := query.SnapshotManager().New(
		ctx,
		parentSnapshot,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("newFile %s", filePath)),
	)
	if err != nil {
		return err
	}

	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		resolvedDest, err := containerdfs.RootPath(root, filePath)
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
	file.Snapshot.setValue(snapshot)
	return nil
}

// Contents handles file content retrieval
func (file *File) Contents(ctx context.Context, self dagql.ObjectResult[*File], offset, limit *int) ([]byte, error) {
	if limit != nil && *limit == 0 {
		// edge case: 0 limit, possibly from maths, just don't do anything
		return nil, nil
	}

	var buf bytes.Buffer
	w := &limitedWriter{
		Limit:  engineutil.MaxFileContentsSize,
		Writer: &buf,
	}

	snapshot, err := file.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, errEmptyResultRef
	}
	filePath, err := file.File.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}

	err = MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		fullPath, err := containerdfs.RootPath(root, filePath)
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
		return 0, fmt.Errorf("file size %d exceeds limit %d", cw.wrote+len(p), engineutil.MaxFileContentsSize)
	}
	n, err := cw.Writer.Write(p)
	if err != nil {
		return n, err
	}
	cw.wrote += n
	return n, nil
}

func (file *File) Search(ctx context.Context, self dagql.ObjectResult[*File], opts SearchOpts, verbose bool) ([]*SearchResult, error) {
	ref, err := file.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, errEmptyResultRef
	}
	filePath, err := file.File.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}

	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContextFromContext(ctx))

	results := []*SearchResult{}
	err = MountRef(ctx, ref, func(root string, _ *mount.Mount) error {
		resolvedDir, err := containerdfs.RootPath(root, filepath.Dir(filePath))
		if err != nil {
			return err
		}
		rgArgs := opts.RipgrepArgs()
		rgArgs = append(rgArgs, "--", filepath.Base(filePath))
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

//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func (file *File) WithReplaced(ctx context.Context, parent dagql.ObjectResult[*File], searchStr, replacementStr string, firstFrom *int, all bool) error {
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
	parentPath, err := parent.Self().File.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	file.File.setValue(parentPath)
	parentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	if parentSnapshot == nil {
		return fmt.Errorf("replace file: nil snapshot")
	}

	// reuse Search internally so we get convenient line numbers for an error if
	// there are multiple matches
	matches, err := parent.Self().Search(ctx, parent, SearchOpts{
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
			reopened, err := query.SnapshotManager().GetBySnapshotID(ctx, parentSnapshot.SnapshotID(), bkcache.NoUpdateLastUsed)
			if err != nil {
				return err
			}
			file.Snapshot.setValue(reopened)
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
	contents, err := parent.Self().Contents(ctx, parent, offset, nil)
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
		previous, err := parent.Self().Contents(ctx, parent, nil, offset)
		if err != nil {
			return err
		}
		contents = append(previous, contents...)
	}

	// Create a new layer for the replaced content
	newRef, err := query.SnapshotManager().New(ctx, parentSnapshot, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("patch"))
	if err != nil {
		return err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) (rerr error) {
		resolvedPath, err := containerdfs.RootPath(root, parentPath)
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
	file.Snapshot.setValue(snap)
	return nil
}

func (file *File) Digest(ctx context.Context, self dagql.ObjectResult[*File], excludeMetadata bool) (string, error) {
	// If metadata are included, directly compute the digest of the file
	if !excludeMetadata {
		snapshot, err := file.Snapshot.GetOrEval(ctx, self.Result)
		if err != nil {
			return "", fmt.Errorf("failed to evaluate file: %w", err)
		}
		if snapshot == nil {
			return "", fmt.Errorf("failed to evaluate null file")
		}
		filePath, err := file.File.GetOrEval(ctx, self.Result)
		if err != nil {
			return "", fmt.Errorf("failed to get file path: %w", err)
		}

		digest, err := bkcontenthash.Checksum(
			ctx,
			snapshot,
			filePath,
			bkcontenthash.ChecksumOpts{},
		)
		if err != nil {
			return "", fmt.Errorf("failed to compute digest: %w", err)
		}

		return digest.String(), nil
	}

	// If metadata are excluded, compute the digest of the file from its content.
	reader, err := file.Open(ctx, self)
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

func (file *File) Stat(ctx context.Context, self dagql.ObjectResult[*File]) (*Stat, error) {
	immutableRef, err := file.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	if immutableRef == nil {
		filePath, _ := file.File.Peek()
		return nil, &os.PathError{Op: "stat", Path: filePath, Err: syscall.ENOENT}
	}
	filePath, err := file.File.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
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
	err = MountRef(ctx, immutableRef, func(root string, _ *mount.Mount) error {
		resolvedPath, err := rootPathFunc(root, filePath)
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

func (file *File) WithName(ctx context.Context, parent dagql.ObjectResult[*File], filename string) error {
	if dir, _ := filepath.Split(filename); dir != "" {
		return fmt.Errorf("file name %q must not contain a directory", filename)
	}

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
	sourcePath, err := parent.Self().File.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	destPath := filepath.Join(filepath.Dir(sourcePath), filename)
	file.File.setValue(destPath)
	parentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	if parentSnapshot == nil {
		return fmt.Errorf("rename file: nil snapshot")
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		parentSnapshot,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("withName %s", filename)),
	)
	if err != nil {
		return err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		src, err := RootPathWithoutFinalSymlink(root, sourcePath)
		if err != nil {
			return err
		}
		dst, err := RootPathWithoutFinalSymlink(root, destPath)
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
	file.Snapshot.setValue(snapshot)
	return nil
}

func (file *File) WithTimestamps(ctx context.Context, parent dagql.ObjectResult[*File], unix int) error {
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
	parentPath, err := parent.Self().File.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	file.File.setValue(parentPath)
	parentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	if parentSnapshot == nil {
		return fmt.Errorf("file withTimestamps: nil parent snapshot")
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		parentSnapshot,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("withTimestamps %d", unix)),
	)
	if err != nil {
		return err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		fullPath, err := RootPathWithoutFinalSymlink(root, parentPath)
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
	file.Snapshot.setValue(snapshot)
	return nil
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

func (file *File) Open(ctx context.Context, self dagql.ObjectResult[*File]) (io.ReadCloser, error) {
	snapshot, err := file.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, errEmptyResultRef
	}
	filePath, err := file.File.GetOrEval(ctx, self.Result)
	if err != nil {
		return nil, err
	}

	root, _, closer, err := MountRefCloser(ctx, snapshot, mountRefAsReadOnly)
	if err != nil {
		return nil, err
	}

	resolvedPath, err := containerdfs.RootPath(root, filePath)
	if err != nil {
		closeErr := closer()
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return nil, err
	}

	r, err := os.Open(resolvedPath)
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

func ExportFile(ctx context.Context, snapshot bkcache.ImmutableRef, filePath, dest string, allowParentDirPath bool) (rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return fmt.Errorf("failed to get engine client: %w", err)
	}

	ctx, vtx := Tracer(ctx).Start(ctx, fmt.Sprintf("export file %s to host %s", filepath.Base(filePath), dest))
	defer telemetry.EndWithCause(vtx, &rerr)

	if snapshot == nil {
		return errEmptyResultRef
	}

	return MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		path, err := containerdfs.RootPath(root, filePath)
		if err != nil {
			return err
		}
		return bk.LocalFileExport(ctx, path, filePath, dest, allowParentDirPath)
	})
}

func (file *File) Mount(ctx context.Context, self dagql.ObjectResult[*File], f func(string) error) error {
	snapshot, err := file.Snapshot.GetOrEval(ctx, self.Result)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return errEmptyResultRef
	}
	filePath, err := file.File.GetOrEval(ctx, self.Result)
	if err != nil {
		return err
	}
	err = MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		src, err := containerdfs.RootPath(root, filePath)
		if err != nil {
			return err
		}
		return f(src)
	}, mountRefAsReadOnly)
	return err
}

// AsJSON returns the file contents as JSON when possible, otherwise returns an error
func (file *File) AsJSON(ctx context.Context, self dagql.ObjectResult[*File]) (JSON, error) {
	contents, err := file.Contents(ctx, self, nil, nil)
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
func (file *File) AsEnvFile(ctx context.Context, self dagql.ObjectResult[*File], expand bool) (*EnvFile, error) {
	contents, err := file.Contents(ctx, self, nil, nil)
	if err != nil {
		return nil, err
	}
	return (&EnvFile{
		Expand: expand,
	}).WithContents(string(contents))
}

func (file *File) Chown(ctx context.Context, parent dagql.ObjectResult[*File], owner string) error {
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
	parentPath, err := parent.Self().File.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	file.File.setValue(parentPath)
	parentSnapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return err
	}
	if parentSnapshot == nil {
		return fmt.Errorf("file chown: nil parent snapshot")
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		parentSnapshot,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("chown %s %s", parentPath, owner)),
	)
	if err != nil {
		return err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		ownership, err := resolveDirectoryOwner(root, owner)
		if err != nil {
			return fmt.Errorf("failed to parse ownership %s: %w", owner, err)
		}

		chownPath := parentPath
		chownPath, err = containerdfs.RootPath(root, chownPath)
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
	file.Snapshot.setValue(snapshot)
	return nil
}
