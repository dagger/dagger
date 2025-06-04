package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

type ID dagql.String

func (id ID) Load(ctx context.Context, srv *dagql.Server) (dagql.Object, error) {
	var callID = new(call.ID)
	err := callID.Decode(dagql.String(id).String())
	if err != nil {
		return nil, err
	}
	return srv.Load(ctx, callID)
}

func (ID) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Object", // FIXME: ID?
		NonNull:   true,
	}
}

type ContainerID = dagql.ID[*Container]

type ServiceID = dagql.ID[*Service]

type CacheVolumeID = dagql.ID[*CacheVolume]

type DirectoryID = dagql.ID[*Directory]

type FileID = dagql.ID[*File]

type SecretID = dagql.ID[*Secret]

type ModuleID = dagql.ID[*Module]

type ModuleSourceID = dagql.ID[*ModuleSource]

type FunctionID = dagql.ID[*Function]

type FunctionArgID = dagql.ID[*FunctionArg]

type TypeDefID = dagql.ID[*TypeDef]

type SourceMapID = dagql.ID[*SourceMap]

type GeneratedCodeID = dagql.ID[*GeneratedCode]

type GitRepositoryID = dagql.ID[*GitRepository]

type GitRefID = dagql.ID[*GitRef]

type SocketID = dagql.ID[*Socket]

type LLMID = dagql.ID[*LLM]

type EnvID = dagql.ID[*Env]
