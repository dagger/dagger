package core

import (
	"context"
	"fmt"
	"strings"
	"sync"

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

	// The digest of the DagQL ID that accessed this secret, used as its identifier
	// in secret stores.
	IDDigest digest.Digest
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

func (secret *Secret) LLBID() string {
	return string(secret.IDDigest)
}

type SecretStore struct {
	bkSessionManager *bksession.Manager
	secrets          map[digest.Digest]*storedSecret
	mu               sync.RWMutex
}

// storedSecret has the actual metadata of the Secret. The Secret type is just it's key into the
// SecretStore, which allows us to pass it around but still more easily enforce that any code that
// wants to access it has to go through the SecretStore. So storedSecret has all the actual data
// once you've asked for the secret from the store.
type storedSecret struct {
	*Secret

	// The user-designated name of the secret.
	Name string

	// The plaintext value of the secret.
	Plaintext []byte

	// The URI of the secret, if it's stored in a remote store.
	URI string

	// The id of the buildkit session the secret will be retrieved through.
	BuildkitSessionID string
}

func (s *storedSecret) Clone() *storedSecret {
	cp := *s
	cp.Secret = s.Secret.Clone()
	return &cp
}

func NewSecretStore(bkSessionManager *bksession.Manager) *SecretStore {
	return &SecretStore{
		secrets:          map[digest.Digest]*storedSecret{},
		bkSessionManager: bkSessionManager,
	}
}

func (store *SecretStore) AddSecret(secret *Secret, name string, plaintext []byte) error {
	if secret == nil {
		return fmt.Errorf("secret must not be nil")
	}
	if secret.Query == nil {
		return fmt.Errorf("secret must have a query")
	}
	if secret.IDDigest == "" {
		return fmt.Errorf("secret must have an ID digest")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.secrets[secret.IDDigest] = &storedSecret{
		Secret:    secret,
		Name:      name,
		Plaintext: plaintext,
	}
	return nil
}

func (store *SecretStore) NewSecret(secret *Secret, buildkitSessionID, uri string) error {
	if secret == nil {
		return fmt.Errorf("secret must not be nil")
	}
	if secret.Query == nil {
		return fmt.Errorf("secret must have a query")
	}
	if secret.IDDigest == "" {
		return fmt.Errorf("secret must have an ID digest")
	}

	_, _, err := secretprovider.ResolverForID(uri)
	if err != nil {
		return err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.secrets[secret.IDDigest] = &storedSecret{
		Secret:            secret,
		BuildkitSessionID: buildkitSessionID,
		URI:               uri,
	}
	return nil
}

func (store *SecretStore) AddSecretFromOtherStore(secret *Secret, otherStore *SecretStore) error {
	otherStore.mu.RLock()
	secretVals, ok := otherStore.secrets[secret.IDDigest]
	otherStore.mu.RUnlock()
	if !ok {
		return fmt.Errorf("secret %s not found in other store", secret.IDDigest)
	}

	secretVals = secretVals.Clone()
	secretVals.Secret = secret

	store.mu.Lock()
	store.secrets[secret.IDDigest] = secretVals
	store.mu.Unlock()

	return nil
}

func (store *SecretStore) HasSecret(idDgst digest.Digest) bool {
	store.mu.RLock()
	defer store.mu.RUnlock()
	_, ok := store.secrets[idDgst]
	return ok
}

func (store *SecretStore) GetSecret(idDgst digest.Digest) (*Secret, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secrets[idDgst]
	if !ok {
		return nil, false
	}
	return secret.Secret, true
}

func (store *SecretStore) GetSecretName(idDgst digest.Digest) (string, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secrets[idDgst]
	if !ok {
		return "", false
	}
	return secret.Name, true
}

func (store *SecretStore) GetSecretURI(idDgst digest.Digest) (string, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secrets[idDgst]
	if !ok {
		return "", false
	}
	return secret.URI, true
}

func (store *SecretStore) GetSecretNameOrURI(idDgst digest.Digest) (string, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secrets[idDgst]
	if !ok {
		return "", false
	}
	if secret.URI != "" {
		return secret.URI, true
	}
	if secret.Name != "" {
		return secret.Name, true
	}
	return "", true
}

func (store *SecretStore) GetSecretPlaintext(ctx context.Context, idDgst digest.Digest) ([]byte, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, ok := store.secrets[idDgst]
	if !ok {
		// It seems like when setting secret using SetSecret, it is stored in the secretStore as Secret@xxh3:......,
		// but with the new secrets api, the key does not have that prefix.
		// TODO(rajatjindal): check with Justin/Andrea if we need to handle it differently
		idDgst = digest.Digest(strings.TrimPrefix(idDgst.String(), "Secret@"))
		// fallback to removing Secret@ prefix
		secret, ok = store.secrets[idDgst]
		if !ok {
			return nil, fmt.Errorf("secret xxxx %s: %w", idDgst, secrets.ErrNotFound)
		}
	}

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
