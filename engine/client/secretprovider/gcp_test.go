package secretprovider

import (
	"context"
	"fmt"
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

func TestGCPConcurrentAccess(t *testing.T) {
	// Save original cache state
	gcpMutex.Lock()
	originalCache := make(map[string]gcpSecretWithTTL)
	for k, v := range gcpSecretCache {
		originalCache[k] = v
	}
	gcpMutex.Unlock()

	// Clean up after test
	defer func() {
		gcpMutex.Lock()
		gcpSecretCache = originalCache
		gcpMutex.Unlock()
	}()

	// Pre-populate cache with test data
	testData := []struct {
		key  string
		data []byte
		ttl  time.Duration
	}{
		{"secret1", []byte("data1"), 0},
		{"secret2", []byte("data2"), time.Hour},
		{"secret3", []byte("data3"), -time.Hour}, // expired
	}

	gcpMutex.Lock()
	for _, td := range testData {
		cached := gcpSecretWithTTL{data: td.data}
		if td.ttl != 0 {
			cached.expiresAt = time.Now().Add(td.ttl)
		}
		gcpSecretCache[td.key] = cached
	}
	gcpMutex.Unlock()

	// Test concurrent reads
	ctx := context.Background()
	const numGoroutines = 10
	const numReads = 100

	errChan := make(chan error, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < numReads; j++ {
				// Read existing cached secrets
				data, err := gcpProvider(ctx, "secret1")
				if err != nil {
					errChan <- err
					return
				}
				if string(data) != "data1" {
					errChan <- fmt.Errorf("unexpected data: %s", string(data))
					return
				}

				// Read TTL secret
				data, err = gcpProvider(ctx, "secret2")
				if err != nil {
					errChan <- err
					return
				}
				if string(data) != "data2" {
					errChan <- fmt.Errorf("unexpected data: %s", string(data))
					return
				}
			}
			errChan <- nil
		}()
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		err := <-errChan
		require.NoError(t, err)
	}
}

func TestGCPCacheEviction(t *testing.T) {
	// Save original state
	gcpMutex.Lock()
	originalCache := make(map[string]gcpSecretWithTTL)
	for k, v := range gcpSecretCache {
		originalCache[k] = v
	}
	originalOrder := make([]string, len(gcpCacheOrder))
	copy(originalOrder, gcpCacheOrder)
	originalMaxSize := gcpMaxCacheSize
	gcpMutex.Unlock()

	// Clean up after test
	defer func() {
		gcpMutex.Lock()
		gcpSecretCache = originalCache
		gcpCacheOrder = originalOrder
		gcpMaxCacheSize = originalMaxSize
		gcpMutex.Unlock()
	}()

	// Set a small cache size for testing
	gcpMutex.Lock()
	gcpMaxCacheSize = 3
	gcpSecretCache = make(map[string]gcpSecretWithTTL)
	gcpCacheOrder = []string{}
	gcpMutex.Unlock()

	// Add secrets to fill cache
	secrets := []struct {
		key  string
		data []byte
	}{
		{"secret1", []byte("data1")},
		{"secret2", []byte("data2")},
		{"secret3", []byte("data3")},
		{"secret4", []byte("data4")}, // This should evict secret1
	}

	gcpMutex.Lock()
	for _, s := range secrets[:3] {
		gcpSecretCache[s.key] = gcpSecretWithTTL{data: s.data}
		gcpUpdateCacheOrder(s.key)
	}
	gcpMutex.Unlock()

	// Verify cache has 3 items
	gcpMutex.RLock()
	require.Equal(t, 3, len(gcpSecretCache))
	gcpMutex.RUnlock()

	// Add fourth secret, should evict first
	gcpMutex.Lock()
	gcpSecretCache["secret4"] = gcpSecretWithTTL{data: []byte("data4")}
	gcpUpdateCacheOrder("secret4")
	gcpEvictIfNeeded()
	gcpMutex.Unlock()

	// Verify eviction
	gcpMutex.RLock()
	require.Equal(t, 3, len(gcpSecretCache))
	_, exists := gcpSecretCache["secret1"]
	require.False(t, exists, "secret1 should have been evicted")
	
	// Verify remaining secrets
	for _, key := range []string{"secret2", "secret3", "secret4"} {
		_, exists := gcpSecretCache[key]
		require.True(t, exists, "secret %s should still be in cache", key)
	}
	gcpMutex.RUnlock()
}
