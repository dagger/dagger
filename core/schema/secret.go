package schema

import (
	"context"

	"github.com/dagger/dagger/core"
)

type secretSchema struct {
	*MergedSchemas
}

var _ ExecutableSchema = &secretSchema{}

func (s *secretSchema) Name() string {
	return "secret"
}

func (s *secretSchema) SourceModuleName() string {
	return coreModuleName
}

func (s *secretSchema) Schema() string {
	return Secret
}

func (s *secretSchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Query": ObjectResolver{
			"secret":    ToCachedResolver(s.queryCache, s.secret),
			"setSecret": ToResolver(s.setSecret), // XXX(vito): test
		},
	}

	ResolveIDable[*core.Secret](s.queryCache, s.MergedSchemas, rs, "Secret", ObjectResolver{
		"plaintext": ToResolver(s.plaintext), // XXX(vito): test
	})

	return rs
}

type secretArgs struct {
	Name string
}

func (s *secretSchema) secret(ctx context.Context, parent *core.Query, args secretArgs) (*core.Secret, error) {
	return core.NewDynamicSecret(args.Name), nil
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

func (s *secretSchema) setSecret(ctx context.Context, parent *core.Query, args setSecretArgs) (*core.Secret, error) {
	secretID, err := s.secrets.AddSecret(ctx, args.Name, []byte(args.Plaintext))
	if err != nil {
		return nil, err
	}

	secret := core.NewDynamicSecret(args.Name)
	secret.SetID(secretID.ID)
	return secret, nil
}

func (s *secretSchema) plaintext(ctx context.Context, parent *core.Secret, args any) (string, error) {
	bytes, err := s.secrets.GetSecret(ctx, parent.Name)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}
