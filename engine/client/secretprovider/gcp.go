package secretprovider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/option"
)

type gcpSecretWithTTL struct {
	expiresAt time.Time
	data      []byte
}

var (
	gcpMutex       sync.RWMutex
	gcpClient      *secretmanager.Client
	gcpSecretCache = make(map[string]gcpSecretWithTTL)
	gcpCacheOrder  []string // Track access order for LRU eviction
	gcpMaxCacheSize = 100   // Maximum number of cached secrets
)

// GCP Secrets Manager provider for SecretProvider
func gcpProvider(ctx context.Context, pathWithQuery string) ([]byte, error) {
	parsed, err := url.Parse(pathWithQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to parse secret URL %q: %w", pathWithQuery, err)
	}

	// this is just path part without the query params such as ttl
	key := parsed.Path

	var ttl time.Duration
	ttlStr := strings.TrimSpace(parsed.Query().Get("ttl"))
	if ttlStr != "" {
		ttl, err = time.ParseDuration(ttlStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ttl %q provided for secret %q: %w", ttlStr, key, err)
		}
	}

	// Try to get from cache with read lock first
	gcpMutex.RLock()
	if existing, ok := gcpSecretCache[key]; ok && !gcpHasExpired(existing) {
		gcpMutex.RUnlock()
		return existing.data, nil
	}
	gcpMutex.RUnlock()

	// Need to fetch - acquire write lock
	gcpMutex.Lock()
	defer gcpMutex.Unlock()

	// Double-check after acquiring write lock
	if existing, ok := gcpSecretCache[key]; ok && !gcpHasExpired(existing) {
		gcpUpdateCacheOrder(key)
		return existing.data, nil
	}

	// Check context before potentially long operations
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled while fetching secret %q: %w", key, ctx.Err())
	default:
	}
	// check if client is initialized
	if gcpClient == nil {
		err := gcpConfigureClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize GCP client: %w", err)
		}
	}

	// Parse the key to extract project, secret name, and optional version
	// Expected format: project/PROJECT_ID/secrets/SECRET_NAME[/versions/VERSION]
	// or just SECRET_NAME (will use default project)
	secretName, err := parseGCPSecretPath(key)
	if err != nil {
		return nil, fmt.Errorf("failed to parse secret path %q: %w", key, err)
	}

	// Access the secret
	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretName,
	}

	result, err := gcpClient.AccessSecretVersion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to access GCP secret %q: %w", secretName, err)
	}

	// Get the secret data
	data := gcpSecretWithTTL{
		data: result.Payload.Data,
	}

	if ttl > 0 {
		data.expiresAt = time.Now().Add(ttl)
	}

	// cache response
	gcpSecretCache[key] = data
	gcpUpdateCacheOrder(key)
	gcpEvictIfNeeded()

	return data.data, nil
}

func gcpHasExpired(data gcpSecretWithTTL) bool {
	// if no ttl set, assume no ttl required
	if data.expiresAt.IsZero() {
		return false
	}

	if data.expiresAt.After(time.Now()) {
		return false
	}

	return true
}

// parseGCPSecretPath parses the secret path and returns the full resource name
func parseGCPSecretPath(path string) (string, error) {
	// If path already starts with "projects/", assume it's a full resource name
	if strings.HasPrefix(path, "projects/") {
		// Ensure it has the correct format and add /versions/latest if not specified
		parts := strings.Split(path, "/")
		if len(parts) < 4 || parts[2] != "secrets" {
			return "", fmt.Errorf("invalid GCP secret path format: %s", path)
		}

		// If no version specified, use latest
		if len(parts) == 4 {
			return path + "/versions/latest", nil
		}
		return path, nil
	}

	// Otherwise, construct the path using the project from environment
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		// Try to get from Google Application Default Credentials
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
		if projectID == "" {
			projectID = os.Getenv("GCLOUD_PROJECT")
			if projectID == "" {
				return "", fmt.Errorf("GCP project ID not set. Set GCP_PROJECT_ID, GOOGLE_CLOUD_PROJECT, or GCLOUD_PROJECT environment variable")
			}
		}
	}

	// Handle simple secret names or paths with version
	parts := strings.Split(path, "/")
	if len(parts) == 1 {
		// Just the secret name, use latest version
		return fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, path), nil
	} else if len(parts) == 2 && parts[0] == "versions" {
		// Format: versions/VERSION (secret name must be in a different field)
		return "", fmt.Errorf("invalid format: version specified without secret name")
	} else if len(parts) == 3 && parts[1] == "versions" {
		// Format: SECRET_NAME/versions/VERSION
		return fmt.Sprintf("projects/%s/secrets/%s/versions/%s", projectID, parts[0], parts[2]), nil
	}

	return "", fmt.Errorf("invalid GCP secret path format: %s", path)
}

// Load configuration from environment and create a new GCP Secret Manager client
func gcpConfigureClient(ctx context.Context) error {
	var opts []option.ClientOption

	// Check for explicit credentials file
	if credFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); credFile != "" {
		opts = append(opts, option.WithCredentialsFile(credFile))
	}
	// Otherwise, the client will use Application Default Credentials (ADC)
	// which includes:
	// 1. GOOGLE_APPLICATION_CREDENTIALS environment variable
	// 2. gcloud auth application-default login
	// 3. GCE/GKE metadata service
	// 4. Other Google Cloud environments

	// Create the client
	client, err := secretmanager.NewClient(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to create GCP Secret Manager client: %w", err)
	}

	gcpClient = client
	return nil
}

// gcpUpdateCacheOrder moves the key to the end of the access order (most recently used)
func gcpUpdateCacheOrder(key string) {
	// Remove existing occurrence
	for i, k := range gcpCacheOrder {
		if k == key {
			gcpCacheOrder = append(gcpCacheOrder[:i], gcpCacheOrder[i+1:]...)
			break
		}
	}
	// Add to end
	gcpCacheOrder = append(gcpCacheOrder, key)
}

// gcpEvictIfNeeded removes least recently used items if cache exceeds max size
func gcpEvictIfNeeded() {
	for len(gcpSecretCache) > gcpMaxCacheSize && len(gcpCacheOrder) > 0 {
		// Remove least recently used (first in gcpCacheOrder)
		keyToEvict := gcpCacheOrder[0]
		delete(gcpSecretCache, keyToEvict)
		gcpCacheOrder = gcpCacheOrder[1:]
	}
}
