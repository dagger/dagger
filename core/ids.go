package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

type JSONValueID = dagql.ID[*JSONValue]

type AddressID = dagql.ID[*Address]

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

type ID struct {
	Inner *call.ID
}

func (id ID) Load(ctx context.Context, srv *dagql.Server) (dagql.AnyResult, error) {
	return srv.Load(ctx, id.Inner)
}

func (ID) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ID",
		NonNull:   true,
	}
}

var _ dagql.Typed = ID{}

func (ID) TypeName() string {
	return "ID"
}

func (ID) TypeDescription() string {
	return "A generic object ID"
}

var _ dagql.ScalarType = ID{}

var _ dagql.Input = ID{}

func (id ID) Decoder() dagql.InputDecoder {
	return id
}

func (id ID) ToLiteral() call.Literal {
	return call.NewLiteralID(id.Inner)
}

func (id ID) DecodeInput(val any) (res dagql.Input, err error) {
	switch x := val.(type) {
	case string:
		if x == "" {
			return nil, nil
		}
		newID := call.New()
		err := newID.Decode(x)
		if err != nil {
			return nil, fmt.Errorf("invalid ID string: %w", err)
		}
		return ID{newID}, nil
	case *call.ID:
		return ID{x}, nil
	default:
		return nil, fmt.Errorf("cannot convert %T to ID", val)
	}
}
