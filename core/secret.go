package core

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/client/secretprovider"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Secret is a content-addressed secret.
type Secret struct {
	Query *Query

	// The URI of the secret, if it's stored in a remote store.
	URI string

	// The user-designated name of the secret.
	Name string

	// The id of the buildkit session the secret will be retrieved through.
	BuildkitSessionID string
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
	cp := *secret
	return &cp
}

/*
func (secret *Secret) OnReturn(ctx context.Context, root dagql.Typed) error {
	query, ok := root.(*Query)
	if !ok {
		return fmt.Errorf("expected *Query, got %T", root)
	}
	callerSecretStore, err := query.Secrets(ctx)
	if err != nil {
		return err
	}
}
*/

type SecretStore struct {
	bkSessionManager *bksession.Manager
	secrets          map[digest.Digest]dagql.Instance[*Secret]
	plaintexts       map[digest.Digest][]byte
	mu               sync.RWMutex
}

func NewSecretStore(bkSessionManager *bksession.Manager) *SecretStore {
	return &SecretStore{
		secrets:          map[digest.Digest]dagql.Instance[*Secret]{},
		plaintexts:       map[digest.Digest][]byte{},
		bkSessionManager: bkSessionManager,
	}
}

func (store *SecretStore) AddSecret(secret dagql.Instance[*Secret]) error {
	if secret.Self == nil {
		return fmt.Errorf("secret must not be nil")
	}
	if secret.Self.Query == nil {
		return fmt.Errorf("secret must have a query")
	}

	_, _, err := secretprovider.ResolverForID(secret.Self.URI)
	if err != nil {
		return err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.secrets[secret.ID().Digest()] = secret
	return nil
}

func (store *SecretStore) AddSecretFromOtherStore(
	ctx context.Context,
	secret dagql.Instance[*Secret],
	otherStore *SecretStore,
) error {
	// TODO: uggo
	if !strings.HasPrefix(secret.Self.URI, "named://") {
		return store.AddSecret(secret)
	}

	plaintext, err := otherStore.GetSecretPlaintext(ctx, secret.ID().Digest())
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.secrets[secret.ID().Digest()] = secret
	store.plaintexts[secret.ID().Digest()] = plaintext
	return nil
}

func (store *SecretStore) HasSecret(idDgst digest.Digest) bool {
	store.mu.RLock()
	defer store.mu.RUnlock()
	_, ok := store.secrets[idDgst]
	return ok
}

func (store *SecretStore) GetSecret(idDgst digest.Digest) (inst dagql.Instance[*Secret], ok bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secrets[idDgst]
	if !ok {
		return inst, false
	}
	return secret, true
}

func (store *SecretStore) GetSecretName(idDgst digest.Digest) (string, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secrets[idDgst]
	if !ok {
		return "", false
	}
	return secret.Self.Name, true
}

func (store *SecretStore) GetSecretURI(idDgst digest.Digest) (string, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secrets[idDgst]
	if !ok {
		return "", false
	}
	return secret.Self.URI, true
}

func (store *SecretStore) GetSecretNameOrURI(idDgst digest.Digest) (string, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secrets[idDgst]
	if !ok {
		return "", false
	}
	if secret.Self.URI != "" {
		return secret.Self.URI, true
	}
	if secret.Self.Name != "" {
		return secret.Self.Name, true
	}
	return "", true
}

func (store *SecretStore) SetSecretPlaintext(idDgst digest.Digest, plaintext []byte) error {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secrets[idDgst]
	if !ok {
		return fmt.Errorf("secret %s: %w", idDgst, secrets.ErrNotFound)
	}
	// TODO: uggo
	if !strings.HasPrefix(secret.Self.URI, "named://") {
		return fmt.Errorf("cannot set plaintext for remote secret %s", idDgst)
	}
	store.plaintexts[idDgst] = plaintext
	return nil
}

func (store *SecretStore) GetSecretPlaintext(ctx context.Context, idDgst digest.Digest) ([]byte, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secrets[idDgst]
	if !ok {
		return nil, fmt.Errorf("secret %s: %w", idDgst, secrets.ErrNotFound)
	}

	// If the secret is stored locally (setSecret), return the plaintext.
	// TODO: uggo
	if strings.HasPrefix(secret.Self.URI, "named://") {
		plaintext, ok := store.plaintexts[idDgst]
		if !ok {
			return nil, fmt.Errorf("secret %s plaintext: %w", idDgst, secrets.ErrNotFound)
		}
		return plaintext, nil
	}

	buildkitSessionID := secret.Self.BuildkitSessionID
	if buildkitSessionID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "missing buildkit session id")
	}
	caller, err := store.bkSessionManager.Get(ctx, buildkitSessionID, true)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get buildkit session: %s", err)
	}

	resp, err := secrets.NewSecretsClient(caller.Conn()).GetSecret(ctx, &secrets.GetSecretRequest{
		ID: secret.Self.URI,
	})
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (store *SecretStore) AsBuildkitSecretStore() secrets.SecretStore {
	return &buildkitSecretStore{inner: store}
}

// adapts our SecretStore to the interface buildkit wants
type buildkitSecretStore struct {
	inner *SecretStore
}

var _ secrets.SecretStore = &buildkitSecretStore{}

func (bkStore *buildkitSecretStore) GetSecret(ctx context.Context, llbID string) ([]byte, error) {
	return bkStore.inner.GetSecretPlaintext(ctx, digest.Digest(llbID))
}
