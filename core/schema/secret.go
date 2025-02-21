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
			Impure("`secret` mutates state in the internal secret store.").
			Doc(`Creates a new secret.`).
			ArgDoc("uri", `The URI of the secret store`),

		dagql.Func("loadSecretFromName", s.loadSecretFromName).
			Doc(`Load a Secret from its Name.`),
	}.Install(s.srv)

	dagql.Fields[*core.Secret]{
		dagql.Func("name", s.name).
			Doc(`The name of this secret.`),
		dagql.Func("uri", s.uri).
			Doc(`The URI of this secret.`),
		dagql.Func("plaintext", s.plaintext).
			Sensitive().
			Impure("A secret's `plaintext` value in the internal secret store state can change.").
			Doc(`The value of this secret.`),
	}.Install(s.srv)
}

type loadSecretFromNameArgs struct {
	Name string

	// Accessor is the scoped per-module name, which should guarantee uniqueness.
	// It is used to ensure the dagql ID digest is unique per module; the digest is what's
	// used as the actual key for the secret store.
	Accessor dagql.Optional[dagql.String]
}

func (s *secretSchema) loadSecretFromName(ctx context.Context, parent *core.Query, args loadSecretFromNameArgs) (*core.Secret, error) {
	return &core.Secret{
		Query:    parent,
		IDDigest: dagql.CurrentID(ctx).Digest(),
	}, nil
}

type setSecretArgs struct {
	Name      string
	Plaintext string `sensitive:"true"` // NB: redundant with ArgSensitive above
}

func (s *secretSchema) setSecret(ctx context.Context, parent *core.Query, args setSecretArgs) (i dagql.Instance[*core.Secret], err error) {
	secretStore, err := parent.Secrets(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get secret store: %w", err)
	}

	accessor, err := core.GetClientResourceAccessor(ctx, parent, args.Name)
	if err != nil {
		return i, fmt.Errorf("failed to get client resource name: %w", err)
	}

	// NB: to avoid putting the plaintext value in the graph, return a freshly
	// minted Object that just gets the secret by name
	if err := s.srv.Select(ctx, s.srv.Root(), &i, dagql.Selector{
		Field: "loadSecretFromName",
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
		return i, fmt.Errorf("failed to select secret: %w", err)
	}

	if err := secretStore.AddSecret(i.Self, args.Name, []byte(args.Plaintext)); err != nil {
		return i, fmt.Errorf("failed to add secret: %w", err)
	}

	return i, nil
}

type secretArgs struct {
	URI string
}

func (s *secretSchema) secret(ctx context.Context, parent *core.Query, args secretArgs) (i dagql.Instance[*core.Secret], err error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get client metadata from context: %w", err)
	}

	secretStore, err := parent.Secrets(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get secret store: %w", err)
	}

	accessor, err := core.GetClientResourceAccessor(ctx, parent, args.URI)
	if err != nil {
		return i, fmt.Errorf("failed to get client resource name: %w", err)
	}

	// NB: to avoid putting the plaintext value in the graph, return a freshly
	// minted Object that just gets the secret by name
	if err := s.srv.Select(ctx, s.srv.Root(), &i, dagql.Selector{
		Field: "loadSecretFromName",
		Args: []dagql.NamedInput{
			{
				Name:  "name",
				Value: dagql.NewString(args.URI),
			},
			{
				Name:  "accessor",
				Value: dagql.Opt(dagql.NewString(accessor)),
			},
		},
	}); err != nil {
		return i, fmt.Errorf("failed to select secret: %w", err)
	}

	if err := secretStore.NewSecret(i.Self, clientMetadata.ClientID, args.URI); err != nil {
		return i, fmt.Errorf("failed to map secret: %w", err)
	}

	return i, nil
}

func (s *secretSchema) name(ctx context.Context, secret *core.Secret, args struct{}) (dagql.String, error) {
	secretStore, err := secret.Query.Secrets(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get secret store: %w", err)
	}
	name, ok := secretStore.GetSecretName(secret.IDDigest)
	if !ok {
		return "", fmt.Errorf("secret not found: %s", secret.IDDigest)
	}

	return dagql.NewString(name), nil
}

func (s *secretSchema) uri(ctx context.Context, secret *core.Secret, args struct{}) (dagql.String, error) {
	secretStore, err := secret.Query.Secrets(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get secret store: %w", err)
	}
	name, ok := secretStore.GetSecretURI(secret.IDDigest)
	if !ok {
		return "", fmt.Errorf("secret not found: %s", secret.IDDigest)
	}

	return dagql.NewString(name), nil
}

func (s *secretSchema) plaintext(ctx context.Context, secret *core.Secret, args struct{}) (dagql.String, error) {
	secretStore, err := secret.Query.Secrets(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get secret store: %w", err)
	}
	plaintext, err := secretStore.GetSecretPlaintext(ctx, secret.IDDigest)
	if err != nil {
		return "", err
	}

	return dagql.NewString(string(plaintext)), nil
}
