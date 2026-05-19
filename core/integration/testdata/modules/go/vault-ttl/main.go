package main

import (
	"context"
	"dagger/foo/internal/dagger"
	"fmt"
	"time"
)

type Foo struct{}

func (m *Foo) VerifySecret(ctx context.Context, vault *dagger.Service, secret *dagger.Secret, tc string) (string, error) {
	_, err := dag.Container().From("hashicorp/vault").
		WithEnvVariable("VAULT_ADDR", "http://vault:8200").
		WithEnvVariable("VAULT_TOKEN", "vault-root-token").
		WithServiceBinding("vault", vault).
		WithExec([]string{"sh", "-c", fmt.Sprintf("vault kv put secret/%s username=\"admin\" password=\"original-password\"", tc)}).
		Sync(ctx)
	if err != nil {
		return "", err
	}

	original, err := secret.Plaintext(ctx)
	if err != nil {
		return "", err
	}

	_, err = dag.Container().From("hashicorp/vault").
		WithEnvVariable("VAULT_ADDR", "http://vault:8200").
		WithEnvVariable("VAULT_TOKEN", "vault-root-token").
		WithServiceBinding("vault", vault).
		WithExec([]string{"sh", "-c", fmt.Sprintf("vault kv put secret/%s username=\"admin\" password=\"updated-password\"", tc)}).Sync(ctx)
	if err != nil {
		return "", err
	}

	time.Sleep(5 * time.Second)

	updated, err := secret.Plaintext(ctx)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("original: %s\nupdated: %s", original, updated), nil
}
