package secretprovider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGCPSecretPathParsing(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		projectID string
		want      string
		wantErr   bool
	}{
		{
			name:      "simple secret name",
			path:      "my-secret",
			projectID: "test-project",
			want:      "projects/test-project/secrets/my-secret/versions/latest",
		},
		{
			name:      "secret with version",
			path:      "my-secret/versions/1",
			projectID: "test-project",
			want:      "projects/test-project/secrets/my-secret/versions/1",
		},
		{
			name:      "full resource name",
			path:      "projects/test-project/secrets/my-secret",
			projectID: "",
			want:      "projects/test-project/secrets/my-secret/versions/latest",
		},
		{
			name:      "full resource name with version",
			path:      "projects/test-project/secrets/my-secret/versions/2",
			projectID: "",
			want:      "projects/test-project/secrets/my-secret/versions/2",
		},
		{
			name:      "invalid format - version without secret",
			path:      "versions/1",
			projectID: "test-project",
			wantErr:   true,
		},
		{
			name:      "invalid full resource name",
			path:      "projects/test-project/invalid/my-secret",
			projectID: "",
			wantErr:   true,
		},
		{
			name:      "no project ID set",
			path:      "my-secret",
			projectID: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			if tt.projectID != "" {
				os.Setenv("GCP_PROJECT_ID", tt.projectID)
				defer os.Unsetenv("GCP_PROJECT_ID")
			}

			got, err := parseGCPSecretPath(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGCPProviderIntegration(t *testing.T) {
	// Skip this test if not running integration tests
	if os.Getenv("DAGGER_TEST_INTEGRATION") == "" {
		t.Skip("Set DAGGER_TEST_INTEGRATION to run integration tests")
	}

	// Skip if no GCP credentials are available
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" && os.Getenv("GCP_PROJECT_ID") == "" {
		t.Skip("Set GOOGLE_APPLICATION_CREDENTIALS and GCP_PROJECT_ID to run GCP integration tests")
	}

	ctx := context.Background()

	// Test with a real secret (you'll need to create this in your GCP project)
	testSecretName := os.Getenv("GCP_TEST_SECRET_NAME")
	if testSecretName == "" {
		t.Skip("Set GCP_TEST_SECRET_NAME to run GCP integration tests")
	}

	// Test basic secret retrieval
	data, err := gcpProvider(ctx, testSecretName)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Test with TTL
	dataWithTTL, err := gcpProvider(ctx, testSecretName+"?ttl=1m")
	require.NoError(t, err)
	require.Equal(t, data, dataWithTTL)

	// Verify caching works
	// Clear the client to ensure we don't make another API call
	gcpMutex.Lock()
	gcpClient = nil
	gcpMutex.Unlock()

	// This should use cached value
	cachedData, err := gcpProvider(ctx, testSecretName+"?ttl=1m")
	require.NoError(t, err)
	require.Equal(t, data, cachedData)
}

func TestGCPCacheExpiration(t *testing.T) {
	tests := []struct {
		name    string
		data    gcpSecretWithTTL
		expired bool
	}{
		{
			name:    "no TTL set",
			data:    gcpSecretWithTTL{},
			expired: false,
		},
		{
			name: "TTL not expired",
			data: gcpSecretWithTTL{
				expiresAt: time.Now().Add(1 * time.Hour),
			},
			expired: false,
		},
		{
			name: "TTL expired",
			data: gcpSecretWithTTL{
				expiresAt: time.Now().Add(-1 * time.Hour),
			},
			expired: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gcpHasExpired(tt.data)
			require.Equal(t, tt.expired, got)
		})
	}
}
