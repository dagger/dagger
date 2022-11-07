package router

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/graphql"
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
	ResolveParams graphql.ResolveParams
	SessionID     string
}

type sessionIDKey struct{}

// ToResolver transforms any function f with a *Context, a parent P and some args A that returns a Response R and an error
// into a graphql resolver graphql.FieldResolveFn.
func ToResolver[P any, A any, R any](f func(*Context, P, A) (R, error)) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		sid, ok := p.Context.Value(sessionIDKey{}).(string)
		if !ok {
			sid = ""
		}

		ctx := Context{
			Context:       p.Context,
			ResolveParams: p,
			SessionID:     sid,
		}

		var args A
		argBytes, err := json.Marshal(p.Args)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal args: %w", err)
		}
		if err := json.Unmarshal(argBytes, &args); err != nil {
			return nil, fmt.Errorf("failed to unmarshal args: %w", err)
		}

		var parent P
		parentBytes, err := json.Marshal(p.Source)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal parent: %w", err)
		}
		if err := json.Unmarshal(parentBytes, &parent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal parent: %w", err)
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

func ErrResolver(err error) graphql.FieldResolveFn {
	return ToResolver(func(ctx *Context, parent any, args any) (any, error) {
		return nil, err
	})
}
