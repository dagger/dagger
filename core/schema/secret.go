package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

type secretSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &secretSchema{}

func (s *secretSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.NodeFuncWithCacheKey("secret", s.secret, dagql.CachePerCall).
			Doc(`Creates a new secret.`).
			ArgDoc("uri", `The URI of the secret store`),

		dagql.NodeFuncWithCacheKey("setSecret", s.setSecret, dagql.CachePerCall).
			Doc(`Sets a secret given a user defined name to its plaintext and returns the secret.`,
				`The plaintext value is limited to a size of 128000 bytes.`).
			ArgDoc("name", `The user defined name for this secret`).
			ArgDoc("plaintext", `The plaintext of the secret`).
			ArgSensitive("plaintext"),

		// TODO: re-add LoadSecretFromName for back compat?
		// TODO: re-add LoadSecretFromName for back compat?
	}.Install(s.srv)

	dagql.Fields[*core.Secret]{
		dagql.NodeFunc("name", s.name).
			Doc(`The name of this secret.`),
		dagql.NodeFunc("uri", s.uri).
			Doc(`The URI of this secret.`),
		dagql.NodeFunc("plaintext", s.plaintext).
			Sensitive().
			DoNotCache("Do not include plaintext secret in the cache.").
			Doc(`The value of this secret.`),
	}.Install(s.srv)
}

type secretArgs struct {
	URI string
}

func (s *secretSchema) secret(
	ctx context.Context,
	parent dagql.Instance[*core.Query],
	args secretArgs,
) (i dagql.Instance[*core.Secret], err error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get client metadata from context: %w", err)
	}

	secretStore, err := parent.Self.Secrets(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get secret store: %w", err)
	}

	secret := &core.Secret{
		Query:             parent.Self,
		URI:               args.URI,
		BuildkitSessionID: clientMetadata.ClientID,
	}
	i, err = dagql.NewInstanceForCurrentID(ctx, s.srv, parent, secret)
	if err != nil {
		return i, fmt.Errorf("failed to create instance: %w", err)
	}

	accessor, err := core.GetClientResourceAccessor(ctx, parent.Self, args.URI)
	if err != nil {
		return i, fmt.Errorf("failed to get client resource accessor: %w", err)
	}
	dgst := dagql.HashFrom(args.URI, accessor)
	i = i.WithDigest(dgst)

	if err := secretStore.AddSecret(i); err != nil {
		return i, fmt.Errorf("failed to add secret: %w", err)
	}

	return i, nil
}

type setSecretArgs struct {
	Name      string
	Plaintext string `sensitive:"true"` // NB: redundant with ArgSensitive above
}

func (s *secretSchema) setSecret(
	ctx context.Context,
	parent dagql.Instance[*core.Query],
	args setSecretArgs,
) (i dagql.Instance[*core.Secret], err error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get client metadata from context: %w", err)
	}
	accessor, err := core.GetClientResourceAccessor(ctx, parent.Self, args.Name)
	if err != nil {
		return i, fmt.Errorf("failed to get client resource accessor: %w", err)
	}
	dgst := dagql.HashFrom(
		args.Name,
		accessor,
		clientMetadata.SessionID,
	)

	currentID := dagql.CurrentID(ctx)
	callID := currentID.Receiver().Append(
		currentID.Type().ToAST(),
		currentID.Field(),
		currentID.View(),
		currentID.Module(),
		0,
		dgst,
		call.NewArgument("name", call.NewLiteralString(args.Name), false),
		// hide plaintext in the returned ID, we instead rely on the
		// digest of the ID for uniqueness+identity
		call.NewArgument("plaintext", call.NewLiteralString("***"), false),
	)

	secretStore, err := parent.Self.Secrets(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get secret store: %w", err)
	}
	secretVal := &core.Secret{
		Query:     parent.Self,
		Name:      args.Name,
		Plaintext: []byte(args.Plaintext),
	}
	secret, err := dagql.NewInstanceForID(s.srv, parent, secretVal, callID)
	if err != nil {
		return i, fmt.Errorf("failed to create secret instance: %w", err)
	}
	if err := secretStore.AddSecret(secret); err != nil {
		return i, fmt.Errorf("failed to add secret: %w", err)
	}

	return secret, nil
}

func (s *secretSchema) name(ctx context.Context, secret dagql.Instance[*core.Secret], args struct{}) (dagql.String, error) {
	secretStore, err := secret.Self.Query.Secrets(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get secret store: %w", err)
	}
	name, ok := secretStore.GetSecretName(secret.ID().Digest())
	if !ok {
		return "", fmt.Errorf("secret not found: %s", secret.ID().Digest())
	}

	return dagql.NewString(name), nil
}

func (s *secretSchema) uri(ctx context.Context, secret dagql.Instance[*core.Secret], args struct{}) (dagql.String, error) {
	secretStore, err := secret.Self.Query.Secrets(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get secret store: %w", err)
	}
	name, ok := secretStore.GetSecretURI(secret.ID().Digest())
	if !ok {
		return "", fmt.Errorf("secret not found: %s", secret.ID().Digest())
	}

	return dagql.NewString(name), nil
}

func (s *secretSchema) plaintext(ctx context.Context, secret dagql.Instance[*core.Secret], args struct{}) (dagql.String, error) {
	secretStore, err := secret.Self.Query.Secrets(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get secret store: %w", err)
	}
	plaintext, err := secretStore.GetSecretPlaintext(ctx, secret.ID().Digest())
	if err != nil {
		return "", err
	}

	return dagql.NewString(string(plaintext)), nil
}
