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

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	fstypes "github.com/tonistiigi/fsutil/types"
)

// Directory is a content-addressed directory.
type Directory struct {
	LLB      *pb.Definition `json:"llb"`
	Dir      string         `json:"dir"`
	Platform specs.Platform `json:"platform"`
	Pipeline pipeline.Path  `json:"pipeline"`

	// Services necessary to provision the directory.
	Services ServiceBindings `json:"services,omitempty"`
}

func NewDirectory(ctx context.Context, def *pb.Definition, dir string, pipeline pipeline.Path, platform specs.Platform, services ServiceBindings) *Directory {
	return &Directory{
		LLB:      def,
		Dir:      dir,
		Platform: platform,
		Pipeline: pipeline.Copy(),
		Services: services,
	}
}

func NewScratchDirectory(pipeline pipeline.Path, platform specs.Platform) *Directory {
	return &Directory{
		Dir:      "/",
		Platform: platform,
		Pipeline: pipeline.Copy(),
	}
}

func NewDirectorySt(ctx context.Context, st llb.State, dir string, pipeline pipeline.Path, platform specs.Platform, services ServiceBindings) (*Directory, error) {
	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	return NewDirectory(ctx, def.ToPB(), dir, pipeline, platform, services), nil
}

// Clone returns a deep copy of the container suitable for modifying in a
// WithXXX method.
func (dir *Directory) Clone() *Directory {
	cp := *dir
	cp.Pipeline = cloneSlice(cp.Pipeline)
	cp.Services = cloneMap(cp.Services)
	return &cp
}

// DirectoryID is an opaque value representing a content-addressed directory.
type DirectoryID string

func (id DirectoryID) String() string {
	return string(id)
}

// DirectoryID is digestible so that smaller hashes can be displayed in
// --debug vertex names.
var _ Digestible = DirectoryID("")

func (id DirectoryID) Digest() (digest.Digest, error) {
	dir, err := id.ToDirectory()
	if err != nil {
		return "", err
	}
	return dir.Digest()
}

// ToDirectory converts the ID into a real Directory.
func (id DirectoryID) ToDirectory() (*Directory, error) {
	var dir Directory

	if id == "" {
		return &dir, nil
	}

	if err := resourceid.Decode(&dir, id); err != nil {
		return nil, err
	}

	return &dir, nil
}

// ID marshals the directory into a content-addressed ID.
func (dir *Directory) ID() (DirectoryID, error) {
	return resourceid.Encode[DirectoryID](dir)
}

var _ pipeline.Pipelineable = (*Directory)(nil)

func (dir *Directory) PipelinePath() pipeline.Path {
	// TODO(vito): test
	return dir.Pipeline
}

// Directory is digestible so that it can be recorded as an output of the
// --debug vertex that created it.
var _ Digestible = (*Directory)(nil)

// Digest returns the directory's content hash.
func (dir *Directory) Digest() (digest.Digest, error) {
	return stableDigest(dir)
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
	def, err := st.Marshal(ctx, llb.Platform(dir.Platform))
	if err != nil {
		return nil
	}

	dir.LLB = def.ToPB()
	return nil
}

func (dir *Directory) WithPipeline(ctx context.Context, name, description string, labels []pipeline.Label) (*Directory, error) {
	dir = dir.Clone()
	dir.Pipeline = dir.Pipeline.Add(pipeline.Pipeline{
		Name:        name,
		Description: description,
		Labels:      labels,
	})
	return dir, nil
}

func (dir *Directory) Evaluate(ctx context.Context, bk *buildkit.Client) error {
	if dir.LLB == nil {
		return nil
	}
	_, err := WithServices(ctx, bk, dir.Services, func() (*buildkit.Result, error) {
		return bk.Solve(ctx, bkgw.SolveRequest{
			Evaluate:   true,
			Definition: dir.LLB,
		})
	})
	return err
}

func (dir *Directory) Stat(ctx context.Context, bk *buildkit.Client, src string) (*fstypes.Stat, error) {
	src = path.Join(dir.Dir, src)

	return WithServices(ctx, bk, dir.Services, func() (*fstypes.Stat, error) {
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
	})
}

func (dir *Directory) Entries(ctx context.Context, bk *buildkit.Client, src string) ([]string, error) {
	src = path.Join(dir.Dir, src)

	return WithServices(ctx, bk, dir.Services, func() ([]string, error) {
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
	})
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

func (dir *Directory) Directory(ctx context.Context, bk *buildkit.Client, subdir string) (*Directory, error) {
	dir = dir.Clone()
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

func (dir *Directory) File(ctx context.Context, bk *buildkit.Client, file string) (*File, error) {
	err := validateFileName(file)
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
		File:     path.Join(dir.Dir, file),
		Pipeline: dir.Pipeline,
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

func (dir *Directory) WithFile(ctx context.Context, destPath string, src *File, permissions fs.FileMode, owner *Ownership) (*Directory, error) {
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

	Permissions fs.FileMode
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
	if input.Permissions != 0 {
		copyInfo.Mode = &input.Permissions
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

	err = dir.SetState(ctx, st.File(llb.Rm(path, llb.WithAllowWildcard(true))))
	if err != nil {
		return nil, err
	}

	return dir, nil
}

func (dir *Directory) Export(
	ctx context.Context,
	bk *buildkit.Client,
	host *Host,
	destPath string,
) (rerr error) {
	var defPB *pb.Definition
	if dir.Dir != "" {
		src, err := dir.State()
		if err != nil {
			return err
		}
		src = llb.Scratch().File(llb.Copy(src, dir.Dir, ".", &llb.CopyInfo{
			CopyDirContentsOnly: true,
		}))

		def, err := src.Marshal(ctx, llb.Platform(dir.Platform))
		if err != nil {
			return err
		}
		defPB = def.ToPB()
	} else {
		defPB = dir.LLB
	}

	_, err := WithServices(ctx, bk, dir.Services, func() (any, error) {
		return nil, bk.LocalDirExport(ctx, defPB, destPath)
	})
	return err
}

// Root removes any relative path from the directory.
func (dir *Directory) Root() (*Directory, error) {
	dir = dir.Clone()
	dir.Dir = "/"
	return dir, nil
}

func validateFileName(file string) error {
	baseFileName := filepath.Base(file)
	if len(baseFileName) > 255 {
		return errors.Errorf("File name length exceeds the maximum supported 255 characters")
	}
	return nil
}
