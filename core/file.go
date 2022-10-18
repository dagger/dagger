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
	LLB       *pb.Definition    `json:"llb"`
	File      string            `json:"file"`
	Platform  specs.Platform    `json:"platform"`
	LocalDirs map[string]string `json:"local_dirs,omitempty"`
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

func NewFile(ctx context.Context, st llb.State, file string, platform specs.Platform, localDirs map[string]string) (*File, error) {
	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	return (&fileIDPayload{
		LLB:       def.ToPB(),
		File:      file,
		Platform:  platform,
		LocalDirs: localDirs,
	}).ToFile()
}

func (file *File) Contents(ctx context.Context, session *Session) ([]byte, error) {
	payload, err := file.ID.decode()
	if err != nil {
		return nil, err
	}

	return withRef(ctx,
		session.WithLocalDirs(payload.LocalDirs),
		payload.LLB,
		func(ref bkgw.Reference) ([]byte, error) {
			return ref.ReadFile(ctx, bkgw.ReadRequest{
				Filename: payload.File,
			})
		},
	)
}

func (file *File) Secret(ctx context.Context) (*Secret, error) {
	return NewSecretFromFile(file.ID)
}

func (file *File) Stat(ctx context.Context, session *Session) (*fstypes.Stat, error) {
	payload, err := file.ID.decode()
	if err != nil {
		return nil, err
	}

	return withRef(ctx,
		session.WithLocalDirs(payload.LocalDirs),
		payload.LLB,
		func(ref bkgw.Reference) (*fstypes.Stat, error) {
			return ref.StatFile(ctx, bkgw.StatRequest{
				Path: payload.File,
			})
		},
	)
}

func withRef[T any](ctx context.Context, session *Session, def *pb.Definition, f func(bkgw.Reference) (T, error)) (T, error) {
	var ret T
	err := session.Build(ctx, func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
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
			return res, fmt.Errorf("empty reference")
		}

		ret, err = f(ref)
		if err != nil {
			return nil, err
		}

		return bkgw.NewResult(), nil
	})
	if err != nil {
		return ret, err
	}

	return ret, nil
}
