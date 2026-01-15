package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
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
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	fscopy "github.com/dagger/dagger/internal/fsutil/copy"
	"github.com/dagger/dagger/util/patternmatcher"
	"github.com/dustin/go-humanize"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sys/unix"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/slog"
)

// Directory is a content-addressed directory.
type Directory struct {
	LLB    *pb.Definition
	Result bkcache.ImmutableRef // only valid when returned by dagop

	Dir      string
	Platform Platform

	// Services necessary to provision the directory.
	Services ServiceBindings
}

func (*Directory) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Directory",
		NonNull:   true,
	}
}

func (*Directory) TypeDescription() string {
	return "A directory."
}

func (dir *Directory) getResult() bkcache.ImmutableRef {
	return dir.Result
}
func (dir *Directory) setResult(ref bkcache.ImmutableRef) {
	dir.Result = ref
}

func (dir *Directory) IsRootDir() bool {
	return dir.Dir == "" || dir.Dir == "/"
}

var _ HasPBDefinitions = (*Directory)(nil)

func (dir *Directory) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if dir.LLB != nil {
		defs = append(defs, dir.LLB)
	}
	for _, bnd := range dir.Services {
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

func NewDirectory(def *pb.Definition, dir string, platform Platform, services ServiceBindings) *Directory {
	return &Directory{
		LLB:      def,
		Dir:      dir,
		Platform: platform,
		Services: services,
	}
}

func NewScratchDirectory(ctx context.Context, platform Platform) (*Directory, error) {
	return NewDirectorySt(ctx, llb.Scratch(), "/", platform, nil)
}

func NewDirectorySt(ctx context.Context, st llb.State, dir string, platform Platform, services ServiceBindings) (*Directory, error) {
	def, err := st.Marshal(ctx, llb.Platform(platform.Spec()))
	if err != nil {
		return nil, err
	}

	return NewDirectory(def.ToPB(), dir, platform, services), nil
}

// Clone returns a deep copy of the container suitable for modifying in a
// WithXXX method.
func (dir *Directory) Clone() *Directory {
	if dir == nil {
		return nil
	}
	cp := *dir
	cp.Services = slices.Clone(cp.Services)

	return &cp
}

func (dir *Directory) WithoutInputs() *Directory {
	dir = dir.Clone()

	dir.LLB = nil
	dir.Result = nil

	return dir
}

var _ dagql.OnReleaser = (*Directory)(nil)

func (dir *Directory) OnRelease(ctx context.Context) error {
	if dir.Result != nil {
		return dir.Result.Release(ctx)
	}
	return nil
}

func (dir *Directory) State() (llb.State, error) {
	if dir.LLB == nil {
		return llb.Scratch(), nil
	}

	return defToState(dir.LLB)
}

func (dir *Directory) StateWithSourcePath() (llb.State, error) {
	dirSt, err := dir.State()
	if err != nil {
		return llb.State{}, err
	}

	if dir.Dir == "/" {
		return dirSt, nil
	}

	if dir.Dir == "" {
		return llb.State{}, fmt.Errorf("got empty dir path, which shouldnt happen")
	}

	return llb.Scratch().File(
		llb.Copy(dirSt, dir.Dir, ".", &llb.CopyInfo{
			CopyDirContentsOnly: true,
		}),
	), nil
}

func (dir *Directory) SetState(ctx context.Context, st llb.State) error {
	def, err := st.Marshal(ctx,
		llb.Platform(dir.Platform.Spec()),
		buildkit.WithTracePropagation(ctx),
		buildkit.WithPassthrough(), // these spans aren't particularly interesting
	)
	if err != nil {
		return err
	}

	dir.LLB = def.ToPB()
	return nil
}

func (dir *Directory) Evaluate(ctx context.Context) (*buildkit.Result, error) {
	if dir.LLB == nil {
		return nil, nil
	}

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
		Definition: dir.LLB,
	})
}

func (dir *Directory) Digest(ctx context.Context) (string, error) {
	result, err := dir.Evaluate(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to evaluate directory: %w", err)
	}
	if result == nil {
		return "", fmt.Errorf("failed to evaluate null directory")
	}

	digest, err := result.Ref.Digest(ctx, dir.Dir)
	if err != nil {
		return "", fmt.Errorf("failed to compute digest: %w", err)
	}

	return digest.String(), nil
}

func (dir *Directory) Entries(ctx context.Context, src string) ([]string, error) {
	src = path.Join(dir.Dir, src)
	paths := []string{}
	useSlash := SupportsDirSlash(ctx)
	_, err := execInMount(ctx, dir, func(root string) error {
		resolvedDir, err := containerdfs.RootPath(root, src)
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(resolvedDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			path := entry.Name()
			if useSlash && entry.IsDir() {
				path += "/"
			}
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errEmptyResultRef) {
			// empty directory, i.e. llb.Scratch()
			if clean := path.Clean(src); clean == "." || clean == "/" {
				return []string{}, nil
			}
			return nil, fmt.Errorf("%s: %w", src, os.ErrNotExist)
		}
		return nil, err
	}
	return paths, nil
}

// patternWithoutTrailingGlob is from fsuitls
func patternWithoutTrailingGlob(p *patternmatcher.Pattern) string {
	patStr := p.String()
	// We use filepath.Separator here because patternmatcher.Pattern patterns
	// get transformed to use the native path separator:
	// https://github.com/moby/patternmatcher/blob/130b41bafc16209dc1b52a103fdac1decad04f1a/patternmatcher.go#L52
	patStr = strings.TrimSuffix(patStr, string(filepath.Separator)+"**")
	patStr = strings.TrimSuffix(patStr, string(filepath.Separator)+"*")
	return patStr
}

// Glob returns a list of files that matches the given pattern.
func (dir *Directory) Glob(ctx context.Context, pattern string) ([]string, error) {
	paths := []string{}

	pat, err := patternmatcher.NewPattern(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to create glob pattern matcher: %w", err)
	}

	// from fsutils
	patternChars := "*[]?^"
	if filepath.Separator != '\\' {
		patternChars += `\`
	}
	onlyPrefixIncludes := !strings.ContainsAny(patternWithoutTrailingGlob(pat), patternChars)

	useSlash := SupportsDirSlash(ctx)
	_, err = execInMount(ctx, dir, func(root string) error {
		resolvedDir, err := containerdfs.RootPath(root, dir.Dir)
		if err != nil {
			return err
		}

		return filepath.WalkDir(resolvedDir, func(path string, d fs.DirEntry, prevErr error) error {
			if prevErr != nil {
				return prevErr
			}

			path, err := filepath.Rel(resolvedDir, path)
			if err != nil {
				return err
			}
			// Skip root
			if path == "." {
				return nil
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				break
			}

			match, err := pat.Match(path)
			if err != nil {
				return err
			}

			if match {
				if useSlash && d.IsDir() {
					path += "/"
				}
				paths = append(paths, path)
			} else if d.IsDir() && onlyPrefixIncludes {
				// fsutils Optimization: we can skip walking this dir if no include
				// patterns could match anything inside it.
				dirSlash := path + string(filepath.Separator)
				if !pat.Exclusion() {
					patStr := patternWithoutTrailingGlob(pat) + string(filepath.Separator)
					if !strings.HasPrefix(patStr, dirSlash) {
						return filepath.SkipDir
					}
				}
			}

			return nil
		})
	})
	if err != nil {
		if errors.Is(err, errEmptyResultRef) {
			// empty directory, i.e. llb.Scratch()
			return []string{}, nil
		}
		return nil, err
	}

	return paths, nil
}

func (dir *Directory) WithNewFile(ctx context.Context, dest string, content []byte, permissions fs.FileMode, ownership *Ownership) (*Directory, error) {
	dir = dir.Clone()

	err := validateFileName(dest)
	if err != nil {
		return nil, err
	}

	if permissions == 0 {
		permissions = 0o644
	}

	// be sure to create the file under the working directory
	dest = path.Join(dir.Dir, dest)

	st, err := dir.State()
	if err != nil {
		return nil, err
	}

	parent, _ := path.Split(dest)
	if parent != "" {
		st = st.File(llb.Mkdir(parent, 0755, llb.WithParents(true)))
	}

	opts := []llb.MkfileOption{}
	if ownership != nil {
		opts = append(opts, ownership.Opt())
	}

	st = st.File(llb.Mkfile(dest, permissions, content, opts...))

	err = dir.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	return dir, nil
}

func (dir *Directory) WithNewFileDagOp(ctx context.Context, dest string, content []byte, permissions fs.FileMode, ownership *Ownership) (*Directory, error) {
	dir = dir.Clone()

	err := validateFileName(dest)
	if err != nil {
		return nil, err
	}

	if permissions == 0 {
		permissions = 0o644
	}

	return execInMount(ctx, dir, func(root string) error {
		resolvedDest, err := containerdfs.RootPath(root, path.Join(dir.Dir, dest))
		if err != nil {
			return err
		}
		destPathDir, _ := filepath.Split(resolvedDest)
		err = os.MkdirAll(filepath.Dir(destPathDir), 0755)
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

		_, err = dst.Write(content)
		if err != nil {
			return err
		}

		err = dst.Close()
		if err != nil {
			return err
		}
		dst = nil

		if ownership != nil {
			err = os.Chown(resolvedDest, ownership.UID, ownership.GID)
			if err != nil {
				return fmt.Errorf("failed to set chown %s: err", resolvedDest)
			}
		}

		return nil
	}, withSavedSnapshot("withNewFile %s (%s)", dest, humanize.Bytes(uint64(len(content)))))
}

func (dir *Directory) WithPatch(ctx context.Context, patch string) (*Directory, error) {
	dir = dir.Clone()

	parentRef, err := getRefOrEvaluate(ctx, dir)
	if err != nil {
		return nil, err
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	opt, ok := buildkit.CurrentOpOpts(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit opts in context")
	}
	ctx = trace.ContextWithSpanContext(ctx, opt.CauseCtx)
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()

	newRef, err := query.BuildkitCache().New(ctx, parentRef, bkSessionGroup, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("patch"))
	if err != nil {
		return nil, err
	}
	err = MountRef(ctx, newRef, bkSessionGroup, func(root string, _ *mount.Mount) (rerr error) {
		resolvedDir, err := containerdfs.RootPath(root, dir.Dir)
		if err != nil {
			return err
		}
		apply := exec.Command("git", "apply", "--allow-empty", "-")
		apply.Dir = resolvedDir
		apply.Stdin = strings.NewReader(patch)
		apply.Stdout = stdio.Stdout
		apply.Stderr = stdio.Stderr
		if err := apply.Run(); err != nil {
			// NB: we could technically populate a buildkit.ExecError here, but that
			// feels like it leaks implementation details; "exit status 128" isn't
			// exactly clear
			return errors.New("failed to apply patch")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	snap, err := newRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	dir.Result = snap
	return dir, nil
}

func (dir *Directory) Search(ctx context.Context, opts SearchOpts, verbose bool, paths []string, globs []string) ([]*SearchResult, error) {
	// Validate and normalize paths to prevent directory traversal attacks
	for i, p := range paths {
		// If absolute, make it relative to the directory
		if filepath.IsAbs(p) {
			paths[i] = strings.TrimPrefix(p, "/")
		}

		// Clean the path (e.g., remove ../, ./, etc.)
		paths[i] = filepath.Clean(paths[i])

		// Check if the normalized path would escape the directory
		if !filepath.IsLocal(paths[i]) {
			return nil, fmt.Errorf("path cannot escape directory: %s", p)
		}
	}

	ref, err := getRefOrEvaluate(ctx, dir)
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
		resolvedDir, err := containerdfs.RootPath(root, dir.Dir)
		if err != nil {
			return err
		}
		rgArgs := opts.RipgrepArgs()
		for _, glob := range globs {
			rgArgs = append(rgArgs, "--glob="+glob)
		}
		if len(paths) > 0 {
			rgArgs = append(rgArgs, "--")
			for _, p := range paths {
				resolved, err := containerdfs.RootPath(resolvedDir, p)
				if err != nil {
					return err
				}
				// make it relative, now that it's safe, just for less obtuse errors
				resolved, err = filepath.Rel(resolvedDir, resolved)
				if err != nil {
					return err
				}
				rgArgs = append(rgArgs, resolved)
			}
		}
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

// cleanDotsAndSlashes is similar to path.Clean; however it does not remove any directory names, e.g. "keep/../this//.//" will return "keep/../this".
// This is needed for cases where a referenced directory is a symlink, e.g. consider keep linking to some/other/directory, then keep/../this,
// would end up being some/other/directory/../this, which would end up as some/other/this
func cleanDotsAndSlashes(path string) string {
	cleaned := []string{}
	for _, d := range filepath.SplitList(path) {
		if d == "" || d == "." || d == "/" {
			continue
		}
		cleaned = append(cleaned, d)
	}
	return filepath.Join(cleaned...)
}

func (dir *Directory) Directory(ctx context.Context, subdir string) (*Directory, error) {
	if cleanDotsAndSlashes(subdir) == "" {
		return dir, nil
	}

	dir = dir.Clone()

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	srv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, err
	}

	dir.Dir = path.Join(dir.Dir, subdir)

	// check that the directory actually exists so the user gets an error earlier
	// rather than when the dir is used
	info, err := dir.Stat(ctx, srv, ".", false)
	if err != nil {
		return nil, RestoreErrPath(err, subdir)
	}

	if info.FileType != FileTypeDirectory {
		return nil, notADirectoryError{fmt.Errorf("path %s is a file, not a directory", subdir)}
	}

	return dir, nil
}

type notADirectoryError struct {
	inner error
}

func (e notADirectoryError) Error() string {
	return e.inner.Error()
}

func (e notADirectoryError) Unwrap() error {
	return e.inner
}

func (dir *Directory) File(ctx context.Context, file string) (*File, error) {
	dir = dir.Clone()
	filePath := path.Join(dir.Dir, file)

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	srv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, err
	}

	stat, err := dir.Stat(ctx, srv, file, false)
	if err != nil {
		return nil, err
	}
	if stat.FileType == FileTypeDirectory {
		return nil, notAFileError{fmt.Errorf("path %s is a directory, not a file", file)}
	}

	dirRef, err := getRefOrEvaluate(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to get directory ref: %w", err)
	}

	return &File{
		LLB:      dir.LLB,
		Result:   dirRef,
		File:     filePath,
		Platform: dir.Platform,
		Services: dir.Services,
	}, nil
}

func (dir *Directory) FileLLB(ctx context.Context, parent dagql.ObjectResult[*Directory], file string) (*File, error) {
	err := validateFileName(file)
	if err != nil {
		return nil, err
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dagql server: %w", err)
	}

	internalSpanCtx, internalSpan := Tracer(ctx).Start(ctx, fmt.Sprintf("file %s", file),
		telemetry.Internal(),
	)
	defer telemetry.EndWithCause(internalSpan, nil)

	var fileStat *Stat
	err = srv.Select(internalSpanCtx, parent, &fileStat,
		dagql.Selector{
			Field: "stat",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(file)},
			},
		},
	)
	if err != nil {
		return nil, err
	}

	if fileStat.IsDir() {
		return nil, notAFileError{fmt.Errorf("path %s is a directory, not a file", file)}
	}

	return &File{
		LLB:      dir.LLB,
		Result:   dir.Result,
		File:     path.Join(dir.Dir, file),
		Platform: dir.Platform,
		Services: dir.Services,
	}, nil
}

type notAFileError struct {
	inner error
}

func (e notAFileError) Error() string {
	return e.inner.Error()
}

func (e notAFileError) Unwrap() error {
	return e.inner
}

type CopyFilter struct {
	Exclude   []string `default:"[]"`
	Include   []string `default:"[]"`
	Gitignore bool     `default:"false"`
}

func (cf *CopyFilter) IsEmpty() bool {
	if cf == nil {
		return true
	}
	return len(cf.Exclude) == 0 && len(cf.Include) == 0 && !cf.Gitignore
}

//nolint:gocyclo
func (dir *Directory) WithDirectory(
	ctx context.Context,
	destDir string,
	srcID *call.ID,
	filter CopyFilter,
	owner string,
) (*Directory, error) {
	dir = dir.Clone()

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current query: %w", err)
	}
	srv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, err
	}

	srcObj, err := dagql.NewID[*Directory](srcID).Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	src := srcObj.Self()

	destDir = path.Join(dir.Dir, destDir)

	dirRef, err := getRefOrEvaluate(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to get directory ref: %w", err)
	}
	srcRef, err := getRefOrEvaluate(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("failed to get source directory ref: %w", err)
	}

	canDoDirectMerge :=
		filter.IsEmpty() &&
			destDir == "/" &&
			src.Dir == "/" &&
			owner == ""

	cache := query.BuildkitCache()

	if dirRef == nil {
		// handle case where WithDirectory is called on an empty dir (i.e. scratch dir)
		// note this always occurs when creating the rebasedDir (to prevent infinite recursion)

		if canDoDirectMerge && srcRef != nil {
			dir.Result = srcRef.Clone()
			return dir, nil
		}
		newRef, err := query.BuildkitCache().New(ctx, nil, nil,
			bkcache.CachePolicyRetain,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
			bkcache.WithDescription("Directory.withDirectory source"))
		if err != nil {
			return nil, fmt.Errorf("buildkitcache.New failed: %w", err)
		}

		err = MountRef(ctx, newRef, nil, func(copyDest string, destMnt *mount.Mount) error {
			resolvedCopyDest, err := containerdfs.RootPath(copyDest, destDir)
			if err != nil {
				return err
			}
			if srcRef == nil {
				err = os.MkdirAll(resolvedCopyDest, 0755)
				if err != nil {
					return err
				}
				if owner != "" {
					ownership, err := parseDirectoryOwner(owner)
					if err != nil {
						return fmt.Errorf("failed to parse ownership %s: %w", owner, err)
					}
					if err := os.Chown(resolvedCopyDest, ownership.UID, ownership.GID); err != nil {
						return fmt.Errorf("failed to set chown %s: err", resolvedCopyDest)
					}
				}
				return nil
			}
			mounter, err := srcRef.Mount(ctx, true, nil)
			if err != nil {
				return fmt.Errorf("failed to mount source directory: %w", err)
			}
			ms, unmountSrc, err := mounter.Mount()
			if err != nil {
				return fmt.Errorf("failed to mount source directory: %w", err)
			}
			defer unmountSrc()
			if len(ms) == 0 {
				return fmt.Errorf("no mounts returned for source directory")
			}
			srcMnt := ms[0]
			lm := snapshot.LocalMounterWithMounts(ms)
			mntedSrcPath, err := lm.Mount()
			if err != nil {
				return fmt.Errorf("failed to mount source directory: %w", err)
			}
			defer lm.Unmount()
			resolvedSrcPath, err := containerdfs.RootPath(mntedSrcPath, src.Dir)
			if err != nil {
				return err
			}
			srcResolver, err := pathResolverForMount(&srcMnt, mntedSrcPath)
			if err != nil {
				return fmt.Errorf("failed to create source path resolver: %w", err)
			}
			destResolver, err := pathResolverForMount(destMnt, copyDest)
			if err != nil {
				return fmt.Errorf("failed to create destination path resolver: %w", err)
			}
			var opts []fscopy.Opt
			opts = append(opts, fscopy.WithCopyInfo(fscopy.CopyInfo{
				AlwaysReplaceExistingDestPaths: true,
				CopyDirContents:                true,
				EnableHardlinkOptimization:     true,
				SourcePathResolver:             srcResolver,
				DestPathResolver:               destResolver,
			}))
			for _, pattern := range filter.Include {
				opts = append(opts, fscopy.WithIncludePattern(pattern))
			}
			for _, pattern := range filter.Exclude {
				opts = append(opts, fscopy.WithExcludePattern(pattern))
			}
			if filter.Gitignore {
				opts = append(opts, fscopy.WithGitignore())
			}
			if owner != "" {
				ownership, err := parseDirectoryOwner(owner)
				if err != nil {
					return fmt.Errorf("failed to parse ownership %s: %w", owner, err)
				}
				opts = append(opts, fscopy.WithChown(ownership.UID, ownership.GID))
			}
			if err := fscopy.Copy(ctx, resolvedSrcPath, ".", resolvedCopyDest, ".", opts...); err != nil {
				return fmt.Errorf("failed to copy source directory: %w", err)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}

		dirRef, err = newRef.Commit(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to commit copied directory: %w", err)
		}

		dir.Result = dirRef
		return dir, nil
	}

	mergeRefs := []bkcache.ImmutableRef{dirRef}

	if canDoDirectMerge {
		// Directly merge the states together, which is lazy, uses hardlinks instead of
		// copies and caches inputs individually instead of invalidating the whole
		// chain following any modified input.
		if srcRef == nil {
			dir.Result = dirRef
			return dir, nil
		}
		mergeRefs = append(mergeRefs, srcRef)
	} else {
		// Even if we can't merge directly, we can still get some optimization by
		// copying to scratch and then merging that. This still results in an on-disk
		// copy but preserves the other caching benefits of MergeOp. This is the same
		// behavior as "COPY --link" in Dockerfiles.

		var rebasedDir dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, srv.Root(), &rebasedDir,
			dagql.Selector{Field: "directory"}, // scratch
			dagql.Selector{Field: "withDirectory", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(destDir)},
				{Name: "source", Value: dagql.NewID[*Directory](srcID)},
				{Name: "exclude", Value: asArrayInput(filter.Exclude, dagql.NewString)},
				{Name: "include", Value: asArrayInput(filter.Include, dagql.NewString)},
				{Name: "gitignore", Value: dagql.Boolean(filter.Gitignore)},
				{Name: "owner", Value: dagql.String(owner)},
			}},
		)
		if err != nil {
			return nil, err
		}

		rebasedDirRef, err := getRefOrEvaluate(ctx, rebasedDir.Self())
		if err != nil {
			return nil, fmt.Errorf("failed to get rebased dir for merging: %w", err)
		}
		mergeRefs = append(mergeRefs, rebasedDirRef)
	}

	ref, err := cache.Merge(ctx, mergeRefs, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to merge directories: %w", err)
	}
	err = ref.Finalize(ctx)
	if err != nil {
		return nil, err
	}
	dir.Result = ref
	return dir, nil
}

func copyFile(srcPath, dstPath string, tryHardlink bool) (err error) {
	if tryHardlink {
		_, err := os.Lstat(dstPath)
		switch {
		case err == nil:
			// destination already exists, remove it
			if removeErr := os.Remove(dstPath); removeErr != nil {
				return fmt.Errorf("failed to remove existing destination file %s: %w", dstPath, removeErr)
			}
		case errors.Is(err, os.ErrNotExist):
			// destination does not exist, proceed
		default:
			return fmt.Errorf("failed to stat destination file %s: %w", dstPath, err)
		}

		err = os.Link(srcPath, dstPath)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, unix.EXDEV), errors.Is(err, unix.EMLINK):
			// cross-device link or too many links, fall back to copy
			slog.ExtraDebug("hardlink file failed, falling back to copy", "source", srcPath, "destination", dstPath, "error", err)
		default:
			return fmt.Errorf("failed to hard link file from %s to %s: %w", srcPath, dstPath, err)
		}
	}

	srcStat, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	srcPerm := srcStat.Mode().Perm()
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, srcPerm)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dstPath)
		}
	}()
	defer func() {
		if dst != nil {
			dst.Close()
		}
	}()
	if err := fscopy.CopyFileContent(dst, src); err != nil {
		return err
	}
	err = dst.Close()
	if err != nil {
		return err
	}
	dst = nil

	modTime := srcStat.ModTime()
	return os.Chtimes(dstPath, modTime, modTime)
}

func isDir(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return fi.Mode().IsDir(), nil
}

func (dir *Directory) WithFile(
	ctx context.Context,
	srv *dagql.Server,
	destPath string,
	src *File,
	permissions *int,
	owner string,
) (*Directory, error) {
	dir = dir.Clone()

	srcCacheRef, err := getRefOrEvaluate(ctx, src)
	if err != nil {
		return nil, err
	}

	dirCacheRef, err := getRefOrEvaluate(ctx, dir)
	if err != nil {
		return nil, err
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	destPath = path.Join(dir.Dir, destPath)
	newRef, err := query.BuildkitCache().New(ctx, dirCacheRef, bkSessionGroup, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("withfile %s %s", destPath, filepath.Base(src.File))))
	if err != nil {
		return nil, err
	}

	var realDestPath string
	if err := MountRef(ctx, newRef, bkSessionGroup, func(root string, destMnt *mount.Mount) (rerr error) {
		mntedDestPath, err := containerdfs.RootPath(root, destPath)
		if err != nil {
			return err
		}
		destIsDir, err := isDir(mntedDestPath)
		if err != nil {
			return err
		}
		if destIsDir {
			_, srcFilename := filepath.Split(src.File)
			mntedDestPath = path.Join(mntedDestPath, srcFilename)
		}

		destPathDir, _ := filepath.Split(mntedDestPath)
		err = os.MkdirAll(filepath.Dir(destPathDir), 0755)
		if err != nil {
			return err
		}

		resolvedDestRelPath, err := filepath.Rel(root, mntedDestPath)
		if err != nil {
			return err
		}
		switch destMnt.Type {
		case "bind", "rbind":
			realDestPath = filepath.Join(destMnt.Source, resolvedDestRelPath)
		case "overlay":
			// touch the dest parent dir to trigger a copy-up of parent dirs
			// we never try to keep directory modtimes consistent right now, so
			// this is okay
			if err := os.Chtimes(destPathDir, time.Now(), time.Now()); err != nil {
				return fmt.Errorf("failed to touch overlay parent dir %s: %w", destPathDir, err)
			}

			var upperdir string
			for _, opt := range destMnt.Options {
				if strings.HasPrefix(opt, "upperdir=") {
					upperdir = strings.TrimPrefix(opt, "upperdir=")
					break
				}
			}
			if upperdir == "" {
				return fmt.Errorf("overlay mount missing upperdir option")
			}
			realDestPath = filepath.Join(upperdir, resolvedDestRelPath)
		default:
			return fmt.Errorf("unsupported mount type for destination: %s", destMnt.Type)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var realSrcPath string
	if err := MountRef(ctx, srcCacheRef, bkSessionGroup, func(root string, srcMnt *mount.Mount) (rerr error) {
		srcPath, err := containerdfs.RootPath(root, src.File)
		if err != nil {
			return err
		}
		srcResolver, err := pathResolverForMount(srcMnt, root)
		if err != nil {
			return err
		}
		realSrcPath, err = srcResolver(srcPath)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}

	tryHardlink := permissions == nil && owner == ""

	err = copyFile(realSrcPath, realDestPath, tryHardlink)
	if err != nil {
		return nil, err
	}

	if permissions != nil {
		if err := os.Chmod(realDestPath, os.FileMode(*permissions)); err != nil {
			return nil, fmt.Errorf("failed to set chmod %s: err", destPath)
		}
	}
	if owner != "" {
		ownership, err := parseDirectoryOwner(owner)
		if err != nil {
			return nil, fmt.Errorf("failed to parse ownership %s: %w", owner, err)
		}
		if err := os.Chown(realDestPath, ownership.UID, ownership.GID); err != nil {
			return nil, fmt.Errorf("failed to set chown %s: err", destPath)
		}
	}

	snap, err := newRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	dir.Result = snap
	return dir, nil
}

// TODO: address https://github.com/dagger/dagger/pull/6556/files#r1482830091
func (dir *Directory) WithFiles(
	ctx context.Context,
	srv *dagql.Server,
	destDir string,
	src []*File,
	permissions *int,
) (*Directory, error) {
	dir = dir.Clone()

	var err error
	for _, file := range src {
		dir, err = dir.WithFile(
			ctx,
			srv,
			path.Join(destDir, path.Base(file.File)),
			file,
			permissions,
			"",
		)
		if err != nil {
			return nil, err
		}
	}

	return dir, nil
}

func (dir *Directory) WithTimestamps(ctx context.Context, unix int) (*Directory, error) {
	dir = dir.Clone()
	return execInMount(ctx, dir, func(root string) error {
		resolvedDir, err := containerdfs.RootPath(root, dir.Dir)
		if err != nil {
			return err
		}
		return filepath.WalkDir(resolvedDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			modTime := time.Unix(int64(unix), 0)
			return os.Chtimes(path, modTime, modTime)
		})
	}, withSavedSnapshot("withTimestamps %d", unix))
}

func (dir *Directory) WithNewDirectory(ctx context.Context, dest string, permissions fs.FileMode) (*Directory, error) {
	dir = dir.Clone()

	dest = path.Clean(dest)
	if strings.HasPrefix(dest, "../") {
		return nil, fmt.Errorf("cannot create directory outside parent: %s", dest)
	}

	// be sure to create the file under the working directory
	dest = path.Join(dir.Dir, dest)

	if permissions == 0 {
		permissions = 0755
	}

	return execInMount(ctx, dir, func(root string) error {
		resolvedDir, err := containerdfs.RootPath(root, dest)
		if err != nil {
			return err
		}
		return TrimErrPathPrefix(os.MkdirAll(resolvedDir, permissions), root)
	}, withSavedSnapshot("withNewDirectory %s", dest))
}

// DiffLLB is legacy and will be deleted once all ops are dagops
func (dir *Directory) DiffLLB(ctx context.Context, other *Directory) (*Directory, error) {
	dir = dir.Clone()

	thisDirPath := dir.Dir
	if thisDirPath == "" {
		thisDirPath = "/"
	}
	otherDirPath := other.Dir
	if otherDirPath == "" {
		otherDirPath = "/"
	}
	if thisDirPath != otherDirPath {
		// TODO(vito): work around with llb.Copy shenanigans?
		return nil, fmt.Errorf("cannot diff with different relative paths: %q != %q", dir.Dir, other.Dir)
	}

	lowerSt, err := dir.State()
	if err != nil {
		return nil, err
	}

	upperSt, err := other.State()
	if err != nil {
		return nil, err
	}

	st := llb.
		Diff(lowerSt, upperSt).
		File(llb.Mkdir(dir.Dir, 0755, llb.WithParents(true)))
	err = dir.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	return dir, nil
}

func (dir *Directory) Diff(ctx context.Context, other *Directory) (*Directory, error) {
	dir = dir.Clone()

	thisDirPath := dir.Dir
	if thisDirPath == "" {
		thisDirPath = "/"
	}
	otherDirPath := other.Dir
	if otherDirPath == "" {
		otherDirPath = "/"
	}
	if thisDirPath != otherDirPath {
		// this shouldnt happen (since core/schema/directory.go code performs copies the directory to /)
		return nil, fmt.Errorf("internal error: Directory.diff received different relative paths: %q != %q", dir.Dir, other.Dir)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current query: %w", err)
	}
	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	thisDirRef, err := getRefOrEvaluate(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to get directory ref: %w", err)
	}
	otherDirRef, err := getRefOrEvaluate(ctx, other)
	if err != nil {
		return nil, fmt.Errorf("failed to get other directory ref: %w", err)
	}

	cache := query.BuildkitCache()

	var ref bkcache.ImmutableRef
	if thisDirRef == nil {
		// lower is nil, so the diff is just the upper ref
		ref = otherDirRef
	} else {
		ref, err = cache.Diff(ctx, thisDirRef, otherDirRef, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to diff directories: %w", err)
		}
	}

	newRef, err := cache.New(ctx, ref, bkSessionGroup, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("diff"))
	if err != nil {
		return nil, err
	}

	err = MountRef(ctx, newRef, bkSessionGroup, func(root string, _ *mount.Mount) error {
		fullPath, err := RootPathWithoutFinalSymlink(root, dir.Dir)
		if err != nil {
			return err
		}
		return os.MkdirAll(fullPath, 0755)
	})
	if err != nil {
		return nil, err
	}

	dirRef, err := newRef.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to commit diff directory: %w", err)
	}

	dir.Result = dirRef
	return dir, nil
}

func (dir *Directory) FindUp(ctx context.Context, name string, start string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name cannot be empty")
	}

	// Start from the given path or current directory
	searchPath := start
	if searchPath == "" {
		searchPath = "."
	}

	// Clean the search path
	searchPath = path.Clean(searchPath)
	if strings.HasPrefix(searchPath, "../") {
		return "", fmt.Errorf("cannot search outside parent: %s", searchPath)
	}

	currentPath := searchPath

	for {
		currentDir, err := dir.Directory(ctx, currentPath)
		if err != nil {
			return "", err
		}
		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return "", err
		}
		exists, err := currentDir.Exists(ctx, srv, name, "", true)
		if err != nil {
			return "", err
		}
		if exists {
			return path.Clean(path.Join(currentPath, name)), nil
		}
		parentPath := path.Dir(currentPath)
		if parentPath == currentPath {
			break
		}
		currentPath = parentPath
	}
	return "", nil
}

func (dir *Directory) WithChanges(ctx context.Context, changes *Changeset) (*Directory, error) {
	dir = dir.Clone()

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dagql server: %w", err)
	}

	var diffDir dagql.ObjectResult[*Directory]
	err = srv.Select(ctx, changes.Before, &diffDir,
		dagql.Selector{Field: "diff", Args: []dagql.NamedInput{
			{Name: "other", Value: dagql.NewID[*Directory](changes.After.ID())},
		}},
	)
	if err != nil {
		return nil, err
	}
	if diffDir.Self().Dir != "/" {
		return nil, fmt.Errorf("internal error: expected diff Dir path to be %q but got %q", "/", diffDir.Self().Dir)
	}

	parentRef, err := getRefOrEvaluate(ctx, dir)
	if err != nil {
		return nil, err
	}

	var diffDirRef bkcache.ImmutableRef

	if dir.IsRootDir() {
		// no need to rebase the directory, since diffDir will also be stored under the root
		diffDirRef, err = getRefOrEvaluate(ctx, diffDir.Self())
		if err != nil {
			return nil, err
		}
	} else {
		var rebasedDir dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, srv.Root(), &rebasedDir,
			dagql.Selector{Field: "directory"}, // scratch
			dagql.Selector{Field: "withDirectory", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(dir.Dir)},
				{Name: "source", Value: dagql.NewID[*Directory](diffDir.ID())},
			}},
		)
		if err != nil {
			return nil, err
		}
		diffDirRef, err = getRefOrEvaluate(ctx, rebasedDir.Self())
		if err != nil {
			return nil, err
		}
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current query: %w", err)
	}

	mergeRefs := []bkcache.ImmutableRef{parentRef, diffDirRef}
	ref, err := query.BuildkitCache().Merge(ctx, mergeRefs, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to merge directories: %w", err)
	}
	err = ref.Finalize(ctx)
	if err != nil {
		return nil, err
	}
	dir.Result = ref

	paths, err := changes.ComputePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute paths: %w", err)
	}

	if len(paths.Removed) > 0 {
		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get dagql server: %w", err)
		}

		dir, _, err = dir.Without(ctx, srv, paths.Removed...)
		if err != nil {
			return nil, fmt.Errorf("failed to remove paths: %w", err)
		}
	}

	return dir, nil
}

func (dir *Directory) Without(ctx context.Context, srv *dagql.Server, paths ...string) (_ *Directory, anyPathsRemoved bool, _ error) {
	dir = dir.Clone()
	dir, err := execInMount(ctx, dir, func(root string) error {
		for _, p := range paths {
			p = path.Join(dir.Dir, p)
			var matches []string
			if strings.Contains(p, "*") {
				var err error
				matches, err = fscopy.ResolveWildcards(root, p, true)
				if err != nil {
					return err
				}
			} else {
				matches = []string{p}
			}

			for _, m := range matches {
				fullPath, err := RootPathWithoutFinalSymlink(root, m)
				if err != nil {
					return err
				}
				_, statErr := os.Lstat(fullPath)
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				} else if statErr != nil {
					return statErr
				}

				anyPathsRemoved = true
				err = os.RemoveAll(fullPath)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}, withSavedSnapshot("without %s", strings.Join(paths, ",")))
	if err != nil {
		return nil, false, err
	}
	return dir, anyPathsRemoved, nil
}

func (dir *Directory) Exists(ctx context.Context, srv *dagql.Server, targetPath string, targetType ExistsType, doNotFollowSymlinks bool) (bool, error) {
	stat, err := dir.Stat(ctx, srv, targetPath, doNotFollowSymlinks || targetType == ExistsTypeSymlink)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	switch targetType {
	case ExistsTypeDirectory:
		return stat.FileType == FileTypeDirectory, nil
	case ExistsTypeRegular:
		return stat.FileType == FileTypeRegular, nil
	case ExistsTypeSymlink:
		return stat.FileType == FileTypeSymlink, nil
	case "":
		return true, nil
	default:
		return false, fmt.Errorf("invalid path type %s", targetType)
	}
}

type Stat struct {
	Size        int      `field:"true" doc:"file size"`
	Name        string   `field:"true" doc:"file name"`
	FileType    FileType `field:"true" doc:"file type"`
	Permissions int      `field:"true" doc:"permission bits"`
}

func (*Stat) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Stat",
		NonNull:   false,
	}
}

func (*Stat) TypeDescription() string {
	return "A file or directory status object."
}

func (s *Stat) IsDir() bool {
	return s != nil && s.FileType == FileTypeDirectory
}

func (s Stat) Clone() *Stat {
	cp := s
	return &cp
}

func (dir *Directory) Stat(ctx context.Context, srv *dagql.Server, targetPath string, doNotFollowSymlinks bool) (*Stat, error) {
	if targetPath == "" {
		return nil, &os.PathError{Op: "stat", Path: targetPath, Err: syscall.ENOENT}
	}

	immutableRef, err := getRefOrEvaluate(ctx, dir)
	if err != nil {
		return nil, err
	}
	if immutableRef == nil {
		return nil, &os.PathError{Op: "stat", Path: targetPath, Err: syscall.ENOENT}
	}

	bkSessionGroup := requiresBuildkitSessionGroup(ctx)

	osStatFunc := os.Stat
	rootPathFunc := containerdfs.RootPath
	if doNotFollowSymlinks {
		// symlink testing requires the Lstat call, which does NOT follow symlinks
		osStatFunc = os.Lstat
		// similarly, containerdfs.RootPath can't be used, since it follows symlinks
		rootPathFunc = RootPathWithoutFinalSymlink
	}

	var fileInfo os.FileInfo
	err = MountRef(ctx, immutableRef, bkSessionGroup, func(root string, _ *mount.Mount) error {
		resolvedPath, err := rootPathFunc(root, path.Join(dir.Dir, targetPath))
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
	}

	if m.IsDir() {
		stat.FileType = FileTypeDirectory
	} else if m.IsRegular() {
		stat.FileType = FileTypeRegular
	} else if m&fs.ModeSymlink != 0 {
		stat.FileType = FileTypeSymlink
	} else {
		stat.FileType = FileTypeUnknown
	}

	return stat, nil
}

func (dir *Directory) Export(ctx context.Context, destPath string, merge bool) (rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("failed to get buildkit client: %w", err)
	}

	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("export directory %s to host %s", dir.Dir, destPath))
	defer telemetry.EndWithCause(span, &rerr)

	root, closer, err := mountObj(ctx, dir)
	if err != nil {
		return fmt.Errorf("failed to mount directory: %w", err)
	}
	defer closer(false)

	root, err = containerdfs.RootPath(root, dir.Dir)
	if err != nil {
		return err
	}

	return bk.LocalDirExport(ctx, root, destPath, merge, nil)
}

// Root removes any relative path from the directory.
func (dir *Directory) Root() (*Directory, error) {
	dir = dir.Clone()
	dir.Dir = "/"
	return dir, nil
}

func (dir *Directory) WithSymlink(ctx context.Context, srv *dagql.Server, target, linkName string) (*Directory, error) {
	dir = dir.Clone()
	return execInMount(ctx, dir, func(root string) error {
		linkName = path.Join(dir.Dir, linkName)
		linkDir, linkBasename := filepath.Split(linkName)
		resolvedLinkDir, err := containerdfs.RootPath(root, linkDir)
		if err != nil {
			return err
		}
		err = os.MkdirAll(resolvedLinkDir, 0755)
		if err != nil {
			return err
		}
		resolvedLinkName := path.Join(resolvedLinkDir, linkBasename)
		return os.Symlink(target, resolvedLinkName)
	}, withSavedSnapshot("symlink %s -> %s", linkName, target))
}

func (dir *Directory) Mount(ctx context.Context, f func(string) error) error {
	return mountLLB(ctx, dir.LLB, func(root string) error {
		src, err := containerdfs.RootPath(root, dir.Dir)
		if err != nil {
			return err
		}
		return f(src)
	})
}

func parseDirectoryOwner(owner string) (*Ownership, error) {
	uidStr, gidStr, hasGroup := strings.Cut(owner, ":")
	var uid, gid int
	uid, err := parseUID(uidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid uid %q: %w", uidStr, err)
	}
	if hasGroup {
		gid, err = parseUID(gidStr)
		if err != nil {
			return nil, fmt.Errorf("invalid gid %q: %w", gidStr, err)
		}
	}
	if !hasGroup {
		gid = uid
	}

	return &Ownership{
		UID: uid,
		GID: gid,
	}, nil
}

func (dir *Directory) Chown(ctx context.Context, chownPath string, owner string) (*Directory, error) {
	ownership, err := parseDirectoryOwner(owner)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ownership %s: %w", owner, err)
	}

	dir = dir.Clone()
	return execInMount(ctx, dir, func(root string) error {
		chownPath := path.Join(dir.Dir, chownPath)
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
	}, withSavedSnapshot("chown %s %s", chownPath, owner))
}

func validateFileName(file string) error {
	baseFileName := filepath.Base(file)
	if len(baseFileName) > 255 {
		return errors.New("File name length exceeds the maximum supported 255 characters")
	}
	return nil
}

func SupportsDirSlash(ctx context.Context) bool {
	return Supports(ctx, "v0.17.0")
}

// TODO deprecate ExistsType in favor of FileType

type ExistsType string

var ExistsTypes = dagql.NewEnum[ExistsType]()

var (

	// NOTE calling ExistsTypes.Register("DIRECTORY", ...) will generate:
	// const (
	//     ExistsTypeDirectory ExistsType = "DIRECTORY"
	//     Directory ExistsType = ExistsTypeDirectory
	// )
	// which will conflict with "type Directory struct { ... }",
	// therefore everything will have a _TYPE suffix to avoid naming conflicts

	ExistsTypeRegular = ExistsTypes.Register("REGULAR_TYPE",
		"Tests path is a regular file")
	ExistsTypeDirectory = ExistsTypes.Register("DIRECTORY_TYPE",
		"Tests path is a directory")
	ExistsTypeSymlink = ExistsTypes.Register("SYMLINK_TYPE",
		"Tests path is a symlink")
)

func (et ExistsType) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ExistsType",
		NonNull:   false,
	}
}

func (et ExistsType) TypeDescription() string {
	return "File type."
}

func (et ExistsType) Decoder() dagql.InputDecoder {
	return ExistsTypes
}

func (et ExistsType) ToLiteral() call.Literal {
	return ExistsTypes.Literal(et)
}

func (et ExistsType) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(et))
}

func (et *ExistsType) UnmarshalJSON(payload []byte) error {
	var str string
	if err := json.Unmarshal(payload, &str); err != nil {
		return err
	}
	*et = ExistsType(str)
	return nil
}

type FileType string

var FileTypes = dagql.NewEnum[FileType]()

var (
	FileTypeRegular   = FileTypes.RegisterView("REGULAR", enumView, "regular file type")
	FileTypeDirectory = FileTypes.RegisterView("DIRECTORY", enumView, "directory file type")
	FileTypeSymlink   = FileTypes.RegisterView("SYMLINK", enumView, "symlink file type")
	FileTypeUnknown   = FileTypes.Register("UNKNOWN", "unknown file type")

	_ = FileTypes.AliasView("REGULAR_TYPE", "REGULAR", enumView)
	_ = FileTypes.AliasView("DIRECTORY_TYPE", "DIRECTORY", enumView)
	_ = FileTypes.AliasView("SYMLINK_TYPE", "SYMLINK", enumView)
)

func (ft FileType) Type() *ast.Type {
	return &ast.Type{
		NamedType: "FileType",
		NonNull:   false,
	}
}

func (ft FileType) TypeDescription() string {
	return "File type."
}

func (ft FileType) Decoder() dagql.InputDecoder {
	return FileTypes
}

func (ft FileType) ToLiteral() call.Literal {
	return FileTypes.Literal(ft)
}

func (ft FileType) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(ft))
}

func (ft *FileType) UnmarshalJSON(payload []byte) error {
	var str string
	if err := json.Unmarshal(payload, &str); err != nil {
		return err
	}
	*ft = FileType(str)
	return nil
}

func FileModeToFileType(m fs.FileMode) FileType {
	if m.IsDir() {
		return FileTypeDirectory
	} else if m.IsRegular() {
		return FileTypeRegular
	} else if m&fs.ModeSymlink != 0 {
		return FileTypeSymlink
	} else {
		return FileTypeUnknown
	}
}
