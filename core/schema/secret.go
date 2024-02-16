package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
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
	Name  string
	Scope dagql.Optional[dagql.String]
}

func (s *secretSchema) secret(ctx context.Context, parent *core.Query, args secretArgs) (*core.Secret, error) {
	scope := string(args.Scope.GetOr(""))
	if scope == "" {
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, err
		}
		scope = clientMetadata.ClientID
	}

	fmt.Printf("secret %q %q\n", scope, args.Name)
	return parent.NewSecret(args.Name, scope), nil
}

type setSecretArgs struct {
	Name      string
	Plaintext string `sensitive:"true"` // NB: redundant with ArgSensitive above
}

func (s *secretSchema) setSecret(ctx context.Context, parent *core.Query, args setSecretArgs) (dagql.Instance[*core.Secret], error) {
	var inst dagql.Instance[*core.Secret]

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, err
	}

	fmt.Printf("setSecret %q %q = %q\n", clientMetadata.ClientID, args.Name, args.Plaintext)
	if err := parent.Secrets.AddSecret(ctx, clientMetadata.ClientID, args.Name, []byte(args.Plaintext)); err != nil {
		return inst, err
	}
	// NB: to avoid putting the plaintext value in the graph, return a freshly
	// minted Object that just gets the secret by name
	if err := s.srv.Select(ctx, s.srv.Root(), &inst, dagql.Selector{
		Field: "secret",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString(args.Name)},
			{Name: "scope", Value: dagql.Opt(dagql.NewString(clientMetadata.ClientID))},
		},
	}); err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *secretSchema) plaintext(ctx context.Context, secret *core.Secret, args struct{}) (dagql.String, error) {
	fmt.Printf("plaintext %q %q\n", secret.Scope, secret.Name)

	// XXX: this shouldn't print the scope on error
	bytes, err := secret.Query.Secrets.GetSecret(ctx, secret.Scope+"/"+secret.Name)
	if err != nil {
		return "", err
	}

	return dagql.NewString(string(bytes)), nil
}
