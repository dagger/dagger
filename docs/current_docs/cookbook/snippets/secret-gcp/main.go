// A simple example of using GCP Secret Manager with Dagger
package main

import (
	"context"
	"fmt"
)

type SecretGcp struct{}

// Example of using a secret from GCP Secret Manager
func (m *SecretGcp) UseSecret(
	ctx context.Context,
	// The GCP secret to use
	// Can be specified as:
	// - Simple name: "my-secret" (requires GCP_PROJECT_ID env var)
	// - With version: "my-secret/versions/2"
	// - Full path: "projects/my-project/secrets/my-secret"
	// - With TTL: "my-secret?ttl=5m"
	// +required
	secretPath string,
) (string, error) {
	// The secret will be retrieved from GCP Secret Manager
	// using the provided path
	return fmt.Sprintf("Secret path configured: gcp://%s", secretPath), nil
}

// Example of building with a GCP secret
func (m *SecretGcp) BuildWithSecret(
	ctx context.Context,
	// The GCP secret containing the API key
	// +required
	apiKeyPath string,
) (string, error) {
	// In actual usage, you would pass this to dagger:
	// dagger call build-with-secret --api-key-path="my-api-key"
	// And the CLI would handle it as: --api-key="gcp://my-api-key"
	return fmt.Sprintf("Would build with API key from: gcp://%s", apiKeyPath), nil
}