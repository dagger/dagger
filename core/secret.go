package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/crypto/argon2"
)

// Secret is a content-addressed secret.
type Secret struct {
	Handle         dagql.SessionResourceHandle
	URIVal         string
	NameVal        string
	PlaintextVal   []byte `json:"-"`
	SourceClientID string
}

func (*Secret) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Secret",
		NonNull:   true,
	}
}

func (*Secret) TypeDescription() string {
	return "A reference to a secret value, which can be handled more safely than the value itself."
}

func (secret *Secret) Clone() *Secret {
	if secret == nil {
		return nil
	}
	cp := *secret
	if secret.PlaintextVal != nil {
		cp.PlaintextVal = append([]byte(nil), secret.PlaintextVal...)
	}
	return &cp
}

type persistedSecretPayload struct {
	Handle dagql.SessionResourceHandle `json:"handle,omitempty"`
	Name   string                      `json:"name,omitempty"`
}

func (secret *Secret) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	payload := persistedSecretPayload{}
	if secret != nil {
		payload.Handle = secret.Handle
		payload.Name = secret.NameVal
	}
	return json.Marshal(payload)
}

func (*Secret) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, call *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedSecretPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &persisted); err != nil {
			return nil, fmt.Errorf("decode persisted secret payload: %w", err)
		}
	}
	return &Secret{
		Handle:  persisted.Handle,
		NameVal: persisted.Name,
	}, nil
}

//nolint:dupl // symmetric with resolveSessionSocket; sharing hides Secret vs Socket semantics
func resolveSessionSecret(ctx context.Context, secret *Secret) (*Secret, error) {
	if secret == nil {
		return nil, nil
	}
	if secret.Handle == "" {
		return secret, nil
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve session secret %q: current client metadata: %w", secret.Handle, err)
	}
	if clientMetadata.SessionID == "" {
		return nil, fmt.Errorf("resolve session secret %q: empty session ID", secret.Handle)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve session secret %q: current dagql cache: %w", secret.Handle, err)
	}
	resolvedAny, err := cache.ResolveSessionResource(ctx, clientMetadata.SessionID, clientMetadata.ClientID, secret.Handle)
	if err != nil {
		return nil, err
	}
	resolved, ok := resolvedAny.(*Secret)
	if !ok {
		return nil, fmt.Errorf("resolve session secret %q: bound value is %T", secret.Handle, resolvedAny)
	}
	if resolved.Handle != "" {
		return nil, fmt.Errorf("resolve session secret %q: bound secret is still a handle", secret.Handle)
	}
	return resolved, nil
}

func (secret *Secret) Name(ctx context.Context) (string, error) {
	resolved, err := resolveSessionSecret(ctx, secret)
	if err != nil {
		return "", err
	}
	if resolved == nil {
		return "", nil
	}
	return resolved.NameVal, nil
}

func (secret *Secret) URI(ctx context.Context) (string, error) {
	resolved, err := resolveSessionSecret(ctx, secret)
	if err != nil {
		return "", err
	}
	if resolved == nil {
		return "", nil
	}
	return resolved.URIVal, nil
}

func (secret *Secret) Plaintext(ctx context.Context) ([]byte, error) {
	resolved, err := resolveSessionSecret(ctx, secret)
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, nil
	}
	if resolved.URIVal == "" {
		return append([]byte(nil), resolved.PlaintextVal...), nil
	}
	if resolved.SourceClientID == "" {
		return nil, fmt.Errorf("secret %q: missing source client ID", resolved.URIVal)
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := query.SpecificClientAttachableConn(ctx, resolved.SourceClientID)
	if err != nil {
		return nil, err
	}
	resp, err := secrets.NewSecretsClient(conn).GetSecret(ctx, &secrets.GetSecretRequest{
		ID: resolved.URIVal,
	})
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func SecretHandleFromCacheKey(cacheKey string) dagql.SessionResourceHandle {
	if cacheKey == "" {
		return ""
	}
	return dagql.SessionResourceHandle(hashutil.HashStrings(cacheKey))
}

func SetSecretHandle(name string, accessor string) dagql.SessionResourceHandle {
	if name == "" || accessor == "" {
		return ""
	}
	return dagql.SessionResourceHandle(hashutil.HashStrings(name, accessor))
}

func SecretHandleFromPlaintext(secretSalt []byte, plaintext []byte) dagql.SessionResourceHandle {
	const (
		timeCost = 10
		memory   = 2 * 1024
		threads  = 1
		keySize  = 32
	)
	key := argon2.IDKey(
		plaintext,
		secretSalt,
		timeCost,
		memory,
		threads,
		keySize,
	)
	b64Key := base64.RawStdEncoding.EncodeToString(key)
	return dagql.SessionResourceHandle(digest.Digest("argon2:" + b64Key))
}
