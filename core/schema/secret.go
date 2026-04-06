package schema

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

type secretSchema struct{}

var _ SchemaResolvers = &secretSchema{}

func (s *secretSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFunc("secret", s.secret).
			WithInput(dagql.PerCallInput).
			Doc(`Creates a new secret.`).
			Args(
				dagql.Arg("uri").Doc(`The URI of the secret store`),
				dagql.Arg("cacheKey").Doc(
					`If set, the given string will be used as the cache key for this secret. This means that any secrets with the same cache key will be considered equivalent in terms of cache lookups, even if they have different URIs or plaintext values.`,
					`For example, two secrets with the same cache key provided as secret env vars to other wise equivalent containers will result in the container withExecs hitting the cache for each other.`,
					`If not set, the cache key for the secret will be derived from its plaintext value as looked up when the secret is constructed.`,
				),
			),

		dagql.NodeFunc("setSecret", s.setSecret).
			WithInput(dagql.PerCallInput).
			Doc(`Sets a secret given a user defined name to its plaintext and returns the secret.`,
				`The plaintext value is limited to a size of 128000 bytes.`).
			Args(
				dagql.Arg("name").
					Doc(`The user defined name for this secret`),
				dagql.Arg("plaintext").
					Sensitive().
					Doc(`The plaintext of the secret`),
			),
	}.Install(srv)

	dagql.Fields[*core.Secret]{
		dagql.NodeFunc("name", s.name).
			Doc(`The name of this secret.`),

		dagql.NodeFunc("uri", s.uri).
			Doc(`The URI of this secret.`),

		dagql.NodeFunc("plaintext", s.plaintext).
			Sensitive().
			DoNotCache("Do not include plaintext secret in the cache.").
			Doc(`The value of this secret.`),
	}.Install(srv)
}

type secretArgs struct {
	URI      string
	CacheKey dagql.Optional[dagql.String]
}

func (s *secretSchema) secret(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Query],
	args secretArgs,
) (dagql.ObjectResult[*core.Secret], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to get dagql server: %w", err)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to get client metadata from context: %w", err)
	}
	if clientMetadata.SessionID == "" {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to get session ID from client metadata")
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to get dagql cache: %w", err)
	}
	if _, _, err := secretprovider.ResolverForID(args.URI); err != nil {
		return dagql.ObjectResult[*core.Secret]{}, err
	}

	concreteVal := &core.Secret{
		URIVal:         args.URI,
		SourceClientID: clientMetadata.ClientID,
	}
	var handle dagql.SessionResourceHandle
	if args.CacheKey.Valid {
		handle = core.SecretHandleFromCacheKey(string(args.CacheKey.Value))
	} else {
		plaintext, err := concreteVal.Plaintext(ctx)
		if err != nil {
			slog.Warn("failed to get secret plaintext, falling back to random cache key", "uri", args.URI, "error", err)
			plaintext = make([]byte, 32)
			if _, err := cryptorand.Read(plaintext); err != nil {
				return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to read random bytes: %w", err)
			}
		}
		handle = core.SecretHandleFromPlaintext(parent.Self().SecretSalt(), plaintext)
	}
	if handle == "" {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("secret must have a session resource handle")
	}

	handleVal := &core.Secret{
		Handle: handle,
	}
	handleRes, err := dagql.NewObjectResultForCurrentCall(ctx, srv, handleVal)
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to create handle secret result: %w", err)
	}
	handleRes, err = handleRes.WithContentDigest(ctx, digest.Digest(handle))
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, err
	}
	handleRes, err = handleRes.WithSessionResourceHandle(ctx, handle)
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, err
	}

	if err := cache.BindSessionResource(ctx, clientMetadata.SessionID, clientMetadata.ClientID, handle, concreteVal); err != nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to bind concrete secret: %w", err)
	}

	return handleRes, nil
}

type setSecretArgs struct {
	Name      string
	Plaintext string `sensitive:"true"` // NB: redundant with ArgSensitive above
}

func (s *secretSchema) setSecret(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Query],
	args setSecretArgs,
) (dagql.ObjectResult[*core.Secret], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to get dagql server: %w", err)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to get client metadata from context: %w", err)
	}
	if clientMetadata.SessionID == "" {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to get session ID from client metadata")
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to get dagql cache: %w", err)
	}

	curCall := dagql.CurrentCall(ctx)
	if curCall == nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("current call is nil")
	}
	sanitizedCall := *curCall
	sanitizedCall.Args = []*dagql.ResultCallArg{
		{
			Name: "name",
			Value: &dagql.ResultCallLiteral{
				Kind:        dagql.ResultCallLiteralKindString,
				StringValue: args.Name,
			},
		},
		{
			Name: "plaintext",
			Value: &dagql.ResultCallLiteral{
				Kind:        dagql.ResultCallLiteralKindString,
				StringValue: "***",
			},
		},
	}

	concreteVal := &core.Secret{
		NameVal:      args.Name,
		PlaintextVal: []byte(args.Plaintext),
	}
	accessor, err := core.GetClientResourceAccessor(ctx, parent.Self(), args.Name)
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to get client resource accessor: %w", err)
	}
	handle := core.SetSecretHandle(args.Name, accessor)
	if handle == "" {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("setSecret must have a session resource handle")
	}

	handleVal := &core.Secret{
		Handle: handle,
	}
	handleRes, err := dagql.NewObjectResultForCall(handleVal, srv, &sanitizedCall)
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to create handle setSecret result: %w", err)
	}
	handleRes, err = handleRes.WithContentDigest(ctx, digest.Digest(handle))
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, err
	}
	handleRes, err = handleRes.WithSessionResourceHandle(ctx, handle)
	if err != nil {
		return dagql.ObjectResult[*core.Secret]{}, err
	}

	if err := cache.BindSessionResource(ctx, clientMetadata.SessionID, clientMetadata.ClientID, handle, concreteVal); err != nil {
		return dagql.ObjectResult[*core.Secret]{}, fmt.Errorf("failed to bind concrete setSecret value: %w", err)
	}

	return handleRes, nil
}

func (s *secretSchema) name(ctx context.Context, secret dagql.ObjectResult[*core.Secret], args struct{}) (string, error) {
	return secret.Self().Name(ctx)
}

func (s *secretSchema) uri(ctx context.Context, secret dagql.ObjectResult[*core.Secret], args struct{}) (string, error) {
	return secret.Self().URI(ctx)
}

func (s *secretSchema) plaintext(ctx context.Context, secret dagql.ObjectResult[*core.Secret], args struct{}) (string, error) {
	plaintext, err := secret.Self().Plaintext(ctx)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
