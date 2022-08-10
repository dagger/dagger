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
