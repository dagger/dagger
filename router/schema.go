package router

import (
	"github.com/graphql-go/graphql"
)

type ExecutableSchema interface {
	Schema() string
	Operations() string
	Resolvers() Resolvers
}

type Resolvers map[string]Resolver

type Resolver interface {
	__resolver()
}

type ObjectResolver map[string]graphql.FieldResolveFn

func (ObjectResolver) __resolver() {}

type ScalarResolver struct {
	Serialize    graphql.SerializeFn
	ParseValue   graphql.ParseValueFn
	ParseLiteral graphql.ParseLiteralFn
}

func (ScalarResolver) __resolver() {}

var _ ExecutableSchema = &staticSchema{}

type staticSchema struct {
	schema     string
	operations string
	resolvers  Resolvers
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
