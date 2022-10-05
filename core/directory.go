package core

import (
	"context"
	"fmt"
	"path"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	fstypes "github.com/tonistiigi/fsutil/types"
	"go.dagger.io/dagger/core/schema"
	"go.dagger.io/dagger/router"
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

func (id DirectoryID) decode() (*directoryIDPayload, error) {
	if id == "" {
		return &directoryIDPayload{}, nil
	}

	var payload directoryIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, err
	}

	return &payload, nil
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

	id, err := encodeID(payload)
	if err != nil {
		return nil, err
	}

	return &Directory{
		ID: DirectoryID(id),
	}, nil
}

func (dir *Directory) Stat(ctx context.Context, gw bkgw.Client, src string) (*fstypes.Stat, error) {
	st, cwd, platform, err := dir.Decode()
	if err != nil {
		return nil, err
	}

	src = path.Join(cwd, src)

	// empty directory, i.e. llb.Scratch()
	if st.Output() == nil {
		if path.Clean(src) == "." {
			// fake out a reasonable response
			return &fstypes.Stat{Path: src}, nil
		} else {
			return nil, fmt.Errorf("%s: no such file or directory", src)
		}
	}

	if st.Output() == nil {
		// empty directory, i.e. llb.Scratch()
		return nil, fmt.Errorf("cannot stat scratch")
	}

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

	stat, err := ref.StatFile(ctx, bkgw.StatRequest{
		Path: src,
	})
	if err != nil {
		return nil, err
	}

	return stat, nil
}

func (dir *Directory) Contents(ctx context.Context, gw bkgw.Client, src string) ([]string, error) {
	st, cwd, platform, err := dir.Decode()
	if err != nil {
		return nil, err
	}

	src = path.Join(cwd, src)

	// empty directory, i.e. llb.Scratch()
	if st.Output() == nil {
		if path.Clean(src) == "." {
			return []string{}, nil
		} else {
			return nil, fmt.Errorf("%s: no such file or directory", src)
		}
	}

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
	st, cwd, platform, err := dir.Decode()
	if err != nil {
		return nil, err
	}

	// be sure to create the file under the working directory
	dest = path.Join(cwd, dest)

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

	return NewDirectory(ctx, st, cwd, platform)
}

func (dir *Directory) Directory(ctx context.Context, subdir string) (*Directory, error) {
	st, cwd, platform, err := dir.Decode()
	if err != nil {
		return nil, err
	}

	return NewDirectory(ctx, st, path.Join(cwd, subdir), platform)
}

func (dir *Directory) File(ctx context.Context, file string) (*File, error) {
	st, cwd, platform, err := dir.Decode()
	if err != nil {
		return nil, err
	}

	return NewFile(ctx, st, path.Join(cwd, file), platform)
}

func (dir *Directory) WithDirectory(ctx context.Context, subdir string, src *Directory) (*Directory, error) {
	st, cwd, platform, err := dir.Decode()
	if err != nil {
		return nil, err
	}

	srcSt, srcCwd, _, err := src.Decode()
	if err != nil {
		return nil, err
	}

	st = st.File(llb.Copy(srcSt, srcCwd, path.Join(cwd, subdir), &llb.CopyInfo{
		CreateDestPath: true,
	}))

	return NewDirectory(ctx, st, cwd, platform)
}

func (dir *Directory) WithCopiedFile(ctx context.Context, subdir string, src *File) (*Directory, error) {
	st, cwd, platform, err := dir.Decode()
	if err != nil {
		return nil, err
	}

	srcSt, srcPath, _, err := src.Decode()
	if err != nil {
		return nil, err
	}

	st = st.File(llb.Copy(srcSt, srcPath, path.Join(cwd, subdir)))

	return NewDirectory(ctx, st, cwd, platform)
}

func (dir *Directory) Decode() (llb.State, string, specs.Platform, error) {
	payload, err := dir.ID.decode()
	if err != nil {
		return llb.State{}, "", specs.Platform{}, err
	}

	if payload.LLB == nil {
		return llb.Scratch(), payload.Dir, specs.Platform{}, nil
	}

	st, err := defToState(payload.LLB)
	if err != nil {
		return llb.State{}, "", specs.Platform{}, err
	}

	return st, payload.Dir, payload.Platform, nil
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
			"file":             router.ToResolver(s.file),
			"secret":           router.ErrResolver(ErrNotImplementedYet),
			"withNewFile":      router.ToResolver(s.withNewFile),
			"withCopiedFile":   router.ToResolver(s.withCopiedFile),
			"withoutFile":      router.ErrResolver(ErrNotImplementedYet),
			"directory":        router.ToResolver(s.subdirectory),
			"withDirectory":    router.ToResolver(s.withDirectory),
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

type subdirectoryArgs struct {
	Path string
}

func (s *directorySchema) subdirectory(ctx *router.Context, parent *Directory, args subdirectoryArgs) (*Directory, error) {
	return parent.Directory(ctx, args.Path)
}

type withDirectoryArgs struct {
	Path      string
	Directory DirectoryID
}

func (s *directorySchema) withDirectory(ctx *router.Context, parent *Directory, args withDirectoryArgs) (*Directory, error) {
	return parent.WithDirectory(ctx, args.Path, &Directory{ID: args.Directory})
}

type contentArgs struct {
	Path string
}

func (s *directorySchema) contents(ctx *router.Context, parent *Directory, args contentArgs) ([]string, error) {
	return parent.Contents(ctx, s.gw, args.Path)
}

type dirFileArgs struct {
	Path string
}

func (s *directorySchema) file(ctx *router.Context, parent *Directory, args dirFileArgs) (*File, error) {
	return parent.File(ctx, args.Path)
}

type withNewFileArgs struct {
	Path     string
	Contents string
}

func (s *directorySchema) withNewFile(ctx *router.Context, parent *Directory, args withNewFileArgs) (*Directory, error) {
	return parent.WithNewFile(ctx, s.gw, args.Path, []byte(args.Contents))
}

type withCopiedFileArgs struct {
	Path   string
	Source FileID
}

func (s *directorySchema) withCopiedFile(ctx *router.Context, parent *Directory, args withCopiedFileArgs) (*Directory, error) {
	return parent.WithCopiedFile(ctx, args.Path, &File{ID: args.Source})
}
