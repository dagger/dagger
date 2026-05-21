package secretsprovider

import (
	"context"

	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MaxSecretSize is the maximum byte length allowed for a secret
const MaxSecretSize = 500 * 1024 // 500KB

func NewSecretProvider(store secrets.SecretStore) session.Attachable {
	return &secretProvider{
		store: store,
	}
}

type secretProvider struct {
	store secrets.SecretStore
}

func (sp *secretProvider) Register(server *grpc.Server) {
	secrets.RegisterSecretsServer(server, sp)
}

func (sp *secretProvider) GetSecret(ctx context.Context, req *secrets.GetSecretRequest) (*secrets.GetSecretResponse, error) {
	resp, err := sp.GetSecrets(ctx, &secrets.GetSecretsRequest{
		IDs:         []string{req.ID},
		Annotations: req.Annotations,
	})
	if err != nil {
		return nil, err
	}
	secret, ok := resp.Secrets[req.ID]
	if !ok {
		return nil, status.Error(codes.NotFound, secrets.ErrNotFound.Error())
	}
	return secret, nil
}

func (sp *secretProvider) GetSecrets(ctx context.Context, req *secrets.GetSecretsRequest) (*secrets.GetSecretsResponse, error) {
	resp := &secrets.GetSecretsResponse{
		Secrets: make(map[string]*secrets.GetSecretResponse, len(req.IDs)),
	}
	for _, id := range req.IDs {
		dt, err := sp.store.GetSecret(ctx, id)
		if err != nil {
			if errors.Is(err, secrets.ErrNotFound) {
				return nil, status.Error(codes.NotFound, err.Error())
			}
			return nil, err
		}
		if l := len(dt); l > MaxSecretSize {
			return nil, errors.Errorf("invalid secret size %d", l)
		}
		resp.Secrets[id] = &secrets.GetSecretResponse{Data: dt}
	}
	return resp, nil
}

func FromMap(m map[string][]byte) session.Attachable {
	return NewSecretProvider(mapStore(m))
}

type mapStore map[string][]byte

func (m mapStore) GetSecret(ctx context.Context, id string) ([]byte, error) {
	v, ok := m[id]
	if !ok {
		return nil, errors.WithStack(secrets.ErrNotFound)
	}
	return v, nil
}
