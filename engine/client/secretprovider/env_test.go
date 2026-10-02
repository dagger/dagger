package secretprovider

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestRejectedSecretValueMetadata(t *testing.T) {
	outgoing := ContextWithRejectedSecretValue(t.Context(), "fingerprint")
	if got := RejectedSecretValue(outgoing); got != "fingerprint" {
		t.Fatalf("outgoing fingerprint = %q", got)
	}
	md, _ := metadata.FromOutgoingContext(outgoing)
	incoming := metadata.NewIncomingContext(t.Context(), md)
	if got := RejectedSecretValue(incoming); got != "fingerprint" {
		t.Fatalf("incoming fingerprint = %q", got)
	}
	if got := RejectedSecretValue(ContextWithRejectedSecretValue(incoming, "")); got != "" {
		t.Fatalf("cleared rejection = %q", got)
	}
}

func TestLLMConfigProvider(t *testing.T) {
	_, err := llmConfigProvider(t.Context(), "anthropic/api_key")
	if !errors.Is(err, ErrNoLLMConfigResolver) {
		t.Fatalf("unregistered resolver: err = %v", err)
	}

	RegisterLLMConfigResolver(func(ctx context.Context, path string) ([]byte, error) {
		if path != "anthropic/api_key" {
			t.Errorf("path = %q", path)
		}
		if got := RejectedSecretValue(ctx); got != "fp" {
			t.Errorf("rejected fingerprint = %q", got)
		}
		return []byte("key"), nil
	})
	t.Cleanup(func() { RegisterLLMConfigResolver(nil) })

	resolver, path, err := ResolverForID("llmconfig://anthropic/api_key")
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver(ContextWithRejectedSecretValue(t.Context(), "fp"), path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "key" {
		t.Fatalf("value = %q", got)
	}
}

// TestEnvProviderRedactsName guards the not-found message: users originally
// passed the secret value here rather than its name, so the name may itself be
// a credential and must not be echoed in full.
func TestEnvProviderRedactsName(t *testing.T) {
	_, err := envProvider(t.Context(), "DAGGER_TEST_SECRET_ENV_MISSING")
	if err == nil {
		t.Fatal("envProvider() succeeded for a missing variable")
	}
	if got, want := err.Error(), `secret env var not found: "DAG..."`; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}
