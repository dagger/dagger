package filesystem

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	scratchID = FSID("scratch")
)

type FSID string

type Filesystem struct {
	ID FSID `json:"id"`
}

func (f *Filesystem) ToImage() (*Image, error) {
	if f.ID == scratchID {
		def, err := llb.Scratch().Marshal(context.TODO())
		if err != nil {
			return nil, err
		}
		return &Image{
			FS: def.ToPB(),
		}, nil
	}

	jsonBytes := make([]byte, base64.StdEncoding.DecodedLen(len(f.ID)))
	n, err := base64.StdEncoding.Decode(jsonBytes, []byte(f.ID))
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal fs bytes: %v", err)
	}
	jsonBytes = jsonBytes[:n]

	var info Image
	if err := json.Unmarshal(jsonBytes, &info); err != nil {
		return nil, fmt.Errorf("failed to unmarshal fs info: %v: %s", err, string(jsonBytes))
	}

	return &info, nil
}

func (f *Filesystem) ToDefinition() (*pb.Definition, error) {
	info, err := f.ToImage()
	if err != nil {
		return nil, err
	}

	return info.FS, nil
}

func (f *Filesystem) ToState() (llb.State, error) {
	if f.ID == scratchID {
		return llb.Scratch(), nil
	}

	info, err := f.ToImage()
	if err != nil {
		return llb.State{}, err
	}

	return info.ToState()
}

func (f *Filesystem) Evaluate(ctx context.Context, gw bkgw.Client) error {
	def, err := f.ToDefinition()
	if err != nil {
		return err
	}
	_, err = gw.Solve(ctx, bkgw.SolveRequest{
		Definition: def,
		Evaluate:   true,
	})
	return err
}

func (f *Filesystem) ReadFile(ctx context.Context, gw bkgw.Client, path string) ([]byte, error) {
	def, err := f.ToDefinition()
	if err != nil {
		return nil, err
	}

	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Definition: def,
	})
	if err != nil {
		return nil, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}
	// Support scratch filesystem (nil ref)
	if ref == nil {
		return nil, fmt.Errorf("failed to read file: open %s: no such file or directory", path)
	}
	outputBytes, err := ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: path,
	})
	if err != nil {
		return nil, err
	}
	return outputBytes, nil
}

func New(id FSID) *Filesystem {
	return &Filesystem{
		ID: id,
	}
}

func FromState(ctx context.Context, st llb.State, platform specs.Platform) (*Filesystem, error) {
	info, err := ImageFromState(ctx, st, llb.Platform(platform))
	if err != nil {
		return nil, err
	}
	return info.ToFilesystem()
}

func FromSource(source any) (*Filesystem, error) {
	fs, ok := source.(*Filesystem)
	if ok {
		return fs, nil
	}

	// TODO: when returned by user actions, Filesystem is just a map[string]interface{}, need to fix, hack for now:

	m, ok := source.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid source type: %T", source)
	}
	id, ok := m["id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid source id: %T %v", source, source)
	}
	return &Filesystem{
		ID: FSID(id),
	}, nil
}

func Merged(ctx context.Context, filesystems []*Filesystem, platform specs.Platform) (*Filesystem, error) {
	states := make([]llb.State, 0, len(filesystems))
	for _, fs := range filesystems {
		state, err := fs.ToState()
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return FromState(ctx, llb.Merge(states), platform)
}

func Diffed(ctx context.Context, lower, upper *Filesystem, platform specs.Platform) (*Filesystem, error) {
	lowerState, err := lower.ToState()
	if err != nil {
		return nil, err
	}
	upperState, err := upper.ToState()
	if err != nil {
		return nil, err
	}
	return FromState(ctx, llb.Diff(lowerState, upperState), platform)
}
