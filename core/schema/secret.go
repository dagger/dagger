package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type secretSchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &secretSchema{}

func (s *secretSchema) Name() string {
	return "secret"
}

func (s *secretSchema) Schema() string {
	return Secret
}

var secretIDResolver = stringResolver(core.SecretID(""))

func (s *secretSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"SecretID": secretIDResolver,
		"Query": router.ObjectResolver{
			"secret": router.ToResolver(s.secret),
		},
		"Secret": router.ObjectResolver{
			"plaintext": router.ToResolver(s.plaintext),
		},
	}
}

func (s *secretSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type secretArgs struct {
	ID core.SecretID
}

func (s *secretSchema) secret(ctx *router.Context, parent any, args secretArgs) (*core.Secret, error) {
	return &core.Secret{
		ID: args.ID,
	}, nil
}

func (s *secretSchema) plaintext(ctx *router.Context, parent core.Secret, args any) (string, error) {
	bytes, err := parent.Plaintext(ctx, s.gw)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
