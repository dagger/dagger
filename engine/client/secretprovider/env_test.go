package secretprovider

import (
	"context"
	"errors"
	"testing"
)

// TestEnvProviderRefresherError verifies that a failing refresher does not
// mask the variable that is actually set: refresh is best-effort, so a stale
// (or explicitly exported) value still resolves. The failure is reported
// through the logger and the current span instead of being swallowed.
func TestEnvProviderRefresherError(t *testing.T) {
	t.Setenv("DAGGER_TEST_SECRET_ENV", "stale-but-usable")

	var called int
	RegisterEnvRefresher(func(context.Context, string, bool) error {
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

func TestEnvProviderForcedRefreshErrorIsFatal(t *testing.T) {
	t.Setenv("DAGGER_TEST_SECRET_ENV", "known-stale")
	refreshErr := errors.New("rotation failed")
	RegisterEnvRefresher(func(context.Context, string, bool) error {
		return refreshErr
	})
	t.Cleanup(func() { RegisterEnvRefresher(nil) })

	_, err := envProvider(t.Context(), "DAGGER_TEST_SECRET_ENV?forceRefresh=true")
	if !errors.Is(err, refreshErr) {
		t.Fatalf("forced envProvider error = %v, want refresh failure", err)
	}
}

func TestEnvProviderForceRefreshOption(t *testing.T) {
	t.Setenv("DAGGER_TEST_SECRET_ENV", "refreshed")

	var gotName string
	var gotForce bool
	RegisterEnvRefresher(func(_ context.Context, name string, force bool) error {
		gotName, gotForce = name, force
		return nil
	})
	t.Cleanup(func() { RegisterEnvRefresher(nil) })

	got, err := envProvider(t.Context(), "DAGGER_TEST_SECRET_ENV?forceRefresh=true")
	if err != nil {
		t.Fatalf("envProvider() failed: %v", err)
	}
	if string(got) != "refreshed" {
		t.Errorf("envProvider() = %q, want refreshed value", got)
	}
	if gotName != "DAGGER_TEST_SECRET_ENV" || !gotForce {
		t.Errorf("refresher got name=%q force=%v", gotName, gotForce)
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
