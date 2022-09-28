package core

import (
	"fmt"

	"github.com/graphql-go/graphql/language/ast"
	"go.dagger.io/dagger/router"
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
			"secret":    router.ToResolver(s.secret),
			"addSecret": router.ToResolver(s.addSecret),
		},
	}
}

func (s *secretSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type secretArgs struct {
	ID string `json:"id"`
}

func (s *secretSchema) secret(ctx *router.Context, parent any, args secretArgs) (string, error) {
	plaintext, err := s.secretStore.GetSecret(ctx, args.ID)
	if err != nil {
		return "", fmt.Errorf("secret %s: %w", args.ID, err)
	}
	return string(plaintext), nil
}

type addSecretArgs struct {
	Plaintext string
}

func (s *secretSchema) addSecret(ctx *router.Context, parent any, args addSecretArgs) (string, error) {
	return s.secretStore.AddSecret(ctx, []byte(args.Plaintext)), nil
}
