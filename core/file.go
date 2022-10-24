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

func (payload *fileIDPayload) State() (llb.State, error) {
	return defToState(payload.LLB)
}

func (payload *fileIDPayload) ToFile() (*File, error) {
	id, err := encodeID(payload)
	if err != nil {
		return nil, err
	}

	return &File{
		ID: FileID(id),
	}, nil
}

func NewFile(ctx context.Context, st llb.State, file string, platform specs.Platform) (*File, error) {
	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	return (&fileIDPayload{
		LLB:      def.ToPB(),
		File:     file,
		Platform: platform,
	}).ToFile()
}

func (file *File) Contents(ctx context.Context, gw bkgw.Client, cacheImports []bkgw.CacheOptionsEntry) ([]byte, error) {
	payload, err := file.ID.decode()
	if err != nil {
		return nil, err
	}

	ref, err := gwRef(ctx, gw, payload.LLB, cacheImports)
	if err != nil {
		return nil, err
	}

	return ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: payload.File,
	})
}

func (file *File) Secret(ctx context.Context) (*Secret, error) {
	return NewSecretFromFile(file.ID)
}

func (file *File) Stat(ctx context.Context, gw bkgw.Client, cacheImports []bkgw.CacheOptionsEntry) (*fstypes.Stat, error) {
	payload, err := file.ID.decode()
	if err != nil {
		return nil, err
	}

	ref, err := gwRef(ctx, gw, payload.LLB, cacheImports)
	if err != nil {
		return nil, err
	}

	return ref.StatFile(ctx, bkgw.StatRequest{
		Path: payload.File,
	})
}

func gwRef(ctx context.Context, gw bkgw.Client, def *pb.Definition, cacheImports []bkgw.CacheOptionsEntry) (bkgw.Reference, error) {
	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Definition:   def,
		CacheImports: cacheImports,
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
