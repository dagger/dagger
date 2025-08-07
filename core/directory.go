package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	containerdfs "github.com/containerd/continuity/fs"
	fscopy "github.com/dagger/dagger/engine/sources/local/copy"
	"github.com/dagger/dagger/util/patternmatcher"
	"github.com/dustin/go-humanize"
	bkcache "github.com/moby/buildkit/cache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
	fstypes "github.com/tonistiigi/fsutil/types"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
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

func (dir *Directory) Stat(ctx context.Context, bk *buildkit.Client, src string) (*fstypes.Stat, error) {
	src = path.Join(dir.Dir, src)

	res, err := bk.Solve(ctx, bkgw.SolveRequest{
		Definition: dir.LLB,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to solve: %w", err)
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, fmt.Errorf("failed to get single ref: %w", err)
	}
	// empty directory, i.e. llb.Scratch()
	if ref == nil {
		if clean := path.Clean(src); clean == "." || clean == "/" {
			// fake out a reasonable response
			return &fstypes.Stat{
				Path: src,
				Mode: uint32(fs.ModeDir),
			}, nil
		}

		return nil, fmt.Errorf("%s: %w", src, syscall.ENOENT)
	}

	st, err := ref.StatFile(ctx, bkgw.StatRequest{
		Path: src,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to stat file %s: %w", src, err)
	}
	return st, nil
}

func (dir *Directory) Entries(ctx context.Context, src string) ([]string, error) {
	fmt.Printf("ACB dir.Entries was called\n")
	src = path.Join(dir.Dir, src)
	paths := []string{}
	useSlash, err := SupportsDirSlash(ctx)
	if err != nil {
		return nil, err
	}
	_, err = execInMount(ctx, dir, func(root string) error {
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
			return nil, fmt.Errorf("%s: no such file or directory", src)
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

	useSlash, err := SupportsDirSlash(ctx)
	if err != nil {
		return nil, err
	}
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
	err = MountRef(ctx, newRef, bkSessionGroup, func(root string) (rerr error) {
		resolvedDir, err := containerdfs.RootPath(root, dir.Dir)
		if err != nil {
			return err
		}
		apply := exec.Command("git", "apply", "-")
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

func (dir *Directory) Directory(ctx context.Context, subdir string) (*Directory, error) {
	dir = dir.Clone()

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	dir.Dir = path.Join(dir.Dir, subdir)

	// check that the directory actually exists so the user gets an error earlier
	// rather than when the dir is used
	info, err := dir.Stat(ctx, bk, ".")
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("path %s is a file, not a directory", subdir)
	}

	return dir, nil
}

func (dir *Directory) File(ctx context.Context, file string) (*File, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	err = validateFileName(file)
	if err != nil {
		return nil, err
	}

	// check that the file actually exists so the user gets an error earlier
	// rather than when the file is used
	info, err := dir.Stat(ctx, bk, file)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return nil, fmt.Errorf("path %s is a directory, not a file", file)
	}

	return &File{
		LLB:      dir.LLB,
		Result:   dir.Result,
		File:     path.Join(dir.Dir, file),
		Platform: dir.Platform,
		Services: dir.Services,
	}, nil
}

type CopyFilter struct {
	Exclude []string `default:"[]"`
	Include []string `default:"[]"`
}

func (dir *Directory) WithDirectory(ctx context.Context, destDir string, src *Directory, filter CopyFilter, owner *Ownership) (*Directory, error) {
	dir = dir.Clone()

	destSt, err := dir.State()
	if err != nil {
		return nil, err
	}

	srcSt, err := src.State()
	if err != nil {
		return nil, err
	}

	if err := dir.SetState(ctx, mergeStates(mergeStateInput{
		Dest:            destSt,
		DestDir:         path.Join(dir.Dir, destDir),
		Src:             srcSt,
		SrcDir:          src.Dir,
		IncludePatterns: filter.Include,
		ExcludePatterns: filter.Exclude,
		Owner:           owner,
	})); err != nil {
		return nil, err
	}

	return dir, nil
}

func copyFile(srcPath, dstPath string) (err error) {
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
	_, err = io.Copy(dst, src)
	if err != nil {
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
	owner *Ownership,
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
		bkcache.WithDescription(fmt.Sprintf("withfile %s %s", destPath, src.File)))
	if err != nil {
		return nil, err
	}
	err = MountRef(ctx, newRef, bkSessionGroup, func(dirRoot string) error {
		destPath, err := containerdfs.RootPath(dirRoot, destPath)
		if err != nil {
			return err
		}
		destIsDir, err := isDir(destPath)
		if err != nil {
			return err
		}
		if destIsDir {
			_, srcFilename := filepath.Split(src.File)
			destPath = path.Join(destPath, srcFilename)
		}
		destPathDir, _ := filepath.Split(destPath)
		err = os.MkdirAll(filepath.Dir(destPathDir), 0755)
		if err != nil {
			return err
		}
		err = MountRef(ctx, srcCacheRef, bkSessionGroup, func(srcRoot string) error {
			srcPath, err := containerdfs.RootPath(srcRoot, src.File)
			if err != nil {
				return err
			}
			return copyFile(srcPath, destPath)
		})
		if err != nil {
			return err
		}
		if permissions != nil {
			if err := os.Chmod(destPath, os.FileMode(*permissions)); err != nil {
				return fmt.Errorf("failed to set chmod %s: err", destPath)
			}
		}
		if owner != nil {
			if err := os.Chown(destPath, owner.UID, owner.GID); err != nil {
				return fmt.Errorf("failed to set chown %s: err", destPath)
			}
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

// TODO: address https://github.com/dagger/dagger/pull/6556/files#r1482830091
func (dir *Directory) WithFiles(
	ctx context.Context,
	srv *dagql.Server,
	destDir string,
	src []*File,
	permissions *int,
	owner *Ownership,
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
			owner,
		)
		if err != nil {
			return nil, err
		}
	}

	return dir, nil
}

type mergeStateInput struct {
	Dest         llb.State
	DestDir      string
	DestFileName string

	Src         llb.State
	SrcDir      string
	SrcFileName string

	IncludePatterns []string
	ExcludePatterns []string

	Permissions *int
	Owner       *Ownership
}

func mergeStates(input mergeStateInput) llb.State {
	input.DestDir = path.Join("/", input.DestDir)
	input.SrcDir = path.Join("/", input.SrcDir)

	copyInfo := &llb.CopyInfo{
		CreateDestPath:      true,
		CopyDirContentsOnly: true,
		IncludePatterns:     input.IncludePatterns,
		ExcludePatterns:     input.ExcludePatterns,
	}
	if input.DestFileName == "" && input.SrcFileName != "" {
		input.DestFileName = input.SrcFileName
	}
	if input.Permissions != nil {
		fm := fs.FileMode(*input.Permissions)
		copyInfo.Mode = &fm
	}
	if input.Owner != nil {
		input.Owner.Opt().SetCopyOption(copyInfo)
	}

	// MergeOp currently only supports merging the "/" of states together without any
	// modifications or filtering
	canDoDirectMerge := copyInfo.Mode == nil &&
		copyInfo.ChownOpt == nil &&
		len(copyInfo.ExcludePatterns) == 0 &&
		len(copyInfo.IncludePatterns) == 0 &&
		input.DestDir == "/" &&
		input.SrcDir == "/" &&
		// TODO:(sipsma) we could support direct merge-op with individual files if we can verify
		// there are no other files in the dir, but doing so by just calling ReadDir would result
		// in unlazying the inputs, which defeats some of the performance benefits of merge-op.
		input.DestFileName == "" &&
		input.SrcFileName == ""

	mergeStates := []llb.State{input.Dest}
	if canDoDirectMerge {
		// Directly merge the states together, which is lazy, uses hardlinks instead of
		// copies and caches inputs individually instead of invalidating the whole
		// chain following any modified input.
		mergeStates = append(mergeStates, input.Src)
	} else {
		// Even if we can't merge directly, we can still get some optimization by
		// copying to scratch and then merging that. This still results in an on-disk
		// copy but preserves the other caching benefits of MergeOp. This is the same
		// behavior as "COPY --link" in Dockerfiles.
		mergeStates = append(mergeStates, llb.Scratch().File(llb.Copy(
			input.Src, path.Join(input.SrcDir, input.SrcFileName), path.Join(input.DestDir, input.DestFileName), copyInfo,
		)))
	}
	return llb.Merge(mergeStates)
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
		return os.MkdirAll(resolvedDir, permissions)
	}, withSavedSnapshot("withNewDirectory %s", dest))
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

	err = dir.SetState(ctx, llb.Diff(lowerSt, upperSt))
	if err != nil {
		return nil, err
	}

	return dir, nil
}

func (dir *Directory) Without(ctx context.Context, srv *dagql.Server, paths ...string) (*Directory, error) {
	dir = dir.Clone()
	return execInMount(ctx, dir, func(root string) error {
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
				err = os.RemoveAll(fullPath)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}, withSavedSnapshot("without %s", strings.Join(paths, ",")))
}

func (dir *Directory) Exists(ctx context.Context, srv *dagql.Server, targetPath string, targetType ExistsType, doNotFollowSymlinks bool) (bool, error) {
	res, err := dir.Evaluate(ctx)
	if err != nil {
		return false, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return false, err
	}
	if ref == nil {
		return false, nil
	}

	immutableRef, err := ref.CacheRef(ctx)
	if err != nil {
		return false, err
	}

	osStatFunc := os.Stat
	if targetType == ExistsTypeSymlink || doNotFollowSymlinks {
		// symlink testing requires the Lstat call, which does NOT follow symlinks
		osStatFunc = os.Lstat
	}

	var fileInfo os.FileInfo
	err = MountRef(ctx, immutableRef, nil, func(root string) error {
		fileInfo, err = osStatFunc(path.Join(root, dir.Dir, targetPath))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
	if err != nil {
		return false, err
	}
	if fileInfo == nil {
		return false, nil // ErrNotExist occurred
	}

	m := fileInfo.Mode()

	switch targetType {
	case ExistsTypeDirectory:
		return m.IsDir(), nil
	case ExistsTypeRegular:
		return m.IsRegular(), nil
	case ExistsTypeSymlink:
		return m&fs.ModeSymlink != 0, nil
	case "":
		return true, nil
	default:
		return false, fmt.Errorf("invalid path type %s", targetType)
	}
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

	var defPB *pb.Definition
	if dir.Dir != "" && dir.Dir != "/" {
		src, err := dir.State()
		if err != nil {
			return err
		}
		src = llb.Scratch().File(llb.Copy(src, dir.Dir, ".", &llb.CopyInfo{
			CopyDirContentsOnly: true,
		}))

		def, err := src.Marshal(ctx, llb.Platform(dir.Platform.Spec()))
		if err != nil {
			return err
		}
		defPB = def.ToPB()
	} else {
		defPB = dir.LLB
	}

	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("export directory %s to host %s", dir.Dir, destPath))
	defer telemetry.End(span, func() error { return rerr })

	return bk.LocalDirExport(ctx, defPB, destPath, merge)
}

// Root removes any relative path from the directory.
func (dir *Directory) Root() (*Directory, error) {
	dir = dir.Clone()
	dir.Dir = "/"
	return dir, nil
}

func (dir *Directory) WithSymlink(ctx context.Context, srv *dagql.Server, target, linkName string) (*Directory, error) {
	fmt.Printf("ACB WithSymlink was called %s -> %s\n", linkName, target)
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
		err = os.MkdirAll(resolvedLinkDir+"/"+fmt.Sprintf("hello.%v", time.Now().UnixNano()), 0755)
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

func validateFileName(file string) error {
	baseFileName := filepath.Base(file)
	if len(baseFileName) > 255 {
		return errors.Errorf("File name length exceeds the maximum supported 255 characters")
	}
	return nil
}

func SupportsDirSlash(ctx context.Context) (bool, error) {
	return Supports(ctx, "v0.17.0")
}

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
