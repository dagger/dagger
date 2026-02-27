package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
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
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkcontenthash "github.com/dagger/dagger/internal/buildkit/cache/contenthash"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
)

// File is a content-addressed file.
type File struct {
	File     string
	Platform Platform

	// Services necessary to provision the file.
	Services ServiceBindings

	Parent dagql.ObjectResult[*Directory]
	LazyState
	// Below is lazily initialized and shoud not be accessed directly
	Snapshot bkcache.ImmutableRef
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
	cp.Snapshot = nil

	return &cp
}

func (file *File) Evaluate(ctx context.Context) error {
	return file.LazyState.Evaluate(ctx, "File")
}

func (file *File) getSnapshot(ctx context.Context) (bkcache.ImmutableRef, error) {
	if err := file.Evaluate(ctx); err != nil {
		return nil, err
	}
	if file.Snapshot != nil {
		return file.Snapshot, nil
	}
	if file.Parent.Self() != nil {
		return file.Parent.Self().getSnapshot(ctx)
	}
	return nil, nil
}

func (file *File) setSnapshot(ref bkcache.ImmutableRef) {
	file.Snapshot = ref
}

func (file *File) getParentSnapshot(ctx context.Context) (bkcache.ImmutableRef, error) {
	if file.Parent.Self() == nil {
		return nil, nil
	}
	return file.Parent.Self().getSnapshot(ctx)
}

func (file *File) WithContents(ctx context.Context, content []byte, permissions fs.FileMode, ownership *Ownership) (LazyInitFunc, error) {
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
		parentSnapshot, err := file.getParentSnapshot(ctx)
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
		file.Snapshot = snapshot
		return nil
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

	snapshot, err := file.getSnapshot(ctx)
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
	ref, err := file.getSnapshot(ctx)
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

		parentSnapshot, err := parent.Self().getSnapshot(ctx)
		if err != nil {
			return err
		}

		sourceFile := &File{
			File:      file.File,
			Platform:  file.Platform,
			Services:  slices.Clone(file.Services),
			Parent:    file.Parent,
			LazyState: NewLazyState(),
			Snapshot:  parentSnapshot,
		}
		sourceFile.LazyInit = func(context.Context) error { return nil }

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
					file.Snapshot = parentSnapshot.Clone()
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
		file.Snapshot = snap
		return nil
	}, nil
}

func (file *File) Digest(ctx context.Context, excludeMetadata bool) (string, error) {
	// If metadata are included, directly compute the digest of the file
	if !excludeMetadata {
		snapshot, err := file.getSnapshot(ctx)
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
	immutableRef, err := file.getSnapshot(ctx)
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
		parentSnapshot, err := parent.Self().getSnapshot(ctx)
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
		file.Snapshot = snapshot
		return nil
	}, nil
}

func (file *File) WithTimestamps(ctx context.Context, parent dagql.ObjectResult[*File], unix int) (LazyInitFunc, error) {
	return func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		parentSnapshot, err := parent.Self().getSnapshot(ctx)
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
		file.Snapshot = snapshot
		return nil
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
	snapshot, err := file.getSnapshot(ctx)
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

	snapshot, err := file.getSnapshot(ctx)
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
	snapshot, err := file.getSnapshot(ctx)
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
		parentSnapshot, err := parent.Self().getSnapshot(ctx)
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
		file.Snapshot = snapshot
		return nil
	}, nil
}
