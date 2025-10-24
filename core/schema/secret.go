package schema

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
	"golang.org/x/crypto/argon2"
)

type secretSchema struct{}

var _ SchemaResolvers = &secretSchema{}

func (s *secretSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFuncWithCacheKey("secret", s.secret, dagql.CachePerCall).
			Doc(`Creates a new secret.`).
			Args(
				dagql.Arg("uri").Doc(`The URI of the secret store`),
				dagql.Arg("cacheKey").Doc(
					`If set, the given string will be used as the cache key for this secret. This means that any secrets with the same cache key will be considered equivalent in terms of cache lookups, even if they have different URIs or plaintext values.`,
					`For example, two secrets with the same cache key provided as secret env vars to other wise equivalent containers will result in the container withExecs hitting the cache for each other.`,
					`If not set, the cache key for the secret will be derived from its plaintext value as looked up when the secret is constructed.`,
				),
			),

		dagql.NodeFuncWithCacheKey("setSecret", s.setSecret, dagql.CachePerCall).
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
) (i dagql.ObjectResult[*core.Secret], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get dagql server: %w", err)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get client metadata from context: %w", err)
	}

	secretStore, err := parent.Self().Secrets(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get secret store: %w", err)
	}

	secret := &core.Secret{
		URI:               args.URI,
		BuildkitSessionID: clientMetadata.ClientID,
	}
	i, err = dagql.NewObjectResultForCurrentID(ctx, srv, secret)
	if err != nil {
		return i, fmt.Errorf("failed to create instance: %w", err)
	}

	if args.CacheKey.Valid {
		i = i.WithObjectDigest(hashutil.HashStrings(string(args.CacheKey.Value)))
	} else {
		plaintext, err := secretStore.GetSecretPlaintextDirect(ctx, secret)
		if err != nil {
			// secret wasn't found, but since it may be available later at use, tolerate the error and just use a random cache key
			slog.Warn("failed to get secret plaintext, falling back to random cache key", "uri", args.URI, "error", err)
			plaintext = make([]byte, 32)
			if _, err := cryptorand.Read(plaintext); err != nil {
				return i, fmt.Errorf("failed to read random bytes: %w", err)
			}
		}

		/* Derive the cache key from the plaintext value using argon2.
		We avoid a simple xxh3/sha256/etc. hash since the cache key is public; it's sent around in IDs and stored on the local disk unencrypted.

		This is similar to the problems a web-server avoids when hashing passwords in that we want to make brute-forcing the secret from its hash
		infeasible, even in offline attacks. This argon2 hash takes on the order of 1-100ms, which is 10s of millions of times slower than the
		time to compute e.g. a sha256 hash on a modern GPU, but not slow enough to be a noticeable bottleneck in our execution.

		The main difference from the more typical password-hashing use-case is that we *don't* want a unique salt per secret since we need deterministic
		cache keys. Instead, we use a salt unique to the engine instance as a whole (stored on the local disk along-side the cache).
		*/
		const (
			// Argon2 is flexible in terms of time+memory tradeoffs, tuned by these parameters. We use a relatively low memory cost here and in exchange
			// increase the number of time (aka passes).
			time    = 10       // 10 passes
			memory  = 2 * 1024 // 2MB
			threads = 1        // no parallelism

			// byte size of the returned key; this is mostly arbitrarily chosen right now, with the only consideration being it should be large enough
			// to avoid collisions. 32 bytes should be more than enough.
			keySize = 32
		)
		key := argon2.IDKey(
			plaintext,
			parent.Self().SecretSalt(),
			time, memory, threads,
			keySize,
		)
		b64Key := base64.RawStdEncoding.EncodeToString(key)
		i = i.WithObjectDigest(digest.Digest("argon2:" + b64Key))
	}

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
	parent dagql.ObjectResult[*core.Query],
	args setSecretArgs,
) (i dagql.ObjectResult[*core.Secret], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get dagql server: %w", err)
	}

	accessor, err := core.GetClientResourceAccessor(ctx, parent.Self(), args.Name)
	if err != nil {
		return i, fmt.Errorf("failed to get client resource accessor: %w", err)
	}
	dgst := hashutil.HashStrings(
		args.Name,
		accessor,
	)

	callID := dagql.CurrentID(ctx).With(
		call.WithArgs(
			call.NewArgument("name", call.NewLiteralString(args.Name), false),
			// hide plaintext in the returned ID, we instead rely on the
			// digest of the ID for uniqueness+identity
			call.NewArgument("plaintext", call.NewLiteralString("***"), false),
		),
		call.WithCustomDigest(dgst),
	)

	secretStore, err := parent.Self().Secrets(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get secret store: %w", err)
	}
	secretVal := &core.Secret{
		Name:      args.Name,
		Plaintext: []byte(args.Plaintext),
	}
	secret, err := dagql.NewObjectResultForID(secretVal, srv, callID)
	if err != nil {
		return i, fmt.Errorf("failed to create secret instance: %w", err)
	}
	if err := secretStore.AddSecret(secret); err != nil {
		return i, fmt.Errorf("failed to add secret: %w", err)
	}

	return secret, nil
}

func (s *secretSchema) name(ctx context.Context, secret dagql.ObjectResult[*core.Secret], args struct{}) (string, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	secretStore, err := query.Secrets(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get secret store: %w", err)
	}
	name, ok := secretStore.GetSecretName(secret.ID().Digest())
	if !ok {
		return "", fmt.Errorf("secret not found: %s", secret.ID().Digest())
	}

	return name, nil
}

func (s *secretSchema) uri(ctx context.Context, secret dagql.ObjectResult[*core.Secret], args struct{}) (string, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	secretStore, err := query.Secrets(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get secret store: %w", err)
	}
	name, ok := secretStore.GetSecretURI(secret.ID().Digest())
	if !ok {
		return "", fmt.Errorf("secret not found: %s", secret.ID().Digest())
	}

	return name, nil
}

func (s *secretSchema) plaintext(ctx context.Context, secret dagql.ObjectResult[*core.Secret], args struct{}) (string, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	secretStore, err := query.Secrets(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get secret store: %w", err)
	}
	plaintext, err := secretStore.GetSecretPlaintext(ctx, secret.ID().Digest())
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
