package core

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/patternmatcher"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	fstypes "github.com/tonistiigi/fsutil/types"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/progrock"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
)

// Directory is a content-addressed directory.
type Directory struct {
	Query *Query

	LLB      *pb.Definition
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

var _ HasPBDefinitions = (*Directory)(nil)

func (dir *Directory) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if dir.LLB != nil {
		defs = append(defs, dir.LLB)
	}
	for _, bnd := range dir.Services {
		ctr := bnd.Service.Container
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

func NewDirectory(query *Query, def *pb.Definition, dir string, platform Platform, services ServiceBindings) *Directory {
	if query == nil {
		panic("query must be non-nil")
	}
	return &Directory{
		Query:    query,
		LLB:      def,
		Dir:      dir,
		Platform: platform,
		Services: services,
	}
}

func NewScratchDirectory(query *Query, platform Platform) *Directory {
	if query == nil {
		panic("query must be non-nil")
	}
	return &Directory{
		Query:    query,
		Dir:      "/",
		Platform: platform,
	}
}

func NewDirectorySt(ctx context.Context, query *Query, st llb.State, dir string, platform Platform, services ServiceBindings) (*Directory, error) {
	if query == nil {
		panic("query must be non-nil")
	}
	def, err := st.Marshal(ctx, llb.Platform(platform.Spec()))
	if err != nil {
		return nil, err
	}

	return NewDirectory(query, def.ToPB(), dir, platform, services), nil
}

// Clone returns a deep copy of the container suitable for modifying in a
// WithXXX method.
func (dir *Directory) Clone() *Directory {
	cp := *dir
	cp.Services = cloneSlice(cp.Services)
	return &cp
}

var _ pipeline.Pipelineable = (*Directory)(nil)

func (dir *Directory) PipelinePath() pipeline.Path {
	return dir.Query.Pipeline
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
	def, err := st.Marshal(ctx, llb.Platform(dir.Platform.Spec()))
	if err != nil {
		return nil
	}

	dir.LLB = def.ToPB()
	return nil
}

func (dir *Directory) WithPipeline(ctx context.Context, name, description string, labels []pipeline.Label) (*Directory, error) {
	dir = dir.Clone()
	dir.Query = dir.Query.WithPipeline(name, description, labels)
	return dir, nil
}

func (dir *Directory) Evaluate(ctx context.Context) (*buildkit.Result, error) {
	if dir.LLB == nil {
		return nil, nil
	}

	svcs := dir.Query.Services
	bk := dir.Query.Buildkit

	detach, _, err := svcs.StartBindings(ctx, dir.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	return bk.Solve(ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: dir.LLB,
	})
}

func (dir *Directory) Stat(ctx context.Context, bk *buildkit.Client, svcs *Services, src string) (*fstypes.Stat, error) {
	src = path.Join(dir.Dir, src)

	detach, _, err := svcs.StartBindings(ctx, dir.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	res, err := bk.Solve(ctx, bkgw.SolveRequest{
		Definition: dir.LLB,
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
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

		return nil, fmt.Errorf("%s: no such file or directory", src)
	}

	return ref.StatFile(ctx, bkgw.StatRequest{
		Path: src,
	})
}

func (dir *Directory) Entries(ctx context.Context, src string) ([]string, error) {
	src = path.Join(dir.Dir, src)

	svcs := dir.Query.Services
	bk := dir.Query.Buildkit

	detach, _, err := svcs.StartBindings(ctx, dir.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	res, err := bk.Solve(ctx, bkgw.SolveRequest{
		Definition: dir.LLB,
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}
	// empty directory, i.e. llb.Scratch()
	if ref == nil {
		if clean := path.Clean(src); clean == "." || clean == "/" {
			return []string{}, nil
		}
		return nil, fmt.Errorf("%s: no such file or directory", src)
	}

	entries, err := ref.ReadDir(ctx, bkgw.ReadDirRequest{
		Path: src,
	})
	if err != nil {
		return nil, err
	}

	paths := []string{}
	for _, entry := range entries {
		paths = append(paths, entry.GetPath())
	}

	return paths, nil
}

// Glob returns a list of files that matches the given pattern.
//
// Note(TomChv): Instead of handling the recursive manually, we could update cacheutil.ReadDir
// so it will only mount and unmount the filesystem one time.
// However, this requires to maintain buildkit code and is not mandatory for now until
// we hit performances issues.
func (dir *Directory) Glob(ctx context.Context, src string, pattern string) ([]string, error) {
	src = path.Join(dir.Dir, src)

	svcs := dir.Query.Services
	bk := dir.Query.Buildkit

	detach, _, err := svcs.StartBindings(ctx, dir.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	res, err := bk.Solve(ctx, bkgw.SolveRequest{
		Definition: dir.LLB,
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}
	// empty directory, i.e. llb.Scratch()
	if ref == nil {
		if clean := path.Clean(src); clean == "." || clean == "/" {
			return []string{}, nil
		}
		return nil, fmt.Errorf("%s: no such file or directory", src)
	}

	entries, err := ref.ReadDir(ctx, bkgw.ReadDirRequest{
		Path:           src,
		IncludePattern: pattern,
	})
	if err != nil {
		return nil, err
	}

	paths := []string{}
	for _, entry := range entries {
		entryPath := entry.GetPath()

		if src != "." {
			// We remove `/` because src is `/` by default, but it obfuscates
			// the output.
			entryPath = strings.TrimPrefix(filepath.Join(src, entryPath), "/")
		}

		// We use the same pattern matching function as Buildkit to handle
		// recursive strategy.
		match, err := patternmatcher.MatchesOrParentMatches(entryPath, []string{pattern})
		if err != nil {
			return nil, err
		}

		if match {
			paths = append(paths, entryPath)
		}

		// Handle recursive option
		if entry.IsDir() {
			subEntries, err := dir.Glob(ctx, entryPath, pattern)
			if err != nil {
				return nil, err
			}

			paths = append(paths, subEntries...)
		}
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

func (dir *Directory) Directory(ctx context.Context, subdir string) (*Directory, error) {
	dir = dir.Clone()

	svcs := dir.Query.Services
	bk := dir.Query.Buildkit

	dir.Dir = path.Join(dir.Dir, subdir)

	// check that the directory actually exists so the user gets an error earlier
	// rather than when the dir is used
	info, err := dir.Stat(ctx, bk, svcs, ".")
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("path %s is a file, not a directory", subdir)
	}

	return dir, nil
}

func (dir *Directory) File(ctx context.Context, file string) (*File, error) {
	svcs := dir.Query.Services
	bk := dir.Query.Buildkit

	err := validateFileName(file)
	if err != nil {
		return nil, err
	}

	// check that the file actually exists so the user gets an error earlier
	// rather than when the file is used
	info, err := dir.Stat(ctx, bk, svcs, file)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return nil, fmt.Errorf("path %s is a directory, not a file", file)
	}

	return &File{
		Query:    dir.Query,
		LLB:      dir.LLB,
		File:     path.Join(dir.Dir, file),
		Platform: dir.Platform,
		Services: dir.Services,
	}, nil
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

	dir.Services.Merge(src.Services)

	return dir, nil
}

func (dir *Directory) WithFile(
	ctx context.Context,
	destPath string,
	src *File,
	permissions *int,
	owner *Ownership,
) (*Directory, error) {
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
		Dest:         destSt,
		DestDir:      path.Join(dir.Dir, path.Dir(destPath)),
		DestFileName: path.Base(destPath),
		Src:          srcSt,
		SrcDir:       path.Dir(src.File),
		SrcFileName:  path.Base(src.File),
		Permissions:  permissions,
		Owner:        owner,
	})); err != nil {
		return nil, err
	}

	dir.Services.Merge(src.Services)

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
	return llb.Merge(mergeStates, llb.WithCustomName(buildkit.InternalPrefix+"merge"))
}

func (dir *Directory) WithTimestamps(ctx context.Context, unix int) (*Directory, error) {
	dir = dir.Clone()

	st, err := dir.State()
	if err != nil {
		return nil, err
	}

	t := time.Unix(int64(unix), 0)
	st = llb.Scratch().File(
		llb.Copy(st, dir.Dir, ".", &llb.CopyInfo{
			CopyDirContentsOnly: true,
			CreatedTime:         &t,
		}),
	)

	err = dir.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	dir.Dir = ""

	return dir, nil
}

func (dir *Directory) WithNewDirectory(ctx context.Context, dest string, permissions fs.FileMode) (*Directory, error) {
	dir = dir.Clone()

	dest = path.Clean(dest)
	if strings.HasPrefix(dest, "../") {
		return nil, fmt.Errorf("cannot create directory outside parent: %s", dest)
	}

	// be sure to create the file under the working directory
	dest = path.Join(dir.Dir, dest)

	st, err := dir.State()
	if err != nil {
		return nil, err
	}

	if permissions == 0 {
		permissions = 0755
	}

	st = st.File(llb.Mkdir(dest, permissions, llb.WithParents(true)))

	err = dir.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	return dir, nil
}

func (dir *Directory) Diff(ctx context.Context, other *Directory) (*Directory, error) {
	dir = dir.Clone()

	if dir.Dir != other.Dir {
		// TODO(vito): work around with llb.Copy shenanigans?
		return nil, fmt.Errorf("TODO: cannot diff with different relative paths: %q != %q", dir.Dir, other.Dir)
	}

	if !reflect.DeepEqual(dir.Platform, other.Platform) {
		// TODO(vito): work around with llb.Copy shenanigans?
		return nil, fmt.Errorf("TODO: cannot diff across platforms: %+v != %+v", dir.Platform, other.Platform)
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

func (dir *Directory) Without(ctx context.Context, path string) (*Directory, error) {
	dir = dir.Clone()

	st, err := dir.State()
	if err != nil {
		return nil, err
	}

	path = filepath.Join(dir.Dir, path)
	err = dir.SetState(ctx, st.File(llb.Rm(path, llb.WithAllowWildcard(true), llb.WithAllowNotFound(true))))
	if err != nil {
		return nil, err
	}

	return dir, nil
}

func (dir *Directory) Export(ctx context.Context, destPath string) (rerr error) {
	svcs := dir.Query.Services
	bk := dir.Query.Buildkit

	var defPB *pb.Definition
	if dir.Dir != "" {
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

	rec := progrock.FromContext(ctx)

	vtx := rec.Vertex(
		digest.Digest(identity.NewID()),
		fmt.Sprintf("export directory %s to host %s", dir.Dir, destPath),
	)
	defer vtx.Done(rerr)

	detach, _, err := svcs.StartBindings(ctx, dir.Services)
	if err != nil {
		return err
	}
	defer detach()

	return bk.LocalDirExport(ctx, defPB, destPath)
}

// Root removes any relative path from the directory.
func (dir *Directory) Root() (*Directory, error) {
	dir = dir.Clone()
	dir.Dir = "/"
	return dir, nil
}

// AsBlob converts this directory into a stable content addressed blob, valid for the duration of the current
// session. Currently only used internally to support local module sources.
func (dir *Directory) AsBlob(
	ctx context.Context,
	srv *dagql.Server,
) (inst dagql.Instance[*Directory], rerr error) {
	// currently, all layers need to be squashed to 1 for DefToBlob to work, so
	// unconditionally copy to scratch
	src, err := dir.State()
	if err != nil {
		return inst, fmt.Errorf("failed to get dir state: %w", err)
	}
	src = llb.Scratch().File(llb.Copy(src, dir.Dir, ".", &llb.CopyInfo{
		CopyDirContentsOnly: true,
	}))
	def, err := src.Marshal(ctx, llb.Platform(dir.Platform.Spec()))
	if err != nil {
		return inst, fmt.Errorf("failed to marshal dir state: %w", err)
	}
	pbDef := def.ToPB()

	_, desc, err := dir.Query.Buildkit.DefToBlob(ctx, pbDef)
	if err != nil {
		return inst, fmt.Errorf("failed to get blob descriptor: %w", err)
	}

	inst, err = LoadBlob(ctx, srv, desc)
	if err != nil {
		return inst, fmt.Errorf("failed to load blob: %w", err)
	}
	return inst, nil
}

func validateFileName(file string) error {
	baseFileName := filepath.Base(file)
	if len(baseFileName) > 255 {
		return errors.Errorf("File name length exceeds the maximum supported 255 characters")
	}
	return nil
}
