package core

import (
	"context"
	"fmt"

	bkclient "github.com/moby/buildkit/client"
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

func (file *File) Contents(ctx context.Context, gw bkgw.Client) ([]byte, error) {
	payload, err := file.ID.decode()
	if err != nil {
		return nil, err
	}

	ref, err := gwRef(ctx, gw, payload.LLB)
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

func (file *File) Stat(ctx context.Context, gw bkgw.Client) (*fstypes.Stat, error) {
	payload, err := file.ID.decode()
	if err != nil {
		return nil, err
	}

	ref, err := gwRef(ctx, gw, payload.LLB)
	if err != nil {
		return nil, err
	}

	return ref.StatFile(ctx, bkgw.StatRequest{
		Path: payload.File,
	})
}

func (file *File) Export(
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

	srcPayload, err := file.ID.decode()
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

		src = llb.Scratch().File(llb.Copy(src, srcPayload.File, "."))

		def, err := src.Marshal(ctx, llb.Platform(srcPayload.Platform))
		if err != nil {
			return nil, err
		}

		return gw.Solve(ctx, bkgw.SolveRequest{
			Evaluate:   true,
			Definition: def.ToPB(),
		})
	})
}

func gwRef(ctx context.Context, gw bkgw.Client, def *pb.Definition) (bkgw.Reference, error) {
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

	if ref == nil {
		// empty file, i.e. llb.Scratch()
		return nil, fmt.Errorf("empty reference")
	}

	return ref, nil
}
