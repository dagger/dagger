package core

import (
	"context"
	"path"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"go.dagger.io/dagger/core/schema"
	"go.dagger.io/dagger/router"
)

// Directory is a content-addressed directory.
type Directory struct {
	ID DirectoryID `json:"id"`
}

// DirectoryID is an opaque value representing a content-addressed directory.
type DirectoryID string

// TODO(vito): this might need to include a path to pass to llb.SourcePath when
// mounting the directory in, to support container { directory("./foo") }
type directoryIDPayload struct {
	LLB *pb.Definition `json:"llb"`
}

func (id DirectoryID) decode() (*directoryIDPayload, error) {
	var payload directoryIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, err
	}

	return &payload, nil
}

func DirectoryFromState(ctx context.Context, st llb.State, marshalOpts ...llb.ConstraintsOpt) (*Directory, error) {
	def, err := st.Marshal(ctx, marshalOpts...)
	if err != nil {
		return nil, err
	}

	id, err := encodeID(directoryIDPayload{
		LLB: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	return &Directory{
		ID: DirectoryID(id),
	}, nil
}

func (dir *Directory) Contents(ctx context.Context, gw bkgw.Client, path string) ([]string, error) {
	st, err := dir.ToState()
	if err != nil {
		return nil, err
	}

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
		// empty directory, i.e. llb.Scratch()
		return []string{}, nil
	}

	entries, err := ref.ReadDir(ctx, bkgw.ReadDirRequest{
		Path: path,
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
	st, err := dir.ToState()
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

	return DirectoryFromState(ctx, st)
}

func (dir *Directory) ToState() (llb.State, error) {
	if dir.ID == "" {
		return llb.Scratch(), nil
	}

	payload, err := dir.ID.decode()
	if err != nil {
		return llb.State{}, err
	}

	defop, err := llb.NewDefinitionOp(payload.LLB)
	if err != nil {
		return llb.State{}, err
	}

	return llb.NewState(defop), nil
}

type directorySchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &directorySchema{}

func (s *directorySchema) Name() string {
	return "directory"
}

func (s *directorySchema) Schema() string {
	return schema.Directory
}

var directoryIDResolver = stringResolver(DirectoryID(""))

func (s *directorySchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"DirectoryID": directoryIDResolver,
		"Query": router.ObjectResolver{
			"directory": router.ToResolver(s.directory),
		},
		"Directory": router.ObjectResolver{
			"contents":         router.ToResolver(s.contents),
			"file":             router.ErrResolver(ErrNotImplementedYet),
			"secret":           router.ErrResolver(ErrNotImplementedYet),
			"withNewFile":      router.ToResolver(s.withNewFile),
			"withCopiedFIle":   router.ErrResolver(ErrNotImplementedYet),
			"withoutFile":      router.ErrResolver(ErrNotImplementedYet),
			"directory":        router.ErrResolver(ErrNotImplementedYet),
			"withDirectory":    router.ErrResolver(ErrNotImplementedYet),
			"withoutDirectory": router.ErrResolver(ErrNotImplementedYet),
			"diff":             router.ErrResolver(ErrNotImplementedYet),
		},
	}
}

func (s *directorySchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type directoryArgs struct {
	ID DirectoryID
}

func (s *directorySchema) directory(ctx *router.Context, parent any, args directoryArgs) (*Directory, error) {
	return &Directory{
		ID: args.ID,
	}, nil
}

type contentArgs struct {
	Path string
}

func (s *directorySchema) contents(ctx *router.Context, parent *Directory, args contentArgs) ([]string, error) {
	return parent.Contents(ctx, s.gw, args.Path)
}

type withNewFileArgs struct {
	Path     string
	Contents string
}

func (s *directorySchema) withNewFile(ctx *router.Context, parent *Directory, args withNewFileArgs) (*Directory, error) {
	return parent.WithNewFile(ctx, s.gw, args.Path, []byte(args.Contents))
}
