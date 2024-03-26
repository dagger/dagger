package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/moby/buildkit/session/secrets"
	"github.com/pkg/errors"
)

type secretSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &secretSchema{}

func (s *secretSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("setSecret", s.setSecret).
			Impure("`setSecret` mutates state in the internal secret store.").
			Doc(`Sets a secret given a user defined name to its plaintext and returns the secret.`,
				`The plaintext value is limited to a size of 128000 bytes.`).
			ArgDoc("name", `The user defined name for this secret`).
			ArgDoc("plaintext", `The plaintext of the secret`).
			ArgSensitive("plaintext"),

		dagql.Func("secret", s.secret).
			Impure("A secret is scoped to the client that created it.").
			Doc(`Reference a secret by name.`),
	}.Install(s.srv)

	dagql.Fields[*core.Secret]{
		dagql.Func("plaintext", s.plaintext).
			Impure("A secret's `plaintext` value in the internal secret store state can change.").
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

func (s *secretSchema) setSecret(ctx context.Context, _ *core.Query, args setSecretArgs) (i dagql.Instance[*core.Secret], err error) {
	if err := core.CurrentQuery(ctx).Secrets.AddSecret(ctx, args.Name, []byte(args.Plaintext)); err != nil {
		return i, err
	}

	// NB: to avoid putting the plaintext value in the graph, return a freshly
	// minted Object that just gets the secret by name
	if err := s.srv.Select(ctx, s.srv.Root(), &i, dagql.Selector{
		Field: "secret",
		Args: []dagql.NamedInput{
			{
				Name:  "name",
				Value: dagql.NewString(args.Name),
			},
		},
	}); err != nil {
		return i, err
	}

	return i, nil
}

func (s *secretSchema) plaintext(ctx context.Context, secret *core.Secret, args struct{}) (dagql.String, error) {
	bytes, err := core.CurrentQuery(ctx).Secrets.GetSecret(ctx, secret.Name)
	// bytes, err := secret.Query.Secrets.GetSecret(ctx, secret.Name)
	if err != nil {
		// TODO:
		// TODO:
		// TODO:
		// TODO:
		// TODO:
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return "", err
		}
		fmt.Printf("PLAINTEXT CLIENT %s %s %s\n", secret.Query.ClientID, secret.Name, clientMetadata.ClientID)

		if errors.Is(err, secrets.ErrNotFound) {
			return "", errors.Wrapf(secrets.ErrNotFound, "secret %s", secret.Name)
		}
		return "", err
	}

	return dagql.NewString(string(bytes)), nil
}
