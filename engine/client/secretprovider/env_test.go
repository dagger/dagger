package secretprovider

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/metadata"
)

// TestEnvProviderRefresherError verifies that a failing refresher does not
// mask the variable that is actually set: refresh is best-effort, so a stale
// (or explicitly exported) value still resolves. The failure is reported
// through the logger and the current span instead of being swallowed.
func TestEnvProviderRefresherError(t *testing.T) {
	t.Setenv("DAGGER_TEST_SECRET_ENV", "stale-but-usable")

	var called int
	RegisterEnvRefresher(func(context.Context, string) error {
		called++
		return errors.New("token endpoint unreachable")
	})
	t.Cleanup(func() { RegisterEnvRefresher(nil) })

	got, err := envProvider(t.Context(), "DAGGER_TEST_SECRET_ENV")
	if err != nil {
		t.Fatalf("envProvider() failed: %v", err)
	}
	if string(got) != "stale-but-usable" {
		t.Errorf("envProvider() = %q, want %q", got, "stale-but-usable")
	}
	if called != 1 {
		t.Errorf("refresher called %d times, want 1", called)
	}
}

func TestEnvProviderRejectedValueRefreshError(t *testing.T) {
	t.Setenv("DAGGER_TEST_SECRET_ENV", "known-bad")
	refreshErr := errors.New("token endpoint unreachable")
	RegisterEnvRefresher(func(ctx context.Context, _ string) error {
		if got := RejectedEnvValue(ctx); got != "fingerprint" {
			t.Errorf("rejected value fingerprint = %q", got)
		}
		return refreshErr
	})
	t.Cleanup(func() { RegisterEnvRefresher(nil) })

	outgoing := ContextWithRejectedEnvValue(t.Context(), "fingerprint")
	md, _ := metadata.FromOutgoingContext(outgoing)
	incoming := metadata.NewIncomingContext(t.Context(), md)
	got, err := envProvider(incoming, "DAGGER_TEST_SECRET_ENV")
	if !errors.Is(err, refreshErr) || got != nil {
		t.Fatalf("rejected value must not mask refresh failure: got %q, %v", got, err)
	}
	if got := RejectedEnvValue(ContextWithRejectedEnvValue(incoming, "")); got != "" {
		t.Fatalf("cleared rejection = %q", got)
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
