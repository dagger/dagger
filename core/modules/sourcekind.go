package modules

import (
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

type ModuleSourceKind string

var ModuleSourceKindEnum = dagql.NewEnum[ModuleSourceKind]()

var (
	ModuleSourceKindLocal = ModuleSourceKindEnum.Register("LOCAL_SOURCE")
	ModuleSourceKindGit   = ModuleSourceKindEnum.Register("GIT_SOURCE")
)

func (proto ModuleSourceKind) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleSourceKind",
		NonNull:   true,
	}
}

func (proto ModuleSourceKind) TypeDescription() string {
	return "The kind of module source."
}

func (proto ModuleSourceKind) Decoder() dagql.InputDecoder {
	return ModuleSourceKindEnum
}

func (proto ModuleSourceKind) ToLiteral() call.Literal {
	return ModuleSourceKindEnum.Literal(proto)
}
