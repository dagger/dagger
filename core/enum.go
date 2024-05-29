package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

type EnumObject struct {
	Module  *Module
	TypeDef *EnumTypeDef
	Values  []string
}

func (enum *EnumObject) Type() *ast.Type {
	return &ast.Type{
		NamedType: enum.TypeDef.Name,
		NonNull:   true,
	}
}

func (enum *EnumObject) TypeDescription() string {
	return formatGqlDescription(enum.TypeDef.Description)
}

func (enum *EnumObject) Install(ctx context.Context, dag *dagql.Server) error {
	// TODO: How do I transform my enum into a dagql.EnumValue?
	// dagql.NewEnum()
	// so I can register that to the server

	return nil
}
