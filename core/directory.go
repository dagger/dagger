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
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	fstypes "github.com/tonistiigi/fsutil/types"
)

// Directory is a content-addressed directory.
type Directory struct {
	ID DirectoryID `json:"id"`
}

// DirectoryID is an opaque value representing a content-addressed directory.
type DirectoryID string

// directoryIDPayload is the inner content of a DirectoryID.
type directoryIDPayload struct {
	LLB      *pb.Definition `json:"llb"`
	Dir      string         `json:"dir"`
	Platform specs.Platform `json:"platform"`
	Pipeline pipeline.Path  `json:"pipeline"`

	// Services necessary to provision the directory.
	Services ServiceBindings `json:"services,omitempty"`
}

// Decode returns the private payload of a DirectoryID.
//
// NB(vito): Ideally this would not be exported, but it's currently needed for
// the project/ package. I left the return type private as a compromise.
//
//nolint:revive
func (id DirectoryID) Decode() (*directoryIDPayload, error) {
	if id == "" {
		return &directoryIDPayload{}, nil
	}

	var payload directoryIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, err
	}

	return &payload, nil
}

func (payload *directoryIDPayload) State() (llb.State, error) {
	if payload.LLB == nil {
		return llb.Scratch(), nil
	}

	return defToState(payload.LLB)
}

func (payload *directoryIDPayload) SetState(ctx context.Context, st llb.State) error {
	def, err := st.Marshal(ctx, llb.Platform(payload.Platform))
	if err != nil {
		return nil
	}

	payload.LLB = def.ToPB()
	return nil
}

func (payload *directoryIDPayload) ToDirectory() (*Directory, error) {
	id, err := encodeID(payload)
	if err != nil {
		return nil, err
	}

	return &Directory{
		ID: DirectoryID(id),
	}, nil
}

func NewDirectory(ctx context.Context, st llb.State, cwd string, pipeline pipeline.Path, platform specs.Platform, services ServiceBindings) (*Directory, error) {
	payload := directoryIDPayload{
		Dir:      cwd,
		Platform: platform,
		Pipeline: pipeline.Copy(),
		Services: services,
	}

	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	payload.LLB = def.ToPB()

	return payload.ToDirectory()
}

func (dir *Directory) Pipeline(ctx context.Context, name, description string, labels []pipeline.Label) (*Directory, error) {
	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}
	payload.Pipeline = payload.Pipeline.Add(pipeline.Pipeline{
		Name:        name,
		Description: description,
		Labels:      labels,
	})
	return payload.ToDirectory()
}

func (dir *Directory) Stat(ctx context.Context, gw bkgw.Client, src string) (*fstypes.Stat, error) {
	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}
	src = path.Join(payload.Dir, src)

	return WithServices(ctx, gw, payload.Services, func() (*fstypes.Stat, error) {
		res, err := gw.Solve(ctx, bkgw.SolveRequest{
			Definition: payload.LLB,
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
	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	src = path.Join(payload.Dir, src)

	return WithServices(ctx, gw, payload.Services, func() ([]string, error) {
		res, err := gw.Solve(ctx, bkgw.SolveRequest{
			Definition: payload.LLB,
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

func (dir *Directory) WithNewFile(ctx context.Context, dest string, content []byte, permissions fs.FileMode, uid, gid int) (*Directory, error) {
	err := validateFileName(dest)
	if err != nil {
		return nil, err
	}

	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	if permissions == 0 {
		permissions = 0o644
	}

	// be sure to create the file under the working directory
	dest = path.Join(payload.Dir, dest)

	st, err := payload.State()
	if err != nil {
		return nil, err
	}

	parent, _ := path.Split(dest)
	if parent != "" {
		st = st.File(llb.Mkdir(parent, 0755, llb.WithParents(true)), payload.Pipeline.LLBOpt())
	}

	opts := []llb.MkfileOption{}

	if uid != -1 && gid != -1 {
		opts = append(opts, llb.WithUIDGID(uid, gid))
	}

	st = st.File(llb.Mkfile(dest, permissions, content, opts...), payload.Pipeline.LLBOpt())

	err = payload.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	return payload.ToDirectory()
}

func (dir *Directory) Directory(ctx context.Context, subdir string) (*Directory, error) {
	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	payload.Dir = path.Join(payload.Dir, subdir)

	return payload.ToDirectory()
}

func (dir *Directory) File(ctx context.Context, file string) (*File, error) {
	err := validateFileName(file)
	if err != nil {
		return nil, err
	}

	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	return (&fileIDPayload{
		LLB:      payload.LLB,
		File:     path.Join(payload.Dir, file),
		Platform: payload.Platform,
		Services: payload.Services,
	}).ToFile()
}

func (dir *Directory) WithDirectory(ctx context.Context, subdir string, src *Directory, filter CopyFilter, uid, gid int) (*Directory, error) {
	destPayload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	srcPayload, err := src.ID.Decode()
	if err != nil {
		return nil, err
	}

	st, err := destPayload.State()
	if err != nil {
		return nil, err
	}

	srcSt, err := srcPayload.State()
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

	if uid != -1 && gid != -1 {
		opts = append(opts, llb.WithUIDGID(uid, gid))
	}

	st = st.File(llb.Copy(
		srcSt,
		srcPayload.Dir,
		path.Join(destPayload.Dir, subdir),
		opts...,
	), destPayload.Pipeline.LLBOpt())

	err = destPayload.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	destPayload.Services.Merge(srcPayload.Services)

	return destPayload.ToDirectory()
}

func (dir *Directory) WithTimestamps(ctx context.Context, unix int) (*Directory, error) {
	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	st, err := payload.State()
	if err != nil {
		return nil, err
	}

	t := time.Unix(int64(unix), 0)

	stamped := llb.Scratch().File(
		llb.Copy(st, payload.Dir, ".", &llb.CopyInfo{
			CopyDirContentsOnly: true,
			CreatedTime:         &t,
		}),
		payload.Pipeline.LLBOpt(),
	)

	return NewDirectory(ctx, stamped, "", payload.Pipeline, payload.Platform, payload.Services)
}

func (dir *Directory) WithNewDirectory(ctx context.Context, dest string, permissions fs.FileMode) (*Directory, error) {
	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	dest = path.Clean(dest)
	if strings.HasPrefix(dest, "../") {
		return nil, fmt.Errorf("cannot create directory outside parent: %s", dest)
	}

	// be sure to create the file under the working directory
	dest = path.Join(payload.Dir, dest)

	st, err := payload.State()
	if err != nil {
		return nil, err
	}

	if permissions == 0 {
		permissions = 0755
	}

	st = st.File(llb.Mkdir(dest, permissions, llb.WithParents(true)), payload.Pipeline.LLBOpt())

	err = payload.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	return payload.ToDirectory()
}

func (dir *Directory) WithFile(ctx context.Context, subdir string, src *File, permissions fs.FileMode, uid, gid int) (*Directory, error) {
	destPayload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	srcPayload, err := src.ID.decode()
	if err != nil {
		return nil, err
	}

	st, err := destPayload.State()
	if err != nil {
		return nil, err
	}

	srcSt, err := srcPayload.State()
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

	if uid != -1 && gid != -1 {
		opts = append(opts, llb.WithUIDGID(uid, gid))
	}

	st = st.File(llb.Copy(
		srcSt,
		srcPayload.File,
		path.Join(destPayload.Dir, subdir),
		opts...,
	), destPayload.Pipeline.LLBOpt())

	err = destPayload.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	destPayload.Services.Merge(srcPayload.Services)

	return destPayload.ToDirectory()
}

func MergeDirectories(ctx context.Context, dirs []*Directory, platform specs.Platform) (*Directory, error) {
	states := make([]llb.State, 0, len(dirs))
	var pipeline pipeline.Path
	for _, fs := range dirs {
		payload, err := fs.ID.Decode()
		if err != nil {
			return nil, err
		}

		if !reflect.DeepEqual(platform, payload.Platform) {
			// TODO(vito): work around with llb.Copy shenanigans?
			return nil, fmt.Errorf("TODO: cannot merge across platforms: %+v != %+v", platform, payload.Platform)
		}

		if pipeline.Name() == "" {
			pipeline = payload.Pipeline
		}

		state, err := payload.State()
		if err != nil {
			return nil, err
		}

		states = append(states, state)
	}

	return NewDirectory(ctx, llb.Merge(states, pipeline.LLBOpt()), "", pipeline, platform, nil)
}

func (dir *Directory) Diff(ctx context.Context, other *Directory) (*Directory, error) {
	lowerPayload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	upperPayload, err := other.ID.Decode()
	if err != nil {
		return nil, err
	}

	if lowerPayload.Dir != upperPayload.Dir {
		// TODO(vito): work around with llb.Copy shenanigans?
		return nil, fmt.Errorf("TODO: cannot diff with different relative paths: %q != %q", lowerPayload.Dir, upperPayload.Dir)
	}

	if !reflect.DeepEqual(lowerPayload.Platform, upperPayload.Platform) {
		// TODO(vito): work around with llb.Copy shenanigans?
		return nil, fmt.Errorf("TODO: cannot diff across platforms: %+v != %+v", lowerPayload.Platform, upperPayload.Platform)
	}

	lowerSt, err := lowerPayload.State()
	if err != nil {
		return nil, err
	}

	upperSt, err := upperPayload.State()
	if err != nil {
		return nil, err
	}

	err = lowerPayload.SetState(ctx, llb.Diff(lowerSt, upperSt, lowerPayload.Pipeline.LLBOpt()))
	if err != nil {
		return nil, err
	}

	return lowerPayload.ToDirectory()
}

func (dir *Directory) Without(ctx context.Context, path string) (*Directory, error) {
	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	st, err := payload.State()
	if err != nil {
		return nil, err
	}

	err = payload.SetState(ctx, st.File(llb.Rm(path, llb.WithAllowWildcard(true)), payload.Pipeline.LLBOpt()))
	if err != nil {
		return nil, err
	}

	return payload.ToDirectory()
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

	srcPayload, err := dir.ID.Decode()
	if err != nil {
		return err
	}

	return host.Export(ctx, bkclient.ExportEntry{
		Type:      bkclient.ExporterLocal,
		OutputDir: dest,
	}, bkClient, solveOpts, solveCh, func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		return WithServices(ctx, gw, srcPayload.Services, func() (*bkgw.Result, error) {
			src, err := srcPayload.State()
			if err != nil {
				return nil, err
			}

			var defPB *pb.Definition
			if srcPayload.Dir != "" {
				src = llb.Scratch().File(llb.Copy(src, srcPayload.Dir, ".", &llb.CopyInfo{
					CopyDirContentsOnly: true,
				}),
					srcPayload.Pipeline.LLBOpt(),
				)

				def, err := src.Marshal(ctx, llb.Platform(srcPayload.Platform))
				if err != nil {
					return nil, err
				}

				defPB = def.ToPB()
			} else {
				defPB = srcPayload.LLB
			}

			return gw.Solve(ctx, bkgw.SolveRequest{
				Evaluate:   true,
				Definition: defPB,
			})
		})
	})
}

func validateFileName(file string) error {
	baseFileName := filepath.Base(file)
	if len(baseFileName) > 255 {
		return errors.Errorf("File name length exceeds the maximum supported 255 characters")
	}
	return nil
}
