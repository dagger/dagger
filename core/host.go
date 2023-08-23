package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/socket"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vito/progrock"
)

type Host struct {
}

func NewHost() *Host {
	return &Host{}
}

type CopyFilter struct {
	Exclude []string
	Include []string
}

func (host *Host) Directory(
	ctx context.Context,
	bk *buildkit.Client,
	dirPath string,
	p pipeline.Path,
	pipelineNamePrefix string,
	platform specs.Platform,
	filter CopyFilter,
) (*Directory, error) {
	// TODO: enforcement that requester session is granted access to source session at this path

	// Create a sub-pipeline to group llb.Local instructions
	pipelineName := fmt.Sprintf("%s %s", pipelineNamePrefix, dirPath)
	ctx, subRecorder := progrock.WithGroup(ctx, pipelineName, progrock.Weak())

	localOpts := []llb.LocalOption{llb.WithCustomNamef("upload %s", dirPath)}
	opName := fmt.Sprintf("copy %s", dirPath)
	if len(filter.Exclude) > 0 {
		opName += fmt.Sprintf(" (exclude %s)", strings.Join(filter.Exclude, ", "))
		localOpts = append(localOpts, llb.ExcludePatterns(filter.Exclude))
	}
	if len(filter.Include) > 0 {
		opName += fmt.Sprintf(" (include %s)", strings.Join(filter.Include, ", "))
		localOpts = append(localOpts, llb.IncludePatterns(filter.Include))
	}

	localLLB, err := bk.LocalImportLLB(ctx, dirPath, localOpts...)
	if err != nil {
		return nil, fmt.Errorf("local %s: %w", dirPath, err)
	}
	// copy to scratch to avoid making buildkit's snapshot of the local dir immutable,
	// which makes it unable to reused, which in turn creates cache invalidations
	// TODO: this should be optional, the above issue can also be avoided w/ readonly
	// mount when possible
	st := llb.Scratch().File(
		llb.Copy(localLLB, "/", "/"),
		llb.WithCustomNamef(opName),
	)

	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	defPB := def.ToPB()

	// associate vertexes to the 'host.directory' sub-pipeline
	buildkit.RecordVertexes(subRecorder, defPB)

	_, err = bk.Solve(ctx, bkgw.SolveRequest{
		Definition: defPB,
		Evaluate:   true, // do the sync now, not lazily
	})
	if err != nil {
		return nil, fmt.Errorf("sync %s: %w", dirPath, err)
	}

	return NewDirectory(ctx, defPB, "", p, platform, nil), nil
}

func (host *Host) File(
	ctx context.Context,
	bk *buildkit.Client,
	svcs *Services,
	path string,
	p pipeline.Path,
	platform specs.Platform,
) (*File, error) {
	parentDir, err := host.Directory(ctx, bk, filepath.Dir(path), p, "host.file", platform, CopyFilter{
		Include: []string{filepath.Base(path)},
	})
	if err != nil {
		return nil, err
	}
	return parentDir.File(ctx, bk, svcs, filepath.Base(path))
}

func (host *Host) Socket(ctx context.Context, sockPath string) (*socket.Socket, error) {
	return socket.NewHostUnixSocket(sockPath), nil
}
