package core

import (
	"context"
	"fmt"
	"io/fs"

	"io"
	"path"
	"time"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	fstypes "github.com/tonistiigi/fsutil/types"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/progrock"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/reffs"
	"github.com/dagger/dagger/engine/buildkit"
)

// File is a content-addressed file.
type File struct {
	Query *Query

	LLB      *pb.Definition `json:"llb"`
	File     string         `json:"file"`
	Platform Platform       `json:"platform"`

	// Services necessary to provision the file.
	Services ServiceBindings `json:"services,omitempty"`
}

func (*File) Type() *ast.Type {
	return &ast.Type{
		NamedType: "File",
		NonNull:   true,
	}
}

func (*File) TypeDescription() string {
	return "A file."
}

var _ HasPBDefinitions = (*File)(nil)

func (file *File) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if file.LLB != nil {
		defs = append(defs, file.LLB)
	}
	for _, bnd := range file.Services {
		ctr := bnd.Service.Container
		if ctr == nil {
			continue
		}
		ctrDefs, err := ctr.PBDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		defs = append(defs, ctrDefs...)
	}
	return defs, nil
}

func NewFile(query *Query, def *pb.Definition, file string, platform Platform, services ServiceBindings) *File {
	return &File{
		Query:    query,
		LLB:      def,
		File:     file,
		Platform: platform,
		Services: services,
	}
}

func NewFileWithContents(
	ctx context.Context,
	query *Query,
	name string,
	content []byte,
	permissions fs.FileMode,
	ownership *Ownership,
	platform Platform,
) (*File, error) {
	dir, err := NewScratchDirectory(query, platform).WithNewFile(ctx, name, content, permissions, ownership)
	if err != nil {
		return nil, err
	}
	return dir.File(ctx, name)
}

func NewFileSt(ctx context.Context, query *Query, st llb.State, dir string, platform Platform, services ServiceBindings) (*File, error) {
	def, err := st.Marshal(ctx, llb.Platform(platform.Spec()))
	if err != nil {
		return nil, err
	}

	return NewFile(query, def.ToPB(), dir, platform, services), nil
}

// Clone returns a deep copy of the container suitable for modifying in a
// WithXXX method.
func (file *File) Clone() *File {
	cp := *file
	cp.Services = cloneSlice(cp.Services)
	return &cp
}

var _ pipeline.Pipelineable = (*File)(nil)

func (file *File) PipelinePath() pipeline.Path {
	return file.Query.Pipeline
}

func (file *File) State() (llb.State, error) {
	return defToState(file.LLB)
}

func (file *File) Evaluate(ctx context.Context) (*buildkit.Result, error) {
	svcs := file.Query.Services
	bk := file.Query.Buildkit

	detach, _, err := svcs.StartBindings(ctx, file.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	return bk.Solve(ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: file.LLB,
	})
}

// Contents handles file content retrieval
func (file *File) Contents(ctx context.Context) ([]byte, error) {
	svcs := file.Query.Services
	bk := file.Query.Buildkit

	detach, _, err := svcs.StartBindings(ctx, file.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	ref, err := bkRef(ctx, bk, file.LLB)
	if err != nil {
		return nil, err
	}

	// Stat the file and preallocate file contents buffer:
	st, err := file.Stat(ctx)
	if err != nil {
		return nil, err
	}

	// Error on files that exceed MaxFileContentsSize:
	fileSize := int(st.GetSize_())
	if fileSize > buildkit.MaxFileContentsSize {
		// TODO: move to proper error structure
		return nil, fmt.Errorf("file size %d exceeds limit %d", fileSize, buildkit.MaxFileContentsSize)
	}

	// Allocate buffer with the given file size:
	contents := make([]byte, fileSize)

	// Use a chunked reader to overcome issues when
	// the input file exceeds MaxFileContentsChunkSize:
	var offset int
	for offset < fileSize {
		chunk, err := ref.ReadFile(ctx, bkgw.ReadRequest{
			Filename: file.File,
			Range: &bkgw.FileRange{
				Offset: offset,
				Length: buildkit.MaxFileContentsChunkSize,
			},
		})
		if err != nil {
			return nil, err
		}

		// Copy the chunk and increment offset for subsequent reads:
		copy(contents[offset:], chunk)
		offset += len(chunk)
	}
	return contents, nil
}

func (file *File) Stat(ctx context.Context) (*fstypes.Stat, error) {
	svcs := file.Query.Services
	bk := file.Query.Buildkit

	detach, _, err := svcs.StartBindings(ctx, file.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	ref, err := bkRef(ctx, bk, file.LLB)
	if err != nil {
		return nil, err
	}

	return ref.StatFile(ctx, bkgw.StatRequest{
		Path: file.File,
	})
}

func (file *File) WithTimestamps(ctx context.Context, unix int) (*File, error) {
	file = file.Clone()

	st, err := file.State()
	if err != nil {
		return nil, err
	}

	t := time.Unix(int64(unix), 0)

	stamped := llb.Scratch().File(llb.Copy(st, file.File, ".", llb.WithCreatedTime(t)))

	def, err := stamped.Marshal(ctx, llb.Platform(file.Platform.Spec()))
	if err != nil {
		return nil, err
	}
	file.LLB = def.ToPB()
	file.File = path.Base(file.File)

	return file, nil
}

func (file *File) Open(ctx context.Context) (io.ReadCloser, error) {
	bk := file.Query.Buildkit
	svcs := file.Query.Services

	detach, _, err := svcs.StartBindings(ctx, file.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	fs, err := reffs.OpenDef(ctx, bk, file.LLB)
	if err != nil {
		return nil, err
	}

	return fs.Open(file.File)
}

func (file *File) Export(ctx context.Context, dest string, allowParentDirPath bool) error {
	svcs := file.Query.Services
	bk := file.Query.Buildkit

	src, err := file.State()
	if err != nil {
		return err
	}
	def, err := src.Marshal(ctx, llb.Platform(file.Platform.Spec()))
	if err != nil {
		return err
	}

	rec := progrock.FromContext(ctx)

	vtx := rec.Vertex(
		digest.Digest(identity.NewID()),
		fmt.Sprintf("export file %s to host %s", file.File, dest),
	)
	defer vtx.Done(err)

	detach, _, err := svcs.StartBindings(ctx, file.Services)
	if err != nil {
		return err
	}
	defer detach()

	return bk.LocalFileExport(ctx, def.ToPB(), dest, file.File, allowParentDirPath)
}

// bkRef returns the buildkit reference from the solved def.
func bkRef(ctx context.Context, bk *buildkit.Client, def *pb.Definition) (bkgw.Reference, error) {
	res, err := bk.Solve(ctx, bkgw.SolveRequest{
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
