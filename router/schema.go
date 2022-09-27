package router

import (
	"context"
	"encoding/json"

	"github.com/graphql-go/graphql"
)

type LoadedSchema interface {
	Name() string
	Schema() string
}

type ExecutableSchema interface {
	LoadedSchema
	Resolvers() Resolvers
	Dependencies() []ExecutableSchema
}

type Resolvers map[string]Resolver

type Resolver interface {
	_resolver()
}

type ObjectResolver map[string]graphql.FieldResolveFn

func (ObjectResolver) _resolver() {}

type ScalarResolver struct {
	Serialize    graphql.SerializeFn
	ParseValue   graphql.ParseValueFn
	ParseLiteral graphql.ParseLiteralFn
}

func (ScalarResolver) _resolver() {}

type StaticSchemaParams struct {
	Name         string
	Schema       string
	Resolvers    Resolvers
	Dependencies []ExecutableSchema
}

func StaticSchema(p StaticSchemaParams) ExecutableSchema {
	return &staticSchema{p}
}

var _ ExecutableSchema = &staticSchema{}

type staticSchema struct {
	StaticSchemaParams
}

func (s *staticSchema) Name() string {
	return s.StaticSchemaParams.Name
}

func (s *staticSchema) Schema() string {
	return s.StaticSchemaParams.Schema
}

func (s *staticSchema) Resolvers() Resolvers {
	return s.StaticSchemaParams.Resolvers
}

func (s *staticSchema) Dependencies() []ExecutableSchema {
	return s.StaticSchemaParams.Dependencies
}

type Context struct {
	context.Context
	ResolveInfo graphql.ResolveInfo
}

func ToResolver[P any, A any, R any](f func(*Context, P, A) (R, error)) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		ctx := Context{
			Context:     p.Context,
			ResolveInfo: p.Info,
		}

		var args A
		argBytes, err := json.Marshal(p.Args)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(argBytes, &args); err != nil {
			return nil, err
		}

		var parent P
		parentBytes, err := json.Marshal(p.Source)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(parentBytes, &parent); err != nil {
			return nil, err
		}

		res, err := f(&ctx, parent, args)
		if err != nil {
			return nil, err
		}

		return res, nil
	}
}

func PassthroughResolver(p graphql.ResolveParams) (any, error) {
	return ToResolver(func(ctx *Context, parent any, args any) (any, error) {
		return struct{}{}, nil
	})(p)
}
