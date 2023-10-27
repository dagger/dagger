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
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	fstypes "github.com/tonistiigi/fsutil/types"
	"github.com/vito/progrock"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/reffs"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine/buildkit"
)

// File is a content-addressed file.
type File struct {
	LLB      *pb.Definition `json:"llb"`
	File     string         `json:"file"`
	Pipeline pipeline.Path  `json:"pipeline"`
	Platform specs.Platform `json:"platform"`

	// Services necessary to provision the file.
	Services ServiceBindings `json:"services,omitempty"`
}

func (file *File) PBDefinitions() ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if file.LLB != nil {
		defs = append(defs, file.LLB)
	}
	if file.Services != nil {
		for _, bnd := range file.Services {
			ctr := bnd.Service.Container
			if ctr == nil {
				continue
			}
			ctrDefs, err := ctr.PBDefinitions()
			if err != nil {
				return nil, err
			}
			defs = append(defs, ctrDefs...)
		}
	}
	return defs, nil
}

func NewFile(ctx context.Context, def *pb.Definition, file string, pipeline pipeline.Path, platform specs.Platform, services ServiceBindings) *File {
	return &File{
		LLB:      def,
		File:     file,
		Pipeline: pipeline,
		Platform: platform,
		Services: services,
	}
}

func NewFileWithContents(
	ctx context.Context,
	bk *buildkit.Client,
	svcs *Services,
	name string,
	content []byte,
	permissions fs.FileMode,
	ownership *Ownership,
	pipeline pipeline.Path,
	platform specs.Platform,
) (*File, error) {
	dir, err := NewScratchDirectory(pipeline, platform).WithNewFile(ctx, name, content, permissions, ownership)
	if err != nil {
		return nil, err
	}
	return dir.File(ctx, bk, svcs, name)
}

func NewFileSt(ctx context.Context, st llb.State, dir string, pipeline pipeline.Path, platform specs.Platform, services ServiceBindings) (*File, error) {
	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	return NewFile(ctx, def.ToPB(), dir, pipeline, platform, services), nil
}

// Clone returns a deep copy of the container suitable for modifying in a
// WithXXX method.
func (file *File) Clone() *File {
	cp := *file
	cp.Pipeline = cloneSlice(cp.Pipeline)
	cp.Services = cloneSlice(cp.Services)
	return &cp
}

// ID marshals the file into a content-addressed ID.
func (file *File) ID() (FileID, error) {
	return resourceid.Encode(file)
}

var _ pipeline.Pipelineable = (*File)(nil)

func (file *File) PipelinePath() pipeline.Path {
	// TODO(vito): test
	return file.Pipeline
}

// File is digestible so that it can be recorded as an output of the --debug
// vertex that created it.
var _ resourceid.Digestible = (*File)(nil)

// Digest returns the file's content hash.
func (file *File) Digest() (digest.Digest, error) {
	return stableDigest(file)
}

func (file *File) State() (llb.State, error) {
	return defToState(file.LLB)
}

func (file *File) Evaluate(ctx context.Context, bk *buildkit.Client, svcs *Services) error {
	detach, _, err := svcs.StartBindings(ctx, bk, file.Services)
	if err != nil {
		return err
	}
	defer detach()

	_, err = bk.Solve(ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: file.LLB,
	})
	return err
}

// Contents handles file content retrieval
func (file *File) Contents(ctx context.Context, bk *buildkit.Client, svcs *Services) ([]byte, error) {
	detach, _, err := svcs.StartBindings(ctx, bk, file.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	ref, err := bkRef(ctx, bk, file.LLB)
	if err != nil {
		return nil, err
	}

	// Stat the file and preallocate file contents buffer:
	st, err := file.Stat(ctx, bk, svcs)
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

func (file *File) Stat(ctx context.Context, bk *buildkit.Client, svcs *Services) (*fstypes.Stat, error) {
	detach, _, err := svcs.StartBindings(ctx, bk, file.Services)
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

	def, err := stamped.Marshal(ctx, llb.Platform(file.Platform))
	if err != nil {
		return nil, err
	}
	file.LLB = def.ToPB()
	file.File = path.Base(file.File)

	return file, nil
}

func (file *File) Open(ctx context.Context, host *Host, bk *buildkit.Client, svcs *Services) (io.ReadCloser, error) {
	detach, _, err := svcs.StartBindings(ctx, bk, file.Services)
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

func (file *File) Export(
	ctx context.Context,
	bk *buildkit.Client,
	host *Host,
	svcs *Services,
	dest string,
	allowParentDirPath bool,
) error {
	src, err := file.State()
	if err != nil {
		return err
	}
	def, err := src.Marshal(ctx, llb.Platform(file.Platform))
	if err != nil {
		return err
	}

	rec := progrock.FromContext(ctx)

	vtx := rec.Vertex(
		digest.Digest(identity.NewID()),
		fmt.Sprintf("export file %s to host %s", file.File, dest),
	)
	defer vtx.Done(err)

	detach, _, err := svcs.StartBindings(ctx, bk, file.Services)
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
