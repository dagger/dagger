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
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
)

// File is a content-addressed file.
type File struct {
	LLB    *pb.Definition
	Result bkcache.ImmutableRef // only valid when returned by dagop

	File     string
	Platform Platform

	// Services necessary to provision the file.
	Services ServiceBindings
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

func (file *File) getResult() bkcache.ImmutableRef {
	return file.Result
}
func (file *File) setResult(ref bkcache.ImmutableRef) {
	file.Result = ref
}

var _ HasPBDefinitions = (*File)(nil)

func (file *File) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if file.LLB != nil {
		defs = append(defs, file.LLB)
	}
	for _, bnd := range file.Services {
		ctr := bnd.Service.Self().Container
		if ctr == nil {
			continue
		}
		ctrDefs, err := ctr.PBDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		defs = append(defs, ctrDefs...)
	}
	return defs, nil
}

var _ dagql.OnReleaser = (*File)(nil)

func (file *File) OnRelease(ctx context.Context) error {
	if file.Result != nil {
		return file.Result.Release(ctx)
	}
	return nil
}

func NewFile(def *pb.Definition, file string, platform Platform, services ServiceBindings) *File {
	return &File{
		LLB:      def,
		File:     file,
		Platform: platform,
		Services: services,
	}
}

func NewFileWithContents(
	ctx context.Context,
	name string,
	content []byte,
	permissions fs.FileMode,
	ownership *Ownership,
	platform Platform,
) (*File, error) {
	if dir, _ := filepath.Split(name); dir != "" {
		return nil, fmt.Errorf("file name %q must not contain a directory", name)
	}
	dir, err := NewScratchDirectory(ctx, platform)
	if err != nil {
		return nil, err
	}
	dir, err = dir.WithNewFile(ctx, name, content, permissions, ownership)
	if err != nil {
		return nil, err
	}
	return dir.File(ctx, name)
}

func NewFileSt(ctx context.Context, st llb.State, file string, platform Platform, services ServiceBindings) (*File, error) {
	def, err := st.Marshal(ctx, llb.Platform(platform.Spec()))
	if err != nil {
		return nil, err
	}

	return NewFile(def.ToPB(), file, platform, services), nil
}

// Clone returns a deep copy of the container suitable for modifying in a
// WithXXX method.
func (file *File) Clone() *File {
	cp := *file
	cp.Services = slices.Clone(cp.Services)
	return &cp
}

func (file *File) WithoutInputs() *File {
	file = file.Clone()

	file.LLB = nil
	file.Result = nil

	return file
}

func (file *File) State() (llb.State, error) {
	return defToState(file.LLB)
}

func (file *File) Evaluate(ctx context.Context) (*buildkit.Result, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	return bk.Solve(ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: file.LLB,
	})
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

	_, err := execInMount(ctx, file, func(root string) error {
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
	}, allowNilBuildkitSession)
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
	ref, err := getRefOrEvaluate(ctx, file)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		// empty directory, i.e. llb.Scratch()
		return []*SearchResult{}, nil
	}

	opt, ok := buildkit.CurrentOpOpts(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit opts in context")
	}

	ctx = trace.ContextWithSpanContext(ctx, opt.CauseCtx)

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	results := []*SearchResult{}
	err = MountRef(ctx, ref, bkSessionGroup, func(root string, _ *mount.Mount) error {
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

func (file *File) WithReplaced(ctx context.Context, searchStr, replacementStr string, firstFrom *int, all bool) (*File, error) {
	opt, ok := buildkit.CurrentOpOpts(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit opts in context")
	}
	ctx = trace.ContextWithSpanContext(ctx, opt.CauseCtx)
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	parentRef, err := getRefOrEvaluate(ctx, file)
	if err != nil {
		return nil, err
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	// reuse Search internally so we get convenient line numbers for an error if
	// there are multiple matches
	matches, err := file.Search(ctx, SearchOpts{
		Pattern:   searchStr,
		Literal:   true,
		Multiline: strings.ContainsRune(searchStr, '\n'),
	}, false)
	if err != nil {
		return nil, err
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
			return file, nil
		}
		return nil, fmt.Errorf("search string not found")
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
	contents, err := file.Contents(ctx, offset, nil)
	if err != nil {
		return nil, err
	}
	search := []byte(searchStr)
	replacement := []byte(replacementStr)
	if all {
		contents = bytes.ReplaceAll(contents, search, replacement)
	} else if firstFrom != nil || len(matchedLocs) == 1 {
		contents = bytes.Replace(contents, search, replacement, 1)
	} else if len(matchedLocs) > 0 {
		return nil, fmt.Errorf("search string found multiple times: %s", strings.Join(matchedLocs, ", "))
	}

	// If we replaced after a certain line, bring the content before it back
	if offset != nil && *offset > 0 {
		previous, err := file.Contents(ctx, nil, offset)
		if err != nil {
			return nil, err
		}
		contents = append(previous, contents...)
	}

	// Create a new layer for the replaced content
	newRef, err := query.BuildkitCache().New(ctx, parentRef, bkSessionGroup, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("patch"))
	if err != nil {
		return nil, err
	}
	err = MountRef(ctx, newRef, bkSessionGroup, func(root string, _ *mount.Mount) (rerr error) {
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
		return nil, err
	}
	snap, err := newRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	file = file.Clone()
	file.LLB = nil
	file.Result = snap
	return file, nil
}

func (file *File) Digest(ctx context.Context, excludeMetadata bool) (string, error) {
	// If metadata are included, directly compute the digest of the file
	if !excludeMetadata {
		result, err := file.Evaluate(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to evaluate file: %w", err)
		}

		digest, err := result.Ref.Digest(ctx, file.File)
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
	immutableRef, err := getRefOrEvaluate(ctx, file)
	if err != nil {
		return nil, err
	}
	if immutableRef == nil {
		return nil, &os.PathError{Op: "stat", Path: file.File, Err: syscall.ENOENT}
	}

	bkSessionGroup := requiresBuildkitSessionGroup(ctx)

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
	err = MountRef(ctx, immutableRef, bkSessionGroup, func(root string, _ *mount.Mount) error {
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

func (file *File) WithName(ctx context.Context, filename string) (*File, error) {
	file = file.Clone()
	return execInMount(ctx, file, func(root string) error {
		src, err := RootPathWithoutFinalSymlink(root, file.File)
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
	}, withSavedSnapshot("withName %s", filename))
}

func (file *File) WithTimestamps(ctx context.Context, unix int) (*File, error) {
	file = file.Clone()
	return execInMount(ctx, file, func(root string) error {
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
	}, withSavedSnapshot("withTimestamps %d", unix))
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
	root, closer, err := mountObj(ctx, file, allowNilBuildkitSession)
	if err != nil {
		return nil, err
	}

	filePath, err := containerdfs.RootPath(root, file.File)
	if err != nil {
		_, closeErr := closer(true)
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return nil, err
	}

	r, err := os.Open(filePath)
	if err != nil {
		_, closeErr := closer(true)
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return nil, err
	}

	return &fileReadCloser{
		read: r.Read,
		close: func() error {
			var errs error
			var abort bool
			if err := r.Close(); err != nil {
				errs = errors.Join(errs, err)
				abort = true
			}
			if _, err := closer(abort); err != nil {
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

	root, closer, err := mountObj(ctx, file)
	if err != nil {
		return fmt.Errorf("failed to mount directory: %w", err)
	}
	defer closer(false)

	path, err := containerdfs.RootPath(root, file.File)
	if err != nil {
		return err
	}

	return bk.LocalFileExport(ctx, path, file.File, dest, allowParentDirPath)
}

func (file *File) Mount(ctx context.Context, f func(string) error) error {
	return mountLLB(ctx, file.LLB, func(root string) error {
		src, err := containerdfs.RootPath(root, file.File)
		if err != nil {
			return err
		}
		return f(src)
	})
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

func (file *File) Chown(ctx context.Context, owner string) (*File, error) {
	ownership, err := parseDirectoryOwner(owner)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ownership %s: %w", owner, err)
	}

	file = file.Clone()
	return execInMount(ctx, file, func(root string) error {
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
	}, withSavedSnapshot("chown %s %s", file.File, owner))
}

// bkRef returns the buildkit reference from the solved def.
func bkRef(ctx context.Context, bk *buildkit.Client, def *pb.Definition) (bkgw.Reference, error) {
	res, err := bk.Solve(ctx, bkgw.SolveRequest{
		Definition: def,
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	if ref == nil {
		// empty file, i.e. llb.Scratch()
		return nil, fmt.Errorf("empty reference")
	}

	return ref, nil
}
