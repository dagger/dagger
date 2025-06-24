package core

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/testctx"
)

type SecretGCPSuite struct{}

func TestSecretGCP(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(SecretGCPSuite{})
}

func (SecretGCPSuite) TestBasicSecretRetrieval(ctx context.Context, t *testctx.T) {
	// Skip if not running integration tests or no GCP credentials
	if os.Getenv("DAGGER_TEST_GCP_SECRETS") == "" {
		t.Skip("Set DAGGER_TEST_GCP_SECRETS=1 to run GCP secret tests")
	}

	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" && os.Getenv("GCP_PROJECT_ID") == "" {
		t.Skip("Set GOOGLE_APPLICATION_CREDENTIALS and GCP_PROJECT_ID to run GCP secret tests")
	}

	testSecretName := os.Getenv("GCP_TEST_SECRET_NAME")
	if testSecretName == "" {
		t.Skip("Set GCP_TEST_SECRET_NAME to run GCP secret tests")
	}

	// Optional: if GCP_TEST_SECRET_VALUE is set, we'll verify the actual value
	expectedValue := os.Getenv("GCP_TEST_SECRET_VALUE")

	c := connect(ctx, t)

	secret := c.Secret("gcp://"+testSecretName)
	
	// Verify the secret is set correctly by checking its plaintext value
	if expectedValue != "" {
		plaintext, err := secret.Plaintext(ctx)
		require.NoError(t, err)
		require.Equal(t, expectedValue, plaintext, "Secret value doesn't match expected value")
	}
	
	// Use the secret in a container (output will be scrubbed)
	out, err := c.Container().
		From("alpine:latest").
		WithSecretVariable("TEST_SECRET", secret).
		WithExec([]string{"sh", "-c", "echo -n $TEST_SECRET"}).
		Stdout(ctx)
	
	require.NoError(t, err)
	require.Equal(t, "***", out, "Secret should be scrubbed in output")
	
	// Verify the secret can be used in a container by checking its properties
	// without exposing its value
	lengthOut, err := c.Container().
		From("alpine:latest").
		WithSecretVariable("TEST_SECRET", secret).
		WithExec([]string{"sh", "-c", "echo -n $TEST_SECRET | wc -c"}).
		Stdout(ctx)
	
	require.NoError(t, err)
	require.NotEqual(t, "0", strings.TrimSpace(lengthOut), "Secret should not be empty")
	
	// If we have an expected value, verify the length matches
	if expectedValue != "" {
		expectedLength := fmt.Sprintf("%d", len(expectedValue))
		require.Equal(t, expectedLength, strings.TrimSpace(lengthOut), "Secret length should match expected value length")
	}
	
	// Verify the secret can be used successfully in a command
	_, err = c.Container().
		From("alpine:latest").
		WithSecretVariable("TEST_SECRET", secret).
		WithExec([]string{"sh", "-c", "test -n \"$TEST_SECRET\""}).
		Sync(ctx)
	
	require.NoError(t, err)
}

func (SecretGCPSuite) TestSecretWithVersion(ctx context.Context, t *testctx.T) {
	// Skip if not running integration tests or no GCP credentials
	if os.Getenv("DAGGER_TEST_GCP_SECRETS") == "" {
		t.Skip("Set DAGGER_TEST_GCP_SECRETS=1 to run GCP secret tests")
	}

	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" && os.Getenv("GCP_PROJECT_ID") == "" {
		t.Skip("Set GOOGLE_APPLICATION_CREDENTIALS and GCP_PROJECT_ID to run GCP secret tests")
	}

	testSecretName := os.Getenv("GCP_TEST_SECRET_NAME")
	if testSecretName == "" {
		t.Skip("Set GCP_TEST_SECRET_NAME to run GCP secret tests")
	}

	c := connect(ctx, t)

	// Test with specific version
	secret := c.Secret("gcp://"+testSecretName+"/versions/1")
	
	// Use the secret in a container
	_, err := c.Container().
		From("alpine:latest").
		WithSecretVariable("TEST_SECRET", secret).
		WithExec([]string{"sh", "-c", "test -n \"$TEST_SECRET\""}).
		Sync(ctx)
	
	require.NoError(t, err)
}

func (SecretGCPSuite) TestFullResourceName(ctx context.Context, t *testctx.T) {
	// Skip if not running integration tests or no GCP credentials
	if os.Getenv("DAGGER_TEST_GCP_SECRETS") == "" {
		t.Skip("Set DAGGER_TEST_GCP_SECRETS=1 to run GCP secret tests")
	}

	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" && os.Getenv("GCP_PROJECT_ID") == "" {
		t.Skip("Set GOOGLE_APPLICATION_CREDENTIALS and GCP_PROJECT_ID to run GCP secret tests")
	}

	testSecretName := os.Getenv("GCP_TEST_SECRET_NAME")
	if testSecretName == "" {
		t.Skip("Set GCP_TEST_SECRET_NAME to run GCP secret tests")
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	require.NotEmpty(t, projectID)

	c := connect(ctx, t)

	// Test with full resource name
	fullPath := fmt.Sprintf("gcp://projects/%s/secrets/%s", projectID, testSecretName)
	secret := c.Secret(fullPath)
	
	// Use the secret in a container
	_, err := c.Container().
		From("alpine:latest").
		WithSecretVariable("TEST_SECRET", secret).
		WithExec([]string{"sh", "-c", "test -n \"$TEST_SECRET\""}).
		Sync(ctx)
	
	require.NoError(t, err)
}

func (SecretGCPSuite) TestSecretWithTTL(ctx context.Context, t *testctx.T) {
	// Skip if not running integration tests or no GCP credentials
	if os.Getenv("DAGGER_TEST_GCP_SECRETS") == "" {
		t.Skip("Set DAGGER_TEST_GCP_SECRETS=1 to run GCP secret tests")
	}

	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" && os.Getenv("GCP_PROJECT_ID") == "" {
		t.Skip("Set GOOGLE_APPLICATION_CREDENTIALS and GCP_PROJECT_ID to run GCP secret tests")
	}

	testSecretName := os.Getenv("GCP_TEST_SECRET_NAME")
	if testSecretName == "" {
		t.Skip("Set GCP_TEST_SECRET_NAME to run GCP secret tests")
	}

	c := connect(ctx, t)

	// Test with TTL
	secret := c.Secret("gcp://"+testSecretName+"?ttl=1m")
	
	// Use the secret twice to test caching
	for i := 0; i < 2; i++ {
		_, err := c.Container().
			From("alpine:latest").
			WithSecretVariable("TEST_SECRET", secret).
			WithExec([]string{"sh", "-c", "test -n \"$TEST_SECRET\""}).
			Sync(ctx)
		
		require.NoError(t, err)
	}
}

func (SecretGCPSuite) TestSecretNotExposedInLogs(ctx context.Context, t *testctx.T) {
	// Skip if not running integration tests or no GCP credentials
	if os.Getenv("DAGGER_TEST_GCP_SECRETS") == "" {
		t.Skip("Set DAGGER_TEST_GCP_SECRETS=1 to run GCP secret tests")
	}

	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" && os.Getenv("GCP_PROJECT_ID") == "" {
		t.Skip("Set GOOGLE_APPLICATION_CREDENTIALS and GCP_PROJECT_ID to run GCP secret tests")
	}

	testSecretName := os.Getenv("GCP_TEST_SECRET_NAME")
	if testSecretName == "" {
		t.Skip("Set GCP_TEST_SECRET_NAME to run GCP secret tests")
	}

	expectedValue := os.Getenv("GCP_TEST_SECRET_VALUE")

	c := connect(ctx, t)

	secret := c.Secret("gcp://"+testSecretName)
	
	// Try to echo the secret (should be scrubbed)
	out, err := c.Container().
		From("alpine:latest").
		WithSecretVariable("TEST_SECRET", secret).
		WithExec([]string{"sh", "-c", "echo -n $TEST_SECRET"}).
		Stdout(ctx)
	
	require.NoError(t, err)
	// The secret should be scrubbed in the output
	require.Equal(t, "***", out, "Secret should be fully scrubbed")
	
	// If we know the expected value, verify it's not exposed
	if expectedValue != "" {
		require.NotContains(t, out, expectedValue, "Secret value should not be exposed in logs")
	}
}