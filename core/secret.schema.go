package core

import (
	"fmt"

	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
)

var secretIDResolver = router.ScalarResolver{
	Serialize: func(value interface{}) interface{} {
		switch v := value.(type) {
		case string:
			return v
		default:
			panic(fmt.Sprintf("unexpected secret type %T", v))
		}
	},
	ParseValue: func(value interface{}) interface{} {
		switch v := value.(type) {
		case string:
			return v
		default:
			panic(fmt.Sprintf("unexpected secret value type %T: %+v", v, v))
		}
	},
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.StringValue:
			return valueAST.Value
		default:
			panic(fmt.Sprintf("unexpected secret literal type: %T", valueAST))
		}
	},
}

var _ router.ExecutableSchema = &secretSchema{}

type secretSchema struct {
	*baseSchema
}

func (s *secretSchema) Name() string {
	return "secret"
}

func (s *secretSchema) Schema() string {
	return `
scalar SecretID

extend type Core {
	"Look up a secret by ID"
	secret(id: SecretID!): String!

	"Add a secret"
	addSecret(plaintext: String!): SecretID!
}
`
}

func (s *secretSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"SecretID": secretIDResolver,
		"Core": router.ObjectResolver{
			"secret":    s.secret,
			"addSecret": s.addSecret,
		},
	}
}

func (s *secretSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

func (s *secretSchema) secret(p graphql.ResolveParams) (any, error) {
	id := p.Args["id"].(string)
	plaintext, err := s.secretStore.GetSecret(p.Context, id)
	if err != nil {
		return nil, fmt.Errorf("secret %s: %w", id, err)
	}
	return string(plaintext), nil
}

func (s *secretSchema) addSecret(p graphql.ResolveParams) (any, error) {
	plaintext := p.Args["plaintext"].(string)
	return s.secretStore.AddSecret(p.Context, []byte(plaintext)), nil
}
