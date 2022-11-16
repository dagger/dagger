package core

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"strings"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
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

func NewDirectory(ctx context.Context, st llb.State, cwd string, platform specs.Platform) (*Directory, error) {
	payload := directoryIDPayload{
		Dir:      cwd,
		Platform: platform,
	}

	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	payload.LLB = def.ToPB()

	return payload.ToDirectory()
}

func (dir *Directory) Stat(ctx context.Context, gw bkgw.Client, src string) (*fstypes.Stat, error) {
	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	src = path.Join(payload.Dir, src)

	// empty directory, i.e. llb.Scratch()
	if payload.LLB == nil {
		if path.Clean(src) == "." {
			// fake out a reasonable response
			return &fstypes.Stat{Path: src}, nil
		}

		return nil, fmt.Errorf("%s: no such file or directory", src)
	}

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

	stat, err := ref.StatFile(ctx, bkgw.StatRequest{
		Path: src,
	})
	if err != nil {
		return nil, err
	}

	return stat, nil
}

func (dir *Directory) Entries(ctx context.Context, gw bkgw.Client, src string) ([]string, error) {
	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	src = path.Join(payload.Dir, src)

	// empty directory, i.e. llb.Scratch()
	if payload.LLB == nil {
		if path.Clean(src) == "." {
			return []string{}, nil
		}

		return nil, fmt.Errorf("%s: no such file or directory", src)
	}

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

func (dir *Directory) WithNewFile(ctx context.Context, gw bkgw.Client, dest string, content []byte) (*Directory, error) {
	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	// be sure to create the file under the working directory
	dest = path.Join(payload.Dir, dest)

	st, err := payload.State()
	if err != nil {
		return nil, err
	}

	parent, _ := path.Split(dest)
	if parent != "" {
		st = st.File(llb.Mkdir(parent, 0755, llb.WithParents(true)))
	}

	st = st.File(
		llb.Mkfile(
			dest,
			0644, // TODO(vito): expose, issue: #3167
			content,
		),
	)

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
	payload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	return (&fileIDPayload{
		LLB:      payload.LLB,
		File:     path.Join(payload.Dir, file),
		Platform: payload.Platform,
	}).ToFile()
}

func (dir *Directory) WithDirectory(ctx context.Context, subdir string, src *Directory, filter CopyFilter) (*Directory, error) {
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

	st = st.File(llb.Copy(srcSt, srcPayload.Dir, path.Join(destPayload.Dir, subdir), &llb.CopyInfo{
		CreateDestPath:      true,
		CopyDirContentsOnly: true,
		IncludePatterns:     filter.Include,
		ExcludePatterns:     filter.Exclude,
	}))

	err = destPayload.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	return destPayload.ToDirectory()
}

func (dir *Directory) WithNewDirectory(ctx context.Context, gw bkgw.Client, dest string) (*Directory, error) {
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

	st = st.File(llb.Mkdir(dest, 0755, llb.WithParents(true)))

	err = payload.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	return payload.ToDirectory()
}

func (dir *Directory) WithFile(ctx context.Context, subdir string, src *File) (*Directory, error) {
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

	st = st.File(llb.Copy(srcSt, srcPayload.File, path.Join(destPayload.Dir, subdir), &llb.CopyInfo{
		CreateDestPath: true,
	}))

	err = destPayload.SetState(ctx, st)
	if err != nil {
		return nil, err
	}

	return destPayload.ToDirectory()
}

func MergeDirectories(ctx context.Context, dirs []*Directory, platform specs.Platform) (*Directory, error) {
	states := make([]llb.State, 0, len(dirs))
	for _, fs := range dirs {
		payload, err := fs.ID.Decode()
		if err != nil {
			return nil, err
		}

		if !reflect.DeepEqual(platform, payload.Platform) {
			// TODO(vito): work around with llb.Copy shenanigans?
			return nil, fmt.Errorf("TODO: cannot merge across platforms: %+v != %+v", platform, payload.Platform)
		}

		state, err := payload.State()
		if err != nil {
			return nil, err
		}

		states = append(states, state)
	}

	return NewDirectory(ctx, llb.Merge(states), "", platform)
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

	err = lowerPayload.SetState(ctx, llb.Diff(lowerSt, upperSt))
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

	err = payload.SetState(ctx, st.File(llb.Rm(path, llb.WithAllowWildcard(true))))
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
	}, dest, bkClient, solveOpts, solveCh, func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		src, err := srcPayload.State()
		if err != nil {
			return nil, err
		}

		var defPB *pb.Definition
		if srcPayload.Dir != "" {
			src = llb.Scratch().File(llb.Copy(src, srcPayload.Dir, ".", &llb.CopyInfo{
				CopyDirContentsOnly: true,
			}))

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
}
