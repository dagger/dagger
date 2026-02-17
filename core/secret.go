package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/client/secretprovider"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Secret is a content-addressed secret.
type Secret struct {
	// The URI of the secret, if it's stored in a remote store.
	URI string

	// The id of the buildkit session the secret will be retrieved through.
	BuildkitSessionID string

	// The user-designated name of the secret, if created by setSecret
	Name string

	// The secret plaintext, if created by setSecret
	Plaintext []byte `json:"-"` // we shouldn't be json marshalling this, but disclude just in case
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

type SecretStore struct {
	bkSessionManager *bksession.Manager

	// secrets are keyed by canonical digest (content digest when set,
	// otherwise recipe digest).
	secrets map[digest.Digest]dagql.ObjectResult[*Secret]
	// aliases from any seen secret ID digest (recipe or canonical) to canonical.
	canonicalDigestByIDDigest map[digest.Digest]digest.Digest

	mu sync.RWMutex
}

func NewSecretStore(bkSessionManager *bksession.Manager) *SecretStore {
	return &SecretStore{
		secrets:                   map[digest.Digest]dagql.ObjectResult[*Secret]{},
		canonicalDigestByIDDigest: map[digest.Digest]digest.Digest{},
		bkSessionManager:          bkSessionManager,
	}
}

func SecretIDDigest(id *call.ID) digest.Digest {
	if id == nil {
		return ""
	}
	if contentDigest := id.ContentDigest(); contentDigest != "" {
		return contentDigest
	}
	return id.Digest()
}

func SecretDigest(secret dagql.ObjectResult[*Secret]) digest.Digest {
	return SecretIDDigest(secret.ID())
}

func (store *SecretStore) AddSecret(secret dagql.ObjectResult[*Secret]) error {
	if secret.Self() == nil {
		return fmt.Errorf("secret must not be nil")
	}

	if secret.Self().URI != "" {
		_, _, err := secretprovider.ResolverForID(secret.Self().URI)
		if err != nil {
			return err
		}
	}

	canonicalDigest := SecretDigest(secret)
	if canonicalDigest == "" {
		return fmt.Errorf("secret must have a digest")
	}
	idDigest := secret.ID().Digest()

	store.mu.Lock()
	defer store.mu.Unlock()
	store.secrets[canonicalDigest] = secret
	store.canonicalDigestByIDDigest[canonicalDigest] = canonicalDigest
	if idDigest != "" {
		store.canonicalDigestByIDDigest[idDigest] = canonicalDigest
	}
	return nil
}

func (store *SecretStore) AddSecretFromOtherStore(srcStore *SecretStore, secret dagql.ObjectResult[*Secret]) error {
	secretDgst := SecretDigest(secret)
	srcSecret, ok := srcStore.GetSecret(secretDgst)
	if !ok {
		return fmt.Errorf("secret %s not found in source store", secretDgst)
	}
	return store.AddSecret(srcSecret)
}

func (store *SecretStore) secretByDigest(idDgst digest.Digest) (dagql.ObjectResult[*Secret], bool) {
	canonicalDigest, ok := store.canonicalDigestByIDDigest[idDgst]
	if !ok {
		canonicalDigest = idDgst
	}
	secret, ok := store.secrets[canonicalDigest]
	return secret, ok
}

func (store *SecretStore) HasSecret(idDgst digest.Digest) bool {
	store.mu.RLock()
	defer store.mu.RUnlock()
	_, ok := store.secretByDigest(idDgst)
	return ok
}

func (store *SecretStore) GetSecret(idDgst digest.Digest) (inst dagql.ObjectResult[*Secret], ok bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secretByDigest(idDgst)
	if !ok {
		return inst, false
	}
	return secret, true
}

func (store *SecretStore) GetSecretName(idDgst digest.Digest) (string, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secretByDigest(idDgst)
	if !ok {
		return "", false
	}
	return secret.Self().Name, true
}

func (store *SecretStore) GetSecretURI(idDgst digest.Digest) (string, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secretByDigest(idDgst)
	if !ok {
		return "", false
	}
	return secret.Self().URI, true
}

func (store *SecretStore) GetSecretNameOrURI(idDgst digest.Digest) (string, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secretByDigest(idDgst)
	if !ok {
		return "", false
	}
	if secret.Self().URI != "" {
		return secret.Self().URI, true
	}
	if secret.Self().Name != "" {
		return secret.Self().Name, true
	}
	return "", true
}

func (store *SecretStore) GetSecretPlaintext(ctx context.Context, idDgst digest.Digest) ([]byte, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secretByDigest(idDgst)
	if !ok {
		return nil, fmt.Errorf("secret %s: %w", idDgst, secrets.ErrNotFound)
	}

	return store.GetSecretPlaintextDirect(ctx, secret.Self())
}

// GetSecretPlaintextDirect returns the plaintext of the given secret, even if it's not in the store yet.
// Public to support retrieving the plaintext while deriving the cache key for it (after which it will be
// put in the store).
func (store *SecretStore) GetSecretPlaintextDirect(ctx context.Context, secret *Secret) ([]byte, error) {
	// If the secret is stored locally (setSecret), return the plaintext.
	if secret.URI == "" {
		return secret.Plaintext, nil
	}

	buildkitSessionID := secret.BuildkitSessionID
	if buildkitSessionID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "missing buildkit session id")
	}
	caller, err := store.bkSessionManager.Get(ctx, buildkitSessionID, true)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get buildkit session: %s", err)
	}
	if caller == nil {
		return nil, status.Errorf(codes.Internal, "failed to get buildkit session %q: was nil", buildkitSessionID)
	}

	resp, err := secrets.NewSecretsClient(caller.Conn()).GetSecret(ctx, &secrets.GetSecretRequest{
		ID: secret.URI,
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
