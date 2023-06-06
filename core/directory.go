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
	"github.com/dagger/dagger/router"
	bkclient "github.com/moby/buildkit/client"
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
var _ router.Digestible = DirectoryID("")

func (id DirectoryID) Digest() (digest.Digest, error) {
	return digest.FromString(id.String()), nil
}

// ToDirectory converts the ID into a real Directory.
func (id DirectoryID) ToDirectory() (*Directory, error) {
	var dir Directory

	if id == "" {
		return &dir, nil
	}

	if err := decodeID(&dir, id); err != nil {
		return nil, err
	}

	return &dir, nil
}

// ID marshals the directory into a content-addressed ID.
func (dir *Directory) ID() (DirectoryID, error) {
	return encodeID[DirectoryID](dir)
}

var _ router.Pipelineable = (*Directory)(nil)

func (dir *Directory) PipelinePath() pipeline.Path {
	// TODO(vito): test
	return dir.Pipeline
}

// Directory is digestible so that it can be recorded as an output of the
// --debug vertex that created it.
var _ router.Digestible = (*Directory)(nil)

// Digest returns the directory's content hash.
func (dir *Directory) Digest() (digest.Digest, error) {
	id, err := dir.ID()
	if err != nil {
		return "", err
	}
	return id.Digest()
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

func (dir *Directory) Stat(ctx context.Context, gw bkgw.Client, src string) (*fstypes.Stat, error) {
	src = path.Join(dir.Dir, src)

	return WithServices(ctx, gw, dir.Services, func() (*fstypes.Stat, error) {
		res, err := gw.Solve(ctx, bkgw.SolveRequest{
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

func (dir *Directory) Entries(ctx context.Context, gw bkgw.Client, src string) ([]string, error) {
	src = path.Join(dir.Dir, src)

	return WithServices(ctx, gw, dir.Services, func() ([]string, error) {
		res, err := gw.Solve(ctx, bkgw.SolveRequest{
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

func (dir *Directory) Directory(ctx context.Context, subdir string) (*Directory, error) {
	dir = dir.Clone()
	dir.Dir = path.Join(dir.Dir, subdir)
	return dir, nil
}

func (dir *Directory) File(ctx context.Context, file string) (*File, error) {
	err := validateFileName(file)
	if err != nil {
		return nil, err
	}

	return &File{
		LLB:      dir.LLB,
		File:     path.Join(dir.Dir, file),
		Pipeline: dir.Pipeline,
		Platform: dir.Platform,
		Services: dir.Services,
	}, nil
}

func (dir *Directory) WithDirectory(ctx context.Context, subdir string, src *Directory, filter CopyFilter, owner *Ownership) (*Directory, error) {
	dir = dir.Clone()

	st, err := dir.State()
	if err != nil {
		return nil, err
	}

	srcSt, err := src.State()
	if err != nil {
		return nil, err
	}

	opts := []llb.CopyOption{
		&llb.CopyInfo{
			CreateDestPath:      true,
			CopyDirContentsOnly: true,
			IncludePatterns:     filter.Include,
			ExcludePatterns:     filter.Exclude,
		},
	}
	if owner != nil {
		opts = append(opts, owner.Opt())
	}

	st = st.File(llb.Copy(srcSt, src.Dir, path.Join(dir.Dir, subdir), opts...))

	err = dir.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	dir.Services.Merge(src.Services)

	return dir, nil
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

func (dir *Directory) WithFile(ctx context.Context, subdir string, src *File, permissions fs.FileMode, ownership *Ownership) (*Directory, error) {
	dir = dir.Clone()

	st, err := dir.State()
	if err != nil {
		return nil, err
	}

	srcSt, err := src.State()
	if err != nil {
		return nil, err
	}

	var perm *fs.FileMode
	if permissions != 0 {
		perm = &permissions
	}

	opts := []llb.CopyOption{
		&llb.CopyInfo{
			CreateDestPath: true,
			Mode:           perm,
		},
	}

	if ownership != nil {
		opts = append(opts, ownership.Opt())
	}

	st = st.File(llb.Copy(srcSt, src.File, path.Join(dir.Dir, subdir), opts...))

	err = dir.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	dir.Services.Merge(src.Services)

	return dir, nil
}

func MergeDirectories(ctx context.Context, dirs []*Directory, platform specs.Platform) (*Directory, error) {
	states := make([]llb.State, 0, len(dirs))
	var pipeline pipeline.Path
	for _, dir := range dirs {
		if !reflect.DeepEqual(platform, dir.Platform) {
			// TODO(vito): work around with llb.Copy shenanigans?
			return nil, fmt.Errorf("TODO: cannot merge across platforms: %+v != %+v", platform, dir.Platform)
		}

		if pipeline.Name() == "" {
			pipeline = dir.Pipeline
		}

		state, err := dir.State()
		if err != nil {
			return nil, err
		}

		states = append(states, state)
	}

	return NewDirectorySt(ctx, llb.Merge(states), "", pipeline, platform, nil)
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
	host *Host,
	dest string,
	bkClient *bkclient.Client,
	solveOpts bkclient.SolveOpt,
	solveCh chan<- *bkclient.SolveStatus,
) error {
	dest, err := host.NormalizeDest(dest)
	if err != nil {
		return err
	}

	return host.Export(ctx, bkclient.ExportEntry{
		Type:      bkclient.ExporterLocal,
		OutputDir: dest,
	}, bkClient, solveOpts, solveCh, func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		return WithServices(ctx, gw, dir.Services, func() (*bkgw.Result, error) {
			src, err := dir.State()
			if err != nil {
				return nil, err
			}

			var defPB *pb.Definition
			if dir.Dir != "" {
				src = llb.Scratch().File(llb.Copy(src, dir.Dir, ".", &llb.CopyInfo{
					CopyDirContentsOnly: true,
				}))

				def, err := src.Marshal(ctx, llb.Platform(dir.Platform))
				if err != nil {
					return nil, err
				}

				defPB = def.ToPB()
			} else {
				defPB = dir.LLB
			}

			return gw.Solve(ctx, bkgw.SolveRequest{
				Evaluate:   true,
				Definition: defPB,
			})
		})
	})
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
