package core

import (
	"context"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	fstypes "github.com/tonistiigi/fsutil/types"
)

// File is a content-addressed file.
type File struct {
	ID FileID `json:"id"`
}

// FileID is an opaque value representing a content-addressed file.
type FileID string

// fileIDPayload is the inner content of a FileID.
type fileIDPayload struct {
	LLB      *pb.Definition `json:"llb"`
	File     string         `json:"file"`
	Platform specs.Platform `json:"platform"`
}

func (id FileID) decode() (*fileIDPayload, error) {
	var payload fileIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, err
	}

	return &payload, nil
}

func NewFile(ctx context.Context, st llb.State, file string, platform specs.Platform) (*File, error) {
	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	id, err := encodeID(fileIDPayload{
		LLB:      def.ToPB(),
		File:     file,
		Platform: platform,
	})
	if err != nil {
		return nil, err
	}

	return &File{
		ID: FileID(id),
	}, nil
}

func (file *File) Contents(ctx context.Context, gw bkgw.Client) ([]byte, error) {
	st, filePath, platform, err := file.Decode()
	if err != nil {
		return nil, err
	}

	ref, err := file.ref(ctx, gw, st, platform)
	if err != nil {
		return nil, err
	}

	return ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: filePath,
	})
}

func (file *File) Stat(ctx context.Context, gw bkgw.Client) (*fstypes.Stat, error) {
	st, filePath, platform, err := file.Decode()
	if err != nil {
		return nil, err
	}

	ref, err := file.ref(ctx, gw, st, platform)
	if err != nil {
		return nil, err
	}

	return ref.StatFile(ctx, bkgw.StatRequest{
		Path: filePath,
	})
}

func (file *File) Decode() (llb.State, string, specs.Platform, error) {
	payload, err := file.ID.decode()
	if err != nil {
		return llb.State{}, "", specs.Platform{}, err
	}

	st, err := defToState(payload.LLB)
	if err != nil {
		return llb.State{}, "", specs.Platform{}, err
	}

	return st, payload.File, payload.Platform, nil
}

func (file *File) ref(ctx context.Context, gw bkgw.Client, st llb.State, platform specs.Platform) (bkgw.Reference, error) {
	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Definition: def.ToPB(),
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
