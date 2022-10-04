package core

import (
	"context"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	fstypes "github.com/tonistiigi/fsutil/types"
	"go.dagger.io/dagger/core/schema"
	"go.dagger.io/dagger/router"
)

// File is a content-addressed file.
type File struct {
	ID FileID `json:"id"`
}

// FileID is an opaque value representing a content-addressed file.
type FileID string

// fileIDPayload is the inner content of a FileID.
type fileIDPayload struct {
	LLB  *pb.Definition `json:"llb"`
	File string         `json:"file"`
}

func (id FileID) decode() (*fileIDPayload, error) {
	var payload fileIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, err
	}

	return &payload, nil
}

func NewFile(ctx context.Context, st llb.State, file string) (*File, error) {
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	id, err := encodeID(fileIDPayload{
		LLB:  def.ToPB(),
		File: file,
	})
	if err != nil {
		return nil, err
	}

	return &File{
		ID: FileID(id),
	}, nil
}

func (file *File) Contents(ctx context.Context, gw bkgw.Client) ([]byte, error) {
	st, filePath, err := file.Decode()
	if err != nil {
		return nil, err
	}

	ref, err := file.ref(ctx, gw, st)
	if err != nil {
		return nil, err
	}

	return ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: filePath,
	})
}

func (file *File) Stat(ctx context.Context, gw bkgw.Client) (*fstypes.Stat, error) {
	st, filePath, err := file.Decode()
	if err != nil {
		return nil, err
	}

	ref, err := file.ref(ctx, gw, st)
	if err != nil {
		return nil, err
	}

	return ref.StatFile(ctx, bkgw.StatRequest{
		Path: filePath,
	})
}

func (file *File) Decode() (llb.State, string, error) {
	payload, err := file.ID.decode()
	if err != nil {
		return llb.State{}, "", err
	}

	st, err := defToState(payload.LLB)
	if err != nil {
		return llb.State{}, "", err
	}

	return st, payload.File, nil
}

func (file *File) ref(ctx context.Context, gw bkgw.Client, st llb.State) (bkgw.Reference, error) {
	def, err := st.Marshal(ctx)
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

type fileSchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &fileSchema{}

func (s *fileSchema) Name() string {
	return "file"
}

func (s *fileSchema) Schema() string {
	return schema.File
}

var fileIDResolver = stringResolver(FileID(""))

func (s *fileSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"FileID": fileIDResolver,
		"Query": router.ObjectResolver{
			"file": router.ToResolver(s.file),
		},
		"File": router.ObjectResolver{
			"contents": router.ToResolver(s.contents),
			"secret":   router.ErrResolver(ErrNotImplementedYet),
			"size":     router.ToResolver(s.size),
		},
	}
}

func (s *fileSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type fileArgs struct {
	ID FileID
}

func (s *fileSchema) file(ctx *router.Context, parent any, args fileArgs) (*File, error) {
	return &File{
		ID: args.ID,
	}, nil
}

func (s *fileSchema) contents(ctx *router.Context, file *File, args any) (string, error) {
	content, err := file.Contents(ctx, s.gw)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func (s *fileSchema) size(ctx *router.Context, file *File, args any) (int64, error) {
	info, err := file.Stat(ctx, s.gw)
	if err != nil {
		return 0, err
	}

	return info.Size_, nil
}
