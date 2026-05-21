package secrets

import (
	"context"

	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/util/grpcerrors"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
)

type SecretStore interface {
	GetSecret(context.Context, string) ([]byte, error)
}

var ErrNotFound = errors.Errorf("not found")

func GetSecret(ctx context.Context, c session.Caller, id string) ([]byte, error) {
	client := NewSecretsClient(c.Conn())
	resp, err := client.GetSecret(ctx, &GetSecretRequest{
		ID: id,
	})
	if err != nil {
		if code := grpcerrors.Code(err); code == codes.Unimplemented || code == codes.NotFound {
			return nil, errors.Wrapf(ErrNotFound, "secret %s", id)
		}
		return nil, err
	}
	return resp.Data, nil
}

func GetSecrets(ctx context.Context, c session.Caller, ids []string) (map[string][]byte, error) {
	client := NewSecretsClient(c.Conn())
	resp, err := client.GetSecrets(ctx, &GetSecretsRequest{
		IDs: ids,
	})
	if err != nil {
		if code := grpcerrors.Code(err); code == codes.Unimplemented || code == codes.NotFound {
			return nil, errors.Wrapf(ErrNotFound, "secrets %v", ids)
		}
		return nil, err
	}
	values := make(map[string][]byte, len(resp.Secrets))
	for id, secret := range resp.Secrets {
		values[id] = secret.Data
	}
	return values, nil
}
