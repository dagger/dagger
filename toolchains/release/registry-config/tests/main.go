package main

import (
	"context"
	"dagger/registry-config/tests/internal/dagger"
	"fmt"

	"github.com/sourcegraph/conc/pool"
)

type Tests struct{}

// All executes all tests.
func (m *Tests) All(ctx context.Context) error {
	p := pool.New().WithErrors().WithContext(ctx)

	p.Go(m.WithRegistryAuth)
	p.Go(m.WithRegistryAuth_MultipleCredentials)
	p.Go(m.WithoutRegistryAuth)
	p.Go(m.SecretMount)
	p.Go(m.SecretMount_SkipOnEmpty)

	return p.Wait()
}

func (m *Tests) WithRegistryAuth(ctx context.Context) error {
	secret := dag.RegistryConfig().
		WithRegistryAuth("ghcr.io", "sagikazarmark", dag.SetSecret("WithRegistryAuth-password", "password")).
		WithRegistryAuth("docker.io", "sagikazarmark", dag.SetSecret("WithRegistryAuth-password2", "password2")).
		Secret()

	actual, err := secret.Plaintext(ctx)
	if err != nil {
		return err
	}

	const expected = `{"auths":{"docker.io":{"auth":"c2FnaWthemFybWFyazpwYXNzd29yZDI="},"ghcr.io":{"auth":"c2FnaWthemFybWFyazpwYXNzd29yZA=="}}}`

	if actual != expected {
		return fmt.Errorf("secret does not match the expected value\nactual:   %s\nexpected: %s", actual, expected)
	}

	return nil
}

func (m *Tests) WithRegistryAuth_MultipleCredentials(ctx context.Context) error {
	secret := dag.RegistryConfig().
		WithRegistryAuth("ghcr.io", "sagikazarmark", dag.SetSecret("WithRegistryAuth_MultipleCredentials-passwordold", "passwordold")).
		WithRegistryAuth("docker.io", "sagikazarmark", dag.SetSecret("WithRegistryAuth_MultipleCredentials-password2", "password2")).
		WithRegistryAuth("ghcr.io", "sagikazarmark", dag.SetSecret("WithRegistryAuth_MultipleCredentials-password", "password")).
		Secret()

	actual, err := secret.Plaintext(ctx)
	if err != nil {
		return err
	}

	const expected = `{"auths":{"docker.io":{"auth":"c2FnaWthemFybWFyazpwYXNzd29yZDI="},"ghcr.io":{"auth":"c2FnaWthemFybWFyazpwYXNzd29yZA=="}}}`

	if actual != expected {
		return fmt.Errorf("secret does not match the expected value\nactual:   %s\nexpected: %s", actual, expected)
	}

	return nil
}

func (m *Tests) WithoutRegistryAuth(ctx context.Context) error {
	secret := dag.RegistryConfig().
		WithRegistryAuth("ghcr.io", "sagikazarmark", dag.SetSecret("WithoutRegistryAuth-password", "password")).
		WithRegistryAuth("docker.io", "sagikazarmark", dag.SetSecret("WithoutRegistryAuth-password2", "password2")).
		WithRegistryAuth("gcr.io", "sagikazarmark", dag.SetSecret("WithoutRegistryAuth-password3", "password3")).
		WithoutRegistryAuth("gcr.io").
		Secret()

	actual, err := secret.Plaintext(ctx)
	if err != nil {
		return err
	}

	const expected = `{"auths":{"docker.io":{"auth":"c2FnaWthemFybWFyazpwYXNzd29yZDI="},"ghcr.io":{"auth":"c2FnaWthemFybWFyazpwYXNzd29yZA=="}}}`

	if actual != expected {
		return fmt.Errorf("secret does not match the expected value\nactual:   %s\nexpected: %s", actual, expected)
	}

	return nil
}

func (m *Tests) SecretMount(ctx context.Context) error {
	const expected = `{"auths":{"docker.io":{"auth":"c2FnaWthemFybWFyazpwYXNzd29yZDI="},"ghcr.io":{"auth":"c2FnaWthemFybWFyazpwYXNzd29yZA=="}}}`

	registryConfig := dag.RegistryConfig().
		WithRegistryAuth("ghcr.io", "sagikazarmark", dag.SetSecret("MountSecret-password", "password")).
		WithRegistryAuth("docker.io", "sagikazarmark", dag.SetSecret("MountSecret-password2", "password2"))

	_, err := dag.Container().
		From("alpine").
		With(registryConfig.SecretMount("/actual.json").Mount).
		WithMountedFile("/expected.json", dag.Directory().WithNewFile("expected.json", expected).File("expected.json")).
		WithExec([]string{"diff", "-u", "/expected.json", "/actual.json"}).
		Sync(ctx)

	return err
}

func (m *Tests) SecretMount_SkipOnEmpty(ctx context.Context) error {
	registryConfig := dag.RegistryConfig()

	_, err := dag.Container().
		From("alpine").
		With(registryConfig.SecretMount("/empty.json", dagger.RegistryConfigSecretMountOpts{SkipOnEmpty: true}).Mount).
		WithExec([]string{"sh", "-c", "test ! -f /empty.json"}).
		Sync(ctx)

	return err
}
