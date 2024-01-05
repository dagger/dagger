package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type secretSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &secretSchema{}

func (s *secretSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("setSecret", s.setSecret).
			Doc(`Sets a secret given a user defined name to its plaintext and returns the secret.`,
				`The plaintext value is limited to a size of 128000 bytes.`).
			ArgDoc("name", `The user defined name for this secret`).
			ArgDoc("plaintext", `The plaintext of the secret`).
			ArgSensitive("plaintext").
			Impure(),

		dagql.Func("secret", s.secret).
			Doc(`Reference a secret by name.`),
	}.Install(s.srv)

	dagql.Fields[*core.Secret]{
		dagql.Func("plaintext", s.plaintext).Impure().
			Doc(`The value of this secret.`),
	}.Install(s.srv)
}

type secretArgs struct {
	Name string
}

func (s *secretSchema) secret(ctx context.Context, parent *core.Query, args secretArgs) (*core.Secret, error) {
	return parent.NewSecret(args.Name), nil
}

type setSecretArgs struct {
	Name      string
	Plaintext string `sensitive:"true"` // NB: redundant with ArgSensitive above
}

func (s *secretSchema) setSecret(ctx context.Context, parent *core.Query, args setSecretArgs) (dagql.Instance[*core.Secret], error) {
	var inst dagql.Instance[*core.Secret]
	if err := parent.Secrets.AddSecret(ctx, args.Name, []byte(args.Plaintext)); err != nil {
		return inst, err
	}
	// NB: to avoid putting the plaintext value in the graph, return a freshly
	// minted Object that just gets the secret by name
	if err := s.srv.Select(ctx, s.srv.Root(), &inst, dagql.Selector{
		Field: "secret",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString(args.Name)},
		},
	}); err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *secretSchema) plaintext(ctx context.Context, parent *core.Secret, args struct{}) (dagql.String, error) {
	bytes, err := parent.Plaintext(ctx)
	if err != nil {
		return "", err
	}

	return dagql.NewString(string(bytes)), nil
}
