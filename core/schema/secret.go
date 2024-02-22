package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
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
	Name     string
	Accessor dagql.Optional[dagql.String]
}

func (s *secretSchema) secret(ctx context.Context, parent *core.Query, args secretArgs) (*core.Secret, error) {
	accessor := string(args.Accessor.GetOr(""))
	if accessor == "" {
		m, err := parent.CurrentModule(ctx)
		if err != nil {
			return nil, err
		}
		d, err := m.InstanceID.Digest()
		if err != nil {
			return nil, err
		}
		accessor = core.NewSecretAccessor(args.Name, d.String())
	}

	return parent.NewSecret(accessor), nil
}

type setSecretArgs struct {
	Name      string
	Plaintext string `sensitive:"true"` // NB: redundant with ArgSensitive above
}

func (s *secretSchema) setSecret(ctx context.Context, parent *core.Query, args setSecretArgs) (dagql.Instance[*core.Secret], error) {
	var inst dagql.Instance[*core.Secret]

	m, err := parent.CurrentModule(ctx)
	if err != nil {
		return inst, err
	}
	d, err := m.InstanceID.Digest()
	if err != nil {
		return inst, err
	}
	accessor := core.NewSecretAccessor(args.Name, d.String())

	if err := parent.Secrets.AddSecret(ctx, accessor, []byte(args.Plaintext)); err != nil {
		return inst, err
	}
	// NB: to avoid putting the plaintext value in the graph, return a freshly
	// minted Object that just gets the secret by name
	if err := s.srv.Select(ctx, s.srv.Root(), &inst, dagql.Selector{
		Field: "secret",
		Args: []dagql.NamedInput{
			{
				Name:  "name",
				Value: dagql.NewString(args.Name),
			},
			{
				Name:  "accessor",
				Value: dagql.Opt(dagql.NewString(accessor)),
			},
		},
	}); err != nil {
		return inst, err
	}

	return inst, nil
}

func (s *secretSchema) plaintext(ctx context.Context, secret *core.Secret, args struct{}) (dagql.String, error) {
	bytes, err := secret.Query.Secrets.GetSecret(ctx, secret.Accessor)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return "", errors.Wrapf(secrets.ErrNotFound, "secret %s", secret.Name)
		}
		return "", err
	}

	return dagql.NewString(string(bytes)), nil
}
