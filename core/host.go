package core

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/containerd/containerd/labels"
	"github.com/dagger/dagger/dagql"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/ast"
)

type Host struct {
	Query *Query
}

func (*Host) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Host",
		NonNull:   true,
	}
}

func (*Host) TypeDescription() string {
	return "Information about the host environment."
}

type CopyFilter struct {
	Exclude []string `default:"[]"`
	Include []string `default:"[]"`
}

func LoadBlob(ctx context.Context, srv *dagql.Server, desc specs.Descriptor) (i dagql.Instance[*Directory], err error) {
	// Instead of directly returning a Directory, which would get "stamped" with
	// an impure ID that cannot be passed between modules, we fetch the Directory
	// we just uploaded by its blob, which yields a pure ID.
	err = srv.Select(ctx, srv.Root(), &i, dagql.Selector{
		Field: "blob",
		Args: []dagql.NamedInput{
			{
				Name:  "digest",
				Value: dagql.NewString(desc.Digest.String()),
			},
			{
				Name:  "size",
				Value: dagql.NewInt(desc.Size),
			},
			{
				Name:  "mediaType",
				Value: dagql.NewString(desc.MediaType),
			},
			{
				Name:  "uncompressed",
				Value: dagql.NewString(desc.Annotations[labels.LabelUncompressed]),
			},
		},
	})
	return
}

func (host *Host) Directory(
	ctx context.Context,
	srv *dagql.Server,
	dirPath string,
	pipelineNamePrefix string,
	filter CopyFilter,
) (dagql.Instance[*Directory], error) {
	var i dagql.Instance[*Directory]
	// TODO: enforcement that requester session is granted access to source session at this path

	// Create a sub-pipeline to group llb.Local instructions
	_, desc, err := host.Query.Buildkit.LocalImport(
		ctx,
		host.Query.Platform.Spec(),
		dirPath,
		filter.Exclude,
		filter.Include,
	)
	if err != nil {
		return i, fmt.Errorf("host directory %s: %w", dirPath, err)
	}
	return LoadBlob(ctx, srv, desc)
}

func (host *Host) File(ctx context.Context, srv *dagql.Server, filePath string) (dagql.Instance[*File], error) {
	fileDir, fileName := filepath.Split(filePath)
	var i dagql.Instance[*File]
	if err := srv.Select(ctx, srv.Root(), &i, dagql.Selector{
		Field: "host",
	}, dagql.Selector{
		Field: "directory",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.NewString(fileDir),
			},
			{
				Name:  "include",
				Value: dagql.ArrayInput[dagql.String]{dagql.NewString(fileName)},
			},
		},
	}, dagql.Selector{
		Field: "file",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.NewString(fileName),
			},
		},
	}); err != nil {
		return i, err
	}
	return i, nil
}

func (host *Host) SetSecretFile(ctx context.Context, srv *dagql.Server, secretName string, path string) (i dagql.Instance[*Secret], err error) {
	accessor, err := GetLocalSecretAccessor(ctx, host.Query, secretName)
	if err != nil {
		return i, err
	}

	secretFileContent, err := host.Query.Buildkit.ReadCallerHostFile(ctx, path)
	if err != nil {
		return i, fmt.Errorf("read secret file: %w", err)
	}

	if err := host.Query.Secrets.AddSecret(ctx, accessor, secretFileContent); err != nil {
		return i, err
	}
	err = srv.Select(ctx, srv.Root(), &i, dagql.Selector{
		Field: "secret",
		Args: []dagql.NamedInput{
			{
				Name:  "name",
				Value: dagql.NewString(secretName),
			},
			{
				Name:  "accessor",
				Value: dagql.Opt(dagql.NewString(accessor)),
			},
		},
	})
	return
}

func (host *Host) Socket(sockPath string) *Socket {
	return NewHostUnixSocket(sockPath)
}
