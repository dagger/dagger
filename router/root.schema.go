package router

import (
	"github.com/containerd/containerd/platforms"
	"github.com/graphql-go/graphql"
)

type rootSchema struct {
}

func (r *rootSchema) Name() string {
	return "root"
}

// FIXME:(sipsma) platform should be enum, also this maybe should be in a different file
func (r *rootSchema) Schema() string {
	return `
type Query {
	"TODO"
	withPlatform(platform: String!): Query!
}
`
}

func (r *rootSchema) Operations() string {
	return ""
}

func (r *rootSchema) Resolvers() Resolvers {
	return Resolvers{
		"Query": ObjectResolver{
			"withPlatform": r.withPlatform,
		},
	}
}

func (r *rootSchema) Dependencies() []ExecutableSchema {
	return nil
}

func (r *rootSchema) withPlatform(p graphql.ResolveParams) (any, error) {
	parent := Parent[struct{}](p.Source)

	specifier, _ := p.Args["platform"].(string)
	if specifier != "" {
		var err error
		pl, err := platforms.Parse(specifier)
		if err != nil {
			return nil, err
		}
		parent.Platform = pl
	}

	return parent, nil
}

// TODO:
// TODO:
// TODO:
// TODO: This is extremely promising!!
/*
func convert[A any, R any](f func(context.Context, A) (R, error)) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		bytes, err := json.Marshal(p.Args)
		if err != nil {
			return nil, err
		}
		var args A
		if err := json.Unmarshal(bytes, &args); err != nil {
			return nil, err
		}
		return f(p.Context, args)
	}
}
*/
