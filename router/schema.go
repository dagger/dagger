package router

import (
	"github.com/graphql-go/graphql"
)

type ExecutableSchema interface {
	Name() string
	Schema() string
	Operations() string
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

var _ ExecutableSchema = &staticSchema{}

type staticSchema struct {
	name         string
	schema       string
	operations   string
	resolvers    Resolvers
	dependencies []ExecutableSchema
}

func (s *staticSchema) Name() string {
	return s.name
}

func (s *staticSchema) Schema() string {
	return s.schema
}

func (s *staticSchema) Operations() string {
	return s.operations
}

func (s *staticSchema) Resolvers() Resolvers {
	return s.resolvers
}

func (s *staticSchema) Dependencies() []ExecutableSchema {
	return s.dependencies
}
