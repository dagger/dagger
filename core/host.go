package core

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/socket"
	"github.com/dagger/dagger/engine/buildkit"
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

	defPB, err := bk.LocalImport(ctx, subRecorder, platform, dirPath, filter.Exclude, filter.Include)
	if err != nil {
		return nil, fmt.Errorf("host directory %s: %w", dirPath, err)
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
