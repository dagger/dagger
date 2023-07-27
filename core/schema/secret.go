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
			"secret":    router.ToResolver(s.secret),
			"setSecret": router.ToResolver(s.setSecret),
		},
		"Secret": router.ObjectResolver{
			"id":        router.ToResolver(s.id),
			"plaintext": router.ToResolver(s.plaintext),
		},
	}
}

func (s *secretSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

func (s *secretSchema) id(ctx *router.Context, parent *core.Secret, args any) (core.SecretID, error) {
	return parent.ID()
}

type secretArgs struct {
	ID core.SecretID
}

func (s *secretSchema) secret(ctx *router.Context, parent any, args secretArgs) (*core.Secret, error) {
	return args.ID.ToSecret()
}

type SecretPlaintext string

// This method ensures that the progrock vertex info does not display the plaintext.
func (s SecretPlaintext) MarshalText() ([]byte, error) {
	return []byte("***"), nil
}

type setSecretArgs struct {
	Name      string
	Plaintext SecretPlaintext
}

func (s *secretSchema) setSecret(ctx *router.Context, parent any, args setSecretArgs) (*core.Secret, error) {
	secretID, err := s.secrets.AddSecret(ctx, args.Name, string(args.Plaintext))
	if err != nil {
		return nil, err
	}

	return secretID.ToSecret()
}

func (s *secretSchema) plaintext(ctx *router.Context, parent *core.Secret, args any) (string, error) {
	id, err := parent.ID()
	if err != nil {
		return "", err
	}

	bytes, err := s.secrets.GetSecret(ctx, id.String())
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}
